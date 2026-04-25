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
	CallerID          string // required: scopes visibility to caller's todos
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
	runner dbr.SessionRunner
}

func NewTodoRepo(sess *dbr.Session) *TodoRepo {
	return &TodoRepo{runner: sess}
}

func (r *TodoRepo) Create(todo *model.Todo) error {
	todo.ID = uuid.New().String()
	now := time.Now()
	todo.CreatedAt = now
	todo.UpdatedAt = now
	_, err := r.runner.InsertInto("todos").
		Columns("id", "space_id", "goal_id", "title", "description", "creator_id",
			"status", "deadline", "remind_at", "source_channel_id", "source_channel_type",
			"source_name", "created_at", "updated_at", "deleted_at").
		Record(todo).
		Exec()
	return err
}

// GetByID loads a non-deleted todo that belongs to spaceID. Returns
// apperr.TodoNotFound() if the row does not exist OR lives in another space
// (callers must not distinguish — same-code response closes the tenancy leak).
func (r *TodoRepo) GetByID(id, spaceID string) (*model.Todo, error) {
	var todo model.Todo
	err := r.runner.Select("*").
		From("todos").
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		LoadOne(&todo)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.TodoNotFound()
		}
		return nil, err
	}
	return &todo, nil
}

// ListBySpace returns the page of todos matching filter plus an exact has_more
// flag. Pagination uses composite cursor (created_at, id) — a plain last-id
// cursor would skip or repeat rows when two todos share the same creation
// second. We fetch limit+1 rows and trim: if the extra row exists, has_more
// is true and the caller can stop as soon as it sees false. There is no
// COUNT(*) — cursor pagination intentionally drops row counts (O(N) per page).
func (r *TodoRepo) ListBySpace(spaceID string, filter TodoFilter) ([]*model.Todo, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	q := r.runner.Select("DISTINCT t.*").
		From(dbr.I("todos").As("t")).
		LeftJoin(dbr.I("todo_assignees").As("ta"), "ta.todo_id = t.id").
		LeftJoin(dbr.I("goal_assignees").As("ga"), "ga.goal_id = t.goal_id").
		Where("t.space_id = ? AND t.deleted_at IS NULL", spaceID).
		Where("(t.creator_id = ? OR ta.user_id = ? OR ga.user_id = ?)", filter.CallerID, filter.CallerID, filter.CallerID)

	if filter.GoalID != nil {
		q = q.Where("t.goal_id = ?", *filter.GoalID)
	}
	if filter.Status != nil {
		q = q.Where("t.status = ?", *filter.Status)
	}
	if filter.AssigneeID != nil {
		q = q.Where("t.id IN (SELECT todo_id FROM todo_assignees WHERE user_id = ?)", *filter.AssigneeID)
	}
	if filter.CreatorID != nil {
		q = q.Where("t.creator_id = ?", *filter.CreatorID)
	}
	if filter.SourceChannelID != nil {
		q = q.Where("t.source_channel_id = ?", *filter.SourceChannelID)
	}
	if filter.SourceChannelType != nil {
		q = q.Where("t.source_channel_type = ?", *filter.SourceChannelType)
	}
	if filter.DeadlineBefore != nil {
		q = q.Where("deadline < ?", *filter.DeadlineBefore)
	}
	if filter.DeadlineAfter != nil {
		q = q.Where("deadline > ?", *filter.DeadlineAfter)
	}
	if filter.Query != nil && *filter.Query != "" {
		q = q.Where("title LIKE ?", "%"+*filter.Query+"%")
	}

	// Composite cursor: (created_at DESC, id DESC). The id tie-break is required
	// because two todos created within the same second would otherwise skip or
	// repeat at the page boundary.
	if filter.Cursor != nil && *filter.Cursor != "" {
		cur, err := DecodeCursor(*filter.Cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	var todos []*model.Todo
	_, err := q.OrderBy("created_at DESC").
		OrderBy("id DESC").
		Limit(uint64(limit + 1)).
		Load(&todos)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(todos) > limit
	if hasMore {
		todos = todos[:limit]
	}
	return todos, hasMore, nil
}

func (r *TodoRepo) ListByGoalGroupedByStatus(spaceID, goalID string) (map[string][]*model.Todo, error) {
	var todos []*model.Todo
	_, err := r.runner.Select("*").
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
	_, err := r.runner.Select("status", "COUNT(*) AS cnt").
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
	_, err := r.runner.Update("todos").
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
	_, err := r.runner.Update("todos").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	return err
}

func (r *TodoRepo) SoftDelete(id, spaceID string) error {
	_, err := r.runner.Update("todos").
		Set("deleted_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	return err
}

func (r *TodoRepo) ListBySource(spaceID, channelID string, channelType uint8) ([]*model.Todo, error) {
	var todos []*model.Todo
	_, err := r.runner.Select("*").
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
