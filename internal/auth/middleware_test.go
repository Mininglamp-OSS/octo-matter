package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			"uid":         c.GetString("uid"),
			"name":        c.GetString("name"),
			"role":        c.GetString("role"),
			"owner_uid":   c.GetString("owner_uid"),
			"owner_name":  c.GetString("owner_name"),
			"space_id":    c.GetString("space_id"),
			"ctx_incl":    octoauth.IsContextIncluded(c),
			"sdk_spaces":  octoauth.GetVerifiedSpaces(c),
		})
	})
	return r
}

// TestAuthMiddleware_User_HappyPath pins the user-token path: SDK ctx
// keys are set so SpaceMiddlewareWithSDK can enforce on
// IsContextIncluded + GetVerifiedSpaces (Jerry-Xin P0 review on #87).
func TestAuthMiddleware_User_HappyPath(t *testing.T) {
	r := newAuthTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(contract.VerifyUserResp{
			SchemaVersion:    1, Kind: "user", UID: "u1", Name: "Alice", Role: "admin",
			ContextIncluded:  true,
			Spaces:           []string{"sp_a", "sp_b"},
			OwnedBotsBySpace: map[string][]string{"sp_a": {"b1"}},
		})
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("token", "user-tok-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
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
		_ = json.NewEncoder(w).Encode(contract.VerifyBotResp{
			SchemaVersion: 1, Kind: "bot", BotUID: "b1", BotName: "Bot",
			BotKind: "app", Scope: "space", SpaceID: "sp_A",
			OwnerUID: "u1", OwnerName: "Owner",
		})
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer app_a_real_app_bot_token_here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
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
