package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

type AttachmentService struct {
	attachmentRepo *repository.AttachmentRepo
}

func NewAttachmentService(attachmentRepo *repository.AttachmentRepo) *AttachmentService {
	return &AttachmentService{attachmentRepo: attachmentRepo}
}

func (s *AttachmentService) CreateAttachment(todoID, userID, fileURL string, fileName *string, fileSize *int64, mimeType *string) (*model.TodoAttachment, error) {
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

func (s *AttachmentService) ListAttachments(todoID string) ([]*model.TodoAttachment, error) {
	return s.attachmentRepo.ListByTodo(todoID)
}

func (s *AttachmentService) DeleteAttachment(id, userID string) error {
	return s.attachmentRepo.Delete(id)
}
