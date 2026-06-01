package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm/promptstore"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/google/uuid"
)

// Bounds on a single timeline entry's attachments.
const (
	MaxAttachmentsPerEntry = 10
	MaxAttachmentSizeBytes = int64(100) << 20 // 100 MB
	MaxContentLength       = 10000
)

// TimelineStore is the narrow TimelineRepo surface TimelineService depends on.
// sourceChannelID filters by matter_timelines.source_channel_id when non-empty.
type TimelineStore interface {
	Create(ctx context.Context, e *model.TimelineEntry) error
	GetByID(ctx context.Context, id string) (*model.TimelineEntry, error)
	Delete(ctx context.Context, id string) error
	ListByMatter(ctx context.Context, matterID, sourceChannelID string, cursor *string, limit int) ([]*model.TimelineEntry, bool, error)
	ListRecentByMatter(ctx context.Context, matterID string, limit int) ([]*model.TimelineEntry, error)
}

// TimelineAttachmentStore is the narrow TimelineAttachmentRepo surface.
type TimelineAttachmentStore interface {
	CreateMany(ctx context.Context, atts []*model.TimelineAttachment) error
	ListByEntryIDs(ctx context.Context, ids []string) (map[string][]model.TimelineAttachment, error)
	DeleteByEntryID(ctx context.Context, entryID string) error
	// FindByMatterAndFileURL returns apperr.ErrNotFound when no row matches.
	// Used to skip re-inserting an attachment already linked to the matter
	// via an earlier timeline entry — keeps the outputs view one-row-per-file.
	FindByMatterAndFileURL(ctx context.Context, matterID, fileURL string) (*model.TimelineAttachment, error)
}

// ParticipantUpserter is the single-method interface for upserting a participant
// within a transaction.
type ParticipantUpserter interface {
	Upsert(ctx context.Context, matterID, userID string) error
}

// timelineTxRunner runs timeline, attachment, participant, and channel-link
// writes inside a single transaction. The channel store is included so that the
// AI-extraction path can auto-link a previously-unknown channel atomically with
// the timeline entry write — otherwise a tx-level failure could leave an
// orphan channel link.
type timelineTxRunner interface {
	Do(ctx context.Context, fn func(TimelineStore, TimelineAttachmentStore, ParticipantUpserter, TimelineMatterChannelStore) error) error
}

// matterScopeChecker resolves a matter within a space to reject cross-space access.
type matterScopeChecker interface {
	GetByID(ctx context.Context, id, spaceID string) (*model.Matter, error)
}

// TimelineMatterChannelStore is the narrow MatterChannelRepo surface needed
// by TimelineService — used inside the AI-extraction tx to atomically auto-link
// a previously-unknown channel.
type TimelineMatterChannelStore interface {
	FindByMatterAndChannelID(ctx context.Context, matterID, channelID string) (*model.MatterChannel, error)
	Create(ctx context.Context, mc *model.MatterChannel) error
}

// TimelineAttachmentInput is the service-level representation of one attachment.
type TimelineAttachmentInput struct {
	FileURL  string
	FileName *string
	FileSize *int64
	MimeType *string
}

// TimelineInput carries both direct timeline entry input and LLM-backed timeline
// input. A non-empty Messages slice selects the LLM path.
//
// CallerToken is the user's IM auth token (empty for bot callers). It is
// forwarded to the access checker so octoim can verify the caller is an
// actual member of ChannelID before granting channel-link access.
type TimelineInput struct {
	MatterID       string
	SpaceID        string
	ActorUID       string
	ParticipantUID string
	CallerUIDs     []string
	CallerToken    string
	Content        string
	Attachments    []TimelineAttachmentInput
	ChannelType    uint8
	ChannelID      string
	ChannelName    *string
	Messages       []ExtractMessage
}

