package model

import "time"

// MatterAssignee represents a user assigned to a matter.
type MatterAssignee struct {
	ID        string    `db:"id" json:"id"`
	MatterID  string    `db:"matter_id" json:"matter_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
