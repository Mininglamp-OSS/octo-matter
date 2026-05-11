package repository

import (
	"context"
	"testing"

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
