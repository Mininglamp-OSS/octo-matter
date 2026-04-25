package repository

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type CommentRepo struct {
	sess *dbr.Session
}

func NewCommentRepo(sess *dbr.Session) *CommentRepo {
	return &CommentRepo{sess: sess}
}

func (r *CommentRepo) Create(c *model.TodoComment) error {
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	_, err := r.sess.InsertInto("todo_comments").
		Columns("id", "todo_id", "user_id", "content", "created_at").
		Record(c).
		Exec()
	return err
}

func (r *CommentRepo) Delete(id string) error {
	_, err := r.sess.DeleteFrom("todo_comments").
		Where("id = ?", id).
		Exec()
	return err
}

func (r *CommentRepo) ListByTodo(todoID string) ([]*model.TodoComment, error) {
	var comments []*model.TodoComment
	_, err := r.sess.Select("*").
		From("todo_comments").
		Where("todo_id = ?", todoID).
		OrderDir("created_at", true).
		Load(&comments)
	if err != nil {
		return nil, err
	}
	return comments, nil
}
