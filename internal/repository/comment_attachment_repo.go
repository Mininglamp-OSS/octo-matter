package repository

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type CommentAttachmentRepo struct {
	runner dbr.SessionRunner
}

func NewCommentAttachmentRepo(sess *dbr.Session) *CommentAttachmentRepo {
	return &CommentAttachmentRepo{runner: sess}
}

// CreateMany inserts attachments for a single comment. The caller is expected
// to have already set CommentID on each record; ID and CreatedAt are assigned
// here so the service layer stays out of ID-generation details.
func (r *CommentAttachmentRepo) CreateMany(atts []*model.CommentAttachment) error {
	if len(atts) == 0 {
		return nil
	}
	now := time.Now()
	stmt := r.runner.InsertInto("todo_comment_attachments").
		Columns("id", "comment_id", "file_url", "file_name", "file_size", "mime_type", "created_at")
	for _, a := range atts {
		a.ID = uuid.New().String()
		a.CreatedAt = now
		stmt = stmt.Record(a)
	}
	_, err := stmt.Exec()
	return err
}

// ListByCommentIDs fetches attachments for every comment id in one query and
// returns them grouped by comment id. Returns an empty map when ids is empty.
func (r *CommentAttachmentRepo) ListByCommentIDs(ids []string) (map[string][]model.CommentAttachment, error) {
	out := make(map[string][]model.CommentAttachment, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.CommentAttachment
	_, err := r.runner.Select("*").
		From("todo_comment_attachments").
		Where("comment_id IN ?", ids).
		OrderDir("created_at", true).
		OrderDir("id", true).
		Load(&rows)
	if err != nil {
		return nil, err
	}
	for _, a := range rows {
		out[a.CommentID] = append(out[a.CommentID], a)
	}
	return out, nil
}

// DeleteByCommentID is a belt-and-suspenders cleanup used when deleting a
// comment inside a transaction. The FK already cascades, so this is only
// useful if the comment is deleted via a path that bypasses the cascade
// (none today). Kept so the service layer can be explicit about intent.
func (r *CommentAttachmentRepo) DeleteByCommentID(commentID string) error {
	_, err := r.runner.DeleteFrom("todo_comment_attachments").
		Where("comment_id = ?", commentID).
		Exec()
	return err
}
