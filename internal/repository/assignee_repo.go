package repository

import (
	"context"
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

func (r *AssigneeRepo) Create(ctx context.Context, a *model.MatterAssignee) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("matter_assignees").
		Columns("id", "matter_id", "user_id", "created_at").
		Record(a).
		ExecContext(ctx)
	if err != nil {
		if isDuplicateKeyErr(err) {
			return apperr.DuplicateAssignee()
		}
		return err
	}
	return nil
}

func (r *AssigneeRepo) Delete(ctx context.Context, matterID, userID string) error {
	result, err := r.runner.DeleteFrom("matter_assignees").
		Where("matter_id = ? AND user_id = ?", matterID, userID).
		ExecContext(ctx)
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

func (r *AssigneeRepo) ListByMatter(ctx context.Context, matterID string) ([]*model.MatterAssignee, error) {
	assignees := make([]*model.MatterAssignee, 0)
	_, err := r.runner.Select("*").
		From("matter_assignees").
		Where("matter_id = ?", matterID).
		LoadContext(ctx, &assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

// ListByMatterIDs returns all assignees for the given matter IDs in one query.
// Used to batch-hydrate list responses without N+1. Empty input returns an
// empty slice without touching the database.
func (r *AssigneeRepo) ListByMatterIDs(ctx context.Context, matterIDs []string) ([]*model.MatterAssignee, error) {
	assignees := make([]*model.MatterAssignee, 0)
	if len(matterIDs) == 0 {
		return assignees, nil
	}
	_, err := r.runner.Select("*").
		From("matter_assignees").
		Where("matter_id IN ?", matterIDs).
		LoadContext(ctx, &assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

func (r *AssigneeRepo) IsAssignee(ctx context.Context, matterID, userID string) (bool, error) {
	count, err := r.runner.Select("COUNT(*)").
		From("matter_assignees").
		Where("matter_id = ? AND user_id = ?", matterID, userID).
		ReturnInt64Context(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// IsAssigneeAny reports whether any of userIDs is an assignee of matterID.
func (r *AssigneeRepo) IsAssigneeAny(ctx context.Context, matterID string, userIDs []string) (bool, error) {
	if len(userIDs) == 0 {
		return false, nil
	}
	count, err := r.runner.Select("COUNT(*)").
		From("matter_assignees").
		Where("matter_id = ? AND user_id IN ?", matterID, userIDs).
		ReturnInt64Context(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
