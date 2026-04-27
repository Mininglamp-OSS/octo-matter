package repository

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type AssigneeRepo struct {
	runner dbr.SessionRunner
}

func NewAssigneeRepo(sess *dbr.Session) *AssigneeRepo {
	return &AssigneeRepo{runner: sess}
}

func (r *AssigneeRepo) Create(a *model.TodoAssignee) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("todo_assignees").
		Columns("id", "todo_id", "user_id", "created_at").
		Record(a).
		Exec()
	if err != nil {
		if isDuplicateKeyErr(err) {
			return apperr.DuplicateAssignee()
		}
		return err
	}
	return nil
}

func (r *AssigneeRepo) Delete(todoID, userID string) error {
	result, err := r.runner.DeleteFrom("todo_assignees").
		Where("todo_id = ? AND user_id = ?", todoID, userID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.AssigneeNotFound()
	}
	return nil
}

func (r *AssigneeRepo) ListByTodo(todoID string) ([]*model.TodoAssignee, error) {
	assignees := make([]*model.TodoAssignee, 0)
	_, err := r.runner.Select("*").
		From("todo_assignees").
		Where("todo_id = ?", todoID).
		Load(&assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

func (r *AssigneeRepo) IsAssignee(todoID, userID string) (bool, error) {
	count, err := r.runner.Select("COUNT(*)").
		From("todo_assignees").
		Where("todo_id = ? AND user_id = ?", todoID, userID).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
