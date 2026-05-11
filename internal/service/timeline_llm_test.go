// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// stubLLM lets a test return a canned tool-args payload (or error) and inspect
// the prompts the service built.
type stubLLM struct {
	out         string
	err         error
	gotSystem   string
	gotUser     string
	gotToolName string
}

func (s *stubLLM) CallTool(_ context.Context, system, user string, tool llm.Tool) (string, error) {
	s.gotSystem = system
	s.gotUser = user
	s.gotToolName = tool.Function.Name
	return s.out, s.err
}

// trackingChannelRepoLLM records Create calls so tests can assert atomicity.
type trackingChannelRepoLLM struct {
	createCalls int
	findResult  *model.MatterChannel
}

func (t *trackingChannelRepoLLM) FindByMatterAndChannelID(_ context.Context, matterID, channelID string) (*model.MatterChannel, error) {
	if t.findResult != nil {
		return t.findResult, nil
	}
	return nil, apperr.ErrNotFound
}

func (t *trackingChannelRepoLLM) Create(_ context.Context, mc *model.MatterChannel) error {
	t.createCalls++
	// Mimic the real repo: persist what the caller asked for so that the
	// subsequent Find inside the same closure returns it.
	t.findResult = mc
	return nil
}

// failingTimelineTx runs the closure but reports any error returned, so a
// test can verify rollback semantics. It does NOT actually roll back side
// effects on the fakes — tests that need rollback must use trackers with
// "applied vs committed" counters.
type failingTimelineTx struct {
	inner fakeTimelineTx
	fail  error
}

func (f failingTimelineTx) Do(ctx context.Context, fn func(TimelineStore, TimelineAttachmentStore, ParticipantUpserter, TimelineMatterChannelStore) error) error {
	if err := f.inner.Do(ctx, fn); err != nil {
		return err
	}
	return f.fail
}

func newMatterForLLMTest() *model.Matter {
	deadline := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	desc := "design v3 rollout"
	return &model.Matter{
		ID:          "m1",
		SpaceID:     "space1",
		SeqNo:       42,
		Title:       "Rollout",
		Description: &desc,
		Status:      model.MatterStatusOpen,
		Deadline:    &deadline,
		CreatorID:   "creator",
	}
}

func newLLMTimelineSvc(t *testing.T, llmStub *stubLLM, repo *fakeTimelineRepo, channels TimelineMatterChannelStore) *TimelineService {
	t.Helper()
	matterRepo := &fakeMatterRepo{matters: map[string]*model.Matter{"m1": newMatterForLLMTest()}}
	attRepo := newFakeTimelineAttachmentRepo()
	if channels == nil {
		channels = fakeChannelRepo{}
	}
	return NewTimelineService(
		llmStub,
		repo,
		attRepo,
		matterRepo,
		fakeAccessChecker{},
		nil, // linkVerifier — tests use a stub channels repo and don't exercise IM check
		fakeTimelineTx{timelines: repo, attachments: attRepo, channels: channels},
		fakeParticipantRepo{},
		newFakeAssigneeRepo(),
		nil,
	)
}

func baseLLMInput() TimelineInput {
	return TimelineInput{
		MatterID:       "m1",
		SpaceID:        "space1",
		ActorUID:       "u1",
		ParticipantUID: "u1",
		CallerUIDs:     []string{"u1"},
		// Default to user path so the channel-link write gate (PR #34
		// r4259131241) doesn't reject these tests as bot calls. Bot-path
		// tests can override CallerToken to "".
		CallerToken: "user-tok",
		ChannelType: 2,
		ChannelID:   "ch_x",
		Messages: []ExtractMessage{
			{MessageID: "m_a", FromUID: "u1", Content: "msg one"},
			{MessageID: "m_b", FromUID: "u2", Content: "msg two"},
		},
	}
}

// stubLinkVerifier returns a canned RequireChannelMember result.
type stubLinkVerifier struct {
	allow bool
	err   error
	calls int
}

func (s *stubLinkVerifier) RequireChannelMember(_ context.Context, _, _ string, _ []string) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	if !s.allow {
		return apperr.Forbidden("not a member of channel")
	}
	return nil
}

// TestCreateFromMessages_UserNotChannelMember_AlreadyLinked covers the
// post-r4259157846 fix: even when matter_channels(M, ch-X) ALREADY exists,
// a user who is NOT in ch-X must NOT be able to write a timeline entry
// tagged "from ch-X" — would let an assignee spoof the source. The fix
// runs RequireChannelMember on every user write, not just the auto-link
// branch.
func TestCreateFromMessages_UserNotChannelMember_AlreadyLinked(t *testing.T) {
	matterRepo := &fakeMatterRepo{matters: map[string]*model.Matter{"m1": newMatterForLLMTest()}}
	repo := newFakeTimelineRepo()
	attRepo := newFakeTimelineAttachmentRepo()
	// Channel link already exists — find returns the linked row, no auto-link.
	channels := &trackingChannelRepoLLM{findResult: &model.MatterChannel{
		ID: "mc-existing", MatterID: "m1", ChannelID: "ch_x", ChannelType: 2,
	}}
	verifier := &stubLinkVerifier{allow: false} // IM says caller NOT in ch_x
	svc := NewTimelineService(
		&stubLLM{out: `{"content":"spoofed"}`},
		repo, attRepo, matterRepo,
		fakeAccessChecker{}, verifier,
		fakeTimelineTx{timelines: repo, attachments: attRepo, channels: channels},
		fakeParticipantRepo{}, newFakeAssigneeRepo(),
		nil,
	)

	in := baseLLMInput()
	in.CallerToken = "user-tok"
	_, _, err := svc.CreateEntry(context.Background(), in)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("expected Forbidden for user-not-in-channel + existing link, got %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("RequireChannelMember must fire on every user write, got %d", verifier.calls)
	}
	if len(repo.byID) != 0 {
		t.Fatalf("no timeline entry must be persisted on Forbidden")
	}
}

