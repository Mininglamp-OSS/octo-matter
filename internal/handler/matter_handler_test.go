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
	if got, want := len(bound.SourceMsgIDs), 3; got != want {
		t.Fatalf("SourceMsgIDs len: got %d, want %d", got, want)
	}
	if bound.SourceMsgIDs[0] != "m1" || bound.SourceMsgIDs[2] != "m3" {
		t.Fatalf("SourceMsgIDs content: got %v", bound.SourceMsgIDs)
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
