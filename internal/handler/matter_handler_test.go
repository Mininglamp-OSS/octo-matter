// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCreateMatterReq_BindsSourceMsgIDs guards the manual-create path: clients
// pass the LLM-filtered source_msgs alongside source_channel_id when re-issuing
// a matter creation request. The DTO must surface the field so the handler can
// forward it onto the model (issue #40). Driven through Gin's ShouldBindJSON
// rather than json.Unmarshal so the binding tag is exercised end-to-end.
func TestCreateMatterReq_BindsSourceMsgIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"title": "t",
		"source_channel_id": "ch-1",
		"source_channel_type": 1,
		"source_msg_ids": ["m1", "m2", "m3"]
	}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if bound.SourceMsgIDs == nil {
		t.Fatalf("SourceMsgIDs pointer: got nil, want populated")
	}
	got := *bound.SourceMsgIDs
	if len(got) != 3 || got[0] != "m1" || got[2] != "m3" {
		t.Fatalf("SourceMsgIDs content: got %v", got)
	}
}

// TestCreateMatterReq_AcceptsClientSourceMsgsObjectShape covers the real
// payload the client sends today: a `source_msgs` array of full message
// objects (mirroring the /extract endpoint's `msgs` field) rather than a
// `source_msg_ids` string list. PR #43 added `source_msg_ids` but the client
// kept emitting `source_msgs`, so the field was silently dropped by Gin and
// matters.source_msg_ids landed as NULL even for new matters. Fix: accept the
// object form, extract message_id server-side.
func TestCreateMatterReq_AcceptsClientSourceMsgsObjectShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"title": "t",
		"source_channel_id": "ch-1",
		"source_channel_type": 1,
		"source_msgs": [
			{"message_id": "m-1", "from_uid": "u1", "content": "hello"},
			{"message_id": "m-2", "from_uid": "u2", "content": "world"}
		]
	}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ids := bound.derivedSourceMsgIDs()
	if len(ids) != 2 || ids[0] != "m-1" || ids[1] != "m-2" {
		t.Fatalf("derived ids: got %v, want [m-1 m-2]", ids)
	}
}

// TestCreateMatterReq_ExplicitEmptySourceMsgIDsWinsOverObjectShape covers the
// review #44 round-3 blocking nit: when the client posts an explicit empty
// `source_msg_ids: []` alongside `source_msgs: [{...}]`, the empty list must
// win — otherwise stale msg objects would silently leak into storage despite
// the client's deliberate "clear the list" intent.
func TestCreateMatterReq_ExplicitEmptySourceMsgIDsWinsOverObjectShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"title": "t",
		"source_msg_ids": [],
		"source_msgs": [{"message_id": "stale-msg"}]
	}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ids := bound.derivedSourceMsgIDs()
	if ids == nil {
		t.Fatalf("explicit empty source_msg_ids must yield non-nil slice; got nil")
	}
	if len(ids) != 0 {
		t.Fatalf("explicit empty source_msg_ids must win over source_msgs; got %v", ids)
	}
}

// TestCreateMatterReq_ExplicitEmptySourceMsgIDsAlonePreservesEmpty ensures the
// derived list is a non-nil empty slice (rendered as JSON [] in storage), not
// nil (which would write SQL NULL and lose the explicit-empty signal).
func TestCreateMatterReq_ExplicitEmptySourceMsgIDsAlonePreservesEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"title": "t", "source_msg_ids": []}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ids := bound.derivedSourceMsgIDs()
	if ids == nil || len(ids) != 0 {
		t.Fatalf("explicit empty must derive to non-nil empty slice; got %#v", ids)
	}
}

// TestCreateMatterReq_AbsentSourceMsgIDsFieldFallsBackToObjectShape codifies
// the contract: nil pointer (field truly absent in the JSON body) means the
// derivation falls back to source_msgs.
func TestCreateMatterReq_AbsentSourceMsgIDsFieldFallsBackToObjectShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"title": "t", "source_msgs": [{"message_id": "m-1"}]}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if bound.SourceMsgIDs != nil {
		t.Fatalf("absent source_msg_ids must remain nil pointer; got %v", *bound.SourceMsgIDs)
	}
	ids := bound.derivedSourceMsgIDs()
	if len(ids) != 1 || ids[0] != "m-1" {
		t.Fatalf("absent source_msg_ids must fall back to source_msgs; got %v", ids)
	}
}

// TestCreateMatterReq_RejectsSourceMsgsMissingMessageID guards the nested
// binding tag (review #44 round-3 non-blocking): each source_msgs entry
// must carry a non-empty message_id, otherwise empty-id rows would land in
// the JSON column.
func TestCreateMatterReq_RejectsSourceMsgsMissingMessageID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"title": "t", "source_msgs": [{"from_uid": "u1"}]}`)

	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		var req createMatterReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing message_id, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateMatterReq_RejectsOversizedSourceMsgs guards the count bound on
// the nested array (review #44 round-3 non-blocking).
func TestCreateMatterReq_RejectsOversizedSourceMsgs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	msgs := make([]map[string]string, 0, 201)
	for i := 0; i < 201; i++ {
		msgs = append(msgs, map[string]string{"message_id": fmt.Sprintf("m%d", i)})
	}
	payload, err := json.Marshal(map[string]any{
		"title":       "t",
		"source_msgs": msgs,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		var req createMatterReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 201-entry source_msgs, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateMatterReq_ExplicitSourceMsgIDsTakesPrecedence keeps the cleaner
// contract usable for clients that already send a plain string list — they
// should not have to wrap each id in an object.
func TestCreateMatterReq_ExplicitSourceMsgIDsTakesPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"title": "t",
		"source_msg_ids": ["explicit-1"],
		"source_msgs": [{"message_id": "object-1"}]
	}`)

	var bound createMatterReq
	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&bound); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ids := bound.derivedSourceMsgIDs()
	if len(ids) != 1 || ids[0] != "explicit-1" {
		t.Fatalf("explicit list must win over object shape; got %v", ids)
	}
}

// TestCreateMatterReq_RejectsOversizedSourceMsgIDs guards the manual-create
// path against an unbounded JSON array reaching the storage layer. The extract
// path caps msgs at 200; the manual path must mirror that bound (review #43).
func TestCreateMatterReq_RejectsOversizedSourceMsgIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ids := make([]string, 0, 201)
	for i := 0; i < 201; i++ {
		ids = append(ids, fmt.Sprintf("m%d", i))
	}
	payload, err := json.Marshal(map[string]any{
		"title":          "t",
		"source_msg_ids": ids,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	r := gin.New()
	r.POST("/m", func(c *gin.Context) {
		var req createMatterReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/m", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "SourceMsgIDs") {
		t.Fatalf("expected validation error to mention SourceMsgIDs, got %s", w.Body.String())
	}
}
