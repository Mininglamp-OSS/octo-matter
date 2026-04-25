package service

import (
	"errors"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

type TodoService struct {
	todoRepo     *repository.TodoRepo
	assigneeRepo *repository.AssigneeRepo
}

func NewTodoService(todoRepo *repository.TodoRepo, assigneeRepo *repository.AssigneeRepo) *TodoService {
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

func (s *TodoService) GetTodo(id, userID string) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id)
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

func (s *TodoService) UpdateTodo(id, userID string, title string, description *string, goalID *string, deadline, remindAt *string) (*model.Todo, error) {
	todo, err := s.todoRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if todo.CreatorID != userID {
		return nil, errors.New("only the creator can update a todo")
	}
	todo.Title = title
	todo.Description = description
	todo.GoalID = goalID
	if err := s.todoRepo.Update(todo); err != nil {
		return nil, err
	}
	return todo, nil
}

func (s *TodoService) TransitionStatus(id, userID string, target model.TodoStatus) (*TodoDetail, error) {
	todo, err := s.todoRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	isCreator := todo.CreatorID == userID
	isAssignee, err := s.assigneeRepo.IsAssignee(id, userID)
	if err != nil {
		return nil, err
	}
	if !model.CanTransition(todo.Status, target, isCreator, isAssignee) {
		return nil, errors.New("transition not allowed")
	}
	if err := s.todoRepo.UpdateStatus(id, string(target)); err != nil {
		return nil, err
	}
	// When transitioning to done, mark all assignees as done
	if target == model.TodoStatusDone {
		if err := s.assigneeRepo.MarkAllDone(id); err != nil {
			return nil, err
		}
	}
	// Return updated detail with new allowed_transitions
	todo.Status = target
	assignees, _ := s.assigneeRepo.ListByTodo(id)
	return &TodoDetail{
		Todo:               todo,
		Assignees:          assignees,
		AllowedTransitions: model.AllowedTransitions(target),
	}, nil
}

func (s *TodoService) SoftDelete(id, userID string) error {
	todo, err := s.todoRepo.GetByID(id)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return errors.New("only the creator can delete a todo")
	}
	return s.todoRepo.SoftDelete(id)
}

func (s *TodoService) AddAssignee(todoID, userID, assigneeUserID string) error {
	todo, err := s.todoRepo.GetByID(todoID)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return errors.New("only the creator can add assignees")
	}
	a := &model.TodoAssignee{
		TodoID: todoID,
		UserID: assigneeUserID,
		Status: model.AssigneeStatusPending,
	}
	return s.assigneeRepo.Create(a)
}

func (s *TodoService) RemoveAssignee(todoID, userID, assigneeUserID string) error {
	todo, err := s.todoRepo.GetByID(todoID)
	if err != nil {
		return err
	}
	if todo.CreatorID != userID {
		return errors.New("only the creator can remove assignees")
	}
	return s.assigneeRepo.Delete(todoID, assigneeUserID)
}

func (s *TodoService) UpdateAssigneeStatus(todoID, userID string, status model.AssigneeStatus) error {
	isAssignee, err := s.assigneeRepo.IsAssignee(todoID, userID)
	if err != nil {
		return err
	}
	if !isAssignee {
		return errors.New("user is not an assignee of this todo")
	}
	return s.assigneeRepo.UpdateStatus(todoID, userID, string(status))
}
