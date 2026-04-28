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
	if goal.Status == "" {
		goal.Status = model.GoalStatusActive
	}
	now := time.Now()
	goal.CreatedAt = now
	goal.UpdatedAt = now
	_, err := r.runner.InsertInto("goals").
		Columns("id", "space_id", "title", "description", "creator_id", "status", "deadline", "created_at", "updated_at").
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

// GoalFilter holds optional filters for goal listing.
type GoalFilter struct {
	CallerUIDs []string
	Status     *string
	Cursor     *string
	Limit      int
}

// ListByUser returns non-archived goals where user is creator or assignee, with cursor pagination.
func (r *GoalRepo) ListByUser(spaceID string, filter GoalFilter) ([]*model.Goal, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	goals := make([]*model.Goal, 0)
	q := r.runner.Select("g.*").
		From(dbr.I("goals").As("g")).
		LeftJoin(dbr.I("goal_assignees").As("ga"), "ga.goal_id = g.id").
		Where("g.space_id = ? AND (g.creator_id IN ? OR ga.user_id IN ?)", spaceID, filter.CallerUIDs, filter.CallerUIDs).
		GroupBy("g.id")

	if filter.Status != nil {
		q = q.Where("g.status = ?", *filter.Status)
	} else {
		q = q.Where("g.status != ?", string(model.GoalStatusArchived))
	}

	if filter.Cursor != nil && *filter.Cursor != "" {
		cur, err := DecodeCursor(*filter.Cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(g.created_at < ? OR (g.created_at = ? AND g.id < ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	_, err := q.OrderDir("g.created_at", false).
		OrderDir("g.id", false).
		Limit(uint64(limit + 1)).
		Load(&goals)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(goals) > limit
	if hasMore {
		goals = goals[:limit]
	}
	return goals, hasMore, nil
}

func (r *GoalRepo) Update(goal *model.Goal) error {
	goal.UpdatedAt = time.Now()
	result, err := r.runner.Update("goals").
		Set("title", goal.Title).
		Set("description", goal.Description).
		Set("status", goal.Status).
		Set("deadline", goal.Deadline).
		Set("updated_at", goal.UpdatedAt).
		Where("id = ? AND space_id = ?", goal.ID, goal.SpaceID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.GoalNotFound()
	}
	return nil
}

func (r *GoalRepo) Archive(id, spaceID string) error {
	result, err := r.runner.Update("goals").
		Set("status", string(model.GoalStatusArchived)).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ?", id, spaceID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.GoalNotFound()
	}
	return nil
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
	result, err := r.runner.DeleteFrom("goal_assignees").
		Where("goal_id = ? AND user_id = ?", goalID, userID).
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

func (r *GoalRepo) ListAssignees(goalID string) ([]*model.GoalAssignee, error) {
	assignees := make([]*model.GoalAssignee, 0)
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

// TodoStats returns the count of open and closed todos for a goal.
type TodoStats struct {
	GoalID      string `db:"goal_id" json:"goal_id"`
	OpenCount   int    `db:"open_count" json:"open_count"`
	ClosedCount int    `db:"closed_count" json:"closed_count"`
}

// GetTodoStatsByGoals returns todo stats for multiple goals in one query.
func (r *GoalRepo) GetTodoStatsByGoals(goalIDs []string) (map[string]*TodoStats, error) {
	if len(goalIDs) == 0 {
		return make(map[string]*TodoStats), nil
	}
	var stats []*TodoStats
	_, err := r.runner.SelectBySql(
		"SELECT goal_id, "+
			"SUM(CASE WHEN status = 'open' THEN 1 ELSE 0 END) as open_count, "+
			"SUM(CASE WHEN status = 'closed' THEN 1 ELSE 0 END) as closed_count "+
			"FROM todos WHERE goal_id IN ? AND deleted_at IS NULL GROUP BY goal_id",
		goalIDs,
	).Load(&stats)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*TodoStats, len(stats))
	for _, s := range stats {
		result[s.GoalID] = s
	}
	return result, nil
}