// TimelineLLMResult mirrors the LLM timeline response body.
//
// StatusSuggestion is the LLM's suggested status change ("open" / "done") or
// nil if the LLM did not suggest one. The server does NOT auto-apply this —
// it is surfaced for client UX (e.g. "AI suggests marking as done; confirm?").
// Status changes still go through the dedicated transition endpoint.
type TimelineLLMResult struct {
	ID               string    `json:"id"`
	SeqNo            int       `json:"seq_no"`
	EntryID          string    `json:"entry_id"`
	Timestamp        int64     `json:"timestamp"`
	RelatedUIDs      []string  `json:"related_uids"`
	Content          string    `json:"content"`
	ChannelID        string    `json:"channel_id"`
	ChannelType      uint8     `json:"channel_type"`
	SourceMsgs       []string  `json:"source_msgs"`
	StatusSuggestion *string   `json:"status_suggestion,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// llmLimiter is the narrow surface TimelineService uses to throttle the
// LLM-backed timeline path. Defined here rather than imported from middleware
// so the service can be unit-tested with a stub.
type llmLimiter interface {
	Allow(key string) bool
}

// channelLinkVerifier gates writes to matter_channels. Satisfied by
// MatterService.RequireChannelMember. Returns nil for empty channelID OR
// empty callerToken (bot path) — call site applies bot policy separately.
type channelLinkVerifier interface {
	RequireChannelMember(ctx context.Context, callerToken, channelID string, callerUIDs []string) error
}

// TimelineService creates, lists, and deletes matter timeline entries.
type TimelineService struct {
	llm            LLMToolCaller
	timelineRepo   TimelineStore
	attachmentRepo TimelineAttachmentStore
	matterRepo     matterScopeChecker
	access         MatterAccessChecker
	linkVerifier   channelLinkVerifier
	tx             timelineTxRunner
	participant    ParticipantUpserter
	assignees      assigneeStore
	limiter        llmLimiter // nil → no rate limit (e.g. tests / single-tenant dev)
	prompts        promptstore.Store
}

// TimelineOption configures a TimelineService at construction.
type TimelineOption func(*TimelineService)

// WithTimelinePromptStore overrides the default embed-backed prompt source.
func WithTimelinePromptStore(store promptstore.Store) TimelineOption {
	return func(s *TimelineService) { s.prompts = store }
}

// NewTimelineService wires the timeline service. limiter may be nil — useful
// for tests and dev environments where LLM cost throttling is not needed.
// In production the limiter should be the same per-process RateLimiter that
// guards LLM cost across all callers. linkVerifier may be nil only in tests
// that do not exercise channel auto-link.
func NewTimelineService(
	llmClient LLMToolCaller,
	timelineRepo TimelineStore,
	attachmentRepo TimelineAttachmentStore,
	matterRepo matterScopeChecker,
	access MatterAccessChecker,
	linkVerifier channelLinkVerifier,
	tx timelineTxRunner,
	participant ParticipantUpserter,
	assignees assigneeStore,
	limiter llmLimiter,
	opts ...TimelineOption,
) *TimelineService {
	s := &TimelineService{
		llm:            llmClient,
		timelineRepo:   timelineRepo,
		attachmentRepo: attachmentRepo,
		matterRepo:     matterRepo,
		access:         access,
		linkVerifier:   linkVerifier,
		tx:             tx,
		participant:    participant,
		assignees:      assignees,
		limiter:        limiter,
		prompts:        defaultPromptStore,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// timelineToolArgs mirrors the JSON schema declared for `extract_matter_progress`.
// Per design-v3.md §3.2 the model returns:
//   - content: free text summary of progress
//   - related_uids: subset of input from_uids involved in this progress
//   - source_msg_ids: subset of input message IDs supporting this progress
//   - status_suggestion: one of "open" / "done" / null (suggestion only;
//     server does not auto-apply, surfaces in response for client UX).
//
// Server-side validation in validateTimelineArgs filters the lists to the
// input set so the model cannot fabricate references; bad/missing inputs
// fall back to safe deterministic defaults.
type timelineToolArgs struct {
	Content          string   `json:"content"`
	RelatedUIDs      []string `json:"related_uids"`
	SourceMsgIDs     []string `json:"source_msg_ids"`
	StatusSuggestion *string  `json:"status_suggestion"`
}

// The extract_matter_progress tool schema lives in
// internal/llm/prompts/extract_progress.tool.json — loaded at runtime via
// the promptstore, not declared inline here.

// timelineValidated is the cleaned, server-trusted derivation of an
// extract_matter_progress LLM result.
//
// HasExplicitCitations distinguishes "LLM cited messages and SourceMsgs is
// the filtered subset" from "LLM cited nothing valid and SourceMsgs falls
// back to all input message IDs". The fallback is fine for the timeline
// entry's source_msgs (it's a UX hint, not a security boundary), but the
// attachment persistence path MUST fail closed — otherwise an LLM that
// omits source_msg_ids would leak every file in the batch onto the matter.
type timelineValidated struct {
	Content              string
	SourceMsgs           []string
	HasExplicitCitations bool
	RelatedUIDs          []string
	StatusSuggestion     *string // "open" / "done" / nil
}

// validateTimelineArgs filters the LLM-emitted lists against the input
// messages, applies safe fallbacks, and clamps status_suggestion to the
// allowed enum. Pure function — easily table-tested.
func validateTimelineArgs(args timelineToolArgs, msgs []ExtractMessage) timelineValidated {
	inputMsgIDs := make(map[string]struct{}, len(msgs))
	inputUIDs := make(map[string]struct{}, len(msgs))
	uidsInOrder := make([]string, 0, len(msgs))
	seenUID := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		inputMsgIDs[m.MessageID] = struct{}{}
		if m.FromUID == "" {
			continue
		}
		inputUIDs[m.FromUID] = struct{}{}
		if _, dup := seenUID[m.FromUID]; !dup {
			seenUID[m.FromUID] = struct{}{}
			uidsInOrder = append(uidsInOrder, m.FromUID)
		}
	}

	seenMsg := make(map[string]struct{}, len(args.SourceMsgIDs))
	sourceMsgs := make([]string, 0, len(args.SourceMsgIDs))
	for _, id := range args.SourceMsgIDs {
		if _, ok := inputMsgIDs[id]; !ok {
			continue
		}
		if _, dup := seenMsg[id]; dup {
			continue
		}
		seenMsg[id] = struct{}{}
		sourceMsgs = append(sourceMsgs, id)
	}
	hasExplicitCitations := len(sourceMsgs) > 0
	if !hasExplicitCitations {
		sourceMsgs = make([]string, 0, len(msgs))
		for _, m := range msgs {
			sourceMsgs = append(sourceMsgs, m.MessageID)
		}
	}

	seenRel := make(map[string]struct{}, len(args.RelatedUIDs))
	related := make([]string, 0, len(args.RelatedUIDs))
	for _, uid := range args.RelatedUIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, ok := inputUIDs[uid]; !ok {
			continue
		}
		if _, dup := seenRel[uid]; dup {
			continue
		}
		seenRel[uid] = struct{}{}
		related = append(related, uid)
	}
	if len(related) == 0 {
		related = uidsInOrder
	}

	var status *string
	if args.StatusSuggestion != nil {
		s := strings.TrimSpace(*args.StatusSuggestion)
		if s == string(model.MatterStatusOpen) || s == string(model.MatterStatusDone) {
			status = &s
		}
	}

	return timelineValidated{
		Content:              clipRunes(args.Content, MaxContentLength),
		SourceMsgs:           sourceMsgs,
		HasExplicitCitations: hasExplicitCitations,
		RelatedUIDs:          related,
		StatusSuggestion:     status,
	}
}

// CreateEntry writes a direct timeline entry when Messages is empty. When
// Messages is non-empty, it asks the LLM to summarize the messages and writes
// the generated summary as the timeline entry.
func (s *TimelineService) CreateEntry(ctx context.Context, in TimelineInput) (*model.TimelineEntry, *TimelineLLMResult, error) {
	if len(in.Messages) > 0 {
		return s.createFromMessages(ctx, in)
	}
	entry, err := s.createDirect(ctx, in)
	return entry, nil, err
}

func (s *TimelineService) createDirect(ctx context.Context, in TimelineInput) (*model.TimelineEntry, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" && len(in.Attachments) == 0 {
		return nil, apperr.ValidationError("content or attachments required", "")
	}
	if len(content) > MaxContentLength {
		return nil, apperr.ValidationError("content too long", "content")
	}
	if len(in.Attachments) > MaxAttachmentsPerEntry {
		return nil, apperr.ValidationError("too many attachments", "attachments")
	}
	for _, a := range in.Attachments {
		if a.FileURL == "" {
			return nil, apperr.ValidationError("file_url required", "attachments")
		}
		if a.FileSize != nil && *a.FileSize > MaxAttachmentSizeBytes {
			return nil, apperr.ValidationError("attachment too large", "attachments")
		}
	}

	matter, err := s.matterRepo.GetByID(ctx, in.MatterID, in.SpaceID)
	if err != nil {
		return nil, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, in.CallerUIDs, in.ChannelID, in.CallerToken)
	if accessErr != nil {
		return nil, accessErr
	}
	if !ok {
		return nil, apperr.Forbidden("not authorized to access this matter")
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}
	entry := &model.TimelineEntry{
		MatterID: in.MatterID,
		UserID:   in.ActorUID,
		Content:  contentPtr,
	}

	err = s.tx.Do(ctx, func(ts TimelineStore, as TimelineAttachmentStore, ps ParticipantUpserter, _ TimelineMatterChannelStore) error {
		if err := ts.Create(ctx, entry); err != nil {
			return err
		}
		if len(in.Attachments) > 0 {
			atts := make([]*model.TimelineAttachment, 0, len(in.Attachments))
			for _, a := range in.Attachments {
				atts = append(atts, &model.TimelineAttachment{
					EntryID:   entry.ID,
					MatterID:  in.MatterID,
					FileURL:   a.FileURL,
					FileName:  a.FileName,
					FileSize:  a.FileSize,
					MimeType:  a.MimeType,
					SenderUID: in.ActorUID,
					// SenderUname / SentAt deliberately left zero — the
					// direct path lacks a per-attachment sender name and
					// CreateMany defaults SentAt to insert time, which
					// matches the legacy semantics for this path.
				})
			}
			if err := as.CreateMany(ctx, atts); err != nil {
				return err
			}
			entry.Attachments = make([]model.TimelineAttachment, 0, len(atts))
			for _, a := range atts {
				entry.Attachments = append(entry.Attachments, *a)
			}
		}
		return ps.Upsert(ctx, in.MatterID, in.ActorUID)
	})
	if err != nil {
		return nil, err
	}
	if entry.Attachments == nil {
		entry.Attachments = []model.TimelineAttachment{}
	}
	return entry, nil
}

// BuildContinuationPrompt composes the prompt for a runtime task dispatched
// by a @mention. The agent should be able to pick up where the conversation
// left off, so we feed it:
//   - matter title + description (orientation)
//   - botUID (so the agent knows "this is me")
//   - timeline history oldest-first (last 30 entries — bounded for openclaw
//     context size; longer matters truncate the oldest middle ground)
// Each timeline entry is rendered as "[actor_uid]: content" — opaque uids
// rather than display names because the matter service doesn't resolve names
// (octoim does), and the agent treats them as conversation participants
// regardless.
//
// Returns (prompt, matter.title, err). Matter title is also returned so the
// caller doesn't need a separate matterRepo lookup.
func (s *TimelineService) BuildContinuationPrompt(ctx context.Context, matterID, spaceID, botUID string) (string, string, error) {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return "", "", err
	}
	if matter == nil {
		return "", "", apperr.MatterNotFound()
	}
	const historyLimit = 30
	entries, err := s.timelineRepo.ListRecentByMatter(ctx, matterID, historyLimit)
	if err != nil {
		// Don't fail dispatch on history fetch — proceed with empty history.
		entries = nil
	}

	var b strings.Builder
	b.WriteString("You are agent `")
	b.WriteString(botUID)
	b.WriteString("` working on the following matter.\n\n")
	b.WriteString("# Matter\n\n")
	b.WriteString("## Title\n")
	b.WriteString(matter.Title)
	b.WriteString("\n\n")
	if matter.Description != nil && strings.TrimSpace(*matter.Description) != "" {
		b.WriteString("## Goal\n")
		b.WriteString(*matter.Description)
		b.WriteString("\n\n")
	}

	// ListRecentByMatter returns newest first; reverse for chronological order.
	if len(entries) > 0 {
		b.WriteString("# Conversation so far (oldest first)\n\n")
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Content == nil || strings.TrimSpace(*e.Content) == "" {
				continue
			}
			role := "user"
			if e.UserID == botUID {
				role = "you (agent)"
			}
			b.WriteString("- **")
			b.WriteString(role)
			b.WriteString("** (")
			b.WriteString(e.UserID)
			b.WriteString("): ")
			b.WriteString(*e.Content)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("# Your task now\n\n")
	b.WriteString("Reply to the latest user message. Keep your response focused and actionable.")

	return b.String(), matter.Title, nil
}

// CreateInternalEntry is the bot-writeback path used by /v1/internal/matters/
// :id/timeline. It bypasses the access check because the internal endpoint is
// gated on a shared X-Internal-Token (only trusted services like octo-server's
// bot_task ack handler call it), and the actor is whatever uid the trusted
// caller specifies (typically the bot uid that ran the task). Still verifies
// matter existence in the given space — a wrong space_id is a programming
// error from the caller, not silent-write territory.
func (s *TimelineService) CreateInternalEntry(ctx context.Context, matterID, spaceID, actorUID, content string) (*model.TimelineEntry, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, apperr.ValidationError("content required", "content")
	}
	if len(content) > MaxContentLength {
		return nil, apperr.ValidationError("content too long", "content")
	}
	if strings.TrimSpace(actorUID) == "" {
		return nil, apperr.ValidationError("actor_uid required", "actor_uid")
	}
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, err
	}
	if matter == nil {
		return nil, apperr.MatterNotFound()
	}
	contentPtr := content
	entry := &model.TimelineEntry{
		MatterID: matterID,
		UserID:   actorUID,
		Content:  &contentPtr,
	}
	err = s.tx.Do(ctx, func(ts TimelineStore, _ TimelineAttachmentStore, ps ParticipantUpserter, _ TimelineMatterChannelStore) error {
		if err := ts.Create(ctx, entry); err != nil {
			return err
		}
		return ps.Upsert(ctx, matterID, actorUID)
	})
	if err != nil {
		return nil, err
	}
	if entry.Attachments == nil {
		entry.Attachments = []model.TimelineAttachment{}
	}
	return entry, nil
}

