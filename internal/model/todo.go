package model

import "time"

// TodoStatus represents the status of a todo.
type TodoStatus string

const (
	TodoStatusDraft      TodoStatus = "draft"
	TodoStatusPlanned    TodoStatus = "planned"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusDone       TodoStatus = "done"
	TodoStatusCancelled  TodoStatus = "cancelled"
)

// ValidTransitions defines which status transitions are allowed and who can trigger them.
// Key: from status, Value: map of target status -> required role ("creator" or "creator_or_assignee").
var ValidTransitions = map[TodoStatus]map[TodoStatus]string{
	TodoStatusDraft: {
		TodoStatusPlanned:   "creator",
		TodoStatusCancelled: "creator",
	},
	TodoStatusPlanned: {
		TodoStatusInProgress: "creator_or_assignee",
		TodoStatusCancelled:  "creator",
	},
	TodoStatusInProgress: {
		TodoStatusDone:      "creator_or_assignee",
		TodoStatusCancelled: "creator",
	},
	TodoStatusCancelled: {
		TodoStatusDraft: "creator",
	},
}

// AllowedTransitions returns the list of statuses a todo can transition to from its current status.
func AllowedTransitions(current TodoStatus) []TodoStatus {
	targets, ok := ValidTransitions[current]
	if !ok {
		return nil
	}
	result := make([]TodoStatus, 0, len(targets))
	for target := range targets {
		result = append(result, target)
	}
	return result
}

// AllowedTransitionsForRole returns only the transitions the caller can perform.
func AllowedTransitionsForRole(current TodoStatus, isCreator, isAssignee bool) []TodoStatus {
	targets, ok := ValidTransitions[current]
	if !ok {
		return nil
	}
	var result []TodoStatus
	for target, requiredRole := range targets {
		switch requiredRole {
		case "creator":
			if isCreator {
				result = append(result, target)
			}
		case "creator_or_assignee":
			if isCreator || isAssignee {
				result = append(result, target)
			}
		}
	}
	return result
}

// CanTransition checks if a transition from current to target is valid for the given user role.
// userIsCreator: the user is the todo creator.
// userIsAssignee: the user is an assignee of the todo.
func CanTransition(current, target TodoStatus, userIsCreator, userIsAssignee bool) bool {
	targets, ok := ValidTransitions[current]
	if !ok {
		return false
	}
	requiredRole, ok := targets[target]
	if !ok {
		return false
	}
	switch requiredRole {
	case "creator":
		return userIsCreator
	case "creator_or_assignee":
		return userIsCreator || userIsAssignee
	default:
		return false
	}
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
