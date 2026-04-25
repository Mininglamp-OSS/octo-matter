package handler

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/auth"
	"github.com/gin-gonic/gin"
)

func SetupRouter(
	goalH *GoalHandler,
	todoH *TodoHandler,
	commentH *CommentHandler,
	attachmentH *AttachmentHandler,
) *gin.Engine {
	r := gin.Default()

	api := r.Group("/api/v1")
	api.Use(auth.UserAuthMiddleware(), auth.SpaceMiddleware())

	// Goals
	goals := api.Group("/goals")
	{
		goals.POST("", goalH.Create)
		goals.GET("", goalH.List)
		goals.GET("/:id", goalH.Get)
		goals.PUT("/:id", goalH.Update)
		goals.POST("/:id/archive", goalH.Archive)
		goals.POST("/:id/members", goalH.AddMember)
		goals.DELETE("/:id/members", goalH.RemoveMember)
	}

	// Todos
	todos := api.Group("/todos")
	{
		todos.POST("", todoH.Create)
		todos.GET("", todoH.List)
		todos.GET("/:id", todoH.Get)
		todos.PUT("/:id", todoH.Update)
		todos.POST("/:id/transition", todoH.Transition)
		todos.DELETE("/:id", todoH.Delete)
		todos.POST("/:id/assignees", todoH.AddAssignee)
		todos.DELETE("/:id/assignees", todoH.RemoveAssignee)
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
