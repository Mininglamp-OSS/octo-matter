package handler

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/runtime"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type MatterHandler struct {
	svc         *service.MatterService
	notifier    notification.Notifier
	worker      *notification.Worker
	botTaskRepo *repository.BotTaskRepo // bot dispatch via local DB enqueue
}

func NewMatterHandler(svc *service.MatterService, notifier notification.Notifier, worker *notification.Worker, botTaskRepo *repository.BotTaskRepo) *MatterHandler {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &MatterHandler{svc: svc, notifier: notifier, worker: worker, botTaskRepo: botTaskRepo}
}

// createMatterSourceMsgRef accepts the client's existing payload shape — the
// full message object mirroring /v1/matters/extract's `msgs` field. Only
// message_id is read here; any companion fields (content, from_uid, ...) are
// ignored server-side per issue #40's "store ids, not bodies" design.
type createMatterSourceMsgRef struct {
	MessageID string `json:"message_id" binding:"required,max=255"`
}

type createMatterReq struct {
	Title             string   `json:"title" binding:"required,max=500"`
	Description       *string  `json:"description" binding:"omitempty,max=10000"`
	AssigneeIDs       []string `json:"assignee_ids"`
	Deadline          *string  `json:"deadline"`
	RemindAt          *string  `json:"remind_at"`
	SourceChannelID   *string  `json:"source_channel_id"`
	SourceChannelType *uint8   `json:"source_channel_type" binding:"omitempty,oneof=1 2 5"`
	SourceName        *string  `json:"source_name"`
	// Pointer so the handler can tell "field absent" (nil) from "field
	// explicitly empty" (non-nil, len 0). The former falls back to
	// source_msgs; the latter is a deliberate "clear the list" signal and
	// must be preserved as JSON [] in storage.
	SourceMsgIDs *[]string                  `json:"source_msg_ids" binding:"omitempty,max=200,dive,max=255"`
	SourceMsgs   []createMatterSourceMsgRef `json:"source_msgs" binding:"omitempty,max=200,dive"`
}

// derivedSourceMsgIDs collapses the two accepted payload shapes into the flat
// id list the storage layer wants. Explicit `source_msg_ids` always wins when
// present (including the empty-array case) so a client that has migrated to
// the cleaner contract cannot be re-broadened by stale `source_msgs` data in
// the same request. A returned non-nil empty slice signals "store JSON []";
// nil signals "store SQL NULL".
func (r *createMatterReq) derivedSourceMsgIDs() []string {
	if r.SourceMsgIDs != nil {
		// Return a non-nil slice even for the empty case so the storage
		// layer writes JSON [] (not SQL NULL) — explicit empty is data.
		ids := *r.SourceMsgIDs
		if ids == nil {
			ids = []string{}
		}
		return ids
	}
	if len(r.SourceMsgs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.SourceMsgs))
	for _, m := range r.SourceMsgs {
		ids = append(ids, m.MessageID)
	}
	return ids
}

func (h *MatterHandler) Create(c *gin.Context) {
	var req createMatterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	sid := spaceID(c)
	userID := uid(c)
	matter := &model.Matter{
		SpaceID:           sid,
		Title:             req.Title,
		Description:       req.Description,
		CreatorID:         userID,
		SourceChannelID:   req.SourceChannelID,
		SourceChannelType: req.SourceChannelType,
		SourceName:        req.SourceName,
		SourceMsgIDs:      model.JSONStringSlice(req.derivedSourceMsgIDs()),
	}
	if req.Deadline != nil {
		t, err := service.ParseOptionalRFC3339(*req.Deadline)
		if err != nil {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "deadline must be RFC3339 or empty", nil)
			return
		}
		matter.Deadline = t
	}
	if req.RemindAt != nil {
		t, err := service.ParseOptionalRFC3339(*req.RemindAt)
		if err != nil {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "remind_at must be RFC3339 or empty", nil)
			return
		}
		matter.RemindAt = t
	}
	// Source-channel link gate: user path must be
	// IM-verified channel member; bot path is allowed (one-shot trust at
	// matter creation — bot cannot expand channel links on existing matters).
	if matter.SourceChannelID != nil && *matter.SourceChannelID != "" {
		if err := h.svc.RequireChannelMember(c.Request.Context(), callerToken(c), *matter.SourceChannelID, relatedUIDs(c)); err != nil {
			respondErr(c, err)
			return
		}
	}
	detail, err := h.svc.CreateMatterWithAssignees(c.Request.Context(), matter, req.AssigneeIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	actorName := userName(c)
	h.worker.Submit(func() {
		h.notifier.NotifyMatterCreated(matter, actorName, req.AssigneeIDs)
	})
	// Bot assignee dispatch: fire one bot_task per bot-suffixed assignee.
	// Bots get separate dispatches because they each run independently on
	// their own openclaw agent.
	h.dispatchBotAssignees(c, matter, req.AssigneeIDs)
	created(c, detail)
}

