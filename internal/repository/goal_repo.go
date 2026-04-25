package repository

import (
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type GoalRepo struct {
	runner dbr.SessionRunner
}

func NewGoalRepo(sess *dbr.Session) *GoalRepo {
	return &GoalRepo{runner: sess}
}

func (r *GoalRepo) Create(goal *model.Goal) error {
	goal.ID = uuid.New().String()
	now := time.Now()
	goal.CreatedAt = now
	goal.UpdatedAt = now
	_, err := r.runner.InsertInto("goals").
		Columns("id", "space_id", "title", "description", "creator_id", "archived", "created_at", "updated_at").
		Record(goal).
		Exec()
	return err
}

func (r *GoalRepo) GetByID(id, spaceID string) (*model.Goal, error) {
	var goal model.Goal
	err := r.runner.Select("*").
		From("goals").
		Where("id = ? AND space_id = ?", id, spaceID).
		LoadOne(&goal)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.GoalNotFound()
		}
		return nil, err
	}
	return &goal, nil
}

// ListByUser returns non-archived goals where user is creator or assignee.
func (r *GoalRepo) ListByUser(spaceID, userID string) ([]*model.Goal, error) {
	var goals []*model.Goal
	_, err := r.runner.Select("DISTINCT g.*").
		From(dbr.I("goals").As("g")).
		LeftJoin(dbr.I("goal_assignees").As("ga"), "ga.goal_id = g.id").
		Where("g.space_id = ? AND g.archived = 0 AND (g.creator_id = ? OR ga.user_id = ?)", spaceID, userID, userID).
		OrderDir("g.created_at", false).
		Load(&goals)
	if err != nil {
		return nil, err
	}
	return goals, nil
}

func (r *GoalRepo) Update(goal *model.Goal) error {
	goal.UpdatedAt = time.Now()
	_, err := r.runner.Update("goals").
		Set("title", goal.Title).
		Set("description", goal.Description).
		Set("updated_at", goal.UpdatedAt).
		Where("id = ? AND space_id = ?", goal.ID, goal.SpaceID).
		Exec()
	return err
}

func (r *GoalRepo) Archive(id, spaceID string) error {
	_, err := r.runner.Update("goals").
		Set("archived", 1).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ?", id, spaceID).
		Exec()
	return err
}

func (r *GoalRepo) AddAssignee(goalID, userID string) error {
	_, err := r.runner.InsertInto("goal_assignees").
		Columns("id", "goal_id", "user_id", "created_at").
		Values(uuid.New().String(), goalID, userID, time.Now()).
		Exec()
	if err != nil {
		if isDuplicateKeyErr(err) {
			return apperr.DuplicateAssignee()
		}
		return err
	}
	return nil
}

func (r *GoalRepo) RemoveAssignee(goalID, userID string) error {
	_, err := r.runner.DeleteFrom("goal_assignees").
		Where("goal_id = ? AND user_id = ?", goalID, userID).
		Exec()
	return err
}

func (r *GoalRepo) ListAssignees(goalID string) ([]*model.GoalAssignee, error) {
	var assignees []*model.GoalAssignee
	_, err := r.runner.Select("*").
		From("goal_assignees").
		Where("goal_id = ?", goalID).
		Load(&assignees)
	if err != nil {
		return nil, err
	}
	return assignees, nil
}

func (r *GoalRepo) IsAssignee(goalID, userID string) (bool, error) {
	count, err := r.runner.Select("COUNT(*)").
		From("goal_assignees").
		Where("goal_id = ? AND user_id = ?", goalID, userID).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
