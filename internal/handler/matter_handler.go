package handler

import (
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type MatterHandler struct {
	svc      *service.MatterService
	notifier notification.Notifier
	worker   *notification.Worker
}

func NewMatterHandler(svc *service.MatterService, notifier notification.Notifier, worker *notification.Worker) *MatterHandler {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &MatterHandler{svc: svc, notifier: notifier, worker: worker}
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
	detail, err := h.svc.CreateMatterWithAssignees(matter, req.AssigneeIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	actorName := userName(c)
	h.worker.Submit(func() {
		h.notifier.NotifyMatterCreated(matter, actorName, req.AssigneeIDs)
	})
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
	if sourceChannelTypeStr != "" {
		if v, err := strconv.ParseUint(sourceChannelTypeStr, 10, 8); err == nil {
			u8 := uint8(v)
			filter.SourceChannelType = &u8
		}
	}

	result, err := h.svc.ListMatters(spaceID(c), filter)
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
	detail, err := h.svc.GetMatter(id, spaceID(c), uid(c), c.Query("source_channel_id"))
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
	matter, err := h.svc.UpdateMatter(id, spaceID(c), uid(c), req.Title, req.Description, req.Deadline, req.RemindAt)
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
	detail, err := h.svc.SetStatus(id, spaceID(c), uid(c), model.MatterStatus(req.Status))
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
	h.worker.Submit(func() {
		h.notifier.NotifyStatusChanged(detail.Matter, actorUID, actorName, aIDs)
	})
	ok(c, detail)
}

func (h *MatterHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	if err := h.svc.SoftDelete(id, spaceID(c), uid(c)); err != nil {
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
	if err := h.svc.AddAssignee(matterID, space, uid(c), assigneeUID); err != nil {
		respondErr(c, err)
		return
	}
	h.worker.Submit(func() {
		matter, err := h.svc.GetMatterForNotification(matterID, space)
		if err == nil {
			h.notifier.NotifyAssigneeAdded(matter, actorName, assigneeUID)
		}
	})
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
	if err := h.svc.RemoveAssignee(id, spaceID(c), uid(c), assigneeUID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
