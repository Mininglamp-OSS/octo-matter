// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// spyIMChecker tracks calls and returns canned results.
type spyIMChecker struct {
	calls   int
	allow   bool
	err     error
	gotChan string
	gotUIDs []string
}

func (s *spyIMChecker) IsChannelMember(_ context.Context, _, channelID string, userIDs []string) (bool, error) {
	s.calls++
	s.gotChan = channelID
	s.gotUIDs = userIDs
	return s.allow, s.err
}

func newAccessSvc(matter *model.Matter, im *spyIMChecker, accessGrants map[string]bool) *MatterService {
	matterRepo := &hasAccessFakeMatterRepo{
		fakeMatterRepo: *newFakeMatterRepo(matter),
		accessGrants:   accessGrants,
	}
	return NewMatterService(matterRepo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
}

func TestCanAccessMatter_Creator_SkipsIM(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "creator", Status: model.MatterStatusOpen}
	im := &spyIMChecker{allow: true}
	svc := newAccessSvc(matter, im, nil)

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"creator"}, "ch1", "user-tok")
	if err != nil || !ok {
		t.Fatalf("creator should pass without IM check, got ok=%v err=%v", ok, err)
	}
	if im.calls != 0 {
		t.Fatalf("creator fast-path must not call IM, got %d calls", im.calls)
	}
}

func TestCanAccessMatter_NoChannelID_SkipsIM(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: true}
	svc := newAccessSvc(matter, im, map[string]bool{"m1:user:": true}) // assignee path

	_, err := svc.CanAccessMatter(context.Background(), matter, []string{"user"}, "", "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 0 {
		t.Fatalf("empty channelID must not call IM, got %d", im.calls)
	}
}

// TestCanAccessMatter_BotPath_DropsChannelClaim verifies the post-r4259071422
// tightening: a bot caller (callerToken=="") cannot use channel_id to gain
// access. IM is still skipped (no token to forward), but the channelID is
// dropped before reaching HasAccess so authz must succeed via creator /
// assignee / participant only. Defense-in-depth — even a leaked bot token
// cannot enumerate matters via channel-link.
func TestCanAccessMatter_BotPath_DropsChannelClaim(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: false}
	// Grant access ONLY via channel-link (m1:bot:ch1). With the channel claim
	// dropped, this should NOT match.
	svc := newAccessSvc(matter, im, map[string]bool{"m1:bot:ch1": true})

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"bot"}, "ch1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("bot path must NOT get access via channel-link — defense-in-depth")
	}
	if im.calls != 0 {
		t.Fatalf("bot path must not call IM (no token to forward), got %d", im.calls)
	}
}

// TestCanAccessMatter_BotPath_AssigneeStillAllows confirms that bots with a
// legitimate non-channel access path (creator/assignee/participant) still get
// in — only the channel-link bypass is closed.
func TestCanAccessMatter_BotPath_AssigneeStillAllows(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: false}
	// Grant via assignee (matterID:uid:""). Channel claim should be dropped
	// for the bot, but assignee path remains.
	svc := newAccessSvc(matter, im, map[string]bool{"m1:bot:": true})

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"bot"}, "ch1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("bot path via assignee must still allow")
	}
}

func TestCanAccessMatter_UserPath_IMHit_ChannelLinkAllows(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: true}
	svc := newAccessSvc(matter, im, map[string]bool{"m1:viewer:ch1": true}) // grant via channel-link

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"viewer"}, "ch1", "user-tok")
	if err != nil || !ok {
		t.Fatalf("IM-verified channel member should be allowed, got ok=%v err=%v", ok, err)
	}
	if im.calls != 1 {
		t.Fatalf("expected 1 IM call, got %d", im.calls)
	}
	if im.gotChan != "ch1" {
		t.Fatalf("IM channelID forward mismatch: %q", im.gotChan)
	}
}

func TestCanAccessMatter_UserPath_IMMiss_DropsChannelButAssigneeStillAllows(t *testing.T) {
	// IM says caller is NOT a channel member. We must drop the channel-link
	// claim from the access query — but if caller is independently assignee/
	// participant, they still get access.
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: false}
	// Grant only via assignee (matterID:uid:""), NOT via channel
	svc := newAccessSvc(matter, im, map[string]bool{"m1:viewer:": true})

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"viewer"}, "ch1", "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("IM miss should still allow assignee — channel claim dropped, assignee path remains")
	}
}

