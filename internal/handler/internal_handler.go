package handler

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gocraft/dbr/v2"
)

// InternalHandler exposes endpoints called by trusted backend services.
//   - X-Internal-Token routes: octo-server's bot_task ack writeback (legacy
//     PoC4 — to be removed once PR-B cutover settles)
//   - Daemon JWT routes (PR-B): octo-daemon-cli pulls and acks bot_tasks
//     from matter directly. Auth gated by auth.DaemonJWTMiddleware in
//     SetupRouter — handlers below trust the gin context keys.
type InternalHandler struct {
	timelineSvc *service.TimelineService
	matterSvc   *service.MatterService
	botTaskRepo *repository.BotTaskRepo
	token       string
}

// NewInternalHandler wires the internal handler. token is read from env at
// construction time so the middleware closure can capture it. Empty token =
// reject all requests (fail closed; matches modules/notify on the server side).
func NewInternalHandler(timelineSvc *service.TimelineService, matterSvc *service.MatterService, botTaskRepo *repository.BotTaskRepo) *InternalHandler {
	return &InternalHandler{
		timelineSvc: timelineSvc,
		matterSvc:   matterSvc,
		botTaskRepo: botTaskRepo,
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

// --- PR-B: daemon JWT routes (mounted at /api/v1/internal/bot-tasks) ---

type botTaskOut struct {
	ID          string `json:"id"`
	MatterID    string `json:"matter_id"`
	SpaceID     string `json:"space_id"`
	BotUID      string `json:"bot_uid"`
	Prompt      string `json:"prompt"`
	MatterTitle string `json:"matter_title,omitempty"`
	ClaimToken  string `json:"claim_token"`
	LeaseUntil  string `json:"lease_until"`
}

// ListBotTasksForDaemon — GET /api/v1/internal/bot-tasks?bot_uid=X[&limit=N].
// Atomically claims up to N queued tasks for the bot. Daemon must ack each
// returned task with the matching claim_token before lease_until or matter's
// sweeper will reset to queued (attempt++).
func (h *InternalHandler) ListBotTasksForDaemon(c *gin.Context) {
	if h.botTaskRepo == nil {
		failCode(c, http.StatusServiceUnavailable, "NOT_AVAILABLE", "bot_task repo not wired", nil)
		return
	}
	botUID := strings.TrimSpace(c.Query("bot_uid"))
	if botUID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "bot_uid required", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	claimedBy, _ := c.Get("daemon_id")
	claimedByStr, _ := claimedBy.(string)
	if claimedByStr == "" {
		if uidVal, ok := c.Get("uid"); ok {
			if s, ok2 := uidVal.(string); ok2 {
				claimedByStr = s
			}
		}
	}
	if claimedByStr == "" {
		claimedByStr = "unknown"
	}

	rows, err := h.botTaskRepo.ClaimNextForBot(c.Request.Context(), botUID, claimedByStr, limit, 10*time.Minute)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]botTaskOut, 0, len(rows))
	for _, r := range rows {
		ct := ""
		if r.ClaimToken != nil {
			ct = *r.ClaimToken
		}
		lu := ""
		if r.LeaseUntil != nil {
			lu = r.LeaseUntil.Format(time.RFC3339)
		}
		out = append(out, botTaskOut{
			ID:          r.ID,
			MatterID:    r.MatterID,
			SpaceID:     r.SpaceID,
			BotUID:      r.BotUID,
			Prompt:      r.Prompt,
			MatterTitle: r.MatterTitle,
			ClaimToken:  ct,
			LeaseUntil:  lu,
		})
	}
	c.JSON(http.StatusOK, gin.H{"tasks": out})
}

type ackBotTaskReq struct {
	ClaimToken    string `json:"claim_token" binding:"required"`
	Status        string `json:"status" binding:"required"` // succeeded | failed
	ErrorMsg      string `json:"error_msg"`
	ResultSummary string `json:"result_summary"`
	ElapsedMs     int64  `json:"elapsed_ms"`
}

// AckBotTaskFromDaemon — POST /api/v1/internal/bot-tasks/:id/ack.
// claim_token mismatch returns 409 (daemon must drop result and stop
// retrying); the row may have been reclaimed by another daemon.
func (h *InternalHandler) AckBotTaskFromDaemon(c *gin.Context) {
	if h.botTaskRepo == nil {
		failCode(c, http.StatusServiceUnavailable, "NOT_AVAILABLE", "bot_task repo not wired", nil)
		return
	}
	id := c.Param("id")
	var req ackBotTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindJSONErr(c, err)
		return
	}
	err := h.botTaskRepo.Ack(c.Request.Context(), id, req.ClaimToken, req.Status, req.ErrorMsg, req.ResultSummary, req.ElapsedMs)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			failCode(c, http.StatusConflict, "CLAIM_TOKEN_MISMATCH", "task not in dispatched state with matching token", nil)
			return
		}
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Helpers — model package kept implicit through return types above.
var _ = model.BotTask{}
var _ = fmt.Sprintf
