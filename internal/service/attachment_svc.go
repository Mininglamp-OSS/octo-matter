package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type attachmentStore interface {
	Create(a *model.TodoAttachment) error
	GetByID(id string) (*model.TodoAttachment, error)
	Delete(id string) error
	ListByTodo(todoID string) ([]*model.TodoAttachment, error)
}

type AttachmentService struct {
	attachmentRepo attachmentStore
	todoRepo       todoScopeChecker
}

func NewAttachmentService(attachmentRepo attachmentStore, todoRepo todoScopeChecker) *AttachmentService {
	return &AttachmentService{attachmentRepo: attachmentRepo, todoRepo: todoRepo}
}

func (s *AttachmentService) CreateAttachment(todoID, spaceID, userID, fileURL string, fileName *string, fileSize *int64, mimeType *string) (*model.TodoAttachment, error) {
	if _, err := s.todoRepo.GetByID(todoID, spaceID); err != nil {
		return nil, err
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

func (s *AttachmentService) ListAttachments(todoID, spaceID string) ([]*model.TodoAttachment, error) {
	if _, err := s.todoRepo.GetByID(todoID, spaceID); err != nil {
		return nil, err
	}
	return s.attachmentRepo.ListByTodo(todoID)
}

// DeleteAttachment enforces: (1) the attachment exists, (2) its parent todo is in
// the caller's space, (3) the caller is the uploader.
func (s *AttachmentService) DeleteAttachment(id, spaceID, userID string) error {
	a, err := s.attachmentRepo.GetByID(id)
	if err != nil {
		return err
	}
	if _, err := s.todoRepo.GetByID(a.TodoID, spaceID); err != nil {
		return err
	}
	if a.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.attachmentRepo.Delete(id)
}
