package model

import "time"

type GoalStatus string

const (
	GoalStatusActive    GoalStatus = "active"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusArchived  GoalStatus = "archived"
)

func IsValidGoalStatus(s GoalStatus) bool {
	return s == GoalStatusActive || s == GoalStatusCompleted || s == GoalStatusArchived
}

// Goal represents an optional organizational container for todos.
type Goal struct {
	ID          string     `db:"id" json:"id"`
	SpaceID     string     `db:"space_id" json:"space_id"`
	Title       string     `db:"title" json:"title"`
	Description *string    `db:"description" json:"description,omitempty"`
	CreatorID   string     `db:"creator_id" json:"creator_id"`
	Status      GoalStatus `db:"status" json:"status"`
	Deadline    *time.Time `db:"deadline" json:"deadline,omitempty"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updated_at"`
}

// GoalAssignee represents a user assigned to a goal.
type GoalAssignee struct {
	ID        string    `db:"id" json:"id"`
	GoalID    string    `db:"goal_id" json:"goal_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
