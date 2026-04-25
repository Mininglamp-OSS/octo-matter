package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// todoStore is the narrow TodoRepo surface TodoService depends on. Declared at
// the service layer (not the repo layer) so tests can swap in fakes without
// bringing in real dbr/MySQL machinery.
type todoStore interface {
	Create(todo *model.Todo) error
	GetByID(id, spaceID string) (*model.Todo, error)
	ListBySpace(spaceID string, filter repository.TodoFilter) ([]*model.Todo, int, error)
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

type TodoService struct {
	todoRepo     todoStore
	assigneeRepo assigneeStore
}

func NewTodoService(todoRepo todoStore, assigneeRepo assigneeStore) *TodoService {
	return &TodoService{todoRepo: todoRepo, assigneeRepo: assigneeRepo}
}

func (s *TodoService) CreateTodo(todo *model.Todo) (*model.Todo, error) {
	if todo.Status == "" {
		todo.Status = model.TodoStatusDraft
	}
	if err := s.todoRepo.Create(todo); err != nil {
		return nil, err
	}
	return todo, nil
}

type TodoListResult struct {
	Items      []*model.Todo `json:"items"`
	Total      int           `json:"total"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func (s *TodoService) ListTodos(spaceID string, filter repository.TodoFilter) (*TodoListResult, error) {
	todos, total, err := s.todoRepo.ListBySpace(spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if len(todos) > 0 && len(todos) == filter.Limit {
		nextCursor = todos[len(todos)-1].ID
	}
	return &TodoListResult{
		Items:      todos,
		Total:      total,
		NextCursor: nextCursor,
	}, nil
}

type TodoDetail struct {
	*model.Todo
	Assignees          []*model.TodoAssignee `json:"assignees"`
	AllowedTransitions []model.TodoStatus    `json:"allowed_transitions"`
}

func (s *TodoService) GetTodo(id, spaceID string) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
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
	if err := s.todoRepo.Update(todo); err != nil {
		return nil, err
	}
	return todo, nil
}

func (s *TodoService) TransitionStatus(id, spaceID, userID string, target model.TodoStatus) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	isCreator := todo.CreatorID == userID
	isAssignee, err := s.assigneeRepo.IsAssignee(id, userID)
	if err != nil {
		return nil, err
	}
	if !model.CanTransition(todo.Status, target, isCreator, isAssignee) {
		return nil, apperr.ErrForbidden
	}
	if err := s.todoRepo.UpdateStatus(id, spaceID, string(target)); err != nil {
		return nil, err
	}
	// When transitioning to done, mark all assignees as done.
	if target == model.TodoStatusDone {
		if err := s.assigneeRepo.MarkAllDone(id); err != nil {
			return nil, err
		}
	}
	todo.Status = target
	assignees, _ := s.assigneeRepo.ListByTodo(id)
	return &TodoDetail{
		Todo:               todo,
		Assignees:          assignees,
		AllowedTransitions: model.AllowedTransitions(target),
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
