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

// InternalHandler exposes endpoints called by trusted backend services
// and daemons via DualAuth (see internal/auth/dual_auth.go).
//
//   - X-Internal-Token routes (kept indefinitely for legacy infra peers
//     like fleet's bot-feed proxy and any server→matter notify path)
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
// construction time so the middleware closure can capture it.
//
// Deployment note: matter MUST keep NOTIFY_INTERNAL_TOKEN set even after
// daemons stop using it — fleet's bot-feed proxy still calls matter with
// X-Internal-Token, and an empty token fails closed (every X-Internal-Token
// request returns 401). The daemon side, by contrast, no longer reads this
// env at all — it uses its JWT for writeback now.
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
	// TaskID + ClaimToken bind this writeback to the in-flight bot_task
	// the daemon just processed. Required on the daemon JWT path (DualAuth
	// JWT branch); ignored on the legacy X-Internal-Token path where the
	// caller (octo-server/fleet) is already a trusted infra peer.
	TaskID     string `json:"task_id"`
	ClaimToken string `json:"claim_token"`
}

// WriteTimeline handles POST /api/v1/internal/matters/:id/timeline.
//
// Auth model (DualAuth):
//   - X-Internal-Token path: trusted infra caller (e.g. fleet bot-feed
//     proxy in future, server notify). Caller-supplied actor_uid is
//     trusted as before.
//   - daemon JWT path: untrusted-by-default. The handler resolves
//     (task_id, claim_token) → matter_bot_task row and asserts:
//        body.actor_uid == row.bot_uid       (no impersonation)
//        body.space_id  == row.space_id      (no cross-space)
//        jwt.daemon_id  == row.claimed_by    (no daemon hijack)
//     A daemon JWT without a matching in-flight task gets 403 — closing
//     the actor_uid spoofing gap reviewer flagged on the DualAuth landing.
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
	if err := h.assertDaemonWritebackContext(c, req.TaskID, req.ClaimToken, req.ActorUID, req.SpaceID); err != nil {
		failCode(c, http.StatusForbidden, "WRITEBACK_FORBIDDEN", err.Error(), nil)
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
	// Same DualAuth-daemon-JWT binding fields as internalTimelineReq.
	// Required on JWT path; ignored on X-Internal-Token path.
	TaskID     string `json:"task_id"`
	ClaimToken string `json:"claim_token"`
	// SpaceID optional in body — for the JWT path we cross-check against the
	// resolved task's space_id; for the X-Internal-Token path it's not used.
	SpaceID string `json:"space_id"`
}

// WriteActivity handles POST /api/v1/internal/matters/:id/activities. Used
// by daemon's agent_task_completed / agent_task_failed writeback so the
// matter activity feed reflects what the runtime did. Returns 204 on
// success. See WriteTimeline for the daemon-JWT actor binding contract.
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
	if err := h.assertDaemonWritebackContext(c, req.TaskID, req.ClaimToken, req.ActorUID, req.SpaceID); err != nil {
		failCode(c, http.StatusForbidden, "WRITEBACK_FORBIDDEN", err.Error(), nil)
		return
	}
	h.matterSvc.RecordAgentActivity(c.Request.Context(), matterID, req.ActorUID, req.Action, req.Detail)
	c.Status(http.StatusNoContent)
}

// assertDaemonWritebackContext closes the actor_uid spoofing gap that
// DualAuth introduced. Returns nil on:
//   - X-Internal-Token path (no daemon_id in ctx — trusted infra caller)
//   - daemon JWT path with (task_id, claim_token) matching an in-flight
//     matter_bot_task whose bot_uid + space_id + claimed_by line up with
//     the asserted actor_uid + space_id + JWT daemon_id
//
// Detection of "daemon JWT path": presence of "daemon_id" gin key, set by
// auth.DaemonJWTMiddleware. X-Internal-Token middleware doesn't set it.
func (h *InternalHandler) assertDaemonWritebackContext(c *gin.Context, taskID, claimToken, actorUID, spaceID string) error {
	daemonIDVal, ok := c.Get("daemon_id")
	if !ok {
		return nil // X-Internal-Token path — legacy trusted contract
	}
	daemonID, _ := daemonIDVal.(string)
	if daemonID == "" {
		// JWT minted without daemon_id claim — refuse, this should never
		// happen for a daemon-scope JWT but better to fail closed than
		// degrade silently to "any valid JWT can write anywhere".
		return errors.New("daemon JWT missing daemon_id claim")
	}
	if h.botTaskRepo == nil {
		return errors.New("matter bot_task repo not wired")
	}
	if taskID == "" || claimToken == "" {
		return errors.New("daemon JWT writeback requires task_id + claim_token in body")
	}
	t, err := h.botTaskRepo.LoadDispatchedForWriteback(c.Request.Context(), taskID, claimToken)
	if err != nil {
		// DB error — fail closed; daemon will retry.
		return fmt.Errorf("writeback context lookup failed: %w", err)
	}
	if t == nil {
		// Task gone / claim_token mismatch / lease expired and reclaimed —
		// daemon's writeback is no longer authoritative.
		return errors.New("task not found or no longer dispatched (claim_token stale?)")
	}
	if t.BotUID != actorUID {
		return errors.New("actor_uid does not match task's bot_uid")
	}
	// space_id is mandatory on JWT path to avoid fail-open if a future
	// caller (or a buggy daemon build) forgets to send it. internalTimelineReq
	// already has binding:"required" on SpaceID; internalActivityReq doesn't,
	// so we enforce the check here uniformly.
	if spaceID == "" || t.SpaceID != spaceID {
		return errors.New("space_id missing or does not match task's space_id")
	}
	if t.ClaimedBy == nil || *t.ClaimedBy != daemonID {
		return errors.New("this task is not claimed by your daemon")
	}
	return nil
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
