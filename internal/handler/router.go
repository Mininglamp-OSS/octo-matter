package handler

import (
	"context"
	"net/http"
	"time"

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
	daemonJWTMW gin.HandlerFunc,
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
	api.Use(RequestTimeout(30*time.Second), MaxBodySize(maxBodySize), authMW, spaceMW)

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

	// Internal API (X-Internal-Token auth, NO session auth, NO space middleware).
	// Used by octo-server's bot_task ack handler to write bot replies back to
	// matter timelines. Kept outside /api/v1 group so the session middleware
	// chain doesn't apply.
	if internalH != nil {
		internal := r.Group("/api/v1/internal", RequestTimeout(15*time.Second), MaxBodySize(maxBodySize), internalH.AuthMiddleware())
		internal.POST("/matters/:id/timeline", internalH.WriteTimeline)
		internal.POST("/matters/:id/activities", internalH.WriteActivity)
		internal.GET("/bots/:bot_uid/feed", internalH.BotFeed)
	}

	// PR-B: daemon-facing bot_task endpoints — JWT auth instead of
	// X-Internal-Token so the daemon's existing JWT chain (server JWKS)
	// works without a shared secret.
	if internalH != nil && daemonJWTMW != nil {
		dgrp := r.Group("/api/v1/internal", RequestTimeout(15*time.Second), MaxBodySize(maxBodySize), daemonJWTMW)
		dgrp.GET("/bot-tasks", internalH.ListBotTasksForDaemon)
		dgrp.POST("/bot-tasks/:id/ack", internalH.AckBotTaskFromDaemon)
	}

	return r
}
