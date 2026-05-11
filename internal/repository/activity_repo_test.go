package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

func newMockSession(t *testing.T) (*dbr.Session, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	conn := &dbr.Connection{
		DB:            db,
		EventReceiver: &dbr.NullEventReceiver{},
		Dialect:       dialect.MySQL,
	}
	return conn.NewSession(nil), mock, func() { _ = db.Close() }
}

// TestActivityRepo_Record_DetailRenderedAsQuotedString is a regression guard
// for PR #39 review: matter_activities.detail is a MySQL JSON column. Binding
// []byte triggers MySQL error 3144 (cannot coerce binary into JSON), and
// because recordActivity is best-effort the failure would be silently logged.
// The detail must reach MySQL as a quoted string literal (text charset).
func TestActivityRepo_Record_DetailRenderedAsQuotedString(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	// Detail must be rendered as a quoted JSON string (",'{...}',"). If the
	// type were []byte, dbr would emit a placeholder or a _binary literal,
	// which MySQL rejects against a JSON column.
	mock.ExpectExec(`,'\{`).WillReturnResult(sqlmock.NewResult(1, 1))

	r := &ActivityRepo{runner: sess}
	err := r.Record(context.Background(), "matter-1", "actor-1", "status_changed",
		map[string]string{"from": "open", "to": "done"})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestActivityRepo_ListByMatter_NULLDetailScansAsNil(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	now := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{"id", "matter_id", "actor_id", "action", "detail", "created_at"}).
		AddRow("a-1", "matter-1", "actor-1", "created", nil, now).
		AddRow("a-2", "matter-1", "actor-1", "status_changed", []byte(`{"from":"open","to":"done"}`), now)
	mock.ExpectQuery(`SELECT \* FROM matter_activities`).
		WillReturnRows(rows)

	r := &ActivityRepo{runner: sess}
	items, hasMore, err := r.ListByMatter(context.Background(), "matter-1", nil, 50)
	if err != nil {
		t.Fatalf("ListByMatter NULL detail: %v", err)
	}
	if hasMore {
		t.Fatalf("hasMore: got true, want false")
	}
	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2", len(items))
	}
	if items[0].Detail != nil {
		t.Fatalf("NULL detail: got %q, want nil", string(items[0].Detail))
	}
	// MarshalJSON of nil JSONDetail must render as JSON null (wire contract).
	nullJSON, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal NULL row: %v", err)
	}
	if !bytes.Contains(nullJSON, []byte(`"detail":null`)) {
		t.Fatalf("NULL detail JSON: got %s, want detail:null", nullJSON)
	}
	// Populated detail must round-trip as a nested object.
	objJSON, err := json.Marshal(items[1])
	if err != nil {
		t.Fatalf("marshal populated row: %v", err)
	}
	if !bytes.Contains(objJSON, []byte(`"detail":{"from":"open","to":"done"}`)) {
		t.Fatalf("populated detail JSON: got %s", objJSON)
	}
}

func TestActivityRepo_Record_NilDetailRenderedAsNULL(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	// NULL (no quotes) means SQL NULL — what we want for missing detail.
	mock.ExpectExec(`,NULL,`).WillReturnResult(sqlmock.NewResult(1, 1))

	r := &ActivityRepo{runner: sess}
	if err := r.Record(context.Background(), "matter-1", "actor-1", "created", nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
