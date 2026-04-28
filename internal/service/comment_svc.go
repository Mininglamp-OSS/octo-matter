package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/notification"
)

type commentStore interface {
	Create(c *model.TodoComment) error
	GetByID(id string) (*model.TodoComment, error)
	Delete(id string) error
	ListByTodo(todoID string) ([]*model.TodoComment, error)
}

// todoScopeChecker resolves a todo within a space to reject cross-space access.
type todoScopeChecker interface {
	GetByID(id, spaceID string) (*model.Todo, error)
}

type CommentService struct {
	commentRepo commentStore
	todoRepo    todoScopeChecker
	access      TodoAccessChecker
	notifier    notification.Notifier
}

func NewCommentService(commentRepo commentStore, todoRepo todoScopeChecker, access TodoAccessChecker, notifier notification.Notifier) *CommentService {
	if notifier == nil {
		notifier = notification.Noop{}
	}
	return &CommentService{commentRepo: commentRepo, todoRepo: todoRepo, access: access, notifier: notifier}
}

func (s *CommentService) CreateComment(todoID, spaceID, userID, content string) (*model.TodoComment, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessTodo(todo, userID) {
		return nil, apperr.Forbidden("not authorized to access this todo")
	}
	c := &model.TodoComment{
		TodoID:  todoID,
		UserID:  userID,
		Content: content,
	}
	if err := s.commentRepo.Create(c); err != nil {
		return nil, err
	}
	go s.notifier.NotifyCommentAdded(todo, userID, userID)
	return c, nil
}

func (s *CommentService) ListComments(todoID, spaceID, userID string) ([]*model.TodoComment, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessTodo(todo, userID) {
		return nil, apperr.Forbidden("not authorized to access this todo")
	}
	return s.commentRepo.ListByTodo(todoID)
}

func (s *CommentService) DeleteComment(id, spaceID, userID string) error {
	c, err := s.commentRepo.GetByID(id)
	if err != nil {
		return err
	}
	todo, err := s.todoRepo.GetByID(c.TodoID, spaceID)
	if err != nil {
		return err
	}
	if !s.access.CanAccessTodo(todo, userID) {
		return apperr.Forbidden("not authorized to access this todo")
	}
	if c.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.commentRepo.Delete(id)
}
