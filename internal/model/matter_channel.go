package model

import "time"

type MatterChannel struct {
	ID          string    `db:"id" json:"id"`
	MatterID    string    `db:"matter_id" json:"matter_id"`
	ChannelID   string    `db:"channel_id" json:"channel_id"`
	ChannelType uint8     `db:"channel_type" json:"channel_type"`
	ChannelName *string   `db:"channel_name" json:"channel_name,omitempty"`
	LinkedBy    string    `db:"linked_by" json:"linked_by"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
}
