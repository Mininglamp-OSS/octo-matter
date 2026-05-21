package model

import "time"

// TimelineAttachment is a file reference owned by a single timeline entry. Its
// lifecycle is bound to that entry through ON DELETE CASCADE.
//
// MatterID is denormalized from the owning entry so the matter outputs view
// can scan a single index without joining matter_timelines. SenderUID /
// SenderUname / SentAt come from the source IM message (LLM path); for the
// legacy direct path SenderUID falls back to the entry's user_id and SentAt
// to created_at via the 007 backfill.
type TimelineAttachment struct {
	ID          string    `db:"id" json:"id"`
	EntryID     string    `db:"entry_id" json:"entry_id"`
	MatterID    string    `db:"matter_id" json:"matter_id"`
	FileURL     string    `db:"file_url" json:"file_url"`
	FileName    *string   `db:"file_name" json:"file_name,omitempty"`
	FileSize    *int64    `db:"file_size" json:"file_size,omitempty"`
	MimeType    *string   `db:"mime_type" json:"mime_type,omitempty"`
	Description *string   `db:"description" json:"description,omitempty"`
	SenderUID   string    `db:"sender_uid" json:"sender_uid"`
	SenderUname string    `db:"sender_uname" json:"sender_uname"`
	SentAt      time.Time `db:"sent_at" json:"sent_at"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
}
