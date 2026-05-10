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

// Record inserts a matter_activities row. detail is marshalled as JSON (nil
// detail stores NULL).
func (r *ActivityRepo) Record(ctx context.Context, matterID, actorID, action string, detail interface{}) error {
	var detailJSON *string
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		s := string(b)
		detailJSON = &s
	}
	row := &model.MatterActivity{
		ID:        uuid.New().String(),
		MatterID:  matterID,
		ActorID:   actorID,
		Action:    action,
		Detail:    detailJSON,
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
