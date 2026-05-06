package repository

import (
	"errors"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type TodoFilter struct {
	CallerUIDs        []string // required: scopes visibility to caller's todos
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

	q := r.runner.Select("*").
		From("todos").
		Where("space_id = ? AND deleted_at IS NULL", spaceID)

	// Visibility: user must be creator, assignee, or goal-assignee. When a
	// source_channel_id is provided, todos originating from that channel are
	// ALSO visible (channel membership is validated upstream by Space
	// middleware). The channel filter NEVER bypasses the CallerUIDs check for
	// non-channel todos — otherwise any user could enumerate another
	// channel's todos by guessing channel IDs.
	if filter.SourceChannelID != nil {
		q = q.Where(
			"(creator_id IN ? OR EXISTS (SELECT 1 FROM todo_assignees WHERE todo_assignees.todo_id = todos.id AND todo_assignees.user_id IN ?) OR EXISTS (SELECT 1 FROM goal_assignees WHERE goal_assignees.goal_id = todos.goal_id AND goal_assignees.user_id IN ?) OR source_channel_id = ?)",
			filter.CallerUIDs, filter.CallerUIDs, filter.CallerUIDs, *filter.SourceChannelID,
		)
	} else {
		q = q.Where(
			"(creator_id IN ? OR EXISTS (SELECT 1 FROM todo_assignees WHERE todo_assignees.todo_id = todos.id AND todo_assignees.user_id IN ?) OR EXISTS (SELECT 1 FROM goal_assignees WHERE goal_assignees.goal_id = todos.goal_id AND goal_assignees.user_id IN ?))",
			filter.CallerUIDs, filter.CallerUIDs, filter.CallerUIDs,
		)
	}

	q = q.Where("(goal_id IS NULL OR NOT EXISTS (SELECT 1 FROM goals WHERE goals.id = todos.goal_id AND goals.status = 'archived'))")

	if filter.GoalID != nil {
		q = q.Where("goal_id = ?", *filter.GoalID)
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	if filter.AssigneeID != nil {
		q = q.Where("id IN (SELECT todo_id FROM todo_assignees WHERE user_id = ?)", *filter.AssigneeID)
	}
	if filter.CreatorID != nil {
		q = q.Where("creator_id = ?", *filter.CreatorID)
	}
	if filter.SourceChannelID != nil {
		q = q.Where("source_channel_id = ?", *filter.SourceChannelID)
	}
	if filter.SourceChannelType != nil {
		q = q.Where("source_channel_type = ?", *filter.SourceChannelType)
	}
	if filter.DeadlineBefore != nil {
		q = q.Where("deadline < ?", *filter.DeadlineBefore)
	}
	if filter.DeadlineAfter != nil {
		q = q.Where("deadline > ?", *filter.DeadlineAfter)
	}
	if filter.Query != nil && *filter.Query != "" {
		escaped := escapeLikePattern(*filter.Query)
		q = q.Where("title LIKE ?", "%"+escaped+"%")
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
	if todos == nil {
		todos = make([]*model.Todo, 0)
	}

	hasMore := len(todos) > limit
	if hasMore {
		todos = todos[:limit]
	}
	return todos, hasMore, nil
}


// GetByIDForUpdate loads a non-deleted todo with SELECT ... FOR UPDATE for use
// inside transactions. This locks the row until the transaction commits/rolls back.
func (r *TodoRepo) GetByIDForUpdate(id, spaceID string) (*model.Todo, error) {
	var todo model.Todo
	err := r.runner.SelectBySql(
		"SELECT * FROM todos WHERE id = ? AND space_id = ? AND deleted_at IS NULL FOR UPDATE",
		id, spaceID,
	).LoadOne(&todo)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.TodoNotFound()
		}
		return nil, err
	}
	return &todo, nil
}

// Update writes all editable fields of todo. The WHERE clause includes space_id
// so a stale struct from another space cannot overwrite a row here.
func (r *TodoRepo) Update(todo *model.Todo) error {
	todo.UpdatedAt = time.Now()
	result, err := r.runner.Update("todos").
		Set("title", todo.Title).
		Set("description", todo.Description).
		Set("goal_id", todo.GoalID).
		Set("deadline", todo.Deadline).
		Set("remind_at", todo.RemindAt).
		Set("updated_at", todo.UpdatedAt).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", todo.ID, todo.SpaceID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.TodoNotFound()
	}
	return nil
}

func (r *TodoRepo) UpdateStatus(id, spaceID, status string) error {
	result, err := r.runner.Update("todos").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.TodoNotFound()
	}
	return nil
}

func (r *TodoRepo) SoftDelete(id, spaceID string) error {
	result, err := r.runner.Update("todos").
		Set("deleted_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.TodoNotFound()
	}
	return nil
}

// escapeLikePattern escapes SQL LIKE wildcards (% and _) in user input.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
