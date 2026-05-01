package handler

import (
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
	var req createAttachmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	att, err := h.svc.CreateAttachment(c.Param("id"), spaceID(c), uid(c), req.FileURL, req.FileName, req.FileSize, req.MimeType, "")
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, att)
}

func (h *AttachmentHandler) List(c *gin.Context) {
	attachments, err := h.svc.ListAttachments(c.Param("id"), spaceID(c), uid(c), c.Query("source_channel_id"))
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, attachments)
}

func (h *AttachmentHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteAttachment(c.Param("attachment_id"), spaceID(c), uid(c), ""); err != nil {
		respondErr(c, err)
		return
	}
	ok(c, nil)
}
