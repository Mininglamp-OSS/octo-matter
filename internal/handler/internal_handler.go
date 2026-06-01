package handler

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
)

// InternalHandler exposes endpoints called by trusted backend services
// (currently only octo-server's bot_task ack writeback). Auth is a single
// shared X-Internal-Token; the same secret octo-server uses for /v1/internal/
// notify. Unauthenticated callers get 401.
type InternalHandler struct {
	timelineSvc *service.TimelineService
	matterSvc   *service.MatterService
	token       string
}

// NewInternalHandler wires the internal handler. token is read from env at
// construction time so the middleware closure can capture it. Empty token =
// reject all requests (fail closed; matches modules/notify on the server side).
func NewInternalHandler(timelineSvc *service.TimelineService, matterSvc *service.MatterService) *InternalHandler {
	return &InternalHandler{
		timelineSvc: timelineSvc,
		matterSvc:   matterSvc,
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

// internalActivityReq is the POST body for /v1/internal/matters/:id/activities.
// detail is opaque JSON the caller controls; we passthrough to recordActivity
// which marshals it. Trusted caller is expected to follow the documented
// shape per action kind (see service/matter_svc.go ActionAgent* constants).
type internalActivityReq struct {
	ActorUID string         `json:"actor_uid" binding:"required,max=64"`
	Action   string         `json:"action" binding:"required,max=64"`
	Detail   map[string]any `json:"detail"`
}

// WriteActivity handles POST /api/v1/internal/matters/:id/activities. Used
// by octo-server's ackBotTask handler to record agent_task_completed /
// agent_task_failed against the matter, so the activity feed reflects what
// the runtime did. Returns 204 on success — caller doesn't need the row back.
func (h *InternalHandler) WriteActivity(c *gin.Context) {
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	var req internalActivityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	h.matterSvc.RecordAgentActivity(c.Request.Context(), matterID, req.ActorUID, req.Action, req.Detail)
	c.Status(http.StatusNoContent)
}

// BotFeed handles GET /api/v1/internal/bots/:bot_uid/feed?limit=50.
// Returns merged timeline+activity rows where the bot is author/actor.
// PoC4: trusted internal-only endpoint (X-Internal-Token); no per-space
// gating because the caller (octo-server bot detail handler) has already
// verified the bot's owner.
func (h *InternalHandler) BotFeed(c *gin.Context) {
	botUID := c.Param("bot_uid")
	if strings.TrimSpace(botUID) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "bot_uid required", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.timelineSvc.ListBotFeed(c.Request.Context(), botUID, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	ok(c, items)
}
