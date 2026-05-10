package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

type ExtractHandler struct {
	svc *service.ExtractService
}

func NewExtractHandler(svc *service.ExtractService) *ExtractHandler {
	return &ExtractHandler{svc: svc}
}

type extractMessageAttachmentReq struct {
	FileName string `json:"file_name"`
	FileURL  string `json:"file_url"`
}

type extractMessageReq struct {
	MessageID   string                        `json:"message_id" binding:"required"`
	FromUID     string                        `json:"from_uid" binding:"required"`
	FromUname   string                        `json:"from_uname"`
	Timestamp   int64                         `json:"timestamp"`
	Content     string                        `json:"content"`
	Attachments []extractMessageAttachmentReq `json:"attachments"`
}

type extractReq struct {
	ChannelType uint8               `json:"channel_type" binding:"required,oneof=1 2 5"`
	ChannelID   string              `json:"channel_id" binding:"required"`
	ChannelName *string             `json:"channel_name"`
	CreatorUID  string              `json:"creator_uid" binding:"required"`
	Msgs        []extractMessageReq `json:"msgs" binding:"required,min=1,max=200,dive"`
}

// Create handles POST /matters/extract.
func (h *ExtractHandler) Create(c *gin.Context) {
	var req extractReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	// Auth: creator_uid must match one of caller's UIDs (user path) or the
	// bot's owner_uid (bot path). A bot can only create a matter on behalf of
	// its owner, never arbitrary users.
	if !authorizedActor(c, req.CreatorUID) {
		failCode(c, http.StatusForbidden, "FORBIDDEN", "creator_uid not authorized for caller", nil)
		return
	}

	msgs := make([]service.ExtractMessage, 0, len(req.Msgs))
	for _, m := range req.Msgs {
		atts := make([]service.ExtractMessageAttachment, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			atts = append(atts, service.ExtractMessageAttachment{FileName: a.FileName, FileURL: a.FileURL})
		}
		msgs = append(msgs, service.ExtractMessage{
			MessageID:   m.MessageID,
			FromUID:     m.FromUID,
			FromUname:   m.FromUname,
			Timestamp:   m.Timestamp,
			Content:     m.Content,
			Attachments: atts,
		})
	}

	in := service.ExtractInput{
		SpaceID:     spaceID(c),
		ChannelType: req.ChannelType,
		ChannelID:   req.ChannelID,
		ChannelName: req.ChannelName,
		CreatorUID:  req.CreatorUID,
		CallerUIDs:  effectiveCallerUIDs(c),
		CallerToken: callerToken(c),
		Messages:    msgs,
	}

	res, err := h.svc.CreateFromMessages(c.Request.Context(), in)
	if err != nil {
		if errors.Is(err, llm.ErrEmptyToolCall) {
			failCode(c, http.StatusUnprocessableEntity, "LLM_EMPTY_EXTRACTION", "LLM returned no extraction", nil)
			return
		}
		if isLLMUpstreamErr(err) {
			failCode(c, http.StatusBadGateway, "LLM_UPSTREAM_ERROR", "upstream LLM error", nil)
			return
		}
		if _, ok := apperr.AsAppError(err); ok {
			respondErr(c, err)
			return
		}
		respondErr(c, err)
		return
	}
	created(c, res)
}

func uidAuthorized(callerUIDs []string, target string) bool {
	if target == "" {
		return false
	}
	for _, u := range callerUIDs {
		if u == target {
			return true
		}
	}
	return false
}

// authorizedActor validates that `target` is a uid the current caller is
// allowed to act on behalf of:
//   - user token → target must be the caller uid or one of its owned bots
//   - bot token  → target must be the bot's owner_uid (bots can only act for
//     their owner, never arbitrary users)
func authorizedActor(c *gin.Context, target string) bool {
	if target == "" {
		return false
	}
	if role := c.GetString("role"); role == "bot" {
		owner := c.GetString("owner_uid")
		return owner != "" && target == owner
	}
	return uidAuthorized(relatedUIDs(c), target)
}

// isLLMUpstreamErr matches errors from the LLM service layer. Service-side
// errors are wrapped with prefixes like "llm extract_matter:" or
// "llm extract_matter_progress:" — detect by scanning for the "llm " prefix
// plus a colon later in the string rather than a fixed substring.
func isLLMUpstreamErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "llm ") || strings.HasPrefix(msg, "llm:")
}
