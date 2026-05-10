package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

// --- stub activity service ------------------------------------------------

type stubActivitySvc struct {
	items   []*model.MatterActivity
	hasMore bool
	err     error
}

func (s *stubActivitySvc) ListActivities(
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	cursor *string,
	limit int,
) ([]*model.MatterActivity, bool, error) {
	return s.items, s.hasMore, s.err
}

// --- helper: build a router with the activity handler only ----------------

func newActivityTestRouter(svc activityLister) *gin.Engine {
	r := gin.New()
	h := NewActivityHandler(svc)
	r.GET("/api/v1/matters/:id/activities", func(c *gin.Context) {
		c.Set("space_id", "sp1")
		c.Set("uid", "u1")
		c.Set("related_uids", []string{"u1"})
		h.List(c)
	})
	return r
}

// --- Tests ----------------------------------------------------------------

func TestActivityHandler_List_HappyPath(t *testing.T) {
	now := time.Now()
	detail := model.JSONDetail(`{"from":"open","to":"closed"}`)
	svc := &stubActivitySvc{
		items: []*model.MatterActivity{
			{
				ID:        "act-1",
				MatterID:  "11111111-1111-1111-1111-111111111111",
				ActorID:   "u1",
				Action:    "status_changed",
				Detail:    detail,
				CreatedAt: now,
			},
		},
		hasMore: false,
	}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/11111111-1111-1111-1111-111111111111/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body["data"] == nil {
		t.Error("missing 'data' field")
	}
	if body["pagination"] == nil {
		t.Error("missing 'pagination' field")
	}
	var pg map[string]interface{}
	json.Unmarshal(body["pagination"], &pg)
	if pg["has_more"] != false {
		t.Errorf("expected has_more=false, got %v", pg["has_more"])
	}
}

func TestActivityHandler_List_InvalidUUID_Returns400(t *testing.T) {
	svc := &stubActivitySvc{}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/not-a-uuid/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "VALIDATION_ERROR")
}

func TestActivityHandler_List_EmptyList(t *testing.T) {
	svc := &stubActivitySvc{items: []*model.MatterActivity{}, hasMore: false}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/22222222-2222-2222-2222-222222222222/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	var items []json.RawMessage
	json.Unmarshal(body["data"], &items)
	if len(items) != 0 {
		t.Errorf("expected empty data array, got %d items", len(items))
	}
}

func TestActivityHandler_List_HasMore_NextCursorPresent(t *testing.T) {
	now := time.Now()
	svc := &stubActivitySvc{
		items: []*model.MatterActivity{
			{ID: "a1", MatterID: "33333333-3333-3333-3333-333333333333", ActorID: "u1", Action: "created", CreatedAt: now},
		},
		hasMore: true,
	}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/33333333-3333-3333-3333-333333333333/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	var pg map[string]interface{}
	json.Unmarshal(body["pagination"], &pg)
	if pg["has_more"] != true {
		t.Errorf("expected has_more=true, got %v", pg["has_more"])
	}
	if pg["next_cursor"] == nil || pg["next_cursor"] == "" {
		t.Errorf("expected next_cursor when hasMore=true, got %v", pg["next_cursor"])
	}
}

func TestActivityHandler_List_ServiceError_NotFound_Returns404(t *testing.T) {
	svc := &stubActivitySvc{err: apperr.ErrNotFound}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/44444444-4444-4444-4444-444444444444/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestActivityHandler_List_ServiceError_Forbidden_Returns403(t *testing.T) {
	svc := &stubActivitySvc{err: apperr.ErrForbidden}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/55555555-5555-5555-5555-555555555555/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "FORBIDDEN")
}

func TestActivityHandler_List_InvalidCursor_Returns400(t *testing.T) {
	svc := &stubActivitySvc{err: repository.ErrInvalidCursor}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/66666666-6666-6666-6666-666666666666/activities?cursor=bad-cursor", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "VALIDATION_ERROR")
}

func TestActivityHandler_List_DefaultLimit(t *testing.T) {
	// When no limit param is given, the handler should not error.
	svc := &stubActivitySvc{items: []*model.MatterActivity{}}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/77777777-7777-7777-7777-777777777777/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestActivityHandler_List_LimitClampedToMax(t *testing.T) {
	// Limit >200 should be clamped to 50 (default), not error.
	svc := &stubActivitySvc{items: []*model.MatterActivity{}}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/88888888-8888-8888-8888-888888888888/activities?limit=9999", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestActivityHandler_List_InternalError_Returns500(t *testing.T) {
	svc := &stubActivitySvc{err: errors.New("unexpected db error")}
	r := newActivityTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/99999999-9999-9999-9999-999999999999/activities", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "INTERNAL_ERROR")
}
