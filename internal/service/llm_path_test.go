package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// TestBuildMessagesPrompt_TruncatesByRuneNotByte ensures buildMessagesPrompt
// keeps exactly ExtractMaxContentChars runes (not bytes) of each message body.
// "中" is 3 bytes per rune; a naïve byte-slice at 500 cuts mid-rune and yields
// invalid UTF-8.
func TestBuildMessagesPrompt_TruncatesByRuneNotByte(t *testing.T) {
	long := strings.Repeat("中", ExtractMaxContentChars+100)
	msgs := []ExtractMessage{{MessageID: "m1", FromUID: "u1", FromUname: "U", Content: long}}

	out := buildMessagesPrompt(msgs)
	if !utf8.ValidString(out) {
		t.Fatalf("prompt is not valid UTF-8 — content was sliced mid-rune")
	}
	expectedKept := strings.Repeat("中", ExtractMaxContentChars)
	if !strings.Contains(out, expectedKept) {
		t.Fatalf("expected %d-rune truncated content in prompt", ExtractMaxContentChars)
	}
	overrun := strings.Repeat("中", ExtractMaxContentChars+1)
	if strings.Contains(out, overrun) {
		t.Fatalf("content was not truncated to %d runes", ExtractMaxContentChars)
	}
}

// stubLLMCaller returns a canned response or error from CallTool.
type stubLLMCaller struct {
	rawArgs string
	err     error
	calls   int
}

func (s *stubLLMCaller) CallTool(_ context.Context, _, _ string, _ llm.Tool, _ ...llm.CallOption) (string, error) {
	s.calls++
	return s.rawArgs, s.err
}

func (s *stubLLMCaller) Model() string { return "stub" }

// spyLimiter records Allow calls and returns canned results.
type spyLimiter struct {
	allow bool
	calls int
	keys  []string
}

func (s *spyLimiter) Allow(key string) bool {
	s.calls++
	s.keys = append(s.keys, key)
	return s.allow
}

