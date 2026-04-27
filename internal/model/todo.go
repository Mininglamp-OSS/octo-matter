package model

import "time"

// TodoStatus represents the status of a todo. Follows the GitHub model:
// open (needs work) and closed (done or won't do). No state machine — any
// creator or assignee can close or reopen.
type TodoStatus string

const (
	TodoStatusOpen   TodoStatus = "open"
	TodoStatusClosed TodoStatus = "closed"
)

// IsValidStatus reports whether s is a known TodoStatus.
func IsValidStatus(s TodoStatus) bool {
	return s == TodoStatusOpen || s == TodoStatusClosed
}

// Todo represents the atomic task unit.
type Todo struct {
	ID                string     `db:"id" json:"id"`
	SpaceID           string     `db:"space_id" json:"space_id"`
	GoalID            *string    `db:"goal_id" json:"goal_id,omitempty"`
	Title             string     `db:"title" json:"title"`
	Description       *string    `db:"description" json:"description,omitempty"`
	CreatorID         string     `db:"creator_id" json:"creator_id"`
	Status            TodoStatus `db:"status" json:"status"`
	Deadline          *time.Time `db:"deadline" json:"deadline,omitempty"`
	RemindAt          *time.Time `db:"remind_at" json:"remind_at,omitempty"`
	SourceChannelID   *string    `db:"source_channel_id" json:"source_channel_id,omitempty"`
	SourceChannelType *uint8     `db:"source_channel_type" json:"source_channel_type,omitempty"`
	SourceName        *string    `db:"source_name" json:"source_name,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
	DeletedAt         *time.Time `db:"deleted_at" json:"deleted_at,omitempty"`
}
