// v3.3.4 §B1 (lml2468 R7 P0) regression: BotTaskRepo.Ack MUST filter by
// space_id in addition to (id, claim_token). Pre-v3.3.4 a daemon in
// SpaceA holding a (id, claim_token) leaked across spaces (logs,
// snapshots, errant routing) could ack a task that lives in SpaceB.
// claim_token is a UUID so 10⁻³⁸ practical, but defense-in-depth +
// symmetry with ClaimNextForBot's space_id filter is required by
// CLAUDE.md "every query MUST filter by space_id".

package repository

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
)

func TestBotTaskRepo_Ack_RejectsCrossSpace(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	// Cross-space ack: same (id, claim_token) but spaceID=SpaceB while the
	// row's space_id=SpaceA. WHERE clause includes `space_id=?` so the
	// UPDATE matches 0 rows → dbr.ErrNotFound. The SQL regex asserts the
	// space_id appears in the WHERE clause with the cross-space value.
	// (dbr inlines parameters before sending; we match the rendered SQL.)
	mock.ExpectExec(`UPDATE matter_bot_task.*space_id='space-B'.*claim_token='token-1'.*status='dispatched'`).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected → cross-space rejected

	r := &BotTaskRepo{runner: sess}
	err := r.Ack(context.Background(), "task-1", "space-B", "token-1", "succeeded", "", "ok", 123)
	if !errors.Is(err, dbr.ErrNotFound) {
		t.Fatalf("Ack cross-space: expected dbr.ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBotTaskRepo_Ack_MatchesSameSpace(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	// Happy path: ctx.space_id matches task.space_id → 1 row affected → no error.
	mock.ExpectExec(`UPDATE matter_bot_task.*space_id='space-A'.*claim_token='token-1'.*status='dispatched'`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	r := &BotTaskRepo{runner: sess}
	err := r.Ack(context.Background(), "task-1", "space-A", "token-1", "succeeded", "", "ok", 123)
	if err != nil {
		t.Fatalf("Ack same-space: expected nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
