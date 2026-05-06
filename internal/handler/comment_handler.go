package handler

import (
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type CommentHandler struct {
	svc      *service.CommentService
	todoSvc  *service.TodoService
	notifier notification.Notifier
	worker   *notification.Worker
}

func NewCommentHandler(svc *service.CommentService, todoSvc *service.TodoService, notifier notification.Notifier, worker *notification.Worker) *CommentHandler {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &CommentHandler{svc: svc, todoSvc: todoSvc, notifier: notifier, worker: worker}
}

type createCommentReq struct {
	Content string `json:"content" binding:"required,max=10000"`
}

func (h *CommentHandler) Create(c *gin.Context) {
	var req createCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	todoID := c.Param("id")
	if !validUUID(todoID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	space := spaceID(c)
	actorUID := uid(c)
	actorName := userName(c)
	comment, err := h.svc.CreateComment(todoID, space, actorUID, req.Content, "")
	if err != nil {
		respondErr(c, err)
		return
	}
	h.worker.Submit(func() {
		todo, err := h.todoSvc.GetTodoForNotification(todoID, space)
		if err != nil {
			return
		}
		assignees, _ := h.todoSvc.ListAssigneeIDs(todoID)
		h.notifier.NotifyCommentAdded(todo, actorUID, actorName, assignees)
	})
	created(c, comment)
}

func (h *CommentHandler) List(c *gin.Context) {
	todoID := c.Param("id")
	if !validUUID(todoID) {
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
	comments, hasMore, err := h.svc.ListComments(todoID, spaceID(c), uid(c), c.Query("source_channel_id"), cursor, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	var nextCursor string
	if hasMore && len(comments) > 0 {
		last := comments[len(comments)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	paginated(c, comments, hasMore, nextCursor)
}

func (h *CommentHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteComment(c.Param("comment_id"), spaceID(c), uid(c), ""); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
