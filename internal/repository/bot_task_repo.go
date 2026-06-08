// Package repository — bot_task storage for matter (PR-B).
//
// bot_task moved here from octo-fleet's schema so that comment creation
// and bot dispatch enqueue land in the same transaction. Daemon pulls
// queued tasks via GET /v1/internal/bot-tasks?bot_uid=X using its
// api_key Bearer credential (decisions 1+2 Phase 4 replaced the legacy
// daemon-scope JWT path with api_key direct).
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

// ClaimNextForBots is the batched form of ClaimNextForBot. For each bot_uid
// in the input list it claims up to perBotLimit queued tasks, sharing a
// single transaction so the entire batch either commits or rolls back. The
// per-bot loop preserves fairness (a noisy bot can't starve quiet ones the
// way a single `WHERE bot_uid IN (...) LIMIT N` would).
//
// Defensive: deduplicates botUIDs (preserves first-seen order) since a
// duplicate would re-enter claimNextForBotInner and claim another perBotLimit
// rows for the same bot, doubling the per-bot cap. The HTTP handler also
// dedupes, but this method is exported so other callers get the same
// guarantee.
//
// Returned slice is flat — each task carries its own BotUID for grouping.
// Empty input or empty bot_uids returns (nil, nil) without touching the DB.
func (r *BotTaskRepo) ClaimNextForBots(ctx context.Context, botUIDs []string, spaceID, claimedBy string, perBotLimit int, leaseDuration time.Duration) ([]*model.BotTask, error) {
	if len(botUIDs) == 0 {
		return nil, nil
	}
	if perBotLimit <= 0 {
		perBotLimit = 5
	}
	if leaseDuration <= 0 {
		leaseDuration = 10 * time.Minute
	}

	seen := make(map[string]struct{}, len(botUIDs))
	unique := make([]string, 0, len(botUIDs))
	for _, uid := range botUIDs {
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		unique = append(unique, uid)
	}
	if len(unique) == 0 {
		return nil, nil
	}

	doBatch := func(runner dbr.SessionRunner) ([]*model.BotTask, error) {
		var out []*model.BotTask
		for _, botUID := range unique {
			rows, err := r.claimNextForBotInner(ctx, runner, botUID, spaceID, claimedBy, perBotLimit, leaseDuration)
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				out = append(out, rows...)
			}
		}
		return out, nil
	}

	sess, ok := r.runner.(*dbr.Session)
	if !ok {
		// Tx already — caller wraps. Skip own tx.
		return doBatch(r.runner)
	}
	tx, err := sess.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.RollbackUnlessCommitted()
	rows, err := doBatch(tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

// ClaimNextForBot atomically pulls up to limit queued tasks for the bot,
// stamping them with a fresh claim_token and lease. Returns the claimed
// rows. Caller must finish them before lease_until or matter's sweeper
// will reset them to queued (attempt++).
//
// PR-D.1 #3 (security): spaceID is the caller's authenticated bound space
// (post-Phase-4: server verify-api-key returns it after validating space
// membership at api_key issuance time); we only claim tasks whose own
// space_id matches. Closes cross-space task hijacking (an api_key bound
// to Space A cannot pull/DOS tasks of bots in Space B even if it knows
// the victim bot_uid). Fix #1 (server's space-membership check at
// verify time) is what makes ctx.space_id trustworthy here — these two
// fixes are interdependent and ship together.
func (r *BotTaskRepo) ClaimNextForBot(ctx context.Context, botUID, spaceID, claimedBy string, limit int, leaseDuration time.Duration) ([]*model.BotTask, error) {
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
		return r.claimNextForBotInner(ctx, r.runner, botUID, spaceID, claimedBy, limit, leaseDuration)
	}
	tx, err := sess.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.RollbackUnlessCommitted()
	rows, err := r.claimNextForBotInner(ctx, tx, botUID, spaceID, claimedBy, limit, leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BotTaskRepo) claimNextForBotInner(ctx context.Context, runner dbr.SessionRunner, botUID, spaceID, claimedBy string, limit int, leaseDuration time.Duration) ([]*model.BotTask, error) {
	claimToken := uuid.New().String()
	leaseUntil := time.Now().Add(leaseDuration)
	// Stamp candidates first — UPDATE with ORDER BY + LIMIT in MySQL is
	// allowed and gives us atomic claim across concurrent daemons.
	_, err := runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status='dispatched', claim_token=?, claimed_by=?, claimed_at=NOW(3), lease_until=?
		  WHERE bot_uid=? AND space_id=? AND status='queued'
		  ORDER BY created_at ASC
		  LIMIT ?`,
		claimToken, claimedBy, leaseUntil, botUID, spaceID, limit,
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
		  WHERE bot_uid=? AND space_id=? AND claim_token=? AND status='dispatched'`,
		botUID, spaceID, claimToken,
	).LoadContext(ctx, &rows)
	return rows, err
}

