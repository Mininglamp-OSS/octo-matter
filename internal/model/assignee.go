package model

import "time"

// AssigneeStatus is removed — assignee completion tracking is unnecessary.
// A todo assignee simply records "who is responsible". The todo's own
// open/closed status is the single source of truth for completion.

// TodoAssignee represents a user assigned to a todo.
type TodoAssignee struct {
	ID        string    `db:"id" json:"id"`
	TodoID    string    `db:"todo_id" json:"todo_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
