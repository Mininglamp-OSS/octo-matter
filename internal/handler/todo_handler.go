package handler

import (
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type TodoHandler struct {
	svc *service.TodoService
}

func NewTodoHandler(svc *service.TodoService) *TodoHandler {
	return &TodoHandler{svc: svc}
}

type createTodoReq struct {
	Title             string  `json:"title" binding:"required"`
	Description       *string `json:"description"`
	GoalID            *string `json:"goal_id"`
	Deadline          *string `json:"deadline"`
	RemindAt          *string `json:"remind_at"`
	SourceChannelID   *string `json:"source_channel_id"`
	SourceChannelType *uint8  `json:"source_channel_type"`
	SourceName        *string `json:"source_name"`
}

func (h *TodoHandler) Create(c *gin.Context) {
	var req createTodoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	todo := &model.Todo{
		SpaceID:           spaceID(c),
		Title:             req.Title,
		Description:       req.Description,
		GoalID:            req.GoalID,
		CreatorID:         uid(c),
		SourceChannelID:   req.SourceChannelID,
		SourceChannelType: req.SourceChannelType,
		SourceName:        req.SourceName,
	}
	result, err := h.svc.CreateTodo(todo)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	created(c, result)
}

func (h *TodoHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	cursor := c.Query("cursor")
	goalID := c.Query("goal_id")
	status := c.Query("status")
	assigneeID := c.Query("assignee_id")
	creatorID := c.Query("creator_id")
	query := c.Query("q")

	filter := repository.TodoFilter{
		Limit: limit,
	}
	if cursor != "" {
		filter.Cursor = &cursor
	}
	if goalID != "" {
		filter.GoalID = &goalID
	}
	if status != "" {
		filter.Status = &status
	}
	if assigneeID != "" {
		filter.AssigneeID = &assigneeID
	}
	if creatorID != "" {
		filter.CreatorID = &creatorID
	}
	if query != "" {
		filter.Query = &query
	}

	result, err := h.svc.ListTodos(spaceID(c), filter)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, result)
}

func (h *TodoHandler) Get(c *gin.Context) {
	detail, err := h.svc.GetTodo(c.Param("id"), uid(c))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, detail)
}

type updateTodoReq struct {
	Title       string  `json:"title" binding:"required"`
	Description *string `json:"description"`
	GoalID      *string `json:"goal_id"`
	Deadline    *string `json:"deadline"`
	RemindAt    *string `json:"remind_at"`
}

func (h *TodoHandler) Update(c *gin.Context) {
	var req updateTodoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	todo, err := h.svc.UpdateTodo(c.Param("id"), uid(c), req.Title, req.Description, req.GoalID, req.Deadline, req.RemindAt)
	if err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, todo)
}

type transitionReq struct {
	Status string `json:"status" binding:"required"`
}

func (h *TodoHandler) Transition(c *gin.Context) {
	var req transitionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.TransitionStatus(c.Param("id"), uid(c), model.TodoStatus(req.Status)); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, nil)
}

func (h *TodoHandler) Delete(c *gin.Context) {
	if err := h.svc.SoftDelete(c.Param("id"), uid(c)); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, nil)
}

type addAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *TodoHandler) AddAssignee(c *gin.Context) {
	var req addAssigneeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.AddAssignee(c.Param("id"), uid(c), req.UserID); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, nil)
}

type removeAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *TodoHandler) RemoveAssignee(c *gin.Context) {
	var req removeAssigneeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.RemoveAssignee(c.Param("id"), uid(c), req.UserID); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, nil)
}

type updateAssigneeStatusReq struct {
	Status string `json:"status" binding:"required"`
}

func (h *TodoHandler) UpdateAssigneeStatus(c *gin.Context) {
	var req updateAssigneeStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.UpdateAssigneeStatus(c.Param("id"), uid(c), model.AssigneeStatus(req.Status)); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	ok(c, nil)
}
