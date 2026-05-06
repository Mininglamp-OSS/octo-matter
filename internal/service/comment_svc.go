package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type commentStore interface {
	Create(c *model.TodoComment) error
	GetByID(id string) (*model.TodoComment, error)
	Delete(id string) error
	ListByTodo(todoID string, cursor *string, limit int) ([]*model.TodoComment, bool, error)
}

// todoScopeChecker resolves a todo within a space to reject cross-space access.
type todoScopeChecker interface {
	GetByID(id, spaceID string) (*model.Todo, error)
}

type CommentService struct {
	commentRepo commentStore
	todoRepo    todoScopeChecker
	access      TodoAccessChecker
}

func NewCommentService(commentRepo commentStore, todoRepo todoScopeChecker, access TodoAccessChecker) *CommentService {
	return &CommentService{commentRepo: commentRepo, todoRepo: todoRepo, access: access}
}

func (s *CommentService) CreateComment(todoID, spaceID, userID, content string, sourceChannelID string) (*model.TodoComment, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
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
	return c, nil
}

func (s *CommentService) ListComments(todoID, spaceID, userID string, sourceChannelID string, cursor *string, limit int) ([]*model.TodoComment, bool, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, false, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return nil, false, apperr.Forbidden("not authorized to access this todo")
	}
	return s.commentRepo.ListByTodo(todoID, cursor, limit)
}

func (s *CommentService) DeleteComment(id, spaceID, userID string, sourceChannelID string) error {
	c, err := s.commentRepo.GetByID(id)
	if err != nil {
		return err
	}
	todo, err := s.todoRepo.GetByID(c.TodoID, spaceID)
	if err != nil {
		return err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return apperr.Forbidden("not authorized to access this todo")
	}
	if c.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.commentRepo.Delete(id)
}
