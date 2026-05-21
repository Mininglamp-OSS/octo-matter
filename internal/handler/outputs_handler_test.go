package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gin-gonic/gin"
)

type stubOutputsSvc struct {
	items     []*model.MatterOutput
	hasMore   bool
	err       error
	gotQ      string
	gotLimit  int
	gotCursor *string
}

func (s *stubOutputsSvc) ListOutputs(
	_ context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	q string,
	cursor *string,
	limit int,
) ([]*model.MatterOutput, bool, error) {
	s.gotQ = q
	s.gotLimit = limit
	s.gotCursor = cursor
	return s.items, s.hasMore, s.err
}

func newOutputsTestRouter(svc outputsLister) *gin.Engine {
	r := gin.New()
	h := NewOutputsHandler(svc)
	r.GET("/api/v1/matters/:id/outputs", func(c *gin.Context) {
		c.Set("space_id", "sp1")
		c.Set("uid", "u1")
		c.Set("related_uids", []string{"u1"})
		h.List(c)
	})
	return r
}

func TestOutputsHandler_List_HappyPath(t *testing.T) {
	now := time.Now()
	name := "doc.pdf"
	svc := &stubOutputsSvc{items: []*model.MatterOutput{{
		ID: "o1", MatterID: "11111111-1111-1111-1111-111111111111",
		FileURL: "https://x/doc.pdf", FileName: &name,
		SenderUID: "u_sender", SenderUname: "Alice",
		SentAt: now,
	}}}
	r := newOutputsTestRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matters/11111111-1111-1111-1111-111111111111/outputs?q=doc&limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.gotQ != "doc" || svc.gotLimit != 10 {
		t.Errorf("svc inputs mismatch: q=%q limit=%d", svc.gotQ, svc.gotLimit)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body["data"] == nil || body["pagination"] == nil {
		t.Errorf("missing fields: %s", w.Body.String())
	}
}

func TestOutputsHandler_List_InvalidUUID(t *testing.T) {
	r := newOutputsTestRouter(&stubOutputsSvc{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/not-a-uuid/outputs", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	assertErrCode(t, w.Body.Bytes(), "VALIDATION_ERROR")
}

func TestOutputsHandler_List_Forbidden(t *testing.T) {
	r := newOutputsTestRouter(&stubOutputsSvc{err: apperr.ErrForbidden})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/22222222-2222-2222-2222-222222222222/outputs", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestOutputsHandler_List_HasMore_NextCursorPresent(t *testing.T) {
	now := time.Now()
	svc := &stubOutputsSvc{
		items:   []*model.MatterOutput{{ID: "o1", FileURL: "u", SentAt: now}},
		hasMore: true,
	}
	r := newOutputsTestRouter(svc)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/33333333-3333-3333-3333-333333333333/outputs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	var pg map[string]interface{}
	json.Unmarshal(body["pagination"], &pg)
	if pg["has_more"] != true {
		t.Errorf("expected has_more=true")
	}
	if pg["next_cursor"] == nil || pg["next_cursor"] == "" {
		t.Errorf("expected next_cursor when hasMore=true")
	}
}

func TestOutputsHandler_List_LimitClampedAndQTrimmed(t *testing.T) {
	svc := &stubOutputsSvc{}
	r := newOutputsTestRouter(svc)
	w := httptest.NewRecorder()
	// 9999 > 200 → clamped to default 50; q has leading/trailing space.
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/44444444-4444-4444-4444-444444444444/outputs?limit=9999&q=%20%20pdf%20%20", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if svc.gotLimit != 50 {
		t.Errorf("limit not clamped: %d", svc.gotLimit)
	}
	if svc.gotQ != "pdf" {
		t.Errorf("q not trimmed: %q", svc.gotQ)
	}
}
