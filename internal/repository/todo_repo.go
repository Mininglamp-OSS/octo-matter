package repository

import (
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type TodoFilter struct {
	GoalID            *string
	Status            *string
	AssigneeID        *string
	CreatorID         *string
	SourceChannelID   *string
	SourceChannelType *uint8
	DeadlineBefore    *time.Time
	DeadlineAfter     *time.Time
	Query             *string
	Cursor            *string
	Limit             int
}

type TodoRepo struct {
	sess *dbr.Session
}

func NewTodoRepo(sess *dbr.Session) *TodoRepo {
	return &TodoRepo{sess: sess}
}

func (r *TodoRepo) Create(todo *model.Todo) error {
	todo.ID = uuid.New().String()
	now := time.Now()
	todo.CreatedAt = now
	todo.UpdatedAt = now
	_, err := r.sess.InsertInto("todos").
		Columns("id", "space_id", "goal_id", "title", "description", "creator_id",
			"status", "deadline", "remind_at", "source_channel_id", "source_channel_type",
			"source_name", "created_at", "updated_at", "deleted_at").
		Record(todo).
		Exec()
	return err
}

// GetByID loads a non-deleted todo that belongs to spaceID. Returns apperr.ErrNotFound
// if the row does not exist OR lives in another space (callers must not distinguish).
func (r *TodoRepo) GetByID(id, spaceID string) (*model.Todo, error) {
	var todo model.Todo
	err := r.sess.Select("*").
		From("todos").
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		LoadOne(&todo)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &todo, nil
}

func (r *TodoRepo) ListBySpace(spaceID string, filter TodoFilter) ([]*model.Todo, int, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	q := r.sess.Select("*").
		From("todos").
		Where("space_id = ? AND deleted_at IS NULL", spaceID)

	countQ := r.sess.Select("COUNT(*)").
		From("todos").
		Where("space_id = ? AND deleted_at IS NULL", spaceID)

	if filter.GoalID != nil {
		q = q.Where("goal_id = ?", *filter.GoalID)
		countQ = countQ.Where("goal_id = ?", *filter.GoalID)
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
		countQ = countQ.Where("status = ?", *filter.Status)
	}
	if filter.AssigneeID != nil {
		sub := "id IN (SELECT todo_id FROM todo_assignees WHERE user_id = ?)"
		q = q.Where(sub, *filter.AssigneeID)
		countQ = countQ.Where(sub, *filter.AssigneeID)
	}
	if filter.CreatorID != nil {
		q = q.Where("creator_id = ?", *filter.CreatorID)
		countQ = countQ.Where("creator_id = ?", *filter.CreatorID)
	}
	if filter.SourceChannelID != nil {
		q = q.Where("source_channel_id = ?", *filter.SourceChannelID)
		countQ = countQ.Where("source_channel_id = ?", *filter.SourceChannelID)
	}
	if filter.SourceChannelType != nil {
		q = q.Where("source_channel_type = ?", *filter.SourceChannelType)
		countQ = countQ.Where("source_channel_type = ?", *filter.SourceChannelType)
	}
	if filter.DeadlineBefore != nil {
		q = q.Where("deadline < ?", *filter.DeadlineBefore)
		countQ = countQ.Where("deadline < ?", *filter.DeadlineBefore)
	}
	if filter.DeadlineAfter != nil {
		q = q.Where("deadline > ?", *filter.DeadlineAfter)
		countQ = countQ.Where("deadline > ?", *filter.DeadlineAfter)
	}
	if filter.Query != nil && *filter.Query != "" {
		like := "%" + *filter.Query + "%"
		q = q.Where("title LIKE ?", like)
		countQ = countQ.Where("title LIKE ?", like)
	}

	total, err := countQ.ReturnInt64()
	if err != nil {
		return nil, 0, err
	}

	if filter.Cursor != nil && *filter.Cursor != "" {
		q = q.Where("id < ?", *filter.Cursor)
	}

	var todos []*model.Todo
	_, err = q.OrderDir("id", false).
		Limit(uint64(limit)).
		Load(&todos)
	if err != nil {
		return nil, 0, err
	}

	return todos, int(total), nil
}

func (r *TodoRepo) ListByGoalGroupedByStatus(spaceID, goalID string) (map[string][]*model.Todo, error) {
	var todos []*model.Todo
	_, err := r.sess.Select("*").
		From("todos").
		Where("space_id = ? AND goal_id = ? AND deleted_at IS NULL", spaceID, goalID).
		OrderDir("created_at", false).
		Load(&todos)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]*model.Todo)
	for _, t := range todos {
		key := string(t.Status)
		grouped[key] = append(grouped[key], t)
	}
	return grouped, nil
}

func (r *TodoRepo) CountByGoalStatus(spaceID, goalID string) (map[string]int, error) {
	type row struct {
		Status string `db:"status"`
		Count  int    `db:"cnt"`
	}
	var rows []row
	_, err := r.sess.Select("status", "COUNT(*) AS cnt").
		From("todos").
		Where("space_id = ? AND goal_id = ? AND deleted_at IS NULL", spaceID, goalID).
		GroupBy("status").
		Load(&rows)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int)
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

// Update writes all editable fields of todo. The WHERE clause includes space_id
// so a stale struct from another space cannot overwrite a row here.
func (r *TodoRepo) Update(todo *model.Todo) error {
	todo.UpdatedAt = time.Now()
	_, err := r.sess.Update("todos").
		Set("title", todo.Title).
		Set("description", todo.Description).
		Set("goal_id", todo.GoalID).
		Set("deadline", todo.Deadline).
		Set("remind_at", todo.RemindAt).
		Set("updated_at", todo.UpdatedAt).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", todo.ID, todo.SpaceID).
		Exec()
	return err
}

func (r *TodoRepo) UpdateStatus(id, spaceID, status string) error {
	_, err := r.sess.Update("todos").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	return err
}

func (r *TodoRepo) SoftDelete(id, spaceID string) error {
	_, err := r.sess.Update("todos").
		Set("deleted_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	return err
}

func (r *TodoRepo) ListBySource(spaceID, channelID string, channelType uint8) ([]*model.Todo, error) {
	var todos []*model.Todo
	_, err := r.sess.Select("*").
		From("todos").
		Where("space_id = ? AND source_channel_id = ? AND source_channel_type = ? AND deleted_at IS NULL",
			spaceID, channelID, channelType).
		OrderDir("created_at", false).
		Load(&todos)
	if err != nil {
		return nil, err
	}
	return todos, nil
}
