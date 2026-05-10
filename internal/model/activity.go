package model

import "time"

// MatterActivity records a mutation on a matter. Detail is a JSON-encoded
// payload whose shape depends on Action (see docs/DESIGN.md — matter_activities).
type MatterActivity struct {
	ID        string    `db:"id" json:"id"`
	MatterID  string    `db:"matter_id" json:"matter_id"`
	ActorID   string    `db:"actor_id" json:"actor_id"`
	Action    string    `db:"action" json:"action"`
	Detail    *string   `db:"detail" json:"detail,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
