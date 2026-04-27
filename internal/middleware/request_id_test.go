package middleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func setupRequestIDRouter() *gin.Engine {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": c.GetString("request_id")})
	})
	return r
}

func TestRequestID_NoHeader_GeneratesUUID(t *testing.T) {
	r := setupRequestIDRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid == "" {
		t.Fatal("expected X-Request-ID header, got empty")
	}
	if !uuidRe.MatchString(rid) {
		t.Errorf("expected UUID format, got %q", rid)
	}
}

func TestRequestID_ValidHeader_Reuses(t *testing.T) {
	r := setupRequestIDRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "my-valid-id-123")
	r.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid != "my-valid-id-123" {
		t.Errorf("expected reused request ID 'my-valid-id-123', got %q", rid)
	}
}

func TestRequestID_TooLong_GeneratesNew(t *testing.T) {
	r := setupRequestIDRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", strings.Repeat("a", 129))
	r.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid == strings.Repeat("a", 129) {
		t.Error("expected new UUID for too-long request ID, but got the original")
	}
	if !uuidRe.MatchString(rid) {
		t.Errorf("expected UUID format, got %q", rid)
	}
}

func TestRequestID_SpecialChars_GeneratesNew(t *testing.T) {
	r := setupRequestIDRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces!")
	r.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	if rid == "bad id with spaces!" {
		t.Error("expected new UUID for invalid request ID, but got the original")
	}
	if !uuidRe.MatchString(rid) {
		t.Errorf("expected UUID format, got %q", rid)
	}
}

func TestRequestID_ResponseHeaderPresent(t *testing.T) {
	r := setupRequestIDRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") == "" {
		t.Error("response missing X-Request-ID header")
	}
}
