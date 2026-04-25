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
	_, err := r.runner.DeleteFrom("todo_comments").
		Where("id = ?", id).
		Exec()
	return err
}

func (r *CommentRepo) ListByTodo(todoID string) ([]*model.TodoComment, error) {
	var comments []*model.TodoComment
	_, err := r.runner.Select("*").
		From("todo_comments").
		Where("todo_id = ?", todoID).
		OrderDir("created_at", true).
		Load(&comments)
	if err != nil {
		return nil, err
	}
	return comments, nil
}