// buildLLMAttachments flattens file attachments carried by messages that the
// LLM EXPLICITLY cited. Fails closed: when the LLM returned no valid
// source_msg_ids, no attachments are persisted — otherwise an LLM that
// omits citations would leak every file in the batch onto the matter
// (the timeline entry's source_msgs falls back to all input msg IDs as a
// UX convenience, but that's not a privacy boundary; attachment persistence
// must be).
//
// Pure function — easily table-tested. Output rows have EntryID, MatterID,
// sender fields, SentAt, and Description populated; the repo fills ID /
// CreatedAt at insert time. Caps at MaxAttachmentsPerEntry to keep one LLM
// call from creating an unbounded number of file rows when a long chat
// contains many uploads.
func buildLLMAttachments(entryID string, in TimelineInput, v timelineValidated) []*model.TimelineAttachment {
	if len(in.Messages) == 0 {
		return nil
	}
	// Fail closed when the LLM did not produce explicit citations. v.SourceMsgs
	// at this point would be the all-messages fallback, which is correct for
	// the timeline entry's source_msgs metadata but wrong as an attachment
	// gate.
	if !v.HasExplicitCitations {
		return nil
	}
	cited := make(map[string]struct{}, len(v.SourceMsgs))
	for _, id := range v.SourceMsgs {
		cited[id] = struct{}{}
	}
	desc := clipRunes(v.Content, 500)
	var descPtr *string
	if desc != "" {
		descPtr = &desc
	}
	seen := make(map[string]struct{})
	out := make([]*model.TimelineAttachment, 0)
	for _, m := range in.Messages {
		if _, ok := cited[m.MessageID]; !ok {
			continue
		}
		for _, a := range m.Attachments {
			url := strings.TrimSpace(a.FileURL)
			if url == "" {
				continue
			}
			if _, dup := seen[url]; dup {
				continue
			}
			seen[url] = struct{}{}
			var name *string
			if a.FileName != "" {
				n := a.FileName
				name = &n
			}
			out = append(out, &model.TimelineAttachment{
				EntryID:     entryID,
				MatterID:    in.MatterID,
				FileURL:     url,
				FileName:    name,
				Description: descPtr,
				SenderUID:   m.FromUID,
				SenderUname: m.FromUname,
				SentAt:      time.Unix(m.Timestamp, 0).UTC(),
			})
			if len(out) >= MaxAttachmentsPerEntry {
				return out
			}
		}
	}
	return out
}