// TestTimelineService_LimiterAfterAccess verifies the ordering: when the
// caller has no access to the matter, the rate limiter is NOT called (so a
// forbidden caller cannot consume the legitimate user's cooldown). When the
// caller passes access, the limiter is called BEFORE the LLM. When the
// limiter denies, the LLM is NOT called.
func TestTimelineService_LimiterAfterAccess(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}

	t.Run("forbidden caller does not consume cooldown", func(t *testing.T) {
		llmStub := &stubLLMCaller{rawArgs: `{"content":"ok"}`}
		limiter := &spyLimiter{allow: true}
		timelineRepo := newFakeTimelineRepo()
		attRepo := newFakeTimelineAttachmentRepo()
		svc := NewTimelineService(
			llmStub, timelineRepo, attRepo,
			newFakeMatterRepo(matter), denyAccessChecker{}, nil,
			fakeTimelineTx{timelines: timelineRepo, attachments: attRepo},
			fakeParticipantRepo{}, newFakeAssigneeRepo(),
			limiter,
		)
		_, _, err := svc.CreateEntry(context.Background(), TimelineInput{
			MatterID: "t1", SpaceID: "sp1",
			ActorUID: "u1", ParticipantUID: "u1",
			CallerUIDs:  []string{"u1"},
			ChannelType: 1, ChannelID: "ch-1",
			Messages: []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
		})
		if !errors.Is(err, apperr.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
		if limiter.calls != 0 {
			t.Fatalf("limiter must NOT fire for forbidden caller, got %d calls", limiter.calls)
		}
		if llmStub.calls != 0 {
			t.Fatalf("LLM must NOT be called for forbidden caller, got %d calls", llmStub.calls)
		}
	})

	t.Run("limiter denies → LLM not called, returns RateLimited", func(t *testing.T) {
		llmStub := &stubLLMCaller{rawArgs: `{"content":"ok"}`}
		limiter := &spyLimiter{allow: false}
		timelineRepo := newFakeTimelineRepo()
		attRepo := newFakeTimelineAttachmentRepo()
		svc := NewTimelineService(
			llmStub, timelineRepo, attRepo,
			newFakeMatterRepo(matter), fakeAccessChecker{}, nil,
			fakeTimelineTx{timelines: timelineRepo, attachments: attRepo},
			fakeParticipantRepo{}, newFakeAssigneeRepo(),
			limiter,
		)
		_, _, err := svc.CreateEntry(context.Background(), TimelineInput{
			MatterID: "t1", SpaceID: "sp1",
			ActorUID: "u1", ParticipantUID: "u1",
			CallerUIDs:  []string{"u1"},
			ChannelType: 1, ChannelID: "ch-1",
			Messages: []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
		})
		ae, ok := apperr.AsAppError(err)
		if !ok || ae.HTTPStatus() != 429 {
			t.Fatalf("expected 429 RateLimited, got %v", err)
		}
		if limiter.calls != 1 {
			t.Fatalf("expected 1 limiter call, got %d", limiter.calls)
		}
		if llmStub.calls != 0 {
			t.Fatalf("LLM must NOT be called when limiter denies, got %d calls", llmStub.calls)
		}
		if len(limiter.keys) == 0 || limiter.keys[0] != "t1:u1" {
			t.Fatalf("limiter key must be matterID:uid, got %v", limiter.keys)
		}
	})

	t.Run("limiter allows → LLM called", func(t *testing.T) {
		// Use ErrEmptyToolCall so we short-circuit before the tx — we only
		// care that the limiter let the call through to the LLM.
		llmStub := &stubLLMCaller{err: llm.ErrEmptyToolCall}
		limiter := &spyLimiter{allow: true}
		timelineRepo := newFakeTimelineRepo()
		attRepo := newFakeTimelineAttachmentRepo()
		svc := NewTimelineService(
			llmStub, timelineRepo, attRepo,
			newFakeMatterRepo(matter), fakeAccessChecker{}, nil,
			fakeTimelineTx{timelines: timelineRepo, attachments: attRepo},
			fakeParticipantRepo{}, newFakeAssigneeRepo(),
			limiter,
		)
		_, _, _ = svc.CreateEntry(context.Background(), TimelineInput{
			MatterID: "t1", SpaceID: "sp1",
			ActorUID: "u1", ParticipantUID: "u1",
			CallerUIDs:  []string{"u1"},
			ChannelType: 1, ChannelID: "ch-1",
			Messages: []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
		})
		if limiter.calls != 1 {
			t.Fatalf("expected 1 limiter call, got %d", limiter.calls)
		}
		if llmStub.calls != 1 {
			t.Fatalf("expected 1 LLM call (limiter let it through), got %d", llmStub.calls)
		}
	})

	t.Run("nil limiter skips rate limiting", func(t *testing.T) {
		llmStub := &stubLLMCaller{err: llm.ErrEmptyToolCall}
		timelineRepo := newFakeTimelineRepo()
		attRepo := newFakeTimelineAttachmentRepo()
		svc := NewTimelineService(
			llmStub, timelineRepo, attRepo,
			newFakeMatterRepo(matter), fakeAccessChecker{}, nil,
			fakeTimelineTx{timelines: timelineRepo, attachments: attRepo},
			fakeParticipantRepo{}, newFakeAssigneeRepo(),
			nil,
		)
		_, _, _ = svc.CreateEntry(context.Background(), TimelineInput{
			MatterID: "t1", SpaceID: "sp1",
			ActorUID: "u1", ParticipantUID: "u1",
			CallerUIDs:  []string{"u1"},
			ChannelType: 1, ChannelID: "ch-1",
			Messages: []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
		})
		if llmStub.calls != 1 {
			t.Fatalf("expected 1 LLM call (no limiter), got %d", llmStub.calls)
		}
	})
}

