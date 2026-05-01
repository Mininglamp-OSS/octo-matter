package service

import "github.com/Mininglamp-OSS/octo-matter/internal/model"

// TodoAccessChecker verifies if a user can view a todo. sourceChannelID, when
// non-empty, grants read access if it matches the todo's source channel —
// chat-originated todos are visible to anyone in the originating channel.
// Writes should pass an empty string to keep creator/assignee-only semantics.
type TodoAccessChecker interface {
	CanAccessTodo(todo *model.Todo, userID string, sourceChannelID string) bool
}
