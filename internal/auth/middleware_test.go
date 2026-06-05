package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOctoServer simulates octo-server's verify-* endpoints. Each path
// returns a fixed response, or 401 if the token is in the rejects set.
// callCount tracks how many times each endpoint is hit (for cache tests).
type mockOctoServer struct {
	*httptest.Server
	apiKeyCalls  int32
	botCalls     int32
	userCalls    int32
	rejectsAPI   map[string]bool
	rejectsBot   map[string]bool
	rejectsUser  map[string]bool
}

func newMockOctoServer() *mockOctoServer {
	m := &mockOctoServer{
		rejectsAPI:  make(map[string]bool),
		rejectsBot:  make(map[string]bool),
		rejectsUser: make(map[string]bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/verify-api-key", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.apiKeyCalls, 1)
		var req struct {
			APIKey string `json:"api_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if m.rejectsAPI[req.APIKey] || !strings.HasPrefix(req.APIKey, "uk_") {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"uid":      "user_for_" + req.APIKey,
			"space_id": "space_for_" + req.APIKey,
		})
	})
	mux.HandleFunc("/v1/auth/verify-bot", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.botCalls, 1)
		var req struct {
			BotToken string `json:"bot_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if m.rejectsBot[req.BotToken] {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(verifyBotResp{
			BotUID:   "bot_for_" + req.BotToken,
			BotName:  "Bot",
			OwnerUID: "owner",
			SpaceID:  "space_bot",
		})
	})
	mux.HandleFunc("/v1/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.userCalls, 1)
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if m.rejectsUser[req.Token] {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(verifyTokenResp{
			UID:  "user_for_" + req.Token,
			Name: "User",
			Role: "member",
		})
	})
	m.Server = httptest.NewServer(mux)
	return m
}

// newTestRouter mounts AuthMiddleware (and optionally RequireKind) on a
// /probe endpoint that echoes the injected context keys.
func newTestRouter(srvURL string, requireKinds ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mws := []gin.HandlerFunc{AuthMiddleware(Config{OctoIMURL: srvURL})}
	if len(requireKinds) > 0 {
		mws = append(mws, RequireKind(requireKinds...))
	}
	mws = append(mws, func(c *gin.Context) {
		uid, _ := c.Get("uid")
		spaceID, _ := c.Get("space_id")
		kind, _ := c.Get("auth_kind")
		c.JSON(200, gin.H{"uid": uid, "space_id": spaceID, "auth_kind": kind})
	})
	r.GET("/probe", mws...)
	return r
}

// Case: valid api_key (Bearer uk_<≥32 chars>) → 200 + auth_kind=apikey.
func TestAuthMiddleware_APIKey_Valid(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	apiKey := "uk_" + strings.Repeat("a", 32) // 35 chars
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "user_for_"+apiKey, resp["uid"])
	assert.Equal(t, "space_for_"+apiKey, resp["space_id"])
	assert.Equal(t, AuthKindAPIKey, resp["auth_kind"])
	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.apiKeyCalls))
}

// Case: api_key too short (< 35 chars) → falls through to handleBotAuth
// (合并 plan §4 prefix 严格性: 长度校验防 "uk_" + 空字符串绕过).
// mock verify-bot will accept it since we didn't reject it; verify it
// went to bot endpoint not api-key endpoint.
func TestAuthMiddleware_APIKey_TooShort_FallsThroughToBot(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	shortToken := "uk_short" // 8 chars, below 35
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+shortToken)
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code) // server verify-bot accepted it
	assert.Equal(t, int32(0), atomic.LoadInt32(&srv.apiKeyCalls), "api-key endpoint should NOT be hit")
	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.botCalls), "bot endpoint should be hit instead")
}

// Case: server rejects api_key → 401.
func TestAuthMiddleware_APIKey_ServerRejects(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	apiKey := "uk_" + strings.Repeat("b", 32)
	srv.rejectsAPI[apiKey] = true
	r := newTestRouter(srv.URL)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

// Case: cache hit on second call (no extra server round trip).
func TestAuthMiddleware_APIKey_CacheHit(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	apiKey := "uk_" + strings.Repeat("c", 32)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/probe", nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.apiKeyCalls),
		"3 requests with same key should hit server exactly once (cache TTL 60s)")
}

// Case: bot_token (Bearer bf_...) → handleBotAuth → 200 + auth_kind=bot.
func TestAuthMiddleware_BotToken(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer bf_botxxx")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, AuthKindBot, resp["auth_kind"])
}

// Case: session token (token: header) → handleUserAuth → 200 + auth_kind=session.
func TestAuthMiddleware_Session(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("token", "sess_xxx")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, AuthKindSession, resp["auth_kind"])
}

// Case: no auth header at all → 401.
func TestAuthMiddleware_MissingAuth(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

// Case: RequireKind("apikey") + caller is bot → 403 AUTH_KIND_NOT_ALLOWED.
func TestRequireKind_RejectsWrongKind(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL, AuthKindAPIKey) // only apikey allowed

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer bf_botxxx") // bot, not api_key
	r.ServeHTTP(w, req)

	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_KIND_NOT_ALLOWED")
}

// Case: RequireKind("apikey") + caller is api_key → 200.
func TestRequireKind_AllowsMatchingKind(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL, AuthKindAPIKey)

	apiKey := "uk_" + strings.Repeat("d", 32)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
}

// Case: RequireKind accepts multiple kinds.
func TestRequireKind_MultipleAllowed(t *testing.T) {
	srv := newMockOctoServer()
	defer srv.Close()
	r := newTestRouter(srv.URL, AuthKindSession, AuthKindBot)

	// Session caller: allowed.
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/probe", nil)
	req1.Header.Set("token", "sess_xxx")
	r.ServeHTTP(w1, req1)
	assert.Equal(t, 200, w1.Code)

	// Bot caller: allowed.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/probe", nil)
	req2.Header.Set("Authorization", "Bearer bf_botxxx")
	r.ServeHTTP(w2, req2)
	assert.Equal(t, 200, w2.Code)

	// api_key caller: rejected.
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/probe", nil)
	req3.Header.Set("Authorization", "Bearer uk_"+strings.Repeat("e", 32))
	r.ServeHTTP(w3, req3)
	assert.Equal(t, 403, w3.Code)
}
