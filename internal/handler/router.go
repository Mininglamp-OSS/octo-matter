package handler

import (
	"net/http"

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

type ReadinessCheck func() error

func SetupRouter(
	goalH *GoalHandler,
	todoH *TodoHandler,
	commentH *CommentHandler,
	attachmentH *AttachmentHandler,
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
	api.Use(MaxBodySize(maxBodySize), authMW, spaceMW)

	// Goals
	goals := api.Group("/goals")
	{
		goals.POST("", goalH.Create)
		goals.GET("", goalH.List)
		goals.GET("/:id", goalH.Get)
		goals.PUT("/:id", goalH.Update)
		goals.DELETE("/:id", goalH.Archive)
		goals.POST("/:id/assignees", goalH.AddAssignee)
		goals.DELETE("/:id/assignees/:uid", goalH.RemoveAssignee)
	}

	// Todos
	todos := api.Group("/todos")
	{
		todos.POST("", todoH.Create)
		todos.GET("", todoH.List)
		todos.GET("/:id", todoH.Get)
		todos.PUT("/:id", todoH.Update)
		todos.PUT("/:id/status", todoH.Transition)
		todos.DELETE("/:id", todoH.Delete)
		todos.POST("/:id/assignees", todoH.AddAssignee)
		todos.DELETE("/:id/assignees/:uid", todoH.RemoveAssignee)

		todos.POST("/:id/comments", commentH.Create)
		todos.GET("/:id/comments", commentH.List)
		todos.DELETE("/:id/comments/:comment_id", commentH.Delete)

		todos.POST("/:id/attachments", attachmentH.Create)
		todos.GET("/:id/attachments", attachmentH.List)
		todos.DELETE("/:id/attachments/:attachment_id", attachmentH.Delete)
	}

	return r
}
