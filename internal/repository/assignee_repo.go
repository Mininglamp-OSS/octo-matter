package repository

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type AssigneeRepo struct {
	sess *dbr.Session
}

func NewAssigneeRepo(sess *dbr.Session) *AssigneeRepo {
	return &AssigneeRepo{sess: sess}
}

func (r *AssigneeRepo) Create(a *model.TodoAssignee) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.sess.InsertInto("todo_assignees").
		Columns("id", "todo_id", "user_id", "status", "completed_at", "created_at").
		Record(a).
		Exec()
	return err
}

func (r *AssigneeRepo) Delete(todoID, userID string) error {
	_, err := r.sess.DeleteFrom("todo_assignees").
		Where("todo_id = ? AND user_id = ?", todoID, userID).
		Exec()
	return err
}

func (r *AssigneeRepo) UpdateStatus(todoID, userID, status string) error {
	stmt := r.sess.Update("todo_assignees").
		Set("status", status).
		Where("todo_id = ? AND user_id = ?", todoID, userID)
	if status == string(model.AssigneeStatusDone) {
		stmt = stmt.Set("completed_at", time.Now())
	} else {
		stmt = stmt.Set("completed_at", nil)
	}
	_, err := stmt.Exec()
	return err
}

func (r *AssigneeRepo) MarkAllDone(todoID string) error {
	_, err := r.sess.Update("todo_assignees").
		Set("status", string(model.AssigneeStatusDone)).
		Set("completed_at", time.Now()).
		Where("todo_id = ?", todoID).
		Exec()
	return err
}

func (r *AssigneeRepo) ListByTodo(todoID string) ([]*model.TodoAssignee, error) {
	var assignees []*model.TodoAssignee
	_, err := r.sess.Select("*").
		From("todo_assignees").
		Where("todo_id = ?", todoID).
		Load(&assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

func (r *AssigneeRepo) IsAssignee(todoID, userID string) (bool, error) {
	count, err := r.sess.Select("COUNT(*)").
		From("todo_assignees").
		Where("todo_id = ? AND user_id = ?", todoID, userID).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
