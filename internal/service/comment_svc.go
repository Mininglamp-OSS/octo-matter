package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

type CommentService struct {
	commentRepo *repository.CommentRepo
}

func NewCommentService(commentRepo *repository.CommentRepo) *CommentService {
	return &CommentService{commentRepo: commentRepo}
}

func (s *CommentService) CreateComment(todoID, userID, content string) (*model.TodoComment, error) {
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

func (s *CommentService) ListComments(todoID string) ([]*model.TodoComment, error) {
	return s.commentRepo.ListByTodo(todoID)
}

func (s *CommentService) DeleteComment(id, userID string) error {
	return s.commentRepo.Delete(id)
}
