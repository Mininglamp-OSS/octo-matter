package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gocraft/dbr/v2"
)

// InternalHandler exposes endpoints called by daemons via AuthMiddleware
// + RequireKind(apikey) (合并 plan 决策一+二 Phase 4).
//
// 历史: 之前用 DualAuth (daemon JWT + X-Internal-Token fallback), Phase 4
// 全部切到 api_key 直连. X-Internal-Token AuthMiddleware 函数 + token field
// 一起删 (NOTIFY_INTERNAL_TOKEN env 仅 matter notification client → server
// 的反向调用还在用, 跟此 handler 无关).
type InternalHandler struct {
	timelineSvc *service.TimelineService
	matterSvc   *service.MatterService
	botTaskRepo *repository.BotTaskRepo
}

// NewInternalHandler wires the internal handler.
func NewInternalHandler(timelineSvc *service.TimelineService, matterSvc *service.MatterService, botTaskRepo *repository.BotTaskRepo) *InternalHandler {
	return &InternalHandler{
		timelineSvc: timelineSvc,
		matterSvc:   matterSvc,
		botTaskRepo: botTaskRepo,
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
	// 合并 plan 决策一+二 Phase 4: AU5 assertDaemonWritebackContext 已删.
	// 信任模型转移到 caller 持 api_key, RequireKind(apikey) 已拦在路由层.
	// 跨 user 隔离: 同 user 多 daemon 互信 (会议决策接受), 跨 user 写回
	// 拦在 fleet managed_bots 那一头 (daemon 拿不到别 user 的 bot_uid).
	// issue #75 完整修复推迟决策四 (bot 子进程持 bot_token 直接调 matter).
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
	// 合并 plan 决策一+二 Phase 4: AU5 assertDaemonWritebackContext 已删 (同 WriteTimeline).
	h.matterSvc.RecordAgentActivity(c.Request.Context(), matterID, req.ActorUID, req.Action, req.Detail)
	c.Status(http.StatusNoContent)
}

// assertDaemonWritebackContext 已删 (合并 plan 决策一+二 Phase 4).
// 旧的 AU5 4-invariant (bot_uid / space_id / claimed_by / claim_token+status)
// 在 daemon JWT 路径下挡 actor_uid 伪造. Phase 4 daemon 切 api_key 后:
// - 同 user 多 daemon 互信 (决策二信任模型 by design 接受)
// - 跨 user 隔离靠 fleet managed_bots filter (daemon 拿不到别 user 的 bot_uid)
// - 同 space 跨 owner bot 越权 (issue #75) + actor_uid 伪造 (剩余攻击面)
//   等决策四 (bot 子进程持 bot_token 直接调 matter) 自然解.

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

// ListBotTasksForDaemon — GET /api/v1/internal/bot-tasks.
// Two query shapes:
//
//	?bot_uid=X[&limit=N]           single-bot (legacy)
//	?bot_uids=X,Y,Z[&limit=N]      batched per-bot, limit = per-bot
//
// Atomically claims up to N queued tasks per bot. When both params are
// present, bot_uids wins. Daemon must ack each returned task with its
// matching claim_token before lease_until or matter's sweeper will reset
// it to queued (attempt++). Response shape is identical for both forms
// ({"tasks": [...]}) — each task carries its own bot_uid for client-side
// grouping.
func (h *InternalHandler) ListBotTasksForDaemon(c *gin.Context) {
	if h.botTaskRepo == nil {
		failCode(c, http.StatusServiceUnavailable, "NOT_AVAILABLE", "bot_task repo not wired", nil)
		return
	}
	botUIDsParam := strings.TrimSpace(c.Query("bot_uids"))
	botUID := strings.TrimSpace(c.Query("bot_uid"))
	if botUIDsParam == "" && botUID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "bot_uid or bot_uids required", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	// PR-D.1 #3: daemon JWT path carries an authoritative space_id
	// claim (server #1 fix ensures it's not forgeable). Restrict task
	// claims to bots in that space — even if a daemon knows another
	// space's bot_uid it cannot pull/DOS its tasks.
	spaceIDVal, _ := c.Get("space_id")
	spaceID, _ := spaceIDVal.(string)
	if spaceID == "" {
		failCode(c, http.StatusForbidden, "MISSING_SPACE", "JWT missing space_id claim", nil)
		return
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

	var rows []*model.BotTask
	var err error
	if botUIDsParam != "" {
		uids := dedupNonEmpty(splitCSV(botUIDsParam))
		// Cap unique bots per call so a single daemon can't claim against
		// arbitrarily many bots in one tx; per-bot limit further caps each
		// bot's share so a noisy bot can't drown out quiet ones.
		const maxBots = 50
		if len(uids) > maxBots {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR",
				fmt.Sprintf("bot_uids exceeds max=%d (got %d unique); split into multiple calls", maxBots, len(uids)), nil)
			return
		}
		if len(uids) == 0 {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "bot_uids empty after dedup", nil)
			return
		}
		perBot := limit
		if perBot > 50 {
			perBot = 50
		}
		rows, err = h.botTaskRepo.ClaimNextForBots(c.Request.Context(), uids, spaceID, claimedByStr, perBot, 10*time.Minute)
	} else {
		rows, err = h.botTaskRepo.ClaimNextForBot(c.Request.Context(), botUID, spaceID, claimedByStr, limit, 10*time.Minute)
	}
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

// splitCSV splits a comma-separated value, trimming whitespace and
// dropping empty entries.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dedupNonEmpty preserves first-seen order, drops duplicates and empties.
// Without this, ?bot_uids=A,A,A would run claimNextForBotInner three times
// for the same bot, each claim claiming up to perBotLimit more rows.
func dedupNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
