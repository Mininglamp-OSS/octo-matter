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
		Columns("id", "space_id", "title", "description", "owner_id", "archived", "created_at", "updated_at").
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

func (r *GoalRepo) ListBySpace(spaceID string) ([]*model.Goal, error) {
	var goals []*model.Goal
	_, err := r.runner.Select("*").
		From("goals").
		Where("space_id = ? AND archived = 0", spaceID).
		OrderDir("created_at", false).
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

func (r *GoalRepo) AddMember(goalID, userID, role string) error {
	_, err := r.runner.InsertInto("goal_members").
		Columns("id", "goal_id", "user_id", "role", "created_at").
		Values(uuid.New().String(), goalID, userID, role, time.Now()).
		Exec()
	return err
}

func (r *GoalRepo) RemoveMember(goalID, userID string) error {
	_, err := r.runner.DeleteFrom("goal_members").
		Where("goal_id = ? AND user_id = ?", goalID, userID).
		Exec()
	return err
}

func (r *GoalRepo) ListMembers(goalID string) ([]*model.GoalMember, error) {
	var members []*model.GoalMember
	_, err := r.runner.Select("*").
		From("goal_members").
		Where("goal_id = ?", goalID).
		Load(&members)
	if err != nil {
		return nil, err
	}
	return members, nil
}

func (r *GoalRepo) IsMember(goalID, userID string) (bool, error) {
	count, err := r.runner.Select("COUNT(*)").
		From("goal_members").
		Where("goal_id = ? AND user_id = ?", goalID, userID).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
