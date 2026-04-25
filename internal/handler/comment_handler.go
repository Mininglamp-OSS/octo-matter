package handler

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type CommentHandler struct {
	svc *service.CommentService
}

func NewCommentHandler(svc *service.CommentService) *CommentHandler {
	return &CommentHandler{svc: svc}
}

type createCommentReq struct {
	Content string `json:"content" binding:"required"`
}

func (h *CommentHandler) Create(c *gin.Context) {
	var req createCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	comment, err := h.svc.CreateComment(c.Param("id"), spaceID(c), uid(c), req.Content)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, comment)
}

func (h *CommentHandler) List(c *gin.Context) {
	comments, err := h.svc.ListComments(c.Param("id"), spaceID(c))
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
