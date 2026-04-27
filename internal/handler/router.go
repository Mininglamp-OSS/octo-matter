package handler

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-matter/internal/auth"
	"github.com/Mininglamp-OSS/octo-matter/internal/middleware"
	"github.com/gin-gonic/gin"
)

// maxBodySize limits request body to prevent OOM from oversized payloads.
const maxBodySize = 1 << 20 // 1 MB

// MaxBodySize returns a Gin middleware that caps the request body at n bytes.
func MaxBodySize(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

// ReadinessCheck returns nil when the service is ready to serve traffic. Main
// wires this to a function that Pings MySQL (and later Redis / Octo IM). A
// non-nil error renders 503 so k8s pulls the Pod out of rotation until
// dependencies recover.
type ReadinessCheck func() error

// SetupRouter wires handlers to routes. The userAuth middleware is injected so
// main.go can construct the right variant for the configured AuthMode.
func SetupRouter(
	goalH *GoalHandler,
	todoH *TodoHandler,
	commentH *CommentHandler,
	attachmentH *AttachmentHandler,
	userAuth gin.HandlerFunc,
	ready ReadinessCheck,
) *gin.Engine {
	r := gin.Default()
	r.Use(middleware.RequestID())

	// Liveness: cheap, no I/O. Only signals "process is up".
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Readiness: probes dependencies. Fails to 503 when any check errors.
	r.GET("/health/ready", func(c *gin.Context) {
		if ready != nil {
			if err := ready(); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{
						"code":    "NOT_READY",
						"message": "dependencies not reachable",
						"details": gin.H{"reason": err.Error()},
					},
				})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	api := r.Group("/api/v1")
	api.Use(MaxBodySize(maxBodySize), userAuth, auth.SpaceMiddleware())

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

		// Comments
		todos.POST("/:id/comments", commentH.Create)
		todos.GET("/:id/comments", commentH.List)
		todos.DELETE("/:id/comments/:comment_id", commentH.Delete)

		// Attachments
		todos.POST("/:id/attachments", attachmentH.Create)
		todos.GET("/:id/attachments", attachmentH.List)
		todos.DELETE("/:id/attachments/:attachment_id", attachmentH.Delete)
	}

	return r
}
