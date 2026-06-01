package model

import "time"

// MatterOutput is one row of the "matter outputs" view: a deduplicated
// attachment uploaded to a matter, enriched with its source channel name.
// Built by joining matter_timeline_attachments with matter_channels; not a
// table on its own.
type MatterOutput struct {
	ID                string    `db:"id" json:"id"`
	EntryID           string    `db:"entry_id" json:"entry_id"`
	MatterID          string    `db:"matter_id" json:"matter_id"`
	FileURL           string    `db:"file_url" json:"file_url"`
	FileName          *string   `db:"file_name" json:"file_name,omitempty"`
	FileSize          *int64    `db:"file_size" json:"file_size,omitempty"`
	MimeType          *string   `db:"mime_type" json:"mime_type,omitempty"`
	Description       *string   `db:"description" json:"description,omitempty"`
	SenderUID         string    `db:"sender_uid" json:"sender_uid"`
	SenderUname       string    `db:"sender_uname" json:"sender_uname"`
	SourceChannelID   *string   `db:"source_channel_id" json:"source_channel_id,omitempty"`
	SourceChannelName *string   `db:"source_channel_name" json:"source_channel_name,omitempty"`
	SentAt            time.Time `db:"sent_at" json:"sent_at"`
}
