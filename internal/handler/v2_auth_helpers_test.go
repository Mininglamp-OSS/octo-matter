// Tests for v2 鉴权关系数据补全 authorization helpers.
//
// These cover the cross-space + cross-owner attack surface that was the
// core of the reviewer's matter#78 P0-2 critique. The helpers
// (callerOwnsBot, callerOwnedBotsInSpace, intersectStrings) are pure
// against gin.Context state, so unit tests at the helper layer give the
// same regression value as full handler tests with much less plumbing.
package handler

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestCtx returns a gin.Context with pre-set keys mimicking what
// auth.AuthMiddleware would inject after a successful verify call.
func newTestCtx(t *testing.T, keys map[string]any) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for k, v := range keys {
		c.Set(k, v)
	}
	return c
}

// ─────────────────────────────────────────────────────────────────────
// callerOwnsBot — gates BotFeed, the matter#78 P0-2 fix
// ─────────────────────────────────────────────────────────────────────

func TestCallerOwnsBot_BotPathSelfAccess(t *testing.T) {
	// Bot calling its own feed (uid == botUID) is always allowed,
	// regardless of owned_bots_by_space content.
	h := &InternalHandler{}
	c := newTestCtx(t, map[string]any{"uid": "bot_self_uid"})
	if !h.callerOwnsBot(c, "bot_self_uid") {
		t.Errorf("bot calling its own feed must be allowed")
	}
}

func TestCallerOwnsBot_OwnedBotsBySpace_Match(t *testing.T) {
	// Session/api_key caller listed in owned_bots_by_space[space] for any
	// space — allowed.
	h := &InternalHandler{}
	c := newTestCtx(t, map[string]any{
		"uid": "user_x",
		"owned_bots_by_space": map[string][]string{
			"space-A": {"bot_owned_in_a"},
			"space-C": {"bot_owned_in_c"},
		},
	})
	if !h.callerOwnsBot(c, "bot_owned_in_a") {
		t.Errorf("caller listed as owner in owned_bots_by_space must be allowed")
	}
	if !h.callerOwnsBot(c, "bot_owned_in_c") {
		t.Errorf("ownership search must cover every space in the map")
	}
}

// CORE CROSS-SPACE TEST. Reviewer matter#78 P0-2: pre-v2 any caller could
// read any bot's feed. After v2, a caller who doesn't own the bot in any
// of their spaces must be rejected — even if they "look authenticated"
// with a valid uid.
func TestCallerOwnsBot_CrossSpaceBotRejected(t *testing.T) {
	h := &InternalHandler{}
	c := newTestCtx(t, map[string]any{
		"uid": "user_attacker",
		"owned_bots_by_space": map[string][]string{
			"space-attacker": {"bot_legitimately_owned"},
		},
		// Note: NO 'bot_in_victim_space' anywhere in the map.
	})
	if h.callerOwnsBot(c, "bot_in_victim_space") {
		t.Errorf("cross-space bot must be rejected: caller has no ownership claim")
	}
}

func TestCallerOwnsBot_NoVerifiedContext_FallbackToRelatedUIDs(t *testing.T) {
	// Pre-v2 server (no owned_bots_by_space). Fallback to related_uids
	// keeps the rolling-upgrade window working — once server >= v2 the
	// fallback path can be deleted.
	h := &InternalHandler{}
	c := newTestCtx(t, map[string]any{
		"uid":          "user_y",
		"related_uids": []string{"user_y", "bot_legacy_owned"},
	})
	if !h.callerOwnsBot(c, "bot_legacy_owned") {
		t.Errorf("pre-v2 related_uids fallback must allow listed bot")
	}
	if h.callerOwnsBot(c, "bot_not_listed") {
		t.Errorf("fallback path must still reject bots not in related_uids")
	}
}

func TestCallerOwnsBot_EmptyContext_DenyAll(t *testing.T) {
	// No verified context, no related_uids — must deny by default.
	// This is the "fail-secure" property: a misconfigured caller doesn't
	// silently get access.
	h := &InternalHandler{}
	c := newTestCtx(t, map[string]any{"uid": "user_z"})
	if h.callerOwnsBot(c, "any_bot_uid") {
		t.Errorf("caller with no ownership data must be denied (fail-secure)")
	}
}

// ─────────────────────────────────────────────────────────────────────
// callerOwnedBotsInSpace — drives issue #75 fix (ClaimNextForBots filter)
// ─────────────────────────────────────────────────────────────────────

func TestCallerOwnedBotsInSpace_NilMapReturnsNil(t *testing.T) {
	// Pre-v2 server returns no owned_bots_by_space — helper returns nil
	// so the caller (ClaimNextForBots filter) knows to skip the filter
	// (backward compat). Distinct from returning [] which means "I have
	// the map but no bots in this space".
	c := newTestCtx(t, map[string]any{})
	got := callerOwnedBotsInSpace(c, "space-A")
	if got != nil {
		t.Errorf("missing owned_bots_by_space must return nil, got %v", got)
	}
}