func TestCanAccessMatter_UserPath_IMMiss_NoOtherAccess_Denies(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{allow: false}
	// Only channel grants access — once IM denies, drop channel = no access at all
	svc := newAccessSvc(matter, im, map[string]bool{"m1:stranger:ch1": true})

	ok, err := svc.CanAccessMatter(context.Background(), matter, []string{"stranger"}, "ch1", "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("IM-denied caller without other access path must be denied — Blocking 1 fix")
	}
}

func TestCanAccessMatter_UserPath_IMError_PropagatesAsUpstream(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	im := &spyIMChecker{err: errors.New("dial tcp: timeout")}
	svc := newAccessSvc(matter, im, nil)

	_, err := svc.CanAccessMatter(context.Background(), matter, []string{"viewer"}, "ch1", "user-tok")
	if err == nil {
		t.Fatalf("expected error when IM fails")
	}
	ae, ok := apperr.AsAppError(err)
	if !ok {
		t.Fatalf("expected AppError, got %v", err)
	}
	if ae.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", ae.HTTPStatus())
	}
}

// --- ListMatters source_channel_id IM gating (PR #34 review r4259038759) ---

func newListSvc(im *spyIMChecker) *MatterService {
	return NewMatterService(newFakeMatterRepo(), newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
}

// TestListMatters_SourceChannelFilter_UserPath_IMHit_KeepsFilter: when IM
// confirms membership the channel filter survives into the repo query.
func TestListMatters_SourceChannelFilter_UserPath_IMHit_KeepsFilter(t *testing.T) {
	im := &spyIMChecker{allow: true}
	svc := newListSvc(im)
	channel := "ch-known"
	filter := repository.MatterFilter{
		CallerUIDs:      []string{"u1"},
		SourceChannelID: &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 1 {
		t.Fatalf("expected 1 IM call, got %d", im.calls)
	}
	if im.gotChan != "ch-known" {
		t.Fatalf("IM channel mismatch: %q", im.gotChan)
	}
}

// TestListMatters_SourceChannelFilter_UserPath_IMMiss_DropsFilter: an
// IM-denied user has the channel filter stripped before reaching the repo —
// they still see matters via creator/assignee/participant filters, but cannot
// pivot through a known/guessed channel id. Closes the same class of bug as
// Blocking 1 in the list endpoint.
func TestListMatters_SourceChannelFilter_UserPath_IMMiss_DropsFilter(t *testing.T) {
	im := &spyIMChecker{allow: false}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	channel := "ch-leaked"
	filter := repository.MatterFilter{
		CallerUIDs:      []string{"stranger"},
		SourceChannelID: &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotFilter.SourceChannelID != nil {
		t.Fatalf("IM-denied caller must NOT receive channel filter; got %q", *repo.gotFilter.SourceChannelID)
	}
}

// TestListMatters_BotPath_DropsChannelFilter mirrors the canAccessMatter
// tightening from r4259071422: bots cannot use the channel filter to enumerate
// matters they don't otherwise have access to. The IM check is skipped (no
// token to forward), and the channel filter is stripped before reaching the
// repo. Bot still sees matters via creator/assignee/participant.
func TestListMatters_BotPath_DropsChannelFilter(t *testing.T) {
	im := &spyIMChecker{allow: false}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	channel := "ch1"
	filter := repository.MatterFilter{
		CallerUIDs:      []string{"bot"},
		SourceChannelID: &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 0 {
		t.Fatalf("bot path must not call IM (no token to forward), got %d", im.calls)
	}
	if repo.gotFilter.SourceChannelID != nil {
		t.Fatalf("bot path must DROP channel filter — got %q", *repo.gotFilter.SourceChannelID)
	}
}

// --- ListMatters ChannelID (linked-channel) IM gating ---
//
// channel_id is a strict result filter (AND): only matters linked via
// matter_channels to the given channel are returned. Same IM-membership
// gating as source_channel_id — bot path drops; user path keeps only when
// IM confirms membership; IM error returns 503.

// TestListMatters_ChannelFilter_UserPath_IMHit_KeepsFilter: IM hit ⇒ filter
// is forwarded to the repo.
func TestListMatters_ChannelFilter_UserPath_IMHit_KeepsFilter(t *testing.T) {
	im := &spyIMChecker{allow: true}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	channel := "ch-known"
	filter := repository.MatterFilter{
		CallerUIDs: []string{"u1"},
		ChannelID:  &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 1 {
		t.Fatalf("expected 1 IM call, got %d", im.calls)
	}
	if im.gotChan != "ch-known" {
		t.Fatalf("IM channel mismatch: %q", im.gotChan)
	}
	if repo.gotFilter.ChannelID == nil || *repo.gotFilter.ChannelID != "ch-known" {
		t.Fatalf("IM-verified caller must keep channel filter on repo; got %v", repo.gotFilter.ChannelID)
	}
}

// TestListMatters_ChannelFilter_UserPath_IMMiss_DropsFilter: IM-denied user
// has the channel filter stripped before reaching the repo. Without it,
// ListBySpace falls back to creator/assignee/participant visibility — caller
// cannot enumerate matters in a channel they do not belong to.
func TestListMatters_ChannelFilter_UserPath_IMMiss_DropsFilter(t *testing.T) {
	im := &spyIMChecker{allow: false}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	channel := "ch-leaked"
	filter := repository.MatterFilter{
		CallerUIDs: []string{"stranger"},
		ChannelID:  &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotFilter.ChannelID != nil {
		t.Fatalf("IM-denied caller must NOT receive channel filter; got %q", *repo.gotFilter.ChannelID)
	}
}

// TestListMatters_BotPath_DropsChannelIDFilter: bot caller (empty token)
// cannot use channel_id to enumerate — IM is skipped (no token), filter
// stripped before repo.
func TestListMatters_BotPath_DropsChannelIDFilter(t *testing.T) {
	im := &spyIMChecker{allow: false}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	channel := "ch1"
	filter := repository.MatterFilter{
		CallerUIDs: []string{"bot"},
		ChannelID:  &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 0 {
		t.Fatalf("bot path must not call IM, got %d", im.calls)
	}
	if repo.gotFilter.ChannelID != nil {
		t.Fatalf("bot path must DROP channel filter — got %q", *repo.gotFilter.ChannelID)
	}
}

// TestListMatters_ChannelFilter_IMError_PropagatesUpstream: transient IM
// failures fail-closed as 503 instead of silently broadening the result set.
func TestListMatters_ChannelFilter_IMError_PropagatesUpstream(t *testing.T) {
	im := &spyIMChecker{err: errors.New("dial tcp: timeout")}
	svc := newListSvc(im)
	channel := "ch1"
	filter := repository.MatterFilter{
		CallerUIDs: []string{"u1"},
		ChannelID:  &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err == nil {
		t.Fatalf("expected error when IM fails")
	}
	ae, ok := apperr.AsAppError(err)
	if !ok || ae.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 UPSTREAM_UNAVAILABLE, got %v", err)
	}
}

// TestListMatters_BothChannelIDs_BothVerifiedSurvive: review feedback on PR
// #41 — when source_channel_id and channel_id are both supplied and the
// caller is a verified member of both, both must reach the repo so the
// visibility OR can unlock either channel. Guards the dual-membership case
// where a matter linked only to channel_id (chB) was previously invisible
// because SourceChannelID (chA) took priority in the visibility branch.
func TestListMatters_BothChannelIDs_BothVerifiedSurvive(t *testing.T) {
	im := &spyIMChecker{allow: true}
	repo := &captureMatterRepo{fakeMatterRepo: *newFakeMatterRepo()}
	svc := NewMatterService(repo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	chA := "ch-A"
	chB := "ch-B"
	filter := repository.MatterFilter{
		CallerUIDs:      []string{"u1"},
		SourceChannelID: &chA,
		ChannelID:       &chB,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 2 {
		t.Fatalf("expected 2 IM calls (one per channel), got %d", im.calls)
	}
	if repo.gotFilter.SourceChannelID == nil || *repo.gotFilter.SourceChannelID != chA {
		t.Fatalf("verified SourceChannelID must reach repo; got %v", repo.gotFilter.SourceChannelID)
	}
	if repo.gotFilter.ChannelID == nil || *repo.gotFilter.ChannelID != chB {
		t.Fatalf("verified ChannelID must reach repo; got %v", repo.gotFilter.ChannelID)
	}
}

// TestListMatters_NoSourceChannel_SkipsIM: no IM call when no channel filter
// is requested.
func TestListMatters_NoSourceChannel_SkipsIM(t *testing.T) {
	im := &spyIMChecker{allow: true}
	svc := newListSvc(im)
	filter := repository.MatterFilter{CallerUIDs: []string{"u1"}}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if im.calls != 0 {
		t.Fatalf("no channel filter ⇒ no IM call, got %d", im.calls)
	}
}

// TestListMatters_SourceChannelFilter_IMError_PropagatesUpstream: transient
// IM failures surface as apperr.Upstream (HTTP 503) instead of silently
// leaking matters to a maybe-not-member caller (fail-closed).
func TestListMatters_SourceChannelFilter_IMError_PropagatesUpstream(t *testing.T) {
	im := &spyIMChecker{err: errors.New("dial tcp: timeout")}
	svc := newListSvc(im)
	channel := "ch1"
	filter := repository.MatterFilter{
		CallerUIDs:      []string{"u1"},
		SourceChannelID: &channel,
	}
	_, err := svc.ListMatters(context.Background(), "sp1", filter, "user-tok")
	if err == nil {
		t.Fatalf("expected error when IM fails")
	}
	ae, ok := apperr.AsAppError(err)
	if !ok || ae.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 UPSTREAM_UNAVAILABLE, got %v", err)
	}
}

// --- RequireChannelMember unit tests (PR #34 review r4259131241) ---

// TestRequireChannelMember covers the helper that gates matter_channels
// writes. Returns nil for empty channelID OR empty callerToken (bot path —
// caller decides policy); IM hit → nil; IM miss → 403; IM err → 503.
func TestRequireChannelMember(t *testing.T) {
	t.Run("empty channelID is no-op", func(t *testing.T) {
		im := &spyIMChecker{allow: true}
		svc := newAccessSvc(&model.Matter{ID: "x", SpaceID: "sp", CreatorID: "c"}, im, nil)
		if err := svc.RequireChannelMember(context.Background(), "tok", "", []string{"u1"}); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if im.calls != 0 {
			t.Fatalf("empty channelID must skip IM, got %d calls", im.calls)
		}
	})
	t.Run("empty callerToken (bot path) is no-op", func(t *testing.T) {
		im := &spyIMChecker{allow: false}
		svc := newAccessSvc(&model.Matter{ID: "x", SpaceID: "sp", CreatorID: "c"}, im, nil)
		if err := svc.RequireChannelMember(context.Background(), "", "ch", []string{"u1"}); err != nil {
			t.Fatalf("expected nil for bot path, got %v", err)
		}
		if im.calls != 0 {
			t.Fatalf("bot path must skip IM, got %d calls", im.calls)
		}
	})
	t.Run("IM hit returns nil", func(t *testing.T) {
		im := &spyIMChecker{allow: true}
		svc := newAccessSvc(&model.Matter{ID: "x", SpaceID: "sp", CreatorID: "c"}, im, nil)
		if err := svc.RequireChannelMember(context.Background(), "tok", "ch", []string{"u1"}); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("IM miss returns 403 Forbidden", func(t *testing.T) {
		im := &spyIMChecker{allow: false}
		svc := newAccessSvc(&model.Matter{ID: "x", SpaceID: "sp", CreatorID: "c"}, im, nil)
		err := svc.RequireChannelMember(context.Background(), "tok", "ch", []string{"u1"})
		if !errors.Is(err, apperr.ErrForbidden) {
			t.Fatalf("expected Forbidden, got %v", err)
		}
	})
	t.Run("IM error returns 503 Upstream", func(t *testing.T) {
		im := &spyIMChecker{err: errors.New("dial tcp: timeout")}
		svc := newAccessSvc(&model.Matter{ID: "x", SpaceID: "sp", CreatorID: "c"}, im, nil)
		err := svc.RequireChannelMember(context.Background(), "tok", "ch", []string{"u1"})
		ae, ok := apperr.AsAppError(err)
		if !ok || ae.HTTPStatus() != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Upstream, got %v", err)
		}
	})
}

// captureMatterRepo records the filter ListBySpace was called with so tests
// can assert what was actually passed to the repo (e.g. SourceChannelID
// dropped on IM miss).
type captureMatterRepo struct {
	fakeMatterRepo
	gotFilter repository.MatterFilter
}

func (r *captureMatterRepo) ListBySpace(ctx context.Context, spaceID string, filter repository.MatterFilter) ([]*model.Matter, bool, error) {
	r.gotFilter = filter
	return r.fakeMatterRepo.ListBySpace(ctx, spaceID, filter)
}