// TestCreateFromMessages_UserNotChannelMember_NewLink covers the same
// principle on the auto-link path: pre-tx IM verification rejects before
// the tx is even opened.
func TestCreateFromMessages_UserNotChannelMember_NewLink(t *testing.T) {
	matterRepo := &fakeMatterRepo{matters: map[string]*model.Matter{"m1": newMatterForLLMTest()}}
	repo := newFakeTimelineRepo()
	attRepo := newFakeTimelineAttachmentRepo()
	channels := &trackingChannelRepoLLM{} // find always NotFound
	verifier := &stubLinkVerifier{allow: false}
	svc := NewTimelineService(
		&stubLLM{out: `{"content":"x"}`},
		repo, attRepo, matterRepo,
		fakeAccessChecker{}, verifier,
		fakeTimelineTx{timelines: repo, attachments: attRepo, channels: channels},
		fakeParticipantRepo{}, newFakeAssigneeRepo(),
		nil,
	)

	in := baseLLMInput()
	in.CallerToken = "user-tok"
	_, _, err := svc.CreateEntry(context.Background(), in)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("expected Forbidden, got %v", err)
	}
	if channels.createCalls != 0 {
		t.Fatalf("no auto-link must happen on Forbidden")
	}
}

// TestCreateFromMessages_BotPathCannotAutoLink covers the post-r4259131241
// channel-link gate: bots may not introduce a new matter_channels row to an
// existing matter. They must reuse a link a user has already created. A
// successful happy-path (link already exists) is covered by
// TestCreateFromMessages_HappyPath.
func TestCreateFromMessages_BotPathCannotAutoLink(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"ok"}`}
	repo := newFakeTimelineRepo()
	channels := &trackingChannelRepoLLM{} // FindByMatterAndChannelID always NotFound
	svc := newLLMTimelineSvc(t, llmStub, repo, channels)

	in := baseLLMInput()
	in.CallerToken = "" // simulate bot path
	_, _, err := svc.CreateEntry(context.Background(), in)
	if err == nil {
		t.Fatalf("expected Forbidden for bot auto-link, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("expected Forbidden, got %v", err)
	}
	if channels.createCalls != 0 {
		t.Fatalf("bot path must NOT create a channel link, got %d calls", channels.createCalls)
	}
}

func TestCreateFromMessages_HappyPath(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"summary text"}`}
	repo := newFakeTimelineRepo()
	channels := &trackingChannelRepoLLM{}
	svc := newLLMTimelineSvc(t, llmStub, repo, channels)

	entry, llmRes, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if entry == nil || llmRes == nil {
		t.Fatalf("entry/llmRes should be non-nil")
	}
	if entry.Content == nil || *entry.Content != "summary text" {
		t.Errorf("entry content mismatch: %+v", entry.Content)
	}
	if got := []string(entry.SourceMsgs); !equalStrings(got, []string{"m_a", "m_b"}) {
		t.Errorf("source_msgs mismatch: %v", got)
	}
	if got := []string(entry.RelatedUIDs); !equalStrings(got, []string{"u1", "u2"}) {
		t.Errorf("related_uids mismatch: %v", got)
	}
	if entry.ChannelID == nil {
		t.Errorf("channel_id should be set after auto-link")
	}
	if channels.createCalls != 1 {
		t.Errorf("channel auto-link should fire exactly once, got %d", channels.createCalls)
	}
	if llmStub.gotToolName != "extract_matter_progress" {
		t.Errorf("wrong tool: %s", llmStub.gotToolName)
	}
}

// TestCreateFromMessages_LLMEmptyToolCall asserts that the sentinel
// llm.ErrEmptyToolCall is preserved through the wrap chain so the handler can
// emit 422 LLM_EMPTY_EXTRACTION via errors.Is. (Earlier behaviour translated
// the sentinel into a generic VALIDATION_ERROR — the latest review on PR #34
// flagged that as Blocking, see fix in commit 2785f3e.)
func TestCreateFromMessages_LLMEmptyToolCall(t *testing.T) {
	llmStub := &stubLLM{err: llm.ErrEmptyToolCall}
	repo := newFakeTimelineRepo()
	svc := newLLMTimelineSvc(t, llmStub, repo, nil)

	_, _, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if !errors.Is(err, llm.ErrEmptyToolCall) {
		t.Fatalf("expected wrap of llm.ErrEmptyToolCall, got %v", err)
	}
}

