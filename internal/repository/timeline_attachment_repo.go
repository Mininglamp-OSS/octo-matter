// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type TimelineAttachmentRepo struct {
	runner dbr.SessionRunner
}

func NewTimelineAttachmentRepo(sess *dbr.Session) *TimelineAttachmentRepo {
	return &TimelineAttachmentRepo{runner: sess}
}

// CreateMany inserts attachments for a single timeline entry.
func (r *TimelineAttachmentRepo) CreateMany(ctx context.Context, atts []*model.TimelineAttachment) error {
	if len(atts) == 0 {
		return nil
	}
	now := time.Now()
	stmt := r.runner.InsertInto("matter_timeline_attachments").
		Columns("id", "entry_id", "file_url", "file_name", "file_size", "mime_type", "created_at")
	for _, a := range atts {
		a.ID = uuid.New().String()
		a.CreatedAt = now
		stmt = stmt.Record(a)
	}
	_, err := stmt.ExecContext(ctx)
	return err
}

// ListByEntryIDs fetches attachments for every timeline entry id in one query.
func (r *TimelineAttachmentRepo) ListByEntryIDs(ctx context.Context, ids []string) (map[string][]model.TimelineAttachment, error) {
	out := make(map[string][]model.TimelineAttachment, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.TimelineAttachment
	_, err := r.runner.Select("*").
		From("matter_timeline_attachments").
		Where("entry_id IN ?", ids).
		OrderDir("created_at", true).
		OrderDir("id", true).
		LoadContext(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, a := range rows {
		out[a.EntryID] = append(out[a.EntryID], a)
	}
	return out, nil
}

// DeleteByEntryID removes all attachments for a timeline entry.
func (r *TimelineAttachmentRepo) DeleteByEntryID(ctx context.Context, entryID string) error {
	_, err := r.runner.DeleteFrom("matter_timeline_attachments").
		Where("entry_id = ?", entryID).
		ExecContext(ctx)
	return err
}
