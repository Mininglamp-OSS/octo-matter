package model

import "time"

// BotTask is matter's queue row for "user @mentioned this bot in matter
// X, please pick it up and respond". Persisted in the same transaction
// that writes the triggering timeline entry so message loss is
// impossible. Daemons pull queued rows over HTTP from matter.
type BotTask struct {
	ID             string    `db:"id"`
	MatterID       string    `db:"matter_id"`
	SpaceID        string    `db:"space_id"`
	BotUID         string    `db:"bot_uid"`
	TriggerKind    string    `db:"trigger_kind"`
	TriggerEntryID *string   `db:"trigger_entry_id"`
	Prompt         string    `db:"prompt"`
	MatterTitle    string    `db:"matter_title"`
	Status         string    `db:"status"`
	ClaimToken     *string   `db:"claim_token"`
	ClaimedBy      *string   `db:"claimed_by"`
	ClaimedAt      *time.Time `db:"claimed_at"`
	LeaseUntil     *time.Time `db:"lease_until"`
	Attempt        int       `db:"attempt"`
	MaxAttempts    int       `db:"max_attempts"`
	ErrorMsg       *string   `db:"error_msg"`
	ResultSummary  *string   `db:"result_summary"`
	ElapsedMs      *int64    `db:"elapsed_ms"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}
