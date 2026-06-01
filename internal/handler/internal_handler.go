package handler

import (
	"crypto/subtle"
	"net/http"
	"os"

	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

// InternalHandler exposes endpoints called by trusted backend services
// (currently only octo-server's bot_task ack writeback). Auth is a single
// shared X-Internal-Token; the same secret octo-server uses for /v1/internal/
// notify. Unauthenticated callers get 401.
type InternalHandler struct {
	timelineSvc *service.TimelineService
	token       string
}

// NewInternalHandler wires the internal handler. token is read from env at
// construction time so the middleware closure can capture it. Empty token =
// reject all requests (fail closed; matches modules/notify on the server side).
func NewInternalHandler(timelineSvc *service.TimelineService) *InternalHandler {
	return &InternalHandler{
		timelineSvc: timelineSvc,
		token:       os.Getenv("NOTIFY_INTERNAL_TOKEN"),
	}
}

// AuthMiddleware validates X-Internal-Token. Fails closed when token is unset.
func (h *InternalHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "internal API auth not configured"},
			})
			return
		}
		hdr := c.GetHeader("X-Internal-Token")
		if subtle.ConstantTimeCompare([]byte(hdr), []byte(h.token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "unauthorized"},
			})
			return
		}
		c.Next()
	}
}

type internalTimelineReq struct {
	ActorUID string `json:"actor_uid" binding:"required,max=64"`
	SpaceID  string `json:"space_id" binding:"required,max=64"`
	Content  string `json:"content" binding:"required,max=10000"`
}

// WriteTimeline handles POST /api/v1/internal/matters/:id/timeline. Trusted
// caller (octo-server) supplies the actor_uid + space_id; we don't need a
// session because internal-auth has already gated the request.
func (h *InternalHandler) WriteTimeline(c *gin.Context) {
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req internalTimelineReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	entry, err := h.timelineSvc.CreateInternalEntry(c.Request.Context(), matterID, req.SpaceID, req.ActorUID, req.Content)
	if err != nil {
		respondErr(c, err)
		return
	}
	created(c, entry)
}
