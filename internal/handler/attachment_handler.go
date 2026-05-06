package handler

import (
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type AttachmentHandler struct {
	svc *service.AttachmentService
}

func NewAttachmentHandler(svc *service.AttachmentService) *AttachmentHandler {
	return &AttachmentHandler{svc: svc}
}

type createAttachmentReq struct {
	FileURL  string  `json:"file_url" binding:"required,http_url"`
	FileName *string `json:"file_name"`
	FileSize *int64  `json:"file_size"`
	MimeType *string `json:"mime_type"`
}

func (h *AttachmentHandler) Create(c *gin.Context) {
	todoID := c.Param("id")
	if !validUUID(todoID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req createAttachmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	att, err := h.svc.CreateAttachment(todoID, spaceID(c), uid(c), req.FileURL, req.FileName, req.FileSize, req.MimeType, "")
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, att)
}

func (h *AttachmentHandler) List(c *gin.Context) {
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
	attachments, hasMore, err := h.svc.ListAttachments(todoID, spaceID(c), uid(c), c.Query("source_channel_id"), cursor, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	var nextCursor string
	if hasMore && len(attachments) > 0 {
		last := attachments[len(attachments)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	paginated(c, attachments, hasMore, nextCursor)
}

func (h *AttachmentHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteAttachment(c.Param("attachment_id"), spaceID(c), uid(c), ""); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
