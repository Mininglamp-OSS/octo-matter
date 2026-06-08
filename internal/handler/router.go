package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/auth"
	"github.com/Mininglamp-OSS/octo-matter/internal/middleware"
	"github.com/gin-gonic/gin"
)

const maxBodySize = 1 << 20 // 1 MB

func MaxBodySize(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

// RequestTimeout sets a deadline on the request context.
func RequestTimeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

type ReadinessCheck func() error

func SetupRouter(
	matterH *MatterHandler,
	timelineH *TimelineHandler,
	activityH *ActivityHandler,
	outputsH *OutputsHandler,
	extractH *ExtractHandler,
	extractLimiter gin.HandlerFunc,
	internalH *InternalHandler,
	authMW gin.HandlerFunc,
	spaceMW gin.HandlerFunc,
	ready ReadinessCheck,
) *gin.Engine {
	r := gin.Default()
	r.Use(middleware.RequestID())

	// Health
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		if ready != nil {
			if err := ready(); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{"code": "NOT_READY", "message": "dependencies not reachable",
						"details": gin.H{"reason": err.Error()}},
				})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	api := r.Group("/api/v1")
	// v3.3.3 §G (yujiawei v3.3.2 P1): main /api/v1 routes are user-facing
	// (matters CRUD, channels, timeline, etc.). api_key has always been a
	// user-space-scoped daemon credential (BotFather/daemon issued, one
	// per user-space), and daemon-cli + fleet only call /internal/* —
	// verified by grep across both repos. The Phase-4-before routing
	// kept daemon out of user-facing routes via the JWT `scope: daemon`
	// claim (only /internal accepted daemon JWT). Phase 4 removed the
	// JWT layer without porting its scope-segregation effect to the
	// api_key path, so this group accepted api_key on all endpoints
	// by default. v3.3.3 §G restores the segregation explicitly via
	// RequireKind on the main group — matches routing accept scope
	// to daemon's actual use case rather than relying on the implicit
	// "daemon won't call user-facing endpoints" convention.
	api.Use(RequestTimeout(30*time.Second), MaxBodySize(maxBodySize), authMW,
		auth.RequireKind(auth.AuthKindSession, auth.AuthKindBot), spaceMW)

	// Matters
	matters := api.Group("/matters")
	{
		matters.POST("", matterH.Create)
		matters.GET("", matterH.List)

		// AI-powered: registered BEFORE /:id routes so "extract" does not match :id.
		if extractH != nil {
			if extractLimiter != nil {
				matters.POST("/extract", extractLimiter, extractH.Create)
			} else {
				matters.POST("/extract", extractH.Create)
			}
		}

		matters.GET("/:id", matterH.Get)
		matters.PUT("/:id", matterH.Update)
		matters.PUT("/:id/status", matterH.Transition)
		matters.DELETE("/:id", matterH.Delete)
		matters.POST("/:id/assignees", matterH.AddAssignee)
		matters.DELETE("/:id/assignees/:uid", matterH.RemoveAssignee)

		matters.POST("/:id/channels", matterH.LinkChannel)
		matters.DELETE("/:id/channels/:channel_id", matterH.UnlinkChannel)

		matters.POST("/:id/timeline", timelineH.Create)
		matters.GET("/:id/timeline", timelineH.List)
		matters.DELETE("/:id/timeline/:entry_id", timelineH.Delete)

		matters.GET("/:id/activities", activityH.List)
		matters.GET("/:id/outputs", outputsH.List)
	}

	// User-facing bot endpoints — auth-only, no per-space scoping
	// (a bot belongs to one space; ownership is checked against
	// related_uids inside the handler). Replaces the legacy
	// fleet `/v1/runtimes/bots/:id/feed` proxy.
	//
	// v3.3.3 §G: independent mount (not nested under main api group),
	// so the RequireKind on api.Use above does NOT apply here. Add
	// RequireKind(session, bot) symmetrically — same reasoning, same
	// trust boundary. daemon-cli + fleet don't call /api/v1/bots
	// (verified by grep).
	bots := r.Group("/api/v1/bots", RequestTimeout(15*time.Second), MaxBodySize(maxBodySize), authMW,
		auth.RequireKind(auth.AuthKindSession, auth.AuthKindBot))
	bots.GET("/:bot_uid/feed", timelineH.BotFeed)

	// Internal API endpoint groups (auth-decisions plan §4 Endpoint
	// authz matrix). Decisions 1+2 Phase 4 removed the daemonJWTMW arg
	// and DualAuth fallback — everything now goes through the new
	// AuthMiddleware + RequireKind pair.
	if internalH != nil {
		// Daemon writeback + task pull/ack — apikey only.
		// Decision 2: daemon connects with api_key direct,
		// fleet/matter call server verify-api-key for validation.
		// AU5 4-invariant (assertDaemonWritebackContext) was deleted in
		// Phase 4; v3 §3.2 replaced it with a service-layer enforcement
		// (RecordAgentActivity calls matterRepo.GetByID(matter,space)
		// to reject cross-space; WriteTimeline uses verified ctx.space_id
		// to gate matter↔space up-front; actor_uid is constrained by
		// owned_bots_by_space in v3 §3.4 so the actor must be either
		// the caller or one of the caller's own bots).
		daemonAPI := r.Group("/api/v1/internal",
			RequestTimeout(15*time.Second), MaxBodySize(maxBodySize),
			authMW, auth.RequireKind(auth.AuthKindAPIKey))
		daemonAPI.POST("/matters/:id/timeline", internalH.WriteTimeline)
		daemonAPI.POST("/matters/:id/activities", internalH.WriteActivity)
		daemonAPI.GET("/bot-tasks", internalH.ListBotTasksForDaemon)
		daemonAPI.POST("/bot-tasks/:id/ack", internalH.AckBotTaskFromDaemon)

		// Bot feed: browser direct call (session token) or bot token caller.
		botFeedGrp := r.Group("/api/v1/internal",
			RequestTimeout(15*time.Second), MaxBodySize(maxBodySize),
			authMW, auth.RequireKind(auth.AuthKindSession, auth.AuthKindBot))
		botFeedGrp.GET("/bots/:bot_uid/feed", internalH.BotFeed)
	}

	return r
}
