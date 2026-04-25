package handler

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/auth"
	"github.com/gin-gonic/gin"
)

// SetupRouter wires handlers to routes. The userAuth middleware is injected so
// main.go can construct the right variant for the configured AuthMode.
func SetupRouter(
	goalH *GoalHandler,
	todoH *TodoHandler,
	commentH *CommentHandler,
	attachmentH *AttachmentHandler,
	userAuth gin.HandlerFunc,
) *gin.Engine {
	r := gin.Default()

	// Health
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/health/ready", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ready"}) })

	api := r.Group("/api/v1")
	api.Use(userAuth, auth.SpaceMiddleware())

	// Goals
	goals := api.Group("/goals")
	{
		goals.POST("", goalH.Create)
		goals.GET("", goalH.List)
		goals.GET("/:id", goalH.Get)
		goals.PUT("/:id", goalH.Update)
		goals.DELETE("/:id", goalH.Archive)
		goals.POST("/:id/members", goalH.AddMember)
		goals.DELETE("/:id/members/:uid", goalH.RemoveMember)
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
		todos.PUT("/:id/assignee-status", todoH.UpdateAssigneeStatus)

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