func (h *MatterHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	cursor := c.Query("cursor")
	status := c.Query("status")
	assigneeID := c.Query("assignee_id")
	creatorID := c.Query("creator_id")
	query := c.Query("q")
	sourceChannelID := c.Query("source_channel_id")
	sourceChannelTypeStr := c.Query("source_channel_type")
	channelID := c.Query("channel_id")

	filter := repository.MatterFilter{
		CallerUIDs: relatedUIDs(c),
		Limit:      limit,
	}
	if cursor != "" {
		filter.Cursor = &cursor
	}
	if status != "" {
		if !model.IsValidStatus(model.MatterStatus(status)) {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be 'open', 'done', or 'archived'", nil)
			return
		}
		filter.Status = &status
	}
	if assigneeID != "" {
		if assigneeID == "me" {
			assigneeID = uid(c)
		}
		filter.AssigneeID = &assigneeID
	}
	if creatorID != "" {
		if creatorID == "me" {
			creatorID = uid(c)
		}
		filter.CreatorID = &creatorID
	}
	if query != "" {
		filter.Query = &query
	}
	if sourceChannelID != "" {
		filter.SourceChannelID = &sourceChannelID
	}
	if channelID != "" {
		filter.ChannelID = &channelID
	}
	if sourceChannelTypeStr != "" {
		if v, err := strconv.ParseUint(sourceChannelTypeStr, 10, 8); err == nil {
			u8 := uint8(v)
			filter.SourceChannelType = &u8
		}
	}

	result, err := h.svc.ListMatters(c.Request.Context(), spaceID(c), filter, callerToken(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	paginated(c, result.Items, result.HasMore, result.NextCursor)
}

func (h *MatterHandler) Get(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	detail, err := h.svc.GetMatter(c.Request.Context(), id, spaceID(c), relatedUIDs(c), c.Query("source_channel_id"), callerToken(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, detail)
}

type updateMatterReq struct {
	Title       *string `json:"title" binding:"omitempty,max=500"`
	Description *string `json:"description" binding:"omitempty,max=10000"`
	Deadline    *string `json:"deadline"`
	RemindAt    *string `json:"remind_at"`
}

func (h *MatterHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req updateMatterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	matter, err := h.svc.UpdateMatter(c.Request.Context(), id, spaceID(c), relatedUIDs(c), req.Title, req.Description, req.Deadline, req.RemindAt)
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, matter)
}

type transitionReq struct {
	Status string `json:"status" binding:"required"`
}

func (h *MatterHandler) Transition(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req transitionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	if !model.IsValidStatus(model.MatterStatus(req.Status)) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be 'open', 'done', or 'archived'", nil)
		return
	}
	detail, err := h.svc.SetStatus(c.Request.Context(), id, spaceID(c), relatedUIDs(c), model.MatterStatus(req.Status))
	if err != nil {
		respondErr(c, err)
		return
	}
	actorUID := uid(c)
	actorName := userName(c)
	aIDs := make([]string, 0, len(detail.Assignees))
	for _, a := range detail.Assignees {
		aIDs = append(aIDs, a.UserID)
	}
	matterID := id
	h.worker.Submit(func() {
		participantIDs, _ := h.svc.ListParticipantIDs(context.Background(), matterID)
		h.notifier.NotifyStatusChanged(detail.Matter, actorUID, actorName, aIDs, participantIDs)
	})
	ok(c, detail)
}

func (h *MatterHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	if err := h.svc.SoftDelete(c.Request.Context(), id, spaceID(c), relatedUIDs(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

type addMatterAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *MatterHandler) AddAssignee(c *gin.Context) {
	var req addMatterAssigneeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	space := spaceID(c)
	actorName := userName(c)
	assigneeUID := req.UserID
	if err := h.svc.AddAssignee(c.Request.Context(), matterID, space, relatedUIDs(c), assigneeUID); err != nil {
		respondErr(c, err)
		return
	}
	h.worker.Submit(func() {
		matter, err := h.svc.GetMatterForNotification(context.Background(), matterID, space)
		if err == nil {
			h.notifier.NotifyAssigneeAdded(matter, actorName, assigneeUID)
		}
	})
	// Bot dispatch: if a bot was just added as assignee, enqueue a
	// matter_bot_task so the bot can pick it up on its next pull cycle.
	// Fire-and-forget — mirrors the same worker pattern used in Create.
	if runtime.IsBotUID(assigneeUID) && h.botTaskRepo != nil {
		h.worker.Submit(func() {
			matter, err := h.svc.GetMatterForNotification(context.Background(), matterID, space)
			if err != nil || matter == nil {
				return
			}
			dispatchOneBotAssignee(h.svc, h.botTaskRepo, matter, assigneeUID)
		})
	}
	ok(c, nil)
}

func (h *MatterHandler) RemoveAssignee(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	assigneeUID := c.Param("uid")
	if assigneeUID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "assignee uid is required", nil)
		return
	}
	if err := h.svc.RemoveAssignee(c.Request.Context(), id, spaceID(c), relatedUIDs(c), assigneeUID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

type linkChannelReq struct {
	ChannelID   string  `json:"channel_id" binding:"required"`
	ChannelType uint8   `json:"channel_type" binding:"required,oneof=1 2 5"`
	ChannelName *string `json:"channel_name"`
}

func (h *MatterHandler) LinkChannel(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req linkChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	// Manual channel-link gate:
	//   - Bot path: forbidden. Bots may not manually link new channels to
	//     existing matters; only initial source-link at matter creation.
	//   - User path: must be IM-verified member of the target channel.
	tok := callerToken(c)
	if tok == "" {
		failCode(c, http.StatusForbidden, "FORBIDDEN", "bot may not manually link channels", nil)
		return
	}
	if err := h.svc.RequireChannelMember(c.Request.Context(), tok, req.ChannelID, relatedUIDs(c)); err != nil {
		respondErr(c, err)
		return
	}
	mc, err := h.svc.LinkChannel(c.Request.Context(), id, spaceID(c), relatedUIDs(c), req.ChannelID, req.ChannelType, req.ChannelName)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, mc)
}

func (h *MatterHandler) UnlinkChannel(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	channelID := c.Param("channel_id")
	if channelID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "channel_id required", nil)
		return
	}
	if err := h.svc.UnlinkChannel(c.Request.Context(), id, spaceID(c), relatedUIDs(c), channelID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

// dispatchBotAssignees walks the assignee list and fires one bot-task per
// bot-suffixed uid. Each dispatch is fire-and-forget (worker pool) — a
// dispatch failure surfaces as a log line, never blocks the caller.
//
// Dispatch goes through the local matter_bot_task table (botTaskRepo);
// legacy HTTP path to fleet was removed (plan §1 — matter doesn't call
// fleet/server).
func (h *MatterHandler) dispatchBotAssignees(c *gin.Context, matter *model.Matter, assigneeIDs []string) {
	if h.botTaskRepo == nil {
		return
	}
	m := *matter // shallow copy: handler returns before goroutine reads
	for _, aid := range assigneeIDs {
		if !runtime.IsBotUID(aid) {
			continue
		}
		assignee := aid
		h.worker.Submit(func() {
			dispatchOneBotAssignee(h.svc, h.botTaskRepo, &m, assignee)
		})
	}
}

func dispatchOneBotAssignee(svc *service.MatterService, repo *repository.BotTaskRepo, matter *model.Matter, botUID string) {
	desc := ""
	if matter.Description != nil {
		desc = *matter.Description
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Build a minimal continuation prompt — assignee dispatch happens at
	// matter creation/assign time when there's no existing timeline to
	// reflect on, so just stitch title + description.
	prompt := buildAssigneePrompt(matter.Title, desc, botUID)
	task := &model.BotTask{
		MatterID:       matter.ID,
		SpaceID:        matter.SpaceID,
		BotUID:         botUID,
		TriggerKind:    "assignee_added",
		TriggerEntryID: nil, // assignee dispatch has no triggering timeline entry
		Prompt:         prompt,
		MatterTitle:    matter.Title,
	}
	inserted, ierr := repo.Insert(ctx, task)
	if ierr != nil {
		logBotDispatchErr(matter.ID, botUID, ierr)
		if err := svc.RecordAgentActivity(ctx, matter.ID, matter.SpaceID, botUID, service.ActionAgentTaskFailed,
			map[string]any{"bot_uid": botUID, "task_id": "", "error": "dispatch: " + ierr.Error()}); err != nil { // allowed-async-fire-and-forget: caller is worker.Submit fire-and-forget; activity loss logged but not retried (best-effort, see §B3 A4 + §9 outbox follow-up)
			log.Printf("[assignee-dispatch] activity record DB err matter=%s bot=%s: %v — fire-and-forget, dropped", matter.ID, botUID, err)
		}
		return
	}
	if !inserted {
		// Dedup (uk_trigger) — assignee can be added/removed/re-added but
		// trigger_entry_id is NULL so multiple inserts succeed. Insert
		// returning !inserted is an actual unique-key duplicate (rare here).
		log.Printf("[bot-dispatch] matter=%s bot=%s already queued (dedup)", matter.ID, botUID)
		return
	}
	logBotDispatchOK(matter.ID, botUID, task.ID)
	if err := svc.RecordAgentActivity(ctx, matter.ID, matter.SpaceID, botUID, service.ActionAgentDispatched,
		map[string]any{"bot_uid": botUID, "task_id": task.ID, "trigger": "assignee_added"}); err != nil { // allowed-async-fire-and-forget: caller is worker.Submit fire-and-forget; activity loss logged but not retried (best-effort, see §B3 A4 + §9 outbox follow-up)
		log.Printf("[assignee-dispatch] activity record DB err matter=%s bot=%s: %v — fire-and-forget, dropped", matter.ID, botUID, err)
	}
}

// buildAssigneePrompt — minimal initial prompt sent to a bot when it's
// added as an assignee (no existing thread to summarize). Mirrors the
// shape of TimelineService.BuildContinuationPrompt but stripped down.
func buildAssigneePrompt(title, description, botUID string) string {
	out := "You are agent `" + botUID + "` and have just been assigned to a new matter.\n\n"
	out += "Title: " + title + "\n"
	if description != "" {
		out += "Description:\n" + description + "\n"
	}
	out += "\nPlease respond with your initial plan or first action."
	return out
}

func logBotDispatchErr(matterID, botUID string, err error) {
	log.Printf("[bot-dispatch] matter=%s bot=%s ERROR: %v", matterID, botUID, err)
}

func logBotDispatchOK(matterID, botUID, taskID string) {
	log.Printf("[bot-dispatch] matter=%s bot=%s queued task_id=%s", matterID, botUID, taskID)
}
