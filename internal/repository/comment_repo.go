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

type CommentRepo struct {
	runner dbr.SessionRunner
}

func NewCommentRepo(sess *dbr.Session) *CommentRepo {
	return &CommentRepo{runner: sess}
}

func (r *CommentRepo) Create(ctx context.Context, c *model.MatterComment) error {
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("matter_comments").
		Columns("id", "matter_id", "user_id", "content", "created_at").
		Record(c).
		ExecContext(ctx)
	return err
}

// GetByID loads a comment by id. Returns apperr.ErrNotFound if absent.
func (r *CommentRepo) GetByID(ctx context.Context, id string) (*model.MatterComment, error) {
	var c model.MatterComment
	err := r.runner.Select("*").
		From("matter_comments").
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

func (r *CommentRepo) Delete(ctx context.Context, id string) error {
	result, err := r.runner.DeleteFrom("matter_comments").
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

func (r *CommentRepo) ListByMatter(ctx context.Context, matterID string, cursor *string, limit int) ([]*model.MatterComment, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	comments := make([]*model.MatterComment, 0)
	q := r.runner.Select("*").
		From("matter_comments").
		Where("matter_id = ?", matterID)

	if cursor != nil && *cursor != "" {
		cur, err := DecodeCursor(*cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(created_at > ? OR (created_at = ? AND id > ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	_, err := q.OrderDir("created_at", true).
		OrderDir("id", true).
		Limit(uint64(limit + 1)).
		LoadContext(ctx, &comments)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(comments) > limit
	if hasMore {
		comments = comments[:limit]
	}
	return comments, hasMore, nil
}