// Ack updates a claimed task to succeeded/failed. The claim_token MUST
// match — a stale daemon trying to ack with an expired token gets
// dbr.ErrNotFound (caller surfaces 409).
//
// v3.3.4 §B1 (lml2468 R7 P0): space_id is part of the WHERE clause.
// space_id filter relies on invariant: task.space_id ≡ api_key.space_id
// (see model/bot_task.go INVARIANT). Without this filter a (id,
// claim_token) pair leaked across spaces (logs, snapshots, errant
// routing) would let a daemon in SpaceA ack a task that belongs to
// SpaceB. claim_token is a UUID so the practical exploitability is
// ~10⁻³⁸, but defense-in-depth + symmetry with ClaimNextForBot's
// space_id filter is required by CLAUDE.md "every query MUST filter
// by space_id".
func (r *BotTaskRepo) Ack(ctx context.Context, id, spaceID, claimToken, status, errMsg, resultSummary string, elapsedMs int64) error {
	if status != "succeeded" && status != "failed" {
		return errors.New("invalid status (must be 'succeeded' or 'failed')")
	}
	res, err := r.runner.UpdateBySql(
		`UPDATE matter_bot_task
		    SET status=?, error_msg=?, result_summary=?, elapsed_ms=?, updated_at=NOW(3)
		  WHERE id=? AND space_id=? AND claim_token=? AND status='dispatched'`,
		status, errMsg, resultSummary, elapsedMs, id, spaceID, claimToken,
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

// LoadDispatchedForWriteback removed (v3.2 cleanup, yujiawei R1):
// the function was part of the DualAuth AU5 4-invariant guard
// (body.actor_uid == row.bot_uid + jwt.daemon_id == row.claimed_by)
// for daemon JWT writeback. Phase 4 dropped DualAuth + JWT entirely
// (api_key replaces it), so the (task_id, claim_token) lookup no
// longer gates anything — actor authorization moved to v3 §3.4's
// callerCanActAs (owned_bots_by_space). Removing dead code instead
// of leaving it as bait for a future "let's resurrect this" PR.

// HasDispatchedTaskForBotOnMatter returns true iff a task currently
// dispatched on this matter belongs to this bot.
//
// v3.3.3 §A (Jerry-Xin three-round P0): re-implements the AU5 task-
// binding invariant (Phase 4 dropped) at a lighter weight than the
// original 4-invariant AU5. v3 §3.x rebuilt task-PULL ownership via
// owned_bots_by_space filter (a daemon only pulls its own bots' tasks)
// but missed the writeback side — without this gate, a daemon
// holding any api_key for SpaceA could POST timeline/activity to any
// matter in SpaceA as one of its own bots, even matters its bots had
// never been dispatched to.
//
// Wire format unchanged (claim_token + task_id were removed from body
// in Phase 4 and we don't re-add them). The single SELECT here checks
// just (matter_id, space_id, bot_uid, status='dispatched'), equivalent
// to AU5's constraints minus the claim_token rotation guard. Same-user
// multi-daemon互信 is preserved per 决策二 — any of the user's daemons
// can write during an active task lease, not just the one holding the
// specific claim_token.
//
// v3.3.4 §B6 (lml2468 R7 P2, defense-in-depth symmetric with B1):
// space_id filter relies on invariant: task.space_id ≡ api_key.space_id
// (see model/bot_task.go INVARIANT). bot_uid is currently cluster-
// unique (robot=1 minted), so SQL-level cross-space lookup turns up
// nothing today; but the symmetric filter keeps us consistent with
// Ack/ClaimNextForBot and prevents a future "bot_uid scoped per
// space" change from silently widening the surface. Switched
// COUNT(*) → EXISTS — same boolean semantics, allows the planner to
// stop on first match.
//
// Parameter order (matterID, spaceID, botUID) intentionally mirrors the
// SQL WHERE clause column order so a future maintainer's swap is a
// visible diff rather than a silent runtime bug — both spaceID and
// botUID are strings, Go won't catch a transposition at compile time.
// v3.3.4 review bot P1; symmetric with Ack(ctx, id, spaceID, claimToken, ...).
func (r *BotTaskRepo) HasDispatchedTaskForBotOnMatter(ctx context.Context, matterID, spaceID, botUID string) (bool, error) {
	var exists bool
	_, err := r.runner.SelectBySql(
		`SELECT EXISTS(SELECT 1 FROM matter_bot_task
		   WHERE matter_id=? AND space_id=? AND bot_uid=? AND status='dispatched')`,
		matterID, spaceID, botUID,
	).LoadContext(ctx, &exists)
	if err != nil {
		return false, err
	}
	return exists, nil
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
