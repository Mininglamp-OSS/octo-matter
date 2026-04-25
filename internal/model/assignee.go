package model

import "time"

// AssigneeStatus represents the completion status of an assignee.
type AssigneeStatus string

const (
	AssigneeStatusPending AssigneeStatus = "pending"
	AssigneeStatusDone    AssigneeStatus = "done"
)

// TodoAssignee represents a user assigned to a todo.
type TodoAssignee struct {
	ID          string         `db:"id" json:"id"`
	TodoID      string         `db:"todo_id" json:"todo_id"`
	UserID      string         `db:"user_id" json:"user_id"`
	Status      AssigneeStatus `db:"status" json:"status"`
	CompletedAt *time.Time     `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt   time.Time      `db:"created_at" json:"created_at"`
}