// TestExtractService_EmptyToolCall_PreservesSentinel guards the contract that
// ExtractService.CreateFromMessages wraps llm.ErrEmptyToolCall instead of
// translating it into a generic ValidationError. Covers both:
//   - the model returned no tool call at all (CallTool err)
//   - the model called the tool but with a blank title (empty result body)
//
// Both must surface to the handler as the same sentinel so the 422
// LLM_EMPTY_EXTRACTION branch fires.
func TestExtractService_EmptyToolCall_PreservesSentinel(t *testing.T) {
	cases := []struct {
		name string
		stub stubLLMCaller
	}{
		{name: "no tool call", stub: stubLLMCaller{err: llm.ErrEmptyToolCall}},
		{name: "empty title", stub: stubLLMCaller{rawArgs: `{"title":"","description":"x"}`}},
		{name: "whitespace title", stub: stubLLMCaller{rawArgs: `{"title":"   ","description":"x"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matterRepo := newFakeMatterRepo()
			matterSvc := newMatterSvc(matterRepo, newFakeAssigneeRepo())
			svc := NewExtractService(&tc.stub, matterSvc)
			_, err := svc.CreateFromMessages(context.Background(), ExtractInput{
				SpaceID:     "sp1",
				ChannelType: 1,
				ChannelID:   "ch-1",
				CreatorUID:  "u1",
				CallerUIDs:  []string{"u1"},
				Messages:    []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
			})
			if !errors.Is(err, llm.ErrEmptyToolCall) {
				t.Fatalf("expected error to wrap llm.ErrEmptyToolCall, got %v", err)
			}
		})
	}
}

// TestTimelineService_ListEntries_FilterBySourceChannelID guards the contract
// that a channel-scoped GET request returns ONLY entries from that channel.
// Without the filter, a caller granted access via channel-link would receive
// every entry on the matter, including manually-recorded ones and entries from
// other linked channels.
func TestTimelineService_ListEntries_FilterBySourceChannelID(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	contentA := "from A"
	contentB := "from B"
	contentManual := "manual"
	chA := "ch-A"
	chB := "ch-B"
	entryA := &model.TimelineEntry{ID: "ea", MatterID: "t1", UserID: "u1", Content: &contentA, SourceChannelID: &chA}
	entryB := &model.TimelineEntry{ID: "eb", MatterID: "t1", UserID: "u1", Content: &contentB, SourceChannelID: &chB}
	entryManual := &model.TimelineEntry{ID: "em", MatterID: "t1", UserID: "u1", Content: &contentManual}
	svc := newTimelineSvc(
		newFakeTimelineRepo(entryA, entryB, entryManual),
		newFakeTimelineAttachmentRepo(),
		newFakeMatterRepo(matter),
		fakeAccessChecker{},
	)
	entries, _, err := svc.ListEntries(context.Background(), "t1", "sp1", []string{"u1"}, "ch-A", "", nil, 50)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "ea" {
		ids := make([]string, len(entries))
		for i, e := range entries {
			ids[i] = e.ID
		}
		t.Fatalf("expected only [ea] when source_channel_id=ch-A, got %v", ids)
	}
}

// TestTimelineService_ListEntries_NoFilterReturnsAll confirms backward
// compatibility: when no source_channel_id is supplied, all entries are returned.
func TestTimelineService_ListEntries_NoFilterReturnsAll(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	c1 := "a"
	c2 := "b"
	entryA := &model.TimelineEntry{ID: "e1", MatterID: "t1", UserID: "u1", Content: &c1}
	entryB := &model.TimelineEntry{ID: "e2", MatterID: "t1", UserID: "u1", Content: &c2}
	svc := newTimelineSvc(
		newFakeTimelineRepo(entryA, entryB),
		newFakeTimelineAttachmentRepo(),
		newFakeMatterRepo(matter),
		fakeAccessChecker{},
	)
	entries, _, err := svc.ListEntries(context.Background(), "t1", "sp1", []string{"u1"}, "", "", nil, 50)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with no filter, got %d", len(entries))
	}
}

// TestTimelineService_EmptyToolCall_PreservesSentinel mirrors the extract test
// for the timeline LLM path. Covers both the no-tool-call error and the
// empty/whitespace-content tool result.
func TestTimelineService_EmptyToolCall_PreservesSentinel(t *testing.T) {
	cases := []struct {
		name string
		stub stubLLMCaller
	}{
		{name: "no tool call", stub: stubLLMCaller{err: llm.ErrEmptyToolCall}},
		{name: "empty content", stub: stubLLMCaller{rawArgs: `{"content":""}`}},
		{name: "whitespace content", stub: stubLLMCaller{rawArgs: `{"content":"   "}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
			timelineRepo := newFakeTimelineRepo()
			attRepo := newFakeTimelineAttachmentRepo()
			svc := NewTimelineService(
				&tc.stub,
				timelineRepo,
				attRepo,
				newFakeMatterRepo(matter),
				fakeAccessChecker{},
				nil,
				fakeTimelineTx{timelines: timelineRepo, attachments: attRepo},
				fakeParticipantRepo{},
				newFakeAssigneeRepo(),
				nil,
			)
			_, _, err := svc.CreateEntry(context.Background(), TimelineInput{
				MatterID:       "t1",
				SpaceID:        "sp1",
				ActorUID:       "u1",
				ParticipantUID: "u1",
				CallerUIDs:     []string{"u1"},
				ChannelType:    1,
				ChannelID:      "ch-1",
				Messages:       []ExtractMessage{{MessageID: "m1", FromUID: "u1", Content: "hi"}},
			})
			if !errors.Is(err, llm.ErrEmptyToolCall) {
				t.Fatalf("expected error to wrap llm.ErrEmptyToolCall, got %v", err)
			}
		})
	}
}
