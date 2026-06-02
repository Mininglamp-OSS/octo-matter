// Package repository — bot_task storage for matter (PR-B).
//
// bot_task moved here from octo-fleet's schema so that comment creation
// and bot dispatch enqueue land in the same transaction. Daemon pulls
// queued tasks via GET /v1/internal/bot-tasks?bot_uid=X using its
// daemon-scope JWT.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type BotTaskRepo struct {
	runner dbr.SessionRunner
}

func NewBotTaskRepo(sess *dbr.Session) *BotTaskRepo {
	return &BotTaskRepo{runner: sess}
}

// NewBotTaskRepoTx returns a repo bound to an in-progress transaction so
// timeline+activity+bot_task all commit atomically.
func NewBotTaskRepoTx(tx *dbr.Tx) *BotTaskRepo {
	return &BotTaskRepo{runner: tx}
}

// Insert writes a queued bot_task. id is set on the input model. Dedup is
// enforced by the UNIQUE KEY uk_trigger (matter_id, bot_uid,
// trigger_entry_id) — re-insert with the same trigger silently no-ops
// (caller can detect via the returned bool).
func (r *BotTaskRepo) Insert(ctx context.Context, t *model.BotTask) (bool, error) {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.Status == "" {
		t.Status = "queued"
	}
	if t.MaxAttempts == 0 {
		t.MaxAttempts = 3
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now

	res, err := r.runner.InsertInto("matter_bot_task").
		Columns("id", "matter_id", "space_id", "bot_uid", "trigger_kind",
			"trigger_entry_id", "prompt", "matter_title", "status",
			"max_attempts", "created_at", "updated_at").
		Record(t).
		ExecContext(ctx)
	if err != nil {
		// MySQL duplicate-entry on uk_trigger — interpret as "already
		// queued for this trigger". Not an error to the caller.
		if isDuplicateKeyErr(err) {
			return false, nil
		}
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClaimNextForBot atomically pulls up to limit queued tasks for the bot,
// stamping them with a fresh claim_token and lease. Returns the claimed
// rows. Caller must finish them before lease_until or matter's sweeper
// will reset them to queued (attempt++).
func (r *BotTaskRepo) ClaimNextForBot(ctx context.Context, botUID, claimedBy string, limit int, leaseDuration time.Duration) ([]*model.BotTask, error) {
	if limit <= 0 {
		limit = 10
	}
	if leaseDuration <= 0 {
		leaseDuration = 10 * time.Minute
	}

	// Use a transaction so the UPDATE-mark + SELECT-pull are atomic
	// against other daemons claiming the same bot in parallel.
	sess, ok := r.runner.(*dbr.Session)
	if !ok {
		// Tx already — caller wraps. Skip own tx.
		return r.claimNextForBotInner(ctx, r.runner, botUID, claimedBy, limit, leaseDuration)
	}
	tx, err := sess.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.RollbackUnlessCommitted()
	rows, err := r.claimNextForBotInner(ctx, tx, botUID, claimedBy, limit, leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BotTaskRepo) claimNextForBotInner(ctx context.Context, runner dbr.SessionRunner, botUID, claimedBy string, limit int, leaseDuration time.Duration) ([]*model.BotTask, error) {
	claimToken := uuid.New().String()
	leaseUntil := time.Now().Add(leaseDuration)
	// Stamp candidates first — UPDATE with ORDER BY + LIMIT in MySQL is
	// allowed and gives us atomic claim across concurrent daemons.
	_, err := runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status='dispatched', claim_token=?, claimed_by=?, claimed_at=NOW(3), lease_until=?
		  WHERE bot_uid=? AND status='queued'
		  ORDER BY created_at ASC
		  LIMIT ?`,
		claimToken, claimedBy, leaseUntil, botUID, limit,
	).ExecContext(ctx)
	if err != nil {
		return nil, err
	}

	var rows []*model.BotTask
	_, err = runner.SelectBySql(
		`SELECT id, matter_id, space_id, bot_uid, trigger_kind, trigger_entry_id,
		        prompt, matter_title, status, claim_token, claimed_by, claimed_at,
		        lease_until, attempt, max_attempts, error_msg, result_summary,
		        elapsed_ms, created_at, updated_at
		   FROM matter_bot_task
		  WHERE bot_uid=? AND claim_token=? AND status='dispatched'`,
		botUID, claimToken,
	).LoadContext(ctx, &rows)
	return rows, err
}

// Ack updates a claimed task to succeeded/failed. The claim_token MUST
// match — a stale daemon trying to ack with an expired token gets
// dbr.ErrNotFound (caller surfaces 409).
func (r *BotTaskRepo) Ack(ctx context.Context, id, claimToken, status, errMsg, resultSummary string, elapsedMs int64) error {
	if status != "succeeded" && status != "failed" {
		return errors.New("invalid status (must be 'succeeded' or 'failed')")
	}
	res, err := r.runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status=?, error_msg=?, result_summary=?, elapsed_ms=?, updated_at=NOW(3)
		  WHERE id=? AND claim_token=? AND status='dispatched'`,
		status, errMsg, resultSummary, elapsedMs, id, claimToken,
	).ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return dbr.ErrNotFound
	}
	return nil
}

// LoadDispatchedForWriteback resolves a (task_id, claim_token) pair to its
// matter_bot_task row, returning nil when the task is missing, mismatched,
// or no longer in 'dispatched' state. Writeback handlers use this to bind
// timeline/activity inserts to a specific in-flight task — a daemon JWT
// alone is insufficient to write under another bot's identity; the daemon
// must also be the one currently holding the task lease.
//
// Reviewer fix: previously WriteTimeline/WriteActivity trusted body.actor_uid
// as-is, which under DualAuth let any valid daemon JWT impersonate any bot.
// Returning nil here causes the handler to 403, closing the gap.
func (r *BotTaskRepo) LoadDispatchedForWriteback(ctx context.Context, id, claimToken string) (*model.BotTask, error) {
	var t model.BotTask
	_, err := r.runner.SelectBySql(
		`SELECT id, matter_id, space_id, bot_uid, trigger_kind, trigger_entry_id,
		        prompt, matter_title, status, claim_token, claimed_by, claimed_at,
		        lease_until, attempt, max_attempts, error_msg, result_summary,
		        elapsed_ms, created_at, updated_at
		   FROM matter_bot_task
		  WHERE id=? AND claim_token=? AND status='dispatched'`,
		id, claimToken,
	).LoadContext(ctx, &t)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if t.ID == "" {
		return nil, nil
	}
	return &t, nil
}

// Sweeper: reclaim expired leases back to queued (attempt++), or
// dead-letter when attempt >= max_attempts. Called on a 5min ticker
// from cmd/main.go.
func (r *BotTaskRepo) ReclaimExpired(ctx context.Context) (reclaimed, deadLettered int64, err error) {
	// Dead-letter first (so we don't reclaim a row that just hit its cap)
	res, err := r.runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status='failed', error_msg='exceeded max_attempts', updated_at=NOW(3)
		  WHERE status='dispatched'
		    AND lease_until < NOW(3)
		    AND attempt + 1 > max_attempts`,
	).ExecContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	deadLettered, _ = res.RowsAffected()

	res, err = r.runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status='queued', attempt=attempt+1, claim_token=NULL,
		        claimed_by=NULL, claimed_at=NULL, lease_until=NULL, updated_at=NOW(3)
		  WHERE status='dispatched'
		    AND lease_until < NOW(3)`,
	).ExecContext(ctx)
	if err != nil {
		return 0, deadLettered, err
	}
	reclaimed, _ = res.RowsAffected()
	return reclaimed, deadLettered, nil
}