func TestCreateFromMessages_LLMInvalidJSON(t *testing.T) {
	llmStub := &stubLLM{out: `not valid json`}
	repo := newFakeTimelineRepo()
	svc := newLLMTimelineSvc(t, llmStub, repo, nil)

	_, _, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("expected wrap with 'invalid arguments': %v", err)
	}
}

// TestCreateFromMessages_LLMEmptyContent asserts that an empty / whitespace
// content extraction from the LLM also wraps ErrEmptyToolCall so it routes to
// 422 LLM_EMPTY_EXTRACTION (PR #34 Blocking 3 — the empty-content branch
// previously translated to VALIDATION_ERROR which made the 422 path
// unreachable in the handler).
func TestCreateFromMessages_LLMEmptyContent(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"   "}`}
	repo := newFakeTimelineRepo()
	svc := newLLMTimelineSvc(t, llmStub, repo, nil)

	_, _, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if !errors.Is(err, llm.ErrEmptyToolCall) {
		t.Fatalf("expected wrap of llm.ErrEmptyToolCall for empty content, got %v", err)
	}
}

func TestCreateFromMessages_LLMUpstreamErrorPropagates(t *testing.T) {
	upstream := errors.New("llm: upstream status 503")
	llmStub := &stubLLM{err: upstream}
	repo := newFakeTimelineRepo()
	svc := newLLMTimelineSvc(t, llmStub, repo, nil)

	_, _, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if err == nil || !strings.Contains(err.Error(), "extract_matter_progress") {
		t.Fatalf("expected wrapped llm error, got: %v", err)
	}
	if !errors.Is(err, upstream) {
		t.Errorf("upstream error should be wrapped via %%w, got: %v", err)
	}
}

// TestCreateFromMessages_PromptIncludesNewestEntries is the regression guard
// for PR #34 review #4258800148 item 1: ListByMatter ASC was returning the
// OLDEST 3 entries to the LLM as "recent context". The fix uses
// ListRecentByMatter (DESC) so the model sees genuinely recent progress.
func TestCreateFromMessages_PromptIncludesNewestEntries(t *testing.T) {
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	repo := newFakeTimelineRepo()
	for i, txt := range []string{
		"oldest entry", "second oldest", "middle", "second newest", "newest entry",
	} {
		c := txt
		repo.byID[txt] = &model.TimelineEntry{
			ID:        txt,
			MatterID:  "m1",
			Content:   &c,
			CreatedAt: now.Add(time.Duration(i) * time.Hour),
		}
	}

	llmStub := &stubLLM{out: `{"content":"new"}`}
	svc := newLLMTimelineSvc(t, llmStub, repo, &trackingChannelRepoLLM{})

	if _, _, err := svc.CreateEntry(context.Background(), baseLLMInput()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Prompt MUST contain the 3 newest entries.
	for _, want := range []string{"newest entry", "second newest", "middle"} {
		if !strings.Contains(llmStub.gotSystem, want) {
			t.Errorf("system prompt missing newest entry %q\nprompt:\n%s", want, llmStub.gotSystem)
		}
	}
	// And MUST NOT contain the two oldest (would mean ASC regression).
	for _, bad := range []string{"oldest entry", "second oldest"} {
		if strings.Contains(llmStub.gotSystem, bad) {
			t.Errorf("system prompt leaked stale entry %q (ListRecentByMatter regressed to ASC?)", bad)
		}
	}
}

// TestCreateFromMessages_TxFailureDoesNotLeakChannel guards PR #34 review
// item 4: the channel auto-link must run inside the same tx as the entry
// write so a tx-level failure doesn't leave an orphan link.
func TestCreateFromMessages_TxFailureDoesNotLeakChannel(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"ok"}`}
	repo := newFakeTimelineRepo()
	channels := &trackingChannelRepoLLM{}
	matterRepo := &fakeMatterRepo{matters: map[string]*model.Matter{"m1": newMatterForLLMTest()}}
	attRepo := newFakeTimelineAttachmentRepo()
	tx := failingTimelineTx{
		inner: fakeTimelineTx{timelines: repo, attachments: attRepo, channels: channels},
		fail:  errors.New("commit failed"),
	}
	svc := NewTimelineService(llmStub, repo, attRepo, matterRepo, fakeAccessChecker{}, nil, tx, fakeParticipantRepo{}, newFakeAssigneeRepo(), nil)

	_, _, err := svc.CreateEntry(context.Background(), baseLLMInput())
	if err == nil || !strings.Contains(err.Error(), "commit failed") {
		t.Fatalf("expected tx failure to surface, got: %v", err)
	}
	// The structural guarantee: the channel.Create call only happened from
	// inside the tx callback. In a real dbr-backed tx, that op rolls back on
	// the failure here. The point of this assertion is that callers cannot
	// observe a channel write through a non-tx repo handle on the service.
	if channels.createCalls != 1 {
		t.Errorf("channel.Create should be invoked once via the tx-bound store; got %d", channels.createCalls)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
