// Package notification defines the Notifier interface for system notifications.
// Implementations send messages via the "通知助手" system bot so users see
// todo-related events in a dedicated notification conversation.
package notification

import "github.com/Mininglamp-OSS/octo-matter/internal/model"

// Notifier sends system notifications for todo events. Implementations must
// be safe for concurrent use. Delivery failures are logged, never returned —
// callers fire-and-forget with a goroutine.
type Notifier interface {
	// NotifyTodoCreated tells each assignee that a new todo was created.
	NotifyTodoCreated(todo *model.Todo, actorName string, assigneeIDs []string)

	// NotifyStatusChanged tells assignees + creator that status changed.
	NotifyStatusChanged(todo *model.Todo, actorID, actorName string)

	// NotifyAssigneeAdded tells the new assignee they were added.
	NotifyAssigneeAdded(todo *model.Todo, actorName, newAssigneeID string)

	// NotifyCommentAdded tells assignees + creator about a new comment.
	NotifyCommentAdded(todo *model.Todo, actorID, actorName string)
}

// Noop is a no-op Notifier used in tests or when notifications are disabled.
type Noop struct{}

func (Noop) NotifyTodoCreated(_ *model.Todo, _ string, _ []string)   {}
func (Noop) NotifyStatusChanged(_ *model.Todo, _, _ string)          {}
func (Noop) NotifyAssigneeAdded(_ *model.Todo, _, _ string)          {}
func (Noop) NotifyCommentAdded(_ *model.Todo, _, _ string)           {}