func TestCallerOwnedBotsInSpace_MapWithoutSpaceReturnsEmpty(t *testing.T) {
	// Caller has the map (v2 server) but no bots in the requested space —
	// returns empty slice (NOT nil) so the caller's intersect produces
	// an empty allow-set rather than skipping the filter.
	c := newTestCtx(t, map[string]any{
		"owned_bots_by_space": map[string][]string{
			"space-other": {"bot_in_other"},
		},
	})
	got := callerOwnedBotsInSpace(c, "space-target")
	if got == nil || len(got) != 0 {
		t.Errorf("owned_bots_by_space present but no entries for space must return [], got %v", got)
	}
}

func TestCallerOwnedBotsInSpace_HappyPath(t *testing.T) {
	c := newTestCtx(t, map[string]any{
		"owned_bots_by_space": map[string][]string{
			"space-A": {"bot_a_1", "bot_a_2"},
		},
	})
	got := callerOwnedBotsInSpace(c, "space-A")
	want := []string{"bot_a_1", "bot_a_2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ─────────────────────────────────────────────────────────────────────
// intersectStrings — the actual issue #75 + cross-space filter
// ─────────────────────────────────────────────────────────────────────

// CORE ISSUE #75 TEST. Pre-v2 a caller could ClaimNextForBots with
// arbitrary bot_uids including bots they don't own. After v2 the
// intersection with owned set strips the foreign uids.
func TestIntersectStrings_FiltersForeignBotUIDs_Issue75(t *testing.T) {
	// Caller asks for {bot_self, bot_other_owner, bot_completely_foreign}
	requested := []string{"bot_self", "bot_other_owner", "bot_completely_foreign"}
	owned := []string{"bot_self"} // only this one is the caller's
	got := intersectStrings(requested, owned)
	want := []string{"bot_self"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("intersect must drop bots not in owned set; got %v want %v", got, want)
	}
}

func TestIntersectStrings_AllOwned_PassesThrough(t *testing.T) {
	got := intersectStrings([]string{"a", "b", "c"}, []string{"a", "b", "c", "d"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestIntersectStrings_NoneOwned_ReturnsEmpty(t *testing.T) {
	// Caller asked only for bots they don't own (cross-owner attack
	// pattern). Returns an empty slice (len 0) — handler will respond
	// [] (not 403) per the design comment in internal_handler.go.
	got := intersectStrings([]string{"foreign1", "foreign2"}, []string{"mine"})
	if len(got) != 0 {
		t.Errorf("no overlap must yield zero-length slice, got %v", got)
	}
}

func TestIntersectStrings_EmptyInputs(t *testing.T) {
	if got := intersectStrings(nil, []string{"a"}); got != nil {
		t.Errorf("nil a must return nil, got %v", got)
	}
	if got := intersectStrings([]string{"a"}, nil); got != nil {
		t.Errorf("nil b must return nil, got %v", got)
	}
}

// ─────────────────────────────────────────────────────────────────────
// callerCanActAs — v3 §3.4 actor_uid impersonation gate
// ─────────────────────────────────────────────────────────────────────

func TestCallerCanActAs_CallerSelf_Allowed(t *testing.T) {
	c := newTestCtx(t, map[string]any{"uid": "user_alice"})
	if !callerCanActAs(c, "spaceA", "user_alice") {
		t.Error("caller acting as themselves must be allowed")
	}
}

func TestCallerCanActAs_OwnedBot_Allowed(t *testing.T) {
	c := newTestCtx(t, map[string]any{
		"uid":                 "user_alice",
		"owned_bots_by_space": map[string][]string{"spaceA": {"bot_alice_1", "bot_alice_2"}},
	})
	if !callerCanActAs(c, "spaceA", "bot_alice_2") {
		t.Error("caller acting as their owned bot must be allowed")
	}
}

func TestCallerCanActAs_ForeignBot_Forbidden(t *testing.T) {
	// Alice's api_key trying to write as Bob's bot (same space, different owner)
	// — the core attack v3 §3.4 closes.
	c := newTestCtx(t, map[string]any{
		"uid":                 "user_alice",
		"owned_bots_by_space": map[string][]string{"spaceA": {"bot_alice_1"}},
	})
	if callerCanActAs(c, "spaceA", "bot_of_bob") {
		t.Error("caller acting as foreign owner's bot must be forbidden (v3 §3.4)")
	}
}

func TestCallerCanActAs_NoOwnedBotsCtx_OnlyCallerUIDAllowed(t *testing.T) {
	// pre-v2 server fallback: no owned_bots_by_space in ctx. Fail-closed —
	// only caller's own uid is accepted; any bot-impersonation refused.
	c := newTestCtx(t, map[string]any{"uid": "user_alice"})
	if !callerCanActAs(c, "spaceA", "user_alice") {
		t.Error("pre-v2 ctx: caller as self must still be allowed")
	}
	if callerCanActAs(c, "spaceA", "any_bot") {
		t.Error("pre-v2 ctx: bot-impersonation must be refused even though owned_bots is absent")
	}
}

func TestCallerCanActAs_DecisionFour_BotCallsAsSelf(t *testing.T) {
	// 决策四 compatibility: when bot subprocess holds bot_token, ctx.uid
	// IS the bot. callerCanActAs degenerates to "caller-as-self" — still
	// trivially allowed. v3 §3.4 doesn't need a rewrite after 决策四 lands.
	c := newTestCtx(t, map[string]any{"uid": "bot_decision_four"})
	if !callerCanActAs(c, "spaceA", "bot_decision_four") {
		t.Error("post-决策四: bot writing as itself must be allowed without owned_bots map")
	}
}
