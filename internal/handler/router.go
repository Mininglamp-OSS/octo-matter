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
	commentH *CommentHandler,
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
	api.Use(RequestTimeout(30*time.Second), MaxBodySize(maxBodySize), authMW, spaceMW)

	// Matters
	matters := api.Group("/matters")
	{
		matters.POST("", matterH.Create)
		matters.GET("", matterH.List)
		matters.GET("/:id", matterH.Get)
		matters.PUT("/:id", matterH.Update)
		matters.PUT("/:id/status", matterH.Transition)
		matters.DELETE("/:id", matterH.Delete)
		matters.POST("/:id/assignees", matterH.AddAssignee)
		matters.DELETE("/:id/assignees/:uid", matterH.RemoveAssignee)

		matters.POST("/:id/channels", matterH.LinkChannel)
		matters.DELETE("/:id/channels/:channel_id", matterH.UnlinkChannel)

		matters.POST("/:id/comments", commentH.Create)
		matters.GET("/:id/comments", commentH.List)
		matters.DELETE("/:id/comments/:comment_id", commentH.Delete)
	}

	return r
}
