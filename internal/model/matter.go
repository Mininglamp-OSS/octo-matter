package model

import "time"

// MatterStatus represents the status of a matter: open (needs work),
// done (completed), or archived (no longer relevant).
type MatterStatus string

const (
	MatterStatusOpen     MatterStatus = "open"
	MatterStatusDone     MatterStatus = "done"
	MatterStatusArchived MatterStatus = "archived"
)

// IsValidStatus reports whether s is a known MatterStatus.
func IsValidStatus(s MatterStatus) bool {
	return s == MatterStatusOpen || s == MatterStatusDone || s == MatterStatusArchived
}

// Matter represents the atomic task unit.
type Matter struct {
	ID                string       `db:"id" json:"id"`
	SeqNo             int          `db:"seq_no" json:"seq_no"`
	SpaceID           string       `db:"space_id" json:"space_id"`
	Title             string       `db:"title" json:"title"`
	Description       *string      `db:"description" json:"description,omitempty"`
	CreatorID         string       `db:"creator_id" json:"creator_id"`
	Status            MatterStatus `db:"status" json:"status"`
	Deadline          *time.Time   `db:"deadline" json:"deadline,omitempty"`
	RemindAt          *time.Time   `db:"remind_at" json:"remind_at,omitempty"`
	SourceChannelID   *string      `db:"source_channel_id" json:"source_channel_id,omitempty"`
	SourceChannelType *uint8       `db:"source_channel_type" json:"source_channel_type,omitempty"`
	SourceName        *string      `db:"source_name" json:"source_name,omitempty"`
	CreatedAt         time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time    `db:"updated_at" json:"updated_at"`
	DeletedAt         *time.Time   `db:"deleted_at" json:"deleted_at,omitempty"`
}
