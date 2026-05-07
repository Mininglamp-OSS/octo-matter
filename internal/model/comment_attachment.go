package model

import "time"

// CommentAttachment is a file reference owned by a single comment. Its
// lifecycle is bound to that comment — deleting the comment deletes the
// attachment via ON DELETE CASCADE.
type CommentAttachment struct {
	ID        string    `db:"id" json:"id"`
	CommentID string    `db:"comment_id" json:"comment_id"`
	FileURL   string    `db:"file_url" json:"file_url"`
	FileName  *string   `db:"file_name" json:"file_name,omitempty"`
	FileSize  *int64    `db:"file_size" json:"file_size,omitempty"`
	MimeType  *string   `db:"mime_type" json:"mime_type,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
