package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/Mininglamp-OSS/octo-matter/internal/util/mention"
	"github.com/gin-gonic/gin"
)

type TimelineHandler struct {
	svc         *service.TimelineService
	matterSvc   *service.MatterService
	notifier    notification.Notifier
	worker      *notification.Worker
	botTaskRepo *repository.BotTaskRepo // bot dispatch via local DB enqueue
}

// NewTimelineHandler wires the timeline handler. Note: the LLM rate limiter
// is NOT a handler concern — it is injected into TimelineService directly so
// throttling fires AFTER the access check.
func NewTimelineHandler(
	svc *service.TimelineService,
	matterSvc *service.MatterService,
	notifier notification.Notifier,
	worker *notification.Worker,
	botTaskRepo *repository.BotTaskRepo,
) *TimelineHandler {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &TimelineHandler{svc: svc, matterSvc: matterSvc, notifier: notifier, worker: worker, botTaskRepo: botTaskRepo}
}

type attachmentInput struct {
	FileURL  string  `json:"file_url" binding:"required,http_url"`
	FileName *string `json:"file_name"`
	FileSize *int64  `json:"file_size"`
	MimeType *string `json:"mime_type"`
}

type timelineReq struct {
	Content        string              `json:"content" binding:"max=10000"`
	Attachments    []attachmentInput   `json:"attachments" binding:"max=10,dive"`
	ChannelType    uint8               `json:"channel_type" binding:"omitempty,oneof=1 2 5"`
	ChannelID      string              `json:"channel_id"`
	ChannelName    *string             `json:"channel_name"`
	ParticipantUID string              `json:"participant_uid"`
	MatterID       string              `json:"matter_id"`
	Msgs           []extractMessageReq `json:"msgs" binding:"max=200,dive"`
}

func (h *TimelineHandler) Create(c *gin.Context) {
	var req timelineReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	if req.MatterID != "" && req.MatterID != matterID {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "body.matter_id must match path :id", nil)
		return
	}

	isLLMPath := len(req.Msgs) > 0
	if isLLMPath {
		if !authorizedActor(c, req.ParticipantUID) {
			failCode(c, http.StatusForbidden, "FORBIDDEN", "participant_uid not authorized for caller", nil)
			return
		}
		// Rate limiting moved into TimelineService.createFromMessages so it
		// fires AFTER the access check — a forbidden caller must not consume
		// the legitimate user's cooldown.
	}

	atts := make([]service.TimelineAttachmentInput, 0, len(req.Attachments))
	for _, a := range req.Attachments {
		atts = append(atts, service.TimelineAttachmentInput{
			FileURL:  a.FileURL,
			FileName: a.FileName,
			FileSize: a.FileSize,
			MimeType: a.MimeType,
		})
	}

	msgs := make([]service.ExtractMessage, 0, len(req.Msgs))
	for _, m := range req.Msgs {
		msgAtts := make([]service.ExtractMessageAttachment, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			msgAtts = append(msgAtts, service.ExtractMessageAttachment{FileName: a.FileName, FileURL: a.FileURL})
		}
		msgs = append(msgs, service.ExtractMessage{
			MessageID:   m.MessageID,
			FromUID:     m.FromUID,
			FromUname:   m.FromUname,
			Timestamp:   m.Timestamp,
			Content:     m.Content,
			Attachments: msgAtts,
		})
	}

	callerUIDs := relatedUIDs(c)
	if isLLMPath {
		callerUIDs = effectiveCallerUIDs(c)
	}
	entry, llmResult, err := h.svc.CreateEntry(c.Request.Context(), service.TimelineInput{
		MatterID:       matterID,
		SpaceID:        spaceID(c),
		ActorUID:       uid(c),
		ParticipantUID: req.ParticipantUID,
		CallerUIDs:     callerUIDs,
		CallerToken:    callerToken(c),
		Content:        req.Content,
		Attachments:    atts,
		ChannelType:    req.ChannelType,
		ChannelID:      req.ChannelID,
		ChannelName:    req.ChannelName,
		Messages:       msgs,
	})
	if err != nil {
		h.respondCreateErr(c, err)
		return
	}

	h.notifyEntryAdded(c, matterID, spaceID(c), entry.UserID, actorNameFor(c, entry.UserID))
	// PoC3c: if this comment @-mentions one or more agents (kind=agent),
	// fire a dispatch per agent with a continuation prompt that includes
	// the matter context + full timeline so far + this new comment.
	h.dispatchMentionedAgents(c, matterID, spaceID(c), entry)
	if llmResult != nil {
		created(c, llmResult)
		return
	}
	created(c, entry)
}

