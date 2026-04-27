package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// newAuthServer returns an httptest.Server that stubs octo-auth endpoints.
// verifyUser / verifyBot are called for the respective paths; nil means 500.
func newAuthServer(verifyUser, verifyBot http.HandlerFunc) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/auth/verify-user", func(w http.ResponseWriter, r *http.Request) {
		if verifyUser != nil {
			verifyUser(w, r)
			return
		}
		http.Error(w, "not implemented", http.StatusInternalServerError)
	})
	mux.HandleFunc("/internal/v1/auth/verify-bot", func(w http.ResponseWriter, r *http.Request) {
		if verifyBot != nil {
			verifyBot(w, r)
			return
		}
		http.Error(w, "not implemented", http.StatusInternalServerError)
	})
	return httptest.NewServer(mux)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// doRequest runs a single request through the middleware and returns the recorder.
func doRequest(middleware gin.HandlerFunc, headers map[string]string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(middleware)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"uid":  c.GetString("uid"),
			"name": c.GetString("name"),
			"role": c.GetString("role"),
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(w, req)
	return w
}

// ---------- User auth tests ----------

func TestUserAuth_ValidToken(t *testing.T) {
	srv := newAuthServer(func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, userVerifyResponse{UID: "u1", Name: "Alice", Role: "admin"})
	}, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL, InternalKey: "secret"}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"token": "valid-tok"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["uid"] != "u1" || body["name"] != "Alice" || body["role"] != "admin" {
		t.Fatalf("unexpected context values: %v", body)
	}
}

func TestUserAuth_MissingToken(t *testing.T) {
	srv := newAuthServer(nil, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestUserAuth_AuthServerDown(t *testing.T) {
	// Point to a closed server.
	cfg := &config.Config{AuthURL: "http://127.0.0.1:1"}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"token": "tok"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestUserAuth_AuthServerNon200(t *testing.T) {
	srv := newAuthServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"token": "bad"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestUserAuth_InternalKeyHeader(t *testing.T) {
	var gotKey string
	srv := newAuthServer(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Internal-Key")
		jsonOK(w, userVerifyResponse{UID: "u1", Name: "A", Role: "user"})
	}, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL, InternalKey: "my-secret"}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"token": "tok"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if gotKey != "my-secret" {
		t.Fatalf("expected X-Internal-Key=my-secret, got %q", gotKey)
	}
}

// ---------- Bot auth tests ----------

func TestBotAuth_ValidToken(t *testing.T) {
	srv := newAuthServer(nil, func(w http.ResponseWriter, r *http.Request) {
		var req botVerifyRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.BotToken != "bf_robot1_key1" {
			http.Error(w, "unexpected token: "+req.BotToken, http.StatusBadRequest)
			return
		}
		jsonOK(w, botVerifyResponse{RobotID: "robot1", RobotName: "MyBot", SpaceID: "sp-1"})
	})
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL, InternalKey: "k"}

	r := gin.New()
	r.Use(NewAuthMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"uid":      c.GetString("uid"),
			"name":     c.GetString("name"),
			"role":     c.GetString("role"),
			"space_id": c.GetString("space_id"),
		})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bot robot1/key1")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["uid"] != "robot1" || body["name"] != "MyBot" || body["role"] != "bot" || body["space_id"] != "sp-1" {
		t.Fatalf("unexpected: %v", body)
	}
}

func TestBotAuth_InvalidFormat(t *testing.T) {
	srv := newAuthServer(nil, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"Authorization": "Bot noslash"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBotAuth_EmptyToken(t *testing.T) {
	srv := newAuthServer(nil, nil)
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"Authorization": "Bot "})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBotAuth_AuthServerDown(t *testing.T) {
	cfg := &config.Config{AuthURL: "http://127.0.0.1:1"}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"Authorization": "Bot r/k"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestBotAuth_AuthServerNon200(t *testing.T) {
	srv := newAuthServer(nil, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	})
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"Authorization": "Bot r/k"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBotAuth_MissingSpaceID(t *testing.T) {
	srv := newAuthServer(nil, func(w http.ResponseWriter, r *http.Request) {
		// Return empty space_id; no X-Space-ID header either.
		jsonOK(w, botVerifyResponse{RobotID: "r1", RobotName: "B", SpaceID: ""})
	})
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}
	w := doRequest(NewAuthMiddleware(cfg), map[string]string{"Authorization": "Bot r/k"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBotAuth_SpaceIDFallbackToHeader(t *testing.T) {
	srv := newAuthServer(nil, func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, botVerifyResponse{RobotID: "r1", RobotName: "B", SpaceID: ""})
	})
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}

	r := gin.New()
	r.Use(NewAuthMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bot r/k")
	req.Header.Set("X-Space-ID", "header-space")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["space_id"] != "header-space" {
		t.Fatalf("expected space_id=header-space, got %q", body["space_id"])
	}
}

func TestBotAuth_RobotNameFallback(t *testing.T) {
	srv := newAuthServer(nil, func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, botVerifyResponse{RobotID: "r1", RobotName: "", SpaceID: "sp"})
	})
	defer srv.Close()

	cfg := &config.Config{AuthURL: srv.URL}

	r := gin.New()
	r.Use(NewAuthMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"name": c.GetString("name")})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bot r1/k")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	// Fallback: name should be the robotID parsed from the token ("r1").
	if body["name"] != "r1" {
		t.Fatalf("expected name=r1 (fallback), got %q", body["name"])
	}
}

// ---------- SpaceMiddleware tests ----------

func TestSpaceMiddleware_HeaderPresent(t *testing.T) {
	r := gin.New()
	r.Use(SpaceMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Space-ID", "sp-42")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["space_id"] != "sp-42" {
		t.Fatalf("expected sp-42, got %q", body["space_id"])
	}
}

func TestSpaceMiddleware_MissingHeader(t *testing.T) {
	r := gin.New()
	r.Use(SpaceMiddleware())
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSpaceMiddleware_AlreadySet(t *testing.T) {
	r := gin.New()
	// Simulate bot auth already setting space_id.
	r.Use(func(c *gin.Context) {
		c.Set("space_id", "bot-space")
		c.Next()
	})
	r.Use(SpaceMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"space_id": c.GetString("space_id")})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// No X-Space-ID header — should pass because space_id is already set.
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["space_id"] != "bot-space" {
		t.Fatalf("expected bot-space, got %q", body["space_id"])
	}
}
