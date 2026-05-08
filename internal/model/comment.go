package model

import "time"

// MatterComment represents a comment on a matter. A comment may carry zero or
// more attachments; Attachments is populated by the service layer and is not a
// column. Content is nullable — an attachment-only comment stores NULL there.
type MatterComment struct {
	ID          string              `db:"id" json:"id"`
	MatterID    string              `db:"matter_id" json:"matter_id"`
	UserID      string              `db:"user_id" json:"user_id"`
	Content     *string             `db:"content" json:"content"`
	CreatedAt   time.Time           `db:"created_at" json:"created_at"`
	Attachments []CommentAttachment `db:"-" json:"attachments"`
}
