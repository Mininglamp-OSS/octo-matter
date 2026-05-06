package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type attachmentStore interface {
	Create(a *model.TodoAttachment) error
	GetByID(id string) (*model.TodoAttachment, error)
	Delete(id string) error
	ListByTodo(todoID string, cursor *string, limit int) ([]*model.TodoAttachment, bool, error)
}

type AttachmentService struct {
	attachmentRepo attachmentStore
	todoRepo       todoScopeChecker
	access         TodoAccessChecker
}

func NewAttachmentService(attachmentRepo attachmentStore, todoRepo todoScopeChecker, access TodoAccessChecker) *AttachmentService {
	return &AttachmentService{attachmentRepo: attachmentRepo, todoRepo: todoRepo, access: access}
}

func (s *AttachmentService) CreateAttachment(todoID, spaceID, userID, fileURL string, fileName *string, fileSize *int64, mimeType *string, sourceChannelID string) (*model.TodoAttachment, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return nil, apperr.Forbidden("not authorized to access this todo")
	}
	a := &model.TodoAttachment{
		TodoID:   todoID,
		UserID:   userID,
		FileURL:  fileURL,
		FileName: fileName,
		FileSize: fileSize,
		MimeType: mimeType,
	}
	if err := s.attachmentRepo.Create(a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *AttachmentService) ListAttachments(todoID, spaceID, userID string, sourceChannelID string, cursor *string, limit int) ([]*model.TodoAttachment, bool, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, false, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return nil, false, apperr.Forbidden("not authorized to access this todo")
	}
	return s.attachmentRepo.ListByTodo(todoID, cursor, limit)
}

func (s *AttachmentService) DeleteAttachment(id, spaceID, userID string, sourceChannelID string) error {
	a, err := s.attachmentRepo.GetByID(id)
	if err != nil {
		return err
	}
	todo, err := s.todoRepo.GetByID(a.TodoID, spaceID)
	if err != nil {
		return err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return apperr.Forbidden("not authorized to access this todo")
	}
	if a.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.attachmentRepo.Delete(id)
}
