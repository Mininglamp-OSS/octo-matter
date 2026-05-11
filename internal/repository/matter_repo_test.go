package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// TestMatterRepo_Create_PersistsSourceMsgIDs guards the regression where the
// extract path computes a filtered list of source message IDs but the matters
// row drops it (see issue #40). The INSERT must include the source_msg_ids
// column and bind the JSON array as a quoted string literal (MySQL JSON columns
// reject _binary literals just like matter_activities.detail does).
func TestMatterRepo_Create_PersistsSourceMsgIDs(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(seq_no\\)").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))

	// The rendered SQL must list source_msg_ids and inline the JSON-encoded
	// slice as a quoted string literal (text charset for the JSON column).
	// Assert both the column name and the JSON-encoded value land in the
	// rendered SQL — guards against column drop AND against the value being
	// silently bound to the wrong column slot. dbr escapes double-quotes
	// inside the single-quoted string literal as \", which is valid MySQL.
	mock.ExpectExec("`source_msg_ids`.*\\\\\"m1\\\\\",\\\\\"m2\\\\\"").
		WillReturnResult(sqlmock.NewResult(1, 1))

	r := &MatterRepo{runner: sess}
	m := &model.Matter{
		SpaceID:      "space-1",
		Title:        "t",
		CreatorID:    "u-1",
		Status:       model.MatterStatusOpen,
		SourceMsgIDs: model.JSONStringSlice{"m1", "m2"},
	}
	if err := r.Create(context.Background(), m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestMatterRepo_Create_NilSourceMsgIDsStoredAsNULL ensures a matter created
// without messages writes SQL NULL rather than a JSON literal — keeping
// "no source" distinguishable from "source with zero messages" at storage.
func TestMatterRepo_Create_NilSourceMsgIDsStoredAsNULL(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(seq_no\\)").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))

	// Six consecutive NULLs cover deadline, remind_at, source_channel_id,
	// source_channel_type, source_name, source_msg_ids — the source_msg_ids
	// slot is the bare NULL token rather than a quoted JSON literal.
	mock.ExpectExec(`'open',NULL,NULL,NULL,NULL,NULL,NULL,'`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	r := &MatterRepo{runner: sess}
	m := &model.Matter{
		SpaceID:   "space-1",
		Title:     "t",
		CreatorID: "u-1",
		Status:    model.MatterStatusOpen,
	}
	if err := r.Create(context.Background(), m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestMatterRepo_GetByID_HydratesSourceMsgIDs covers the read path that PR #43
// did not exercise: a row whose source_msg_ids column holds a JSON array must
// scan back into model.Matter.SourceMsgIDs, otherwise GET /v1/matters/:id
// silently strips the field even after the column is populated.
func TestMatterRepo_GetByID_HydratesSourceMsgIDs(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	now := time.Date(2026, 5, 11, 3, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{
		"id", "seq_no", "space_id", "title", "description", "creator_id",
		"status", "deadline", "remind_at", "source_channel_id", "source_channel_type",
		"source_name", "source_msg_ids", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"m-1", 1, "sp-1", "t", nil, "u-1",
		"open", nil, nil, "ch-1", uint8(1),
		nil, []byte(`["msg-a","msg-b"]`), now, now, nil,
	)
	mock.ExpectQuery(`SELECT \* FROM matters`).WillReturnRows(rows)

	r := &MatterRepo{runner: sess}
	got, err := r.GetByID(context.Background(), "m-1", "sp-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.SourceMsgIDs) != 2 || got.SourceMsgIDs[0] != "msg-a" || got.SourceMsgIDs[1] != "msg-b" {
		t.Fatalf("SourceMsgIDs scan: got %v, want [msg-a msg-b]", got.SourceMsgIDs)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"source_msgs":["msg-a","msg-b"]`)) {
		t.Fatalf("populated source_msgs must surface in JSON (aligned with timeline wire name); got %s", b)
	}
}

// TestMatterRepo_GetByID_NULLSourceMsgIDsScansAsNil mirrors the legacy-row
// path: matters created before migration 006 have a NULL column. The scan
// yields a nil slice; the wire contract intentionally normalizes both
// "NULL column" and "explicit empty list" to an empty JSON array, so clients
// see a single stable shape and can rely on the field being present.
func TestMatterRepo_GetByID_NULLSourceMsgIDsScansAsNil(t *testing.T) {
	sess, mock, cleanup := newMockSession(t)
	defer cleanup()

	now := time.Date(2026, 5, 11, 3, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{
		"id", "seq_no", "space_id", "title", "description", "creator_id",
		"status", "deadline", "remind_at", "source_channel_id", "source_channel_type",
		"source_name", "source_msg_ids", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"m-2", 1, "sp-1", "t", nil, "u-1",
		"open", nil, nil, nil, nil,
		nil, nil, now, now, nil,
	)
	mock.ExpectQuery(`SELECT \* FROM matters`).WillReturnRows(rows)

	r := &MatterRepo{runner: sess}
	got, err := r.GetByID(context.Background(), "m-2", "sp-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SourceMsgIDs != nil {
		t.Fatalf("expected nil SourceMsgIDs for NULL column, got %v", got.SourceMsgIDs)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"source_msgs":[]`)) {
		t.Fatalf("legacy NULL row must render source_msgs as [] (aligned with timeline wire name); got %s", b)
	}
}
