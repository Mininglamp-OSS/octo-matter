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
	Title             string   `json:"title" binding:"required,max=500"`
	Description       *string  `json:"description" binding:"omitempty,max=10000"`
	GoalID            *string  `json:"goal_id"`
	AssigneeIDs       []string `json:"assignee_ids"`
	Deadline          *string  `json:"deadline"`
	RemindAt          *string  `json:"remind_at"`
	SourceChannelID   *string  `json:"source_channel_id"`
	SourceChannelType *uint8   `json:"source_channel_type" binding:"omitempty,oneof=1 2 5"`
	SourceName        *string  `json:"source_name"`
}

func (h *TodoHandler) Create(c *gin.Context) {
	var req createTodoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	sid := spaceID(c)
	userID := uid(c)
	todo := &model.Todo{
		SpaceID:           sid,
		Title:             req.Title,
		Description:       req.Description,
		GoalID:            req.GoalID,
		CreatorID:         userID,
		SourceChannelID:   req.SourceChannelID,
		SourceChannelType: req.SourceChannelType,
		SourceName:        req.SourceName,
	}
	// Parse optional deadline and remind_at (P1-7 fix).
	if req.Deadline != nil {
		t, err := service.ParseOptionalRFC3339(*req.Deadline)
		if err != nil {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "deadline must be RFC3339 or empty", nil)
			return
		}
		todo.Deadline = t
	}
	if req.RemindAt != nil {
		t, err := service.ParseOptionalRFC3339(*req.RemindAt)
		if err != nil {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "remind_at must be RFC3339 or empty", nil)
			return
		}
		todo.RemindAt = t
	}
	detail, err := h.svc.CreateTodoWithAssignees(todo, req.AssigneeIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, detail)
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
	sourceChannelID := c.Query("source_channel_id")
	sourceChannelTypeStr := c.Query("source_channel_type")

	filter := repository.TodoFilter{
		CallerID: uid(c),
		Limit:    limit,
	}
	if cursor != "" {
		filter.Cursor = &cursor
	}
	if goalID != "" {
		filter.GoalID = &goalID
	}
	if status != "" {
		if !model.IsValidStatus(model.TodoStatus(status)) {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be 'open' or 'closed'", nil)
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

	result, err := h.svc.ListTodos(spaceID(c), filter)
	if err != nil {
		respondErr(c, err)
		return
	}
	paginated(c, result.Items, result.HasMore, result.NextCursor)
}

func (h *TodoHandler) Get(c *gin.Context) {
	detail, err := h.svc.GetTodo(c.Param("id"), spaceID(c), uid(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, detail)
}

type updateTodoReq struct {
	Title       *string `json:"title" binding:"omitempty,max=500"`
	Description *string `json:"description" binding:"omitempty,max=10000"`
	GoalID      *string `json:"goal_id"`
	Deadline    *string `json:"deadline"`
	RemindAt    *string `json:"remind_at"`
}

func (h *TodoHandler) Update(c *gin.Context) {
	var req updateTodoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	todo, err := h.svc.UpdateTodo(c.Param("id"), spaceID(c), uid(c), req.Title, req.Description, req.GoalID, req.Deadline, req.RemindAt)
	if err != nil {
		respondErr(c, err)
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
		bindJSONErr(c, err)
		return
	}
	if !model.IsValidStatus(model.TodoStatus(req.Status)) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be 'open' or 'closed'", nil)
		return
	}
	detail, err := h.svc.SetStatus(c.Param("id"), spaceID(c), uid(c), model.TodoStatus(req.Status))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, detail)
}

func (h *TodoHandler) Delete(c *gin.Context) {
	if err := h.svc.SoftDelete(c.Param("id"), spaceID(c), uid(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}

type addTodoAssigneeReq struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *TodoHandler) AddAssignee(c *gin.Context) {
	var req addTodoAssigneeReq
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

func (h *TodoHandler) RemoveAssignee(c *gin.Context) {
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


