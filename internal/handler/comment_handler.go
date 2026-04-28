package handler

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type CommentHandler struct {
	svc      *service.CommentService
	todoSvc  *service.TodoService
	notifier notification.Notifier
}

func NewCommentHandler(svc *service.CommentService, todoSvc *service.TodoService, notifier notification.Notifier) *CommentHandler {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &CommentHandler{svc: svc, todoSvc: todoSvc, notifier: notifier}
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
	space := spaceID(c)
	actorUID := uid(c)
	actorName := userName(c)
	comment, err := h.svc.CreateComment(todoID, space, actorUID, req.Content)
	if err != nil {
		respondErr(c, err)
		return
	}
	notification.SafeGo(func() {
		todo, err := h.todoSvc.GetTodoRaw(todoID, space)
		if err != nil {
			return
		}
		assignees, _ := h.todoSvc.ListAssigneeIDs(todoID)
		h.notifier.NotifyCommentAdded(todo, actorUID, actorName, assignees)
	})
	created(c, comment)
}

func (h *CommentHandler) List(c *gin.Context) {
	comments, err := h.svc.ListComments(c.Param("id"), spaceID(c), uid(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, comments)
}

func (h *CommentHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteComment(c.Param("comment_id"), spaceID(c), uid(c)); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
