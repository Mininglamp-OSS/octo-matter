package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// stubOutputsReader records the filter it received so tests can assert that
// the service forwards the right inputs to the repo layer.
type stubOutputsReader struct {
	got     repository.OutputsFilter
	items   []*model.MatterOutput
	hasMore bool
	err     error
}

func (s *stubOutputsReader) ListOutputs(_ context.Context, f repository.OutputsFilter) ([]*model.MatterOutput, bool, error) {
	s.got = f
	return s.items, s.hasMore, s.err
}

func TestOutputsService_ListOutputs_ForwardsFilter(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "u1"}
	reader := &stubOutputsReader{items: []*model.MatterOutput{{ID: "o1"}}, hasMore: true}
	svc := NewOutputsService(newFakeMatterRepo(matter), fakeAccessChecker{}, reader)

	cursor := "abc"
	items, hasMore, err := svc.ListOutputs(context.Background(), "m1", "sp1", []string{"u1"}, "pdf", &cursor, 20)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !hasMore || len(items) != 1 {
		t.Fatalf("expected items + hasMore from stub, got items=%d hasMore=%v", len(items), hasMore)
	}
	if reader.got.MatterID != "m1" || reader.got.Q != "pdf" || reader.got.Limit != 20 {
		t.Errorf("filter mismatch: %+v", reader.got)
	}
	if reader.got.Cursor == nil || *reader.got.Cursor != "abc" {
		t.Errorf("cursor not forwarded: %+v", reader.got.Cursor)
	}
}

// TestOutputsService_ListOutputs_DeniedByVisibility mirrors the matter-global
// activity-list access policy: a stranger gets Forbidden, not an empty list.
func TestOutputsService_ListOutputs_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "owner"}
	reader := &stubOutputsReader{}
	svc := NewOutputsService(newFakeMatterRepo(matter), denyAccessChecker{}, reader)

	_, _, err := svc.ListOutputs(context.Background(), "m1", "sp1", []string{"stranger"}, "", nil, 50)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("expected Forbidden, got %v", err)
	}
}

// TestOutputsService_ListOutputs_CrossSpaceReturnsNotFound ensures the
// matter scope check fires before the access check.
func TestOutputsService_ListOutputs_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "u1"}
	svc := NewOutputsService(newFakeMatterRepo(matter), fakeAccessChecker{}, &stubOutputsReader{})
	_, _, err := svc.ListOutputs(context.Background(), "m1", "other-space", []string{"u1"}, "", nil, 50)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space outputs: got %v, want ErrNotFound", err)
	}
}

// --- buildLLMAttachments unit tests -------------------------------------

func TestBuildLLMAttachments_OnlyCitedMessages(t *testing.T) {
	in := TimelineInput{
		MatterID: "m1",
		Messages: []ExtractMessage{
			{
				MessageID: "msg-1", FromUID: "u1", FromUname: "Alice", Timestamp: 1779344220,
				Attachments: []ExtractMessageAttachment{{FileName: "a.pdf", FileURL: "https://x/a.pdf"}},
			},
			{
				MessageID: "msg-2", FromUID: "u2", FromUname: "Bob", Timestamp: 1779344300,
				Attachments: []ExtractMessageAttachment{{FileName: "b.pdf", FileURL: "https://x/b.pdf"}},
			},
		},
	}
	v := timelineValidated{Content: "summary", SourceMsgs: []string{"msg-2"}, HasExplicitCitations: true}

	got := buildLLMAttachments("entry-1", in, v)
	if len(got) != 1 {
		t.Fatalf("expected only cited attachment, got %d", len(got))
	}
	a := got[0]
	if a.FileURL != "https://x/b.pdf" || a.SenderUID != "u2" || a.SenderUname != "Bob" {
		t.Errorf("wrong attachment selected: %+v", a)
	}
	if a.MatterID != "m1" || a.EntryID != "entry-1" {
		t.Errorf("scope fields missing: %+v", a)
	}
	if a.Description == nil || *a.Description != "summary" {
		t.Errorf("description should be the LLM content: %+v", a.Description)
	}
	want := time.Unix(1779344300, 0).UTC()
	if !a.SentAt.Equal(want) {
		t.Errorf("sent_at mismatch: got %s, want %s", a.SentAt, want)
	}
}

