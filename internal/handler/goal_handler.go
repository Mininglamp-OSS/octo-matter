package handler

import (
	"net/http"

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
	Title       string   `json:"title" binding:"required"`
	Description *string  `json:"description"`
	AssigneeIDs []string `json:"assignee_ids"`
}

func (h *GoalHandler) Create(c *gin.Context) {
	var req createGoalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	goal, err := h.svc.CreateGoal(spaceID(c), uid(c), req.Title, req.Description, req.AssigneeIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, goal)
}

func (h *GoalHandler) List(c *gin.Context) {
	goals, err := h.svc.ListGoals(spaceID(c), uid(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, goals)
}

func (h *GoalHandler) Get(c *gin.Context) {
	detail, err := h.svc.GetGoal(c.Param("id"), spaceID(c), uid(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, detail)
}

type updateGoalReq struct {
	Title       string  `json:"title" binding:"required"`
	Description *string `json:"description"`
}

func (h *GoalHandler) Update(c *gin.Context) {
	var req updateGoalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	goal, err := h.svc.UpdateGoal(c.Param("id"), spaceID(c), uid(c), req.Title, req.Description)
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, goal)
}

func (h *GoalHandler) Archive(c *gin.Context) {
	if err := h.svc.ArchiveGoal(c.Param("id"), spaceID(c), uid(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

type addAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *GoalHandler) AddAssignee(c *gin.Context) {
	var req addAssigneeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	if err := h.svc.AddAssignee(c.Param("id"), spaceID(c), uid(c), req.UserID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

func (h *GoalHandler) RemoveAssignee(c *gin.Context) {
	assigneeUID := c.Param("uid")
	if assigneeUID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "assignee uid is required", nil)
		return
	}
	if err := h.svc.RemoveAssignee(c.Param("id"), spaceID(c), uid(c), assigneeUID); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
