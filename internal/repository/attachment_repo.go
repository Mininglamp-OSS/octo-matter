package repository

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type AttachmentRepo struct {
	sess *dbr.Session
}

func NewAttachmentRepo(sess *dbr.Session) *AttachmentRepo {
	return &AttachmentRepo{sess: sess}
}

func (r *AttachmentRepo) Create(a *model.TodoAttachment) error {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	_, err := r.sess.InsertInto("todo_attachments").
		Columns("id", "todo_id", "user_id", "file_url", "file_name", "file_size", "mime_type", "created_at").
		Record(a).
		Exec()
	return err
}

func (r *AttachmentRepo) Delete(id string) error {
	_, err := r.sess.DeleteFrom("todo_attachments").
		Where("id = ?", id).
		Exec()
	return err
}

func (r *AttachmentRepo) ListByTodo(todoID string) ([]*model.TodoAttachment, error) {
	var attachments []*model.TodoAttachment
	_, err := r.sess.Select("*").
		From("todo_attachments").
		Where("todo_id = ?", todoID).
		OrderDir("created_at", true).
		Load(&attachments)
	if err != nil {
		return nil, err
	}
	return attachments, nil
}
