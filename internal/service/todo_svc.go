package service

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// todoStore is the narrow TodoRepo surface TodoService depends on outside of
// transactional flows.
type todoStore interface {
	Create(todo *model.Todo) error
	GetByID(id, spaceID string) (*model.Todo, error)
	ListBySpace(spaceID string, filter repository.TodoFilter) ([]*model.Todo, bool, error)
	Update(todo *model.Todo) error
	UpdateStatus(id, spaceID, status string) error
	SoftDelete(id, spaceID string) error
}

type assigneeStore interface {
	Create(a *model.TodoAssignee) error
	Delete(todoID, userID string) error
	UpdateStatus(todoID, userID, status string) error
	MarkAllDone(todoID string) error
	ListByTodo(todoID string) ([]*model.TodoAssignee, error)
	IsAssignee(todoID, userID string) (bool, error)
}

// txRunner runs a closure against a bundle of tx-bound repos.
type txRunner interface {
	Do(fn func(r *repository.TxRepos) error) error
}

type TodoService struct {
	todoRepo     todoStore
	assigneeRepo assigneeStore
	goalRepo     goalAccessChecker
	tx           txRunner
}

// goalAccessChecker is the subset of GoalRepo needed for visibility + validation checks.
type goalAccessChecker interface {
	IsAssignee(goalID, userID string) (bool, error)
	GetByID(id, spaceID string) (*model.Goal, error)
}

func NewTodoService(todoRepo todoStore, assigneeRepo assigneeStore, goalRepo goalAccessChecker, tx txRunner) *TodoService {
	return &TodoService{todoRepo: todoRepo, assigneeRepo: assigneeRepo, goalRepo: goalRepo, tx: tx}
}

