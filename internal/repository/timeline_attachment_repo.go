package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
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

// CreateMany inserts attachments for a single timeline entry. Callers must
// set MatterID, SenderUID, SenderUname, SentAt — IDs and CreatedAt are
// assigned here so the service does not need to import uuid.
func (r *TimelineAttachmentRepo) CreateMany(ctx context.Context, atts []*model.TimelineAttachment) error {
	if len(atts) == 0 {
		return nil
	}
	now := time.Now()
	stmt := r.runner.InsertInto("matter_timeline_attachments").
		Columns(
			"id", "entry_id", "matter_id",
			"file_url", "file_name", "file_size", "mime_type",
			"description", "sender_uid", "sender_uname",
			"sent_at", "created_at",
		)
	for _, a := range atts {
		a.ID = uuid.New().String()
		a.CreatedAt = now
		if a.SentAt.IsZero() {
			a.SentAt = now
		}
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

// FindByMatterAndFileURL returns the existing attachment row for a given
// (matter_id, file_url) pair, if any. Used by the LLM path to skip re-inserting
// a file that's already attached to the matter via an earlier timeline entry —
// keeping the outputs view one-row-per-file without a hard DB constraint.
// Returns apperr.ErrNotFound when no such row exists.
func (r *TimelineAttachmentRepo) FindByMatterAndFileURL(ctx context.Context, matterID, fileURL string) (*model.TimelineAttachment, error) {
	var a model.TimelineAttachment
	err := r.runner.Select("*").
		From("matter_timeline_attachments").
		Where("matter_id = ?", matterID).
		Where("file_url = ?", fileURL).
		OrderDir("created_at", true).
		OrderDir("id", true).
		Limit(1).
		LoadOneContext(ctx, &a)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// OutputsCursor is the cursor encoding for the matter outputs list. Ordered by
// (sent_at DESC, id DESC) — sent_at is the IM message time (not the row
// insert time), so the timeline reflects the user's mental order.
type OutputsCursor struct {
	SentAt time.Time
	ID     string
}

// OutputsFilter narrows the matter outputs query. Q is matched (case
// insensitive) against file_name / description / sender_uname.
type OutputsFilter struct {
	MatterID string
	Q        string
	Cursor   *string
	Limit    int
}

// ListOutputs returns deduplicated attachments for a matter. Dedup is by
// file_url: the earliest row (MIN(id) over the matter's rows for that file)
// wins so the displayed description / sender / channel reflect the original
// upload, not a later forward. JOINs matter_channels to pull the source
// channel name.
func (r *TimelineAttachmentRepo) ListOutputs(ctx context.Context, f OutputsFilter) ([]*model.MatterOutput, bool, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}

	// Subquery: pick canonical row per (matter_id, file_url).
	canonical := dbr.Select("MIN(id) AS id").
		From("matter_timeline_attachments").
		Where("matter_id = ?", f.MatterID).
		GroupBy("file_url")

	q := r.runner.Select(
		"a.id", "a.entry_id", "a.matter_id",
		"a.file_url", "a.file_name", "a.file_size", "a.mime_type",
		"a.description", "a.sender_uid", "a.sender_uname",
		"t.source_channel_id AS source_channel_id",
		"mc.channel_name AS source_channel_name",
		"a.sent_at",
	).
		From(dbr.I("matter_timeline_attachments").As("a")).
		LeftJoin(dbr.I("matter_timelines").As("t"), "t.id = a.entry_id").
		LeftJoin(dbr.I("matter_channels").As("mc"), "mc.id = t.channel_id").
		Where("a.matter_id = ?", f.MatterID).
		Where(dbr.Expr("a.id IN ?", canonical))

	if f.Q != "" {
		like := "%" + f.Q + "%"
		q = q.Where(
			dbr.Or(
				dbr.Expr("a.file_name LIKE ?", like),
				dbr.Expr("a.description LIKE ?", like),
				dbr.Expr("a.sender_uname LIKE ?", like),
			),
		)
	}

	if f.Cursor != nil && *f.Cursor != "" {
		cur, err := DecodeCursor(*f.Cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(a.sent_at < ? OR (a.sent_at = ? AND a.id < ?))",
			cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	items := make([]*model.MatterOutput, 0)
	_, err := q.OrderBy("a.sent_at DESC").
		OrderBy("a.id DESC").
		Limit(uint64(f.Limit+1)).
		LoadContext(ctx, &items)
	if err != nil && !errors.Is(err, dbr.ErrNotFound) {
		return nil, false, err
	}
	hasMore := len(items) > f.Limit
	if hasMore {
		items = items[:f.Limit]
	}
	return items, hasMore, nil
}
