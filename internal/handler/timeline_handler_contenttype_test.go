package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

// ─── minimal stubs to stand up a real TimelineService end-to-end ───────────
//
// These guard the TIMELINE entry point specifically: the bug was that
// timeline_handler.go built service.ExtractMessage WITHOUT copying ContentType,
// so a type=14 (RichText) message silently lost its type signal before reaching
// messageDisplayContent. A service-level test that constructs ExtractMessage
// directly cannot catch that drop — only driving the real handler → service
// path does. See PR#64 R3 / GH-63.

type ctLLMStub struct {
	gotUser string
}

func (s *ctLLMStub) CallTool(_ context.Context, _, user string, _ llm.Tool, _ ...llm.CallOption) (string, error) {
	s.gotUser = user
	return `{"content":"summary"}`, nil
}
func (s *ctLLMStub) Model() string { return "stub" }

type ctTimelineRepo struct{}

func (ctTimelineRepo) Create(_ context.Context, e *model.TimelineEntry) error {
	if e.ID == "" {
		e.ID = "entry-1"
	}
	return nil
}
func (ctTimelineRepo) GetByID(_ context.Context, _ string) (*model.TimelineEntry, error) {
	return nil, apperr.ErrNotFound
}
func (ctTimelineRepo) Delete(_ context.Context, _ string) error { return nil }
func (ctTimelineRepo) ListByMatter(_ context.Context, _, _ string, _ *string, _ int) ([]*model.TimelineEntry, bool, error) {
	return nil, false, nil
}
func (ctTimelineRepo) ListRecentByMatter(_ context.Context, _ string, _ int) ([]*model.TimelineEntry, error) {
	return nil, nil
}

type ctAttachmentRepo struct{}

func (ctAttachmentRepo) CreateMany(_ context.Context, _ []*model.TimelineAttachment) error {
	return nil
}
func (ctAttachmentRepo) ListByEntryIDs(_ context.Context, _ []string) (map[string][]model.TimelineAttachment, error) {
	return nil, nil
}
func (ctAttachmentRepo) DeleteByEntryID(_ context.Context, _ string) error { return nil }
func (ctAttachmentRepo) FindByMatterAndFileURL(_ context.Context, _, _ string) (*model.TimelineAttachment, error) {
	return nil, apperr.ErrNotFound
}

type ctMatterRepo struct{}

func (ctMatterRepo) GetByID(_ context.Context, id, _ string) (*model.Matter, error) {
	return &model.Matter{ID: id, Title: "Rollout", Status: model.MatterStatusOpen}, nil
}

type ctAccessChecker struct{}

func (ctAccessChecker) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, _, _ string) (bool, error) {
	return true, nil
}

type ctParticipant struct{}

func (ctParticipant) Upsert(_ context.Context, _, _ string) error { return nil }

type ctChannelStore struct{}

func (ctChannelStore) FindByMatterAndChannelID(_ context.Context, matterID, channelID string) (*model.MatterChannel, error) {
	// Pretend the channel is already linked so the user path needs no IM verify.
	return &model.MatterChannel{ID: "mc-1", MatterID: matterID, ChannelID: channelID, ChannelType: 2}, nil
}
func (ctChannelStore) Create(_ context.Context, _ *model.MatterChannel) error { return nil }

type ctTx struct{}

func (ctTx) Do(ctx context.Context, fn func(service.TimelineStore, service.TimelineAttachmentStore, service.ParticipantUpserter, service.TimelineMatterChannelStore) error) error {
	return fn(ctTimelineRepo{}, ctAttachmentRepo{}, ctParticipant{}, ctChannelStore{})
}

type ctAssignees struct{}

func (ctAssignees) Create(_ context.Context, _ *model.MatterAssignee) error { return nil }
func (ctAssignees) Delete(_ context.Context, _, _ string) error             { return nil }
func (ctAssignees) ListByMatter(_ context.Context, _ string) ([]*model.MatterAssignee, error) {
	return nil, nil
}
func (ctAssignees) ListByMatterIDs(_ context.Context, _ []string) ([]*model.MatterAssignee, error) {
	return nil, nil
}
func (ctAssignees) IsAssignee(_ context.Context, _, _ string) (bool, error) { return false, nil }
func (ctAssignees) IsAssigneeAny(_ context.Context, _ string, _ []string) (bool, error) {
	return false, nil
}

// TestTimelineHandler_Create_PassesContentTypeToPrompt is the regression guard
// for PR#64 R3: the timeline handler must forward content_type from the request
// DTO into service.ExtractMessage. A type=14 RichText message whose Content is a
// legacy stringified payload only decodes to plain text on the EXPLICIT type=14
// branch of messageDisplayContent; if the handler drops ContentType the message
// falls to auto-detect, which (correctly) refuses string-content payloads and
// leaks the raw JSON into the prompt. Asserting the prompt carries the plain
// text — and NOT the raw JSON key — proves the signal survived end-to-end.
func TestTimelineHandler_Create_PassesContentTypeToPrompt(t *testing.T) {
	llmStub := &ctLLMStub{}
	svc := service.NewTimelineService(
		llmStub,
		ctTimelineRepo{},
		ctAttachmentRepo{},
		ctMatterRepo{},
		ctAccessChecker{},
		nil, // linkVerifier — channel already linked, user path skips IM verify
		ctTx{},
		ctParticipant{},
		ctAssignees{},
		nil, // limiter
	)
	h := NewTimelineHandler(svc, nil, nil, nil)

	const matterID = "11111111-1111-1111-1111-111111111111"
	r := gin.New()
	r.POST("/api/v1/matters/:id/timeline", func(c *gin.Context) {
		c.Set("space_id", "sp1")
		c.Set("uid", "u1")
		c.Set("related_uids", []string{"u1"})
		h.Create(c)
	})

	// type=14 RichText payload in the legacy "content is a plain string" shape.
	// Decodes to "上线已完成" ONLY via the explicit-14 branch.
	body := map[string]any{
		"channel_type":    2,
		"channel_id":      "ch_x",
		"participant_uid": "u1",
		"msgs": []map[string]any{
			{
				"message_id":   "m_rt",
				"from_uid":     "u1",
				"from_uname":   "Alice",
				"timestamp":    1,
				"content":      `{"content":"上线已完成"}`,
				"content_type": 14,
			},
		},
	}
	buf, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matters/"+matterID+"/timeline", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if llmStub.gotUser == "" {
		t.Fatal("LLM was not called; handler did not reach the timeline LLM path")
	}
	if !strings.Contains(llmStub.gotUser, "上线已完成") {
		t.Fatalf("decoded plain text missing from prompt — ContentType=14 was dropped before messageDisplayContent.\nprompt:\n%s", llmStub.gotUser)
	}
	// The raw JSON wrapper must NOT leak: if ContentType were dropped, auto-detect
	// refuses string-content payloads and the prompt would carry `{"content":...}`.
	if strings.Contains(llmStub.gotUser, `{"content":"上线已完成"}`) {
		t.Fatalf("raw RichText JSON leaked into prompt — type=14 signal lost.\nprompt:\n%s", llmStub.gotUser)
	}
}
