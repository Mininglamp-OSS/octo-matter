package model

import "time"

// Goal represents an optional organizational container for todos.
type Goal struct {
	ID          string    `db:"id" json:"id"`
	SpaceID     string    `db:"space_id" json:"space_id"`
	Title       string    `db:"title" json:"title"`
	Description *string   `db:"description" json:"description,omitempty"`
	OwnerID     string    `db:"owner_id" json:"owner_id"`
	Archived    bool      `db:"archived" json:"archived"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// GoalMember represents a member of a goal.
type GoalMember struct {
	ID        string    `db:"id" json:"id"`
	GoalID    string    `db:"goal_id" json:"goal_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	Role      string    `db:"role" json:"role"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
