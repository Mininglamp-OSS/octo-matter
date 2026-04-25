package service

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// todoStore is the narrow TodoRepo surface TodoService depends on outside of
// transactional flows. Declared at the service layer (not the repo layer) so
// tests can swap in fakes without bringing in real dbr/MySQL machinery.
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

// txRunner runs a closure against a bundle of tx-bound repos. Satisfied by
// *repository.TxManager in production and by tests that need to exercise the
// atomic flows.
type txRunner interface {
	Do(fn func(r *repository.TxRepos) error) error
}

type TodoService struct {
	todoRepo     todoStore
	assigneeRepo assigneeStore
	goalRepo     goalAccessChecker
	tx           txRunner
}

// goalAccessChecker is the subset of GoalRepo needed for visibility checks.
type goalAccessChecker interface {
	IsAssignee(goalID, userID string) (bool, error)
}

func NewTodoService(todoRepo todoStore, assigneeRepo assigneeStore, goalRepo goalAccessChecker, tx txRunner) *TodoService {
	return &TodoService{todoRepo: todoRepo, assigneeRepo: assigneeRepo, goalRepo: goalRepo, tx: tx}
}

// CreateTodoWithAssignees creates the todo and all its initial assignees inside
// a single transaction. A failure on any assignee rolls back the todo too,
// which is the only way to avoid orphan rows when a duplicate assignee_id or
// a dead connection aborts the second write.
//
// Returns a fully-populated TodoDetail so the client has everything it needs
// (assignees, allowed_transitions) without a follow-up GET.
func (s *TodoService) CreateTodoWithAssignees(todo *model.Todo, assigneeIDs []string) (*TodoDetail, error) {
	if todo.Status == "" {
		todo.Status = model.TodoStatusDraft
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
		Todo:               todo,
		Assignees:          created,
		AllowedTransitions: model.AllowedTransitions(todo.Status),
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
	// Only emit next_cursor when there's a next page — repo returns the exact
	// has_more by fetching limit+1 rows internally, so we don't have to guess.
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

type TodoDetail struct {
	*model.Todo
	Assignees          []*model.TodoAssignee `json:"assignees"`
	AllowedTransitions []model.TodoStatus    `json:"allowed_transitions"`
}

func (s *TodoService) GetTodo(id, spaceID, userID string) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	// Visibility check: creator, assignee, or goal assignee
	if !s.canAccessTodo(todo, userID) {
		return nil, apperr.Forbidden("not authorized to view this todo")
	}
	assignees, err := s.assigneeRepo.ListByTodo(id)
	if err != nil {
		return nil, err
	}
	return &TodoDetail{
		Todo:               todo,
		Assignees:          assignees,
		AllowedTransitions: model.AllowedTransitions(todo.Status),
	}, nil
}

// canAccessTodo checks if user can view a todo: creator, assignee, or goal assignee.
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

// UpdateTodo applies editable fields. Deadline and RemindAt arrive as optional
// RFC3339 strings from the JSON body; a malformed value returns an apperr-like
// validation error that handlers map to 400. An explicit null (parsed to nil
// pointer by the handler) is treated as "clear the field"; an absent field
// leaves the existing value in place.
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
	todo.GoalID = goalID
	if deadline != nil {
		t, err := parseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput("deadline must be RFC3339 or empty")
		}
		todo.Deadline = t
	}
	if remindAt != nil {
		t, err := parseOptionalRFC3339(*remindAt)
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

// parseOptionalRFC3339 returns (nil, nil) for the empty string (clear the
// field) and otherwise parses as RFC3339. Returning an error on any other
// input stops silent drops.
func parseOptionalRFC3339(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TransitionStatus changes a todo's status and (when moving to done) marks
// every assignee done atomically, so the "is this todo done?" check can never
// see status=done with assignees still pending.
//
// Errors split along two axes:
//   - state machine disallows target → INVALID_TRANSITION (400) with
//     details.current and details.allowed, so clients don't need a second GET.
//   - state machine allows target, but caller lacks the required role →
//     FORBIDDEN (403).
func (s *TodoService) TransitionStatus(id, spaceID, userID string, target model.TodoStatus) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}

	allowed := model.AllowedTransitions(todo.Status)
	if !containsStatus(allowed, target) {
		return nil, apperr.InvalidTransition(string(todo.Status), statusStrings(allowed))
	}

	isCreator := todo.CreatorID == userID
	isAssignee, err := s.assigneeRepo.IsAssignee(id, userID)
	if err != nil {
		return nil, err
	}
	if !model.CanTransition(todo.Status, target, isCreator, isAssignee) {
		return nil, apperr.Forbidden("caller role may not perform this transition")
	}

	if target == model.TodoStatusDone {
		if err := s.tx.Do(func(r *repository.TxRepos) error {
			if err := r.Todo.UpdateStatus(id, spaceID, string(target)); err != nil {
				return err
			}
			return r.Assignee.MarkAllDone(id)
		}); err != nil {
			return nil, err
		}
	} else {
		if err := s.todoRepo.UpdateStatus(id, spaceID, string(target)); err != nil {
			return nil, err
		}
	}

	todo.Status = target
	assignees, err := s.assigneeRepo.ListByTodo(id)
	if err != nil {
		return nil, err
	}
	return &TodoDetail{
		Todo:               todo,
		Assignees:          assignees,
		AllowedTransitions: model.AllowedTransitions(target),
	}, nil
}

func containsStatus(list []model.TodoStatus, target model.TodoStatus) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func statusStrings(list []model.TodoStatus) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = string(s)
	}
	return out
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
	// Validate the parent todo is in the caller's space before any assignee-table write.
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