func (s *TimelineService) createFromMessages(ctx context.Context, in TimelineInput) (*model.TimelineEntry, *TimelineLLMResult, error) {
	if err := validateMessages(in.Messages); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.ParticipantUID) == "" {
		return nil, nil, apperr.ValidationError("participant_uid required", "participant_uid")
	}
	if strings.TrimSpace(in.ChannelID) == "" {
		return nil, nil, apperr.ValidationError("channel_id required", "channel_id")
	}
	if in.ChannelType != 1 && in.ChannelType != 2 && in.ChannelType != 5 {
		return nil, nil, apperr.ValidationError("channel_type must be one of 1, 2, 5", "channel_type")
	}

	matter, err := s.matterRepo.GetByID(ctx, in.MatterID, in.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	ok, err := s.access.CanAccessMatter(ctx, matter, in.CallerUIDs, in.ChannelID, in.CallerToken)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, apperr.Forbidden("not authorized to access this matter")
	}

	// Rate-limit AFTER access — a forbidden caller must not consume the
	// legitimate user's cooldown. Key is
	// (matter_id, actor_uid) per design-v3.md §8.
	if s.limiter != nil && in.ActorUID != "" {
		if !s.limiter.Allow(in.MatterID + ":" + in.ActorUID) {
			return nil, nil, apperr.RateLimited("please wait before retrying")
		}
	}

	// Channel-source verification: EVERY user
	// timeline write must prove channel membership for in.ChannelID, not
	// just the auto-link branch. Otherwise an assignee could spoof "from
	// channel X" entries on an already-linked matter without being in X.
	// Bot path: skipped here; the existing-link case is accepted (bot
	// trust model — bot can't expand exposure, only spoof source on
	// channels already linked by a user). The new-link case is rejected
	// inside the tx below.
	if s.linkVerifier != nil && in.CallerToken != "" {
		if verr := s.linkVerifier.RequireChannelMember(ctx, in.CallerToken, in.ChannelID, in.CallerUIDs); verr != nil {
			return nil, nil, verr
		}
	}

	recent, err := s.timelineRepo.ListRecentByMatter(ctx, in.MatterID, 3)
	if err != nil {
		return nil, nil, err
	}
	assignees, err := s.assignees.ListByMatter(ctx, in.MatterID)
	if err != nil {
		return nil, nil, err
	}

	prompt, err := s.prompts.Get(ctx, promptExtractProgress)
	if err != nil {
		return nil, nil, fmt.Errorf("load extract_progress prompt: %w", err)
	}
	systemPrompt, err := renderTimelineSystemPrompt(prompt, matter, assignees, recent, in)
	if err != nil {
		return nil, nil, fmt.Errorf("render extract_progress prompt: %w", err)
	}
	userPrompt := buildMessagesPrompt(in.Messages)

	// No explicit temperature pin here — progress extraction tolerates the
	// gateway default. The extract-matter path pins 0.2 for stable title
	// naming, but timeline outputs are summary-style strings without a
	// "right answer" the model needs to converge on. Revisit if A/B data
	// shows otherwise.
	raw, err := s.llm.CallTool(ctx, systemPrompt, userPrompt, prompt.Tool, llm.WithSystemPromptCache())
	if err != nil {
		return nil, nil, fmt.Errorf("llm extract_matter_progress: %w", err)
	}
	var args timelineToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, nil, fmt.Errorf("llm extract_matter_progress: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Content) == "" {
		return nil, nil, fmt.Errorf("llm extract_matter_progress: empty content: %w", llm.ErrEmptyToolCall)
	}

	v := validateTimelineArgs(args, in.Messages)

	content := strings.TrimSpace(v.Content)
	sourceChannelID := in.ChannelID
	channelType := in.ChannelType
	entry := &model.TimelineEntry{
		MatterID:        in.MatterID,
		UserID:          in.ParticipantUID,
		Content:         &content,
		ChannelType:     &channelType,
		SourceChannelID: &sourceChannelID,
		SourceMsgs:      model.JSONStringSlice(v.SourceMsgs),
		RelatedUIDs:     model.JSONStringSlice(v.RelatedUIDs),
	}

	if err := s.tx.Do(ctx, func(ts TimelineStore, as TimelineAttachmentStore, ps ParticipantUpserter, cs TimelineMatterChannelStore) error {
		// Resolve / auto-link the channel inside the same tx so an entry-write
		// failure does not leave an orphan channel link.
		mc, ferr := cs.FindByMatterAndChannelID(ctx, in.MatterID, in.ChannelID)
		if ferr != nil {
			if !errors.Is(ferr, apperr.ErrNotFound) {
				return ferr
			}
			// New-link write site:
			//   - Bot path: forbidden — bots may not introduce new channel
			//     links to existing matters; require a user to link first.
			//   - User path: already IM-verified above (pre-tx); proceed.
			if in.CallerToken == "" {
				return apperr.Forbidden("bot may not link a new channel to an existing matter; ask a channel member to link it first")
			}
			mc = &model.MatterChannel{
				ID:          uuid.New().String(),
				MatterID:    in.MatterID,
				ChannelID:   in.ChannelID,
				ChannelType: in.ChannelType,
				ChannelName: in.ChannelName,
				LinkedBy:    in.ParticipantUID,
				CreatedAt:   time.Now(),
			}
			if cerr := cs.Create(ctx, mc); cerr != nil {
				return cerr
			}
			fetched, ferr2 := cs.FindByMatterAndChannelID(ctx, in.MatterID, in.ChannelID)
			if ferr2 != nil {
				return ferr2
			}
			mc = fetched
		}
		entry.ChannelID = &mc.ID
		if err := ts.Create(ctx, entry); err != nil {
			return err
		}
		// Persist file attachments carried by the source messages so the
		// matter outputs view can show them. Dedup against existing rows on
		// the same matter — a forwarded file should not produce a second
		// entry in the outputs list.
		if atts := buildLLMAttachments(entry.ID, in, v); len(atts) > 0 {
			toInsert := make([]*model.TimelineAttachment, 0, len(atts))
			for _, a := range atts {
				existing, ferr := as.FindByMatterAndFileURL(ctx, in.MatterID, a.FileURL)
				if ferr != nil && !errors.Is(ferr, apperr.ErrNotFound) {
					return ferr
				}
				if existing != nil {
					continue
				}
				toInsert = append(toInsert, a)
			}
			if len(toInsert) > 0 {
				if err := as.CreateMany(ctx, toInsert); err != nil {
					return err
				}
			}
		}
		return ps.Upsert(ctx, in.MatterID, in.ParticipantUID)
	}); err != nil {
		return nil, nil, err
	}
	entry.Attachments = []model.TimelineAttachment{}

	var ts int64
	if n := len(in.Messages); n > 0 {
		ts = in.Messages[n-1].Timestamp
	}

	return entry, &TimelineLLMResult{
		ID:               matter.ID,
		SeqNo:            matter.SeqNo,
		EntryID:          entry.ID,
		Timestamp:        ts,
		RelatedUIDs:      v.RelatedUIDs,
		Content:          content,
		ChannelID:        in.ChannelID,
		ChannelType:      in.ChannelType,
		SourceMsgs:       v.SourceMsgs,
		StatusSuggestion: v.StatusSuggestion,
		CreatedAt:        entry.CreatedAt,
	}, nil
}

