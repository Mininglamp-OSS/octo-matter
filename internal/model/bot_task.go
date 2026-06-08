package model

import "time"

// BotTask is matter's queue row for "user @mentioned this bot in matter
// X, please pick it up and respond". Persisted in the same transaction
// that writes the triggering timeline entry so message loss is
// impossible. Daemons pull queued rows over HTTP from matter.
//
// INVARIANT (v3.3.4 §D9): matter_bot_task.space_id is set from the
// verified api_key context at INSERT time and never updated. Since
// user_api_key.space_id is immutable (see octo-server modules/botfather
// schema; no UPDATE path exists as of 2026-06-08 — verified by
// `grep -rn 'UPDATE.*user_api_key\|UpdateApiKey' --include='*.go'`
// returning 0 hits across the org), the equation
//
//	task.space_id ≡ api_key.space_id
//
// holds for the task's full lifecycle. Repo SQL relies on this
// invariant: Ack and HasDispatchedTaskForBotOnMatter both filter
// `WHERE space_id = ?` using ctx.space_id (the api_key bound space),
// equivalent to filtering by task.space_id under the invariant.
//
// Mutating either side requires migrating the Ack / HasDispatched SQL
// (e.g. allowing cross-space ack with an explicit compensation path)
// AND adding a re-binding mechanism on api_key.space_id rotation.
// Re-audit if a future PR adds an "api_key change space" feature.
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
