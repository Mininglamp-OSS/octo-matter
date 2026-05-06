package handler

import (
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type GoalHandler struct {
	svc *service.GoalService
}

func NewGoalHandler(svc *service.GoalService) *GoalHandler {
	return &GoalHandler{svc: svc}
}

type createGoalReq struct {
	Title       string   `json:"title" binding:"required,max=200"`
	Description *string  `json:"description" binding:"omitempty,max=10000"`
	Deadline    *string  `json:"deadline"`
	AssigneeIDs []string `json:"assignee_ids"`
}

func (h *GoalHandler) Create(c *gin.Context) {
	var req createGoalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	goal, err := h.svc.CreateGoal(spaceID(c), uid(c), req.Title, req.Description, req.Deadline, req.AssigneeIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, goal)
}

func (h *GoalHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	cursor := c.Query("cursor")
	status := c.Query("status")
	filter := repository.GoalFilter{
		CallerUIDs: relatedUIDs(c),
		Limit:      limit,
	}
	if cursor != "" {
		filter.Cursor = &cursor
	}
	if status != "" {
		if !model.IsValidGoalStatus(model.GoalStatus(status)) {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be 'active', 'completed', or 'archived'", nil)
			return
		}
		filter.Status = &status
	}
	result, err := h.svc.ListGoals(spaceID(c), filter)
	if err != nil {
		respondErr(c, err)
		return
	}
	paginated(c, result.Items, result.HasMore, result.NextCursor)
}

func (h *GoalHandler) Get(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	detail, err := h.svc.GetGoal(id, spaceID(c), uid(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, detail)
}

type updateGoalReq struct {
	Title       string  `json:"title" binding:"required,max=200"`
	Description *string `json:"description" binding:"omitempty,max=10000"`
	Deadline    *string `json:"deadline"`
}

func (h *GoalHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req updateGoalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	goal, err := h.svc.UpdateGoal(id, spaceID(c), uid(c), req.Title, req.Description, req.Deadline)
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, goal)
}

func (h *GoalHandler) Archive(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	if err := h.svc.ArchiveGoal(id, spaceID(c), uid(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

type addAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *GoalHandler) AddAssignee(c *gin.Context) {
	id := c.Param("id")
	if !validUUID(id) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req addAssigneeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	if err := h.svc.AddAssignee(id, spaceID(c), uid(c), req.UserID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

func (h *GoalHandler) RemoveAssignee(c *gin.Context) {
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
