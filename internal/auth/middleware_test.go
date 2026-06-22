package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	octoauth "github.com/Mininglamp-OSS/octo-auth/sdk-go/auth"
	"github.com/Mininglamp-OSS/octo-auth/sdk-go/contract"
	"github.com/gin-gonic/gin"
)

// newAuthTestEngine builds a gin engine with AuthMiddleware pointed at
// the supplied octo-server mock + an echo handler that returns the
// matter legacy + SDK ctx keys so assertions can verify wiring.
func newAuthTestEngine(t *testing.T, h http.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := gin.New()
	r.Use(AuthMiddleware(Config{OctoIMURL: srv.URL}))
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"uid":        c.GetString("uid"),
			"name":       c.GetString("name"),
			"role":       c.GetString("role"),
			"owner_uid":  c.GetString("owner_uid"),
			"owner_name": c.GetString("owner_name"),
			"space_id":   c.GetString("space_id"),
			"ctx_incl":   octoauth.IsContextIncluded(c),
			"sdk_spaces": octoauth.GetVerifiedSpaces(c),
		})
	})
	return r
}

// TestAuthMiddleware_User_HappyPath pins the user-token path: SDK ctx
// keys are set so SpaceMiddlewareWithSDK can enforce on
// IsContextIncluded + GetVerifiedSpaces (Jerry-Xin P0 review on #87).
func TestAuthMiddleware_User_HappyPath(t *testing.T) {
	r := newAuthTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(contract.VerifyUserResp{
			SchemaVersion:    1, Kind: "user", UID: "u1", Name: "Alice", Role: "admin",
			ContextIncluded:  true,
			Spaces:           []string{"sp_a", "sp_b"},
			OwnedBotsBySpace: map[string][]string{"sp_a": {"b1"}},
		}); err != nil {
			t.Errorf("mock encode: %v", err)
		}
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("token", "user-tok-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp["uid"] != "u1" || resp["name"] != "Alice" || resp["role"] != "admin" {
		t.Fatalf("legacy keys wrong: %+v", resp)
	}
	if !resp["ctx_incl"].(bool) {
		t.Fatalf("SDK CtxKeyContextIncluded not set; SpaceMiddlewareWithSDK would silently no-op")
	}
	spaces, _ := resp["sdk_spaces"].([]any)
	if len(spaces) != 2 {
		t.Fatalf("SDK CtxKeyVerifiedSpaces wrong: %+v", spaces)
	}
}

// TestAuthMiddleware_Bot_HappyPath pins the bot-token path: SDK ctx
// keys set for Scope="space" bots so RequireSpaceMember enforces
// against the bot binding.
func TestAuthMiddleware_Bot_HappyPath(t *testing.T) {
	r := newAuthTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(contract.VerifyBotResp{
			SchemaVersion: 1, Kind: "bot", BotUID: "b1", BotName: "Bot",
			BotKind: "app", Scope: "space", SpaceID: "sp_A",
			OwnerUID: "u1", OwnerName: "Owner",
		}); err != nil {
			t.Errorf("mock encode: %v", err)
		}
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer app_a_real_app_bot_token_here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp["uid"] != "b1" || resp["role"] != "bot" || resp["owner_uid"] != "u1" || resp["owner_name"] != "Owner" {
		t.Fatalf("bot legacy keys wrong: %+v", resp)
	}
	if resp["space_id"] != "sp_A" {
		t.Fatalf("bot space_id wrong: %+v", resp)
	}
	if !resp["ctx_incl"].(bool) {
		t.Fatalf("SDK CtxKeyContextIncluded not set for space-scoped bot")
	}
	spaces, _ := resp["sdk_spaces"].([]any)
	if len(spaces) != 1 || spaces[0] != "sp_A" {
		t.Fatalf("SDK CtxKeyVerifiedSpaces for bot wrong: %+v", spaces)
	}
}

// TestAuthMiddleware_NoToken_401 pins the no-token path.
func TestAuthMiddleware_NoToken_401(t *testing.T) {
	r := newAuthTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		// shouldn't be called
		w.WriteHeader(http.StatusInternalServerError)
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-token: status=%d want 401", w.Code)
	}
}