func (s *TimelineService) ListEntries(
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	sourceChannelID, callerToken string,
	cursor *string,
	limit int,
) ([]*model.TimelineEntry, bool, error) {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, false, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, sourceChannelID, callerToken)
	if accessErr != nil {
		return nil, false, accessErr
	}
	if !ok {
		return nil, false, apperr.Forbidden("not authorized to access this matter")
	}
	entries, hasMore, err := s.timelineRepo.ListByMatter(ctx, matterID, sourceChannelID, cursor, limit)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return entries, hasMore, nil
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	byEntry, err := s.attachmentRepo.ListByEntryIDs(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	for _, e := range entries {
		if atts, ok := byEntry[e.ID]; ok {
			e.Attachments = atts
		} else {
			e.Attachments = []model.TimelineAttachment{}
		}
	}
	return entries, hasMore, nil
}

// DeleteEntry removes a timeline entry. matterID must match the entry's stored
// matter_id — a request to /matters/A/timeline/<entry-from-B> returns NotFound
// rather than silently deleting from B.
func (s *TimelineService) DeleteEntry(ctx context.Context, matterID, id, spaceID string, callerUIDs []string, actorUID, sourceChannelID, callerToken string) error {
	entry, err := s.timelineRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if entry.MatterID != matterID {
		return apperr.ErrNotFound
	}
	matter, err := s.matterRepo.GetByID(ctx, entry.MatterID, spaceID)
	if err != nil {
		return err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, sourceChannelID, callerToken)
	if accessErr != nil {
		return accessErr
	}
	if !ok {
		return apperr.Forbidden("not authorized to access this matter")
	}
	if entry.UserID != actorUID {
		return apperr.ErrForbidden
	}
	return s.tx.Do(ctx, func(ts TimelineStore, as TimelineAttachmentStore, _ ParticipantUpserter, _ TimelineMatterChannelStore) error {
		if err := as.DeleteByEntryID(ctx, id); err != nil {
			return err
		}
		return ts.Delete(ctx, id)
	})
}