// TestBuildLLMAttachments_FailsClosedWithoutExplicitCitations is the
// privacy regression guard for #35 reviewer feedback. Prior to the fix,
// validateTimelineArgs fell back to all input msg IDs when the LLM
// returned no valid source_msg_ids, and buildLLMAttachments treated that
// fallback as "no citations → keep everything" — which would leak every
// attachment in the batch. Now the function MUST fail closed when
// HasExplicitCitations is false.
func TestBuildLLMAttachments_FailsClosedWithoutExplicitCitations(t *testing.T) {
	in := TimelineInput{
		MatterID: "m1",
		Messages: []ExtractMessage{
			{
				MessageID: "msg-1", FromUID: "u1", Timestamp: 1,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/a"}},
			},
			{
				MessageID: "msg-2", FromUID: "u2", Timestamp: 2,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/b"}},
			},
		},
	}
	// HasExplicitCitations=false even though SourceMsgs is populated — the
	// validator's all-messages fallback. Must NOT leak attachments.
	v := timelineValidated{
		Content:              "s",
		SourceMsgs:           []string{"msg-1", "msg-2"},
		HasExplicitCitations: false,
	}

	got := buildLLMAttachments("e", in, v)
	if len(got) != 0 {
		t.Fatalf("expected fail-closed (no attachments persisted), got %d", len(got))
	}
}

func TestBuildLLMAttachments_DedupByFileURL(t *testing.T) {
	in := TimelineInput{
		MatterID: "m1",
		Messages: []ExtractMessage{
			{MessageID: "msg-1", FromUID: "u1", Timestamp: 1, Attachments: []ExtractMessageAttachment{{FileURL: "https://x/a"}}},
			{MessageID: "msg-2", FromUID: "u2", Timestamp: 2, Attachments: []ExtractMessageAttachment{{FileURL: "https://x/a"}}},
		},
	}
	v := timelineValidated{Content: "s", SourceMsgs: []string{"msg-1", "msg-2"}, HasExplicitCitations: true}

	got := buildLLMAttachments("e", in, v)
	if len(got) != 1 {
		t.Fatalf("expected dedup by file_url, got %d", len(got))
	}
	// First occurrence wins — sender = u1 (msg-1), not u2 (msg-2).
	if got[0].SenderUID != "u1" {
		t.Errorf("first-occurrence dedup expected, got sender %s", got[0].SenderUID)
	}
}

func TestBuildLLMAttachments_HardCapAtMaxPerEntry(t *testing.T) {
	atts := make([]ExtractMessageAttachment, 0, MaxAttachmentsPerEntry+5)
	for i := 0; i < MaxAttachmentsPerEntry+5; i++ {
		atts = append(atts, ExtractMessageAttachment{FileURL: "https://x/" + string(rune('a'+i))})
	}
	in := TimelineInput{
		MatterID: "m1",
		Messages: []ExtractMessage{{MessageID: "msg-1", FromUID: "u1", Attachments: atts}},
	}
	v := timelineValidated{Content: "s", SourceMsgs: []string{"msg-1"}, HasExplicitCitations: true}

	got := buildLLMAttachments("e", in, v)
	if len(got) != MaxAttachmentsPerEntry {
		t.Fatalf("expected cap at %d, got %d", MaxAttachmentsPerEntry, len(got))
	}
}

func TestBuildLLMAttachments_SkipsEmptyURL(t *testing.T) {
	in := TimelineInput{
		MatterID: "m1",
		Messages: []ExtractMessage{{MessageID: "msg-1", FromUID: "u1", Attachments: []ExtractMessageAttachment{
			{FileURL: ""}, {FileURL: "   "}, {FileURL: "https://x/a"},
		}}},
	}
	got := buildLLMAttachments("e", in, timelineValidated{Content: "s", SourceMsgs: []string{"msg-1"}, HasExplicitCitations: true})
	if len(got) != 1 || got[0].FileURL != "https://x/a" {
		t.Fatalf("expected only the non-empty URL to be kept, got %+v", got)
	}
}
