package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type ActivityRepo struct {
	runner dbr.SessionRunner
}

func NewActivityRepo(sess *dbr.Session) *ActivityRepo {
	return &ActivityRepo{runner: sess}
}

// activityRow is an internal DTO used only for INSERT. Detail is *string so
// the MySQL driver sends it with a text charset; binding []byte against a
// JSON column triggers MySQL error 3144 (cannot coerce binary into JSON).
// Same gotcha as model/timeline.go:JSONStringSlice.Value.
type activityRow struct {
	ID        string    `db:"id"`
	MatterID  string    `db:"matter_id"`
	ActorID   string    `db:"actor_id"`
	Action    string    `db:"action"`
	Detail    *string   `db:"detail"`
	CreatedAt time.Time `db:"created_at"`
}

// Record inserts a matter_activities row. detail is marshalled as JSON (nil
// detail stores SQL NULL).
func (r *ActivityRepo) Record(ctx context.Context, matterID, actorID, action string, detail interface{}) error {
	var detailStr *string
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		s := string(b)
		detailStr = &s
	}
	row := &activityRow{
		ID:        uuid.New().String(),
		MatterID:  matterID,
		ActorID:   actorID,
		Action:    action,
		Detail:    detailStr,
		CreatedAt: time.Now(),
	}
	_, err := r.runner.InsertInto("matter_activities").
		Columns("id", "matter_id", "actor_id", "action", "detail", "created_at").
		Record(row).
		ExecContext(ctx)
	return err
}

// ListByMatter returns activities for a matter, newest first, with a simple
// cursor based on (created_at, id).
func (r *ActivityRepo) ListByMatter(ctx context.Context, matterID string, cursor *string, limit int) ([]*model.MatterActivity, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.runner.Select("*").
		From("matter_activities").
		Where("matter_id = ?", matterID)

	if cursor != nil && *cursor != "" {
		cur, err := DecodeCursor(*cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	items := make([]*model.MatterActivity, 0)
	_, err := q.OrderBy("created_at DESC").
		OrderBy("id DESC").
		Limit(uint64(limit + 1)).
		LoadContext(ctx, &items)
	if err != nil && !errors.Is(err, dbr.ErrNotFound) {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

// ListActivitiesByActor (PoC4) returns up to limit activity rows where
// actor_id matches, newest first. Used by the bot feed.
func (r *ActivityRepo) ListActivitiesByActor(ctx context.Context, actorUID string, limit int) ([]*model.MatterActivity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]*model.MatterActivity, 0, limit)
	_, err := r.runner.Select("*").
		From("matter_activities").
		Where("actor_id = ?", actorUID).
		OrderDir("created_at", false).
		OrderDir("id", false).
		Limit(uint64(limit)).
		LoadContext(ctx, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}