// timelinePromptData is the data shape consumed by extract_progress.md.
// Optional fields use the empty string / nil slice as the "absent" signal
// so the template's {{if .X}} branches behave identically to the old
// Go-side conditionals.
type timelinePromptData struct {
	Now               string
	MatterTitle       string
	MatterDescription string
	MatterStatus      string
	Deadline          string
	Assignees         string
	ChannelName       string
	RecentEntries     []timelineRecentEntry
}

type timelineRecentEntry struct {
	When string
	Text string
}

// buildTimelineSystemPrompt is a test-only helper preserved so the golden
// tests keep their signature. Panics on template error for the same reason
// as buildExtractSystemPrompt: embedded prompts are validated by init() at
// process start. Production code must use renderTimelineSystemPrompt via
// createFromMessages, which wraps the error properly.
//
// This helper always reads from the package-level defaultPromptStore and
// IGNORES any per-service override set via WithTimelinePromptStore.
// Test code that wants to stub the store must call renderTimelineSystemPrompt
// against the stub directly.
func buildTimelineSystemPrompt(matter *model.Matter, assignees []*model.MatterAssignee, recent []*model.TimelineEntry, in TimelineInput) string {
	prompt, err := defaultPromptStore.Get(context.Background(), promptExtractProgress)
	if err != nil {
		panic("service: extract_progress prompt unavailable: " + err.Error())
	}
	out, err := renderTimelineSystemPrompt(prompt, matter, assignees, recent, in)
	if err != nil {
		panic("service: extract_progress prompt render: " + err.Error())
	}
	return out
}

func renderTimelineSystemPrompt(prompt promptstore.Prompt, matter *model.Matter, assignees []*model.MatterAssignee, recent []*model.TimelineEntry, in TimelineInput) (string, error) {
	data := timelinePromptData{
		Now:          time.Now().UTC().Format(time.RFC3339),
		MatterTitle:  matter.Title,
		MatterStatus: string(matter.Status),
	}
	if matter.Description != nil && *matter.Description != "" {
		data.MatterDescription = *matter.Description
	}
	if matter.Deadline != nil {
		data.Deadline = matter.Deadline.UTC().Format(time.RFC3339)
	}
	if len(assignees) > 0 {
		ids := make([]string, 0, len(assignees))
		for _, a := range assignees {
			ids = append(ids, a.UserID)
		}
		data.Assignees = strings.Join(ids, ", ")
	}
	if in.ChannelName != nil && *in.ChannelName != "" {
		data.ChannelName = *in.ChannelName
	}
	for _, e := range recent {
		if e.Content == nil {
			continue
		}
		data.RecentEntries = append(data.RecentEntries, timelineRecentEntry{
			When: e.CreatedAt.UTC().Format(time.RFC3339),
			Text: *e.Content,
		})
	}
	return prompt.Render(data)
}
