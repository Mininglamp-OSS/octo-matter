package repository

import (
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

func (r *CommentRepo) Create(c *model.TodoComment) error {
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("todo_comments").
		Columns("id", "todo_id", "user_id", "content", "created_at").
		Record(c).
		Exec()
	return err
}

// GetByID loads a comment by id. Returns apperr.ErrNotFound if absent. Caller is
// responsible for validating the comment's todo lives in the caller's space.
func (r *CommentRepo) GetByID(id string) (*model.TodoComment, error) {
	var c model.TodoComment
	err := r.runner.Select("*").
		From("todo_comments").
		Where("id = ?", id).
		LoadOne(&c)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CommentRepo) Delete(id string) error {
	result, err := r.runner.DeleteFrom("todo_comments").
		Where("id = ?", id).
		Exec()
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

func (r *CommentRepo) ListByTodo(todoID string, cursor *string, limit int) ([]*model.TodoComment, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	comments := make([]*model.TodoComment, 0)
	q := r.runner.Select("*").
		From("todo_comments").
		Where("todo_id = ?", todoID)

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
		Load(&comments)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(comments) > limit
	if hasMore {
		comments = comments[:limit]
	}
	return comments, hasMore, nil
}
