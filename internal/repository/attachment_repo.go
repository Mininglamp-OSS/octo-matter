package repository

import (
	"errors"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type AttachmentRepo struct {
	runner dbr.SessionRunner
}

func NewAttachmentRepo(sess *dbr.Session) *AttachmentRepo {
	return &AttachmentRepo{runner: sess}
}

func (r *AttachmentRepo) Create(a *model.TodoAttachment) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.runner.InsertInto("todo_attachments").
		Columns("id", "todo_id", "user_id", "file_url", "file_name", "file_size", "mime_type", "created_at").
		Record(a).
		Exec()
	return err
}

// GetByID loads an attachment by id. Returns apperr.ErrNotFound if absent. Caller is
// responsible for validating the attachment's todo lives in the caller's space.
func (r *AttachmentRepo) GetByID(id string) (*model.TodoAttachment, error) {
	var a model.TodoAttachment
	err := r.runner.Select("*").
		From("todo_attachments").
		Where("id = ?", id).
		LoadOne(&a)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *AttachmentRepo) Delete(id string) error {
	result, err := r.runner.DeleteFrom("todo_attachments").
		Where("id = ?", id).
		Exec()
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (r *AttachmentRepo) ListByTodo(todoID string) ([]*model.TodoAttachment, error) {
	attachments := make([]*model.TodoAttachment, 0)
	_, err := r.runner.Select("*").
		From("todo_attachments").
		Where("todo_id = ?", todoID).
		OrderDir("created_at", true).
		Load(&attachments)
	if err != nil {
		return nil, err
	}
	return attachments, nil
}
