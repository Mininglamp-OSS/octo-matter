package service

import "github.com/Mininglamp-OSS/octo-matter/internal/model"

// TodoAccessChecker verifies if a user can view a todo.
type TodoAccessChecker interface {
	CanAccessTodo(todo *model.Todo, userID string) bool
}