// TestAuthMiddleware_APIKey_HappyPath pins the API-key path: SDK ctx
// keys plumbed for SpaceMiddlewareWithSDK + the verified-spaces list
// includes the bound space_id when it's not already in OwnedBotsBySpace
// (OctoBoooot review on #86: handleAPIKey was uncovered and the
// merge-dedup logic was easy to break unnoticed).
func TestAuthMiddleware_APIKey_HappyPath(t *testing.T) {
	r := newAuthTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(contract.VerifyAPIKeyResp{
			SchemaVersion: 1, Kind: "apikey",
			UID:              "u_owner",
			KeyID:            "k1",
			SpaceID:          "sp_bound",
			ContextIncluded:  true,
			OwnedBotsBySpace: map[string][]string{"sp_a": {"b1"}, "sp_b": {"b2"}},
		}); err != nil {
			t.Errorf("mock encode: %v", err)
		}
	})
	apiKey := "uk_" + strings.Repeat("k", 32)
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp["uid"] != "u_owner" || resp["space_id"] != "sp_bound" {
		t.Fatalf("api-key legacy keys wrong: %+v", resp)
	}
	if !resp["ctx_incl"].(bool) {
		t.Fatalf("SDK CtxKeyContextIncluded not set for api-key path")
	}
	spaces, _ := resp["sdk_spaces"].([]any)
	got := map[string]bool{}
	for _, s := range spaces {
		got[s.(string)] = true
	}
	if !got["sp_a"] || !got["sp_b"] || !got["sp_bound"] {
		t.Fatalf("verified spaces should include OwnedBotsBySpace keys + bound SpaceID; got %+v", spaces)
	}
}

// TestRespondSDKError_StatusMapping pins the SDK sentinel →
// matter envelope (status code) mapping. OctoBoooot review on #86:
// the mapping was untested and "could silently flip a 401 to a
// 5xx with a future SDK error rename."
func TestRespondSDKError_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"token-invalid → 401", octoauth.ErrTokenInvalid, http.StatusUnauthorized},
		{"token-missing → 401", octoauth.ErrTokenMissing, http.StatusUnauthorized},
		{"kind-mismatch → 401", octoauth.ErrKindMismatch, http.StatusUnauthorized},
		{"bot-unavailable → 503", octoauth.ErrBotUnavailable, http.StatusServiceUnavailable},
		{"upstream-down → 503", octoauth.ErrUpstreamUnavailable, http.StatusServiceUnavailable},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.GET("/x", func(c *gin.Context) {
				respondSDKError(c, tc.err)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil)
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d want %d (body=%s)", w.Code, tc.status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("body missing nested error envelope: %s", w.Body.String())
			}
		})
	}
}

// TestSpaceMiddlewareWithSDK_FailClosed asserts the composite gate
// rejects an X-Space-Id that the verify-context says the caller does
// NOT belong to.
func TestSpaceMiddlewareWithSDK_FailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(octoauth.CtxKeyContextIncluded, true)
		c.Set(octoauth.CtxKeyVerifiedSpaces, []string{"sp_A"})
		c.Next()
	})
	r.Use(SpaceMiddlewareWithSDK(Config{OctoIMURL: ""}))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Space-Id", "sp_B")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("X-Space-Id forgery must 403; got %d (body: %s)", w.Code, w.Body.String())
	}
}

// TestSpaceMiddlewareWithSDK_VerifiedMemberPasses confirms positive control.
func TestSpaceMiddlewareWithSDK_VerifiedMemberPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(octoauth.CtxKeyContextIncluded, true)
		c.Set(octoauth.CtxKeyVerifiedSpaces, []string{"sp_A", "sp_B"})
		c.Next()
	})
	r.Use(SpaceMiddlewareWithSDK(Config{OctoIMURL: ""}))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Space-Id", "sp_B")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("verified member must pass; got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["space_id"] != "sp_B" {
		t.Fatalf("space_id not set: %v", resp)
	}
}

// TestSpaceMiddlewareWithSDK_CompatPassesWithoutContext confirms the
// compatibility window for pre-v1 octo-server.
func TestSpaceMiddlewareWithSDK_CompatPassesWithoutContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SpaceMiddlewareWithSDK(Config{OctoIMURL: ""}))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Space-Id", "anything")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("compat mode (no verify-context) must pass through; got %d (body: %s)", w.Code, w.Body.String())
	}
}