// dispatchMentionedAgents extracts agent mentions from the newly-created
// timeline entry and queues a runtime task per mention. Fire-and-forget,
// best-effort — a dispatch failure is logged, not surfaced to the comment
// author (the comment itself was persisted successfully).
func (h *TimelineHandler) dispatchMentionedAgents(c *gin.Context, matterID, sid string, entry *model.TimelineEntry) {
	if entry == nil || entry.Content == nil {
		return
	}
	// Mention dispatch goes through local matter_bot_task (botTaskRepo).
	// Legacy HTTP dispatch to octo-fleet removed (plan §1 — matter doesn't
	// call fleet/server).
	if h.botTaskRepo == nil {
		return
	}
	agentBotUIDs := mention.AgentIDs(mention.Parse(*entry.Content))
	if len(agentBotUIDs) == 0 {
		return
	}
	// Snapshot fields needed for the goroutine. Span context is unsafe
	// past handler return; use background ctx with our own timeout.
	requesterUID := uid(c)
	for _, botUID := range agentBotUIDs {
		bot := botUID
		h.worker.Submit(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			prompt, matterTitle, err := h.svc.BuildContinuationPrompt(ctx, matterID, sid, bot)
			if err != nil {
				log.Printf("[mention-dispatch] build prompt failed matter=%s bot=%s: %v", matterID, bot, err)
				return
			}

			triggerID := entry.ID
			task := &model.BotTask{
				MatterID:       matterID,
				SpaceID:        sid,
				BotUID:         bot,
				TriggerKind:    "mention",
				TriggerEntryID: &triggerID,
				Prompt:         prompt,
				MatterTitle:    matterTitle,
			}
			inserted, ierr := h.botTaskRepo.Insert(ctx, task)
			if ierr != nil {
				log.Printf("[mention-dispatch] DB insert failed matter=%s bot=%s: %v", matterID, bot, ierr)
				h.matterSvc.RecordAgentActivity(ctx, matterID, sid, bot, service.ActionAgentTaskFailed,
					map[string]any{"bot_uid": bot, "task_id": "", "error": "mention dispatch: " + ierr.Error()})
				return
			}
			if !inserted {
				// Dedup hit on uk_trigger — silently skip; activity already
				// recorded from the earlier enqueue.
				log.Printf("[mention-dispatch] matter=%s bot=%s dedup (trigger=%s already queued)", matterID, bot, triggerID)
				return
			}
			log.Printf("[mention-dispatch] matter=%s bot=%s queued task_id=%s (from comment)", matterID, bot, task.ID)
			h.matterSvc.RecordAgentActivity(ctx, matterID, sid, bot, service.ActionAgentDispatched,
				map[string]any{"bot_uid": bot, "task_id": task.ID, "trigger": "mention", "requester": requesterUID})
		})
	}
}

func (h *TimelineHandler) List(c *gin.Context) {
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var cursor *string
	if cur := c.Query("cursor"); cur != "" {
		cursor = &cur
	}
	entries, hasMore, err := h.svc.ListEntries(c.Request.Context(), matterID, spaceID(c), relatedUIDs(c), c.Query("source_channel_id"), callerToken(c), cursor, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	var nextCursor string
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	paginated(c, entries, hasMore, nextCursor)
}

func (h *TimelineHandler) Delete(c *gin.Context) {
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	entryID := c.Param("entry_id")
	if !validUUID(entryID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid entry_id format", nil)
		return
	}
	if err := h.svc.DeleteEntry(c.Request.Context(), matterID, entryID, spaceID(c), relatedUIDs(c), uid(c), "", callerToken(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

// BotFeed handles GET /api/v1/bots/:bot_uid/feed?limit=50. User-facing
// endpoint: caller must own this bot (user auth path expands related_uids
// to include owned_bots) or BE this bot (bot auth path has bot_uid in
// related_uids). Returns the same merged timeline+activity shape as the
// internal endpoint.
//
// Replaces the fleet `/v1/runtimes/bots/:id/feed` proxy — web now calls
// matter directly, eliminating the X-Internal-Token shared-secret hop.
func (h *TimelineHandler) BotFeed(c *gin.Context) {
	botUID := strings.TrimSpace(c.Param("bot_uid"))
	if botUID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "bot_uid required", nil)
		return
	}
	allowed := false
	for _, u := range relatedUIDs(c) {
		if u == botUID {
			allowed = true
			break
		}
	}
	if !allowed {
		failCode(c, http.StatusForbidden, "FORBIDDEN", "not your bot", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.svc.ListBotFeed(c.Request.Context(), botUID, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, items)
}

func (h *TimelineHandler) respondCreateErr(c *gin.Context, err error) {
	if errors.Is(err, llm.ErrEmptyToolCall) {
		failCode(c, http.StatusUnprocessableEntity, "LLM_EMPTY_EXTRACTION", "LLM returned no extraction", nil)
		return
	}
	if isLLMUpstreamErr(err) {
		failCode(c, http.StatusBadGateway, "LLM_UPSTREAM_ERROR", "upstream LLM error", nil)
		return
	}
	if _, ok := apperr.AsAppError(err); ok {
		respondErr(c, err)
		return
	}
	respondErr(c, err)
}

func (h *TimelineHandler) notifyEntryAdded(c *gin.Context, matterID, spaceID, actorUID, actorName string) {
	if h.worker == nil {
		return
	}
	h.worker.Submit(func() {
		matter, err := h.matterSvc.GetMatterForNotification(context.Background(), matterID, spaceID)
		if err != nil {
			return
		}
		assignees, _ := h.matterSvc.ListAssigneeIDs(context.Background(), matterID)
		participants, _ := h.matterSvc.ListParticipantIDs(context.Background(), matterID)
		h.notifier.NotifyTimelineEntryAdded(matter, actorUID, actorName, assignees, participants)
	})
}
