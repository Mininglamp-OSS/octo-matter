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

type TimelineRepo struct {
	runner dbr.SessionRunner
}

func NewTimelineRepo(sess *dbr.Session) *TimelineRepo {
	return &TimelineRepo{runner: sess}
}

func (r *TimelineRepo) Create(ctx context.Context, c *model.TimelineEntry) error {
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("matter_timelines").
		Columns("id", "matter_id", "user_id", "content",
			"channel_id", "channel_type", "source_channel_id",
			"source_msgs", "related_uids", "created_at").
		Record(c).
		ExecContext(ctx)
	return err
}

// GetByID loads a timeline entry by id. Returns apperr.ErrNotFound if absent.
func (r *TimelineRepo) GetByID(ctx context.Context, id string) (*model.TimelineEntry, error) {
	var c model.TimelineEntry
	err := r.runner.Select("*").
		From("matter_timelines").
		Where("id = ?", id).
		LoadOneContext(ctx, &c)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *TimelineRepo) Delete(ctx context.Context, id string) error {
	result, err := r.runner.DeleteFrom("matter_timelines").
		Where("id = ?", id).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// ListRecentByMatter returns up to limit most recent timeline entries for the
// matter, ordered newest first. Distinct from ListByMatter (which is ASC for
// chronological API pagination) — used to feed the LLM the recent-context
// window so the model can avoid re-summarizing already-recorded progress.
func (r *TimelineRepo) ListRecentByMatter(ctx context.Context, matterID string, limit int) ([]*model.TimelineEntry, error) {
	if limit <= 0 {
		limit = 3
	}
	entries := make([]*model.TimelineEntry, 0, limit)
	_, err := r.runner.Select("*").
		From("matter_timelines").
		Where("matter_id = ?", matterID).
		OrderDir("created_at", false).
		OrderDir("id", false).
		Limit(uint64(limit)).
		LoadContext(ctx, &entries)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// ListByMatter returns timeline entries for matterID, ordered ascending. When
// sourceChannelID is non-empty, results are restricted to entries originating
// from that channel; pass "" to return every entry on the matter.
func (r *TimelineRepo) ListByMatter(ctx context.Context, matterID, sourceChannelID string, cursor *string, limit int) ([]*model.TimelineEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	entries := make([]*model.TimelineEntry, 0)
	q := r.runner.Select("*").
		From("matter_timelines").
		Where("matter_id = ?", matterID)

	if sourceChannelID != "" {
		q = q.Where("source_channel_id = ?", sourceChannelID)
	}

	if cursor != nil && *cursor != "" {
		cur, err := DecodeCursor(*cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(created_at > ? OR (created_at = ? AND id > ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	_, err := q.OrderDir("created_at", true).
		OrderDir("id", true).
		Limit(uint64(limit+1)).
		LoadContext(ctx, &entries)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

// ListTimelinesByActor (PoC4) returns up to limit timeline entries authored by
// actorUID (matter_timelines.user_id = actorUID), newest first. Used by the
// bot feed proxied through octo-server's bot detail page.
func (r *TimelineRepo) ListTimelinesByActor(ctx context.Context, actorUID string, limit int) ([]*model.TimelineEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries := make([]*model.TimelineEntry, 0, limit)
	_, err := r.runner.Select("*").
		From("matter_timelines").
		Where("user_id = ?", actorUID).
		OrderDir("created_at", false).
		OrderDir("id", false).
		Limit(uint64(limit)).
		LoadContext(ctx, &entries)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
