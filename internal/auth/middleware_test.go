package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
)

func TestSpaceMiddleware_MissingHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	SpaceMiddleware()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSpaceMiddleware_WithHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("X-Space-ID", "sp-123")

	SpaceMiddleware()(c)

	if got := c.GetString("space_id"); got != "sp-123" {
		t.Errorf("space_id = %q, want sp-123", got)
	}
}

func TestSpaceMiddleware_SkipsIfAlreadySet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("space_id", "pre-set")

	SpaceMiddleware()(c)

	if got := c.GetString("space_id"); got != "pre-set" {
		t.Errorf("space_id = %q, want pre-set", got)
	}
}

func TestNewAuthMiddleware_ReturnsHandler(t *testing.T) {
	cfg := &config.Config{
		AuthURL:     "http://localhost:9999",
		InternalKey: "test-key",
	}
	mw := NewAuthMiddleware(cfg)
	if mw == nil {
		t.Fatal("NewAuthMiddleware returned nil")
	}
}