// CreateTodoWithAssignees creates the todo and all its initial assignees inside
// a single transaction.
func (s *TodoService) CreateTodoWithAssignees(todo *model.Todo, assigneeIDs []string) (*TodoDetail, error) {
	if todo.Status == "" {
		todo.Status = model.TodoStatusOpen
	}
	var created []*model.TodoAssignee
	err := s.tx.Do(func(r *repository.TxRepos) error {
		if err := r.Todo.Create(todo); err != nil {
			return err
		}
		created = make([]*model.TodoAssignee, 0, len(assigneeIDs))
		for _, aid := range assigneeIDs {
			a := &model.TodoAssignee{
				TodoID: todo.ID,
				UserID: aid,
				Status: model.AssigneeStatusPending,
			}
			if err := r.Assignee.Create(a); err != nil {
				return err
			}
			created = append(created, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &TodoDetail{
		Todo:      todo,
		Assignees: created,
	}, nil
}

type TodoListResult struct {
	Items      []*model.Todo `json:"items"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func (s *TodoService) ListTodos(spaceID string, filter repository.TodoFilter) (*TodoListResult, error) {
	todos, hasMore, err := s.todoRepo.ListBySpace(spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if hasMore && len(todos) > 0 {
		last := todos[len(todos)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	return &TodoListResult{
		Items:      todos,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// TodoDetail is the enriched response for a single todo.
type TodoDetail struct {
	*model.Todo
	Assignees []*model.TodoAssignee `json:"assignees"`
}

func (s *TodoService) GetTodo(id, spaceID, userID string) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.canAccessTodo(todo, userID) {
		return nil, apperr.Forbidden("not authorized to view this todo")
	}
	assignees, err := s.assigneeRepo.ListByTodo(id)
	if err != nil {
		return nil, err
	}
	return &TodoDetail{
		Todo:      todo,
		Assignees: assignees,
	}, nil
}

// CanAccessTodo checks if user can view a todo: creator, assignee, or goal assignee.
func (s *TodoService) CanAccessTodo(todo *model.Todo, userID string) bool {
	return s.canAccessTodo(todo, userID)
}

func (s *TodoService) canAccessTodo(todo *model.Todo, userID string) bool {
	if todo.CreatorID == userID {
		return true
	}
	if ok, _ := s.assigneeRepo.IsAssignee(todo.ID, userID); ok {
		return true
	}
	if todo.GoalID != nil && *todo.GoalID != "" {
		if ok, _ := s.goalRepo.IsAssignee(*todo.GoalID, userID); ok {
			return true
		}
	}
	return false
}

// UpdateTodo applies editable fields.
func (s *TodoService) UpdateTodo(id, spaceID, userID string, title string, description *string, goalID *string, deadline, remindAt *string) (*model.Todo, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if todo.CreatorID != userID {
		return nil, apperr.ErrForbidden
	}
	todo.Title = title
	todo.Description = description
	if goalID != nil && *goalID != "" {
		goal, err := s.goalRepo.GetByID(*goalID, todo.SpaceID)
		if err != nil {
			return nil, apperr.InvalidInput("goal not found in this space")
		}
		if goal.CreatorID != userID {
			if ok, _ := s.goalRepo.IsAssignee(*goalID, userID); !ok {
				return nil, apperr.Forbidden("not authorized to use this goal")
			}
		}
	}
	todo.GoalID = goalID
	if deadline != nil {
		t, err := ParseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput("deadline must be RFC3339 or empty")
		}
		todo.Deadline = t
	}
	if remindAt != nil {
		t, err := ParseOptionalRFC3339(*remindAt)
		if err != nil {
			return nil, apperr.InvalidInput("remind_at must be RFC3339 or empty")
		}
		todo.RemindAt = t
	}
	if err := s.todoRepo.Update(todo); err != nil {
		return nil, err
	}
	return todo, nil
}

// ParseOptionalRFC3339 returns (nil, nil) for the empty string (clear the
// field) and otherwise parses as RFC3339.
func ParseOptionalRFC3339(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SetStatus changes a todo's status. No state machine — creator or assignee
// can close or reopen. Uses a transaction with SELECT FOR UPDATE.
func (s *TodoService) SetStatus(id, spaceID, userID string, target model.TodoStatus) (*TodoDetail, error) {
	if !model.IsValidStatus(target) {
		return nil, apperr.InvalidInput("status must be 'open' or 'closed'")
	}

	err := s.tx.Do(func(r *repository.TxRepos) error {
		todo, err := r.Todo.GetByIDForUpdate(id, spaceID)
		if err != nil {
			return err
		}

		// Creator or assignee can close/reopen.
		isCreator := todo.CreatorID == userID
		isAssignee, _ := r.Assignee.IsAssignee(id, userID)
		if !isCreator && !isAssignee {
			return apperr.Forbidden("only creator or assignee can change status")
		}

		if todo.Status == target {
			return nil // idempotent — no-op
		}

		if err := r.Todo.UpdateStatus(id, spaceID, string(target)); err != nil {
			return err
		}

		// When closing, mark all assignees done.
		if target == model.TodoStatusClosed {
			if err := r.Assignee.MarkAllDone(id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Re-read outside tx for fresh response.
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	assignees, err := s.assigneeRepo.ListByTodo(id)
	if err != nil {
		return nil, err
	}
	return &TodoDetail{
		Todo:      todo,
		Assignees: assignees,
	}, nil
}

func (s *TodoService) SoftDelete(id, spaceID, userID string) error {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return apperr.ErrForbidden
	}
	return s.todoRepo.SoftDelete(id, spaceID)
}

func (s *TodoService) AddAssignee(todoID, spaceID, userID, assigneeUserID string) error {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return apperr.ErrForbidden
	}
	a := &model.TodoAssignee{
		TodoID: todoID,
		UserID: assigneeUserID,
		Status: model.AssigneeStatusPending,
	}
	return s.assigneeRepo.Create(a)
}

func (s *TodoService) RemoveAssignee(todoID, spaceID, userID, assigneeUserID string) error {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return apperr.ErrForbidden
	}
	return s.assigneeRepo.Delete(todoID, assigneeUserID)
}

func (s *TodoService) UpdateAssigneeStatus(todoID, spaceID, userID string, status model.AssigneeStatus) error {
	if _, err := s.todoRepo.GetByID(todoID, spaceID); err != nil {
		return err
	}
	isAssignee, err := s.assigneeRepo.IsAssignee(todoID, userID)
	if err != nil {
		return err
	}
	if !isAssignee {
		return apperr.ErrForbidden
	}
	return s.assigneeRepo.UpdateStatus(todoID, userID, string(status))
}
