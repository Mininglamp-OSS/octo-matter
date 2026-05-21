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

// escapeLikePattern is defined in matter_repo.go and shared across the repo
// package — it escapes the LIKE meta-characters %, _, and \ so they match
// literally. Pair the result with `ESCAPE '\\'` in the SQL.

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
//
// NOTE: this is a best-effort soft dedup. Two concurrent CreateEntry calls
// for the same (matter_id, file_url) can both observe ErrNotFound and both
// insert — there is no UNIQUE index on the pair (the file_url column is too
// long to index unprefixed, see migration 007). The authoritative dedup
// happens at read time in ListOutputs via the (sent_at, id) anti-join, so
// duplicate rows do not surface to clients; the soft check just keeps the
// table from accumulating obvious repeats on the happy path.
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

// OutputsFilter narrows the matter outputs query. Q is matched (case
// insensitive) against file_name / description / sender_uname.
type OutputsFilter struct {
	MatterID string
	Q        string
	Cursor   *string
	Limit    int
}

// ListOutputs returns deduplicated attachments for a matter. Dedup is by
// file_url: the row with the earliest (sent_at, id) per (matter_id, file_url)
// wins so the displayed description / sender / channel reflect the ORIGINAL
// upload, not a later forward. id is included only as a deterministic
// tiebreaker for same-second uploads — UUIDs do not encode order, so
// MIN(id) alone would pick an arbitrary row. JOINs matter_channels to
// pull the source channel name.
func (r *TimelineAttachmentRepo) ListOutputs(ctx context.Context, f OutputsFilter) ([]*model.MatterOutput, bool, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}

	// Anti-join: keep rows for which no other row exists with the same
	// (matter_id, file_url) at an earlier (sent_at, id). This is the
	// per-group "first row by ordering" pattern that does not rely on id
	// being monotonic — UUIDs are random, so MIN(id) GROUP BY file_url
	// picks arbitrary rows.
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
		Where(dbr.Expr(
			`NOT EXISTS (
				SELECT 1 FROM matter_timeline_attachments b
				 WHERE b.matter_id = a.matter_id
				   AND b.file_url  = a.file_url
				   AND (b.sent_at < a.sent_at OR (b.sent_at = a.sent_at AND b.id < a.id))
			)`,
		))

	if f.Q != "" {
		like := "%" + escapeLikePattern(f.Q) + "%"
		q = q.Where(
			dbr.Or(
				dbr.Expr(`a.file_name LIKE ? ESCAPE '\\'`, like),
				dbr.Expr(`a.description LIKE ? ESCAPE '\\'`, like),
				dbr.Expr(`a.sender_uname LIKE ? ESCAPE '\\'`, like),
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
