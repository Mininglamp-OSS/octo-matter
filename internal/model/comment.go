package model

import "time"

// TodoComment represents a comment on a todo.
type TodoComment struct {
	ID        string    `db:"id" json:"id"`
	TodoID    string    `db:"todo_id" json:"todo_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	Content   string    `db:"content" json:"content"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
