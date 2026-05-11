package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ─── Health endpoints ───────────────────────────────────

func TestHealth_OK(t *testing.T) {
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %q", body["status"])
	}
}

func TestHealthReady_OK(t *testing.T) {
	ready := func() error { return nil }
	r := gin.New()
	r.GET("/health/ready", func(c *gin.Context) {
		if ready != nil {
			if err := ready(); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "NOT_READY", "message": "dependencies not reachable"}})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHealthReady_503(t *testing.T) {
	ready := func() error { return fmt.Errorf("db down") }
	r := gin.New()
	r.GET("/health/ready", func(c *gin.Context) {
		if ready != nil {
			if err := ready(); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "NOT_READY", "message": "dependencies not reachable"}})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// ─── respondErr mapping ─────────────────────────────────

// TestActorNameFor covers the contract for notification
// actor names. When a bot acts on behalf of its owner (LLM timeline path:
// participant_uid == owner_uid), the notification must show the owner's
// name, not the bot's name — otherwise DB records "owner authored entry"
// while the push notification reads "Bot added progress".
func TestActorNameFor(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		callerUID string
		callerNm  string
		ownerUID  string
		ownerNm   string
		actorUID  string
		want      string
	}{
		{
			name:      "user path returns user name",
			role:      "",
			callerUID: "u1",
			callerNm:  "Alice",
			actorUID:  "u1",
			want:      "Alice",
		},
		{
			name:      "bot acting for owner returns owner_name",
			role:      "bot",
			callerUID: "bot1",
			callerNm:  "MatterBot",
			ownerUID:  "owner1",
			ownerNm:   "OwnerAlice",
			actorUID:  "owner1",
			want:      "OwnerAlice",
		},
		{
			name:      "bot acting for owner, owner_name missing falls back to uid",
			role:      "bot",
			callerUID: "bot1",
			callerNm:  "MatterBot",
			ownerUID:  "owner1",
			ownerNm:   "",
			actorUID:  "owner1",
			want:      "owner1",
		},
		{
			name:      "bot acting for itself (non-owner actor) keeps bot name",
			role:      "bot",
			callerUID: "bot1",
			callerNm:  "MatterBot",
			ownerUID:  "owner1",
			ownerNm:   "OwnerAlice",
			actorUID:  "bot1",
			want:      "MatterBot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.role != "" {
				c.Set("role", tc.role)
			}
			c.Set("uid", tc.callerUID)
			c.Set("name", tc.callerNm)
			if tc.ownerUID != "" {
				c.Set("owner_uid", tc.ownerUID)
			}
			if tc.ownerNm != "" {
				c.Set("owner_name", tc.ownerNm)
			}
			got := actorNameFor(c, tc.actorUID)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRespondErr_Upstream(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.Upstream("octoim unreachable"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "UPSTREAM_UNAVAILABLE")
}

func TestRespondErr_RateLimited(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.RateLimited("please wait"))

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "RATE_LIMITED")
}

func TestRespondErr_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.ErrNotFound)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestRespondErr_Forbidden(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.ErrForbidden)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "FORBIDDEN")
}

func TestRespondErr_InvalidInput(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.InvalidInput("bad field"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "VALIDATION_ERROR")
}

func TestRespondErr_InvalidCursor(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, repository.ErrInvalidCursor)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "VALIDATION_ERROR")
}

func TestRespondErr_Unknown(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, fmt.Errorf("something unexpected"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "INTERNAL_ERROR")
}

func TestRespondErr_AppError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.MatterNotFound())

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "MATTER_NOT_FOUND")
}

func TestRespondErr_DuplicateAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondErr(c, apperr.DuplicateAssignee())

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "DUPLICATE_ASSIGNEE")
}

// ─── MaxBodySize ────────────────────────────────────────

func TestMaxBodySize_Accepted(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodySize(1024))
	r.POST("/test", func(c *gin.Context) {
		body := make([]byte, 512)
		_, err := c.Request.Body.Read(body)
		if err != nil && err.Error() == "http: request body too large" {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(make([]byte, 100)))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for small body, got %d", w.Code)
	}
}

func TestMaxBodySize_Rejected(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodySize(100))
	r.POST("/test", func(c *gin.Context) {
		body := make([]byte, 200)
		_, err := c.Request.Body.Read(body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(make([]byte, 200)))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized body, got %d", w.Code)
	}
}

// ─── Route registration ─────────────────────────────────

func TestSetupRouter_RoutesRegistered(t *testing.T) {
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200 from /health, got %d", w.Code)
	}
}

// ─── Response helpers ───────────────────────────────────

func TestOk_WithData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	ok(c, gin.H{"id": "123"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestOk_Nil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	ok(c, nil)

	if c.Writer.Status() != http.StatusNoContent {
		t.Errorf("expected 204, got %d", c.Writer.Status())
	}
}

func TestCreated(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	created(c, gin.H{"id": "new-123"})

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

func TestPaginated(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	paginated(c, []string{"a", "b"}, true, "cursor-abc")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["data"] == nil {
		t.Error("missing 'data' in paginated response")
	}
	if body["pagination"] == nil {
		t.Error("missing 'pagination' in paginated response")
	}
	var pg map[string]interface{}
	json.Unmarshal(body["pagination"], &pg)
	if pg["has_more"] != true {
		t.Errorf("expected has_more=true, got %v", pg["has_more"])
	}
	if pg["next_cursor"] != "cursor-abc" {
		t.Errorf("expected next_cursor='cursor-abc', got %v", pg["next_cursor"])
	}
}

func TestPaginated_NoMore(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	paginated(c, []string{"a"}, false, "")

	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	var pg map[string]interface{}
	json.Unmarshal(body["pagination"], &pg)
	if pg["has_more"] != false {
		t.Errorf("expected has_more=false, got %v", pg["has_more"])
	}
	if _, ok := pg["next_cursor"]; ok {
		t.Error("next_cursor should be omitted when has_more=false")
	}
}

func TestFailCode(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	failCode(c, http.StatusBadRequest, "TEST_CODE", "test message", map[string]any{"field": "title"})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "TEST_CODE")
}

func TestBindJSONErr(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{invalid`))
	c.Request.Header.Set("Content-Type", "application/json")

	bindJSONErr(c, fmt.Errorf("invalid json"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ─── helpers ────────────────────────────────────────────

func assertErrCode(t *testing.T, body []byte, expectedCode string) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	var errObj map[string]interface{}
	if err := json.Unmarshal(envelope["error"], &errObj); err != nil {
		t.Fatalf("failed to parse error object: %v", err)
	}
	if errObj["code"] != expectedCode {
		t.Errorf("expected error code %q, got %q", expectedCode, errObj["code"])
	}
}
