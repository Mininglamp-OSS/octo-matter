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

func (r *AssigneeRepo) Create(a *model.MatterAssignee) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("matter_assignees").
		Columns("id", "matter_id", "user_id", "created_at").
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

func (r *AssigneeRepo) Delete(matterID, userID string) error {
	result, err := r.runner.DeleteFrom("matter_assignees").
		Where("matter_id = ? AND user_id = ?", matterID, userID).
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

func (r *AssigneeRepo) ListByMatter(matterID string) ([]*model.MatterAssignee, error) {
	assignees := make([]*model.MatterAssignee, 0)
	_, err := r.runner.Select("*").
		From("matter_assignees").
		Where("matter_id = ?", matterID).
		Load(&assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

func (r *AssigneeRepo) IsAssignee(matterID, userID string) (bool, error) {
	count, err := r.runner.Select("COUNT(*)").
		From("matter_assignees").
		Where("matter_id = ? AND user_id = ?", matterID, userID).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
