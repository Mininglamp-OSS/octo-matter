package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// commentStore is the narrow CommentRepo surface the service depends on.
type commentStore interface {
	Create(c *model.TodoComment) error
	GetByID(id string) (*model.TodoComment, error)
	Delete(id string) error
	ListByTodo(todoID string) ([]*model.TodoComment, error)
}

// todoScopeChecker is satisfied by TodoRepo; it only needs to resolve a todo
// within a space so we can reject cross-space access.
type todoScopeChecker interface {
	GetByID(id, spaceID string) (*model.Todo, error)
}

type CommentService struct {
	commentRepo commentStore
	todoRepo    todoScopeChecker
}

func NewCommentService(commentRepo commentStore, todoRepo todoScopeChecker) *CommentService {
	return &CommentService{commentRepo: commentRepo, todoRepo: todoRepo}
}

// CreateComment verifies the parent todo belongs to spaceID before inserting.
// Returns apperr.ErrNotFound for todos in other spaces — clients cannot
// distinguish "wrong space" from "does not exist".
func (s *CommentService) CreateComment(todoID, spaceID, userID, content string) (*model.TodoComment, error) {
	if _, err := s.todoRepo.GetByID(todoID, spaceID); err != nil {
		return nil, err
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

func (s *CommentService) ListComments(todoID, spaceID string) ([]*model.TodoComment, error) {
	if _, err := s.todoRepo.GetByID(todoID, spaceID); err != nil {
		return nil, err
	}
	return s.commentRepo.ListByTodo(todoID)
}

// DeleteComment enforces: (1) the comment exists, (2) its parent todo is in the
// caller's space (via a GetByID check), (3) the caller is the comment's author.
func (s *CommentService) DeleteComment(id, spaceID, userID string) error {
	c, err := s.commentRepo.GetByID(id)
	if err != nil {
		return err
	}
	if _, err := s.todoRepo.GetByID(c.TodoID, spaceID); err != nil {
		return err
	}
	if c.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.commentRepo.Delete(id)
}
