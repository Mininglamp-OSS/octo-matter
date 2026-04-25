package model

import "time"

// TodoAttachment represents a file attached to a todo.
type TodoAttachment struct {
	ID        string    `db:"id" json:"id"`
	TodoID    string    `db:"todo_id" json:"todo_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	FileURL   string    `db:"file_url" json:"file_url"`
	FileName  *string   `db:"file_name" json:"file_name,omitempty"`
	FileSize  *int64    `db:"file_size" json:"file_size,omitempty"`
	MimeType  *string   `db:"mime_type" json:"mime_type,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
