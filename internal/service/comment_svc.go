package service

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// Bounds on a single comment's attachments. Hard-enforced in the service
// layer so a client bypassing the upload gateway still can't exceed them.
const (
	MaxAttachmentsPerComment = 10
	MaxAttachmentSizeBytes   = 100 << 20 // 100 MB
	MaxContentLength         = 10000
)

// CommentStore is the narrow CommentRepo surface CommentService depends on.
type CommentStore interface {
	Create(c *model.TodoComment) error
	GetByID(id string) (*model.TodoComment, error)
	Delete(id string) error
	ListByTodo(todoID string, cursor *string, limit int) ([]*model.TodoComment, bool, error)
}

// CommentAttachmentStore is the narrow CommentAttachmentRepo surface.
type CommentAttachmentStore interface {
	CreateMany(atts []*model.CommentAttachment) error
	ListByCommentIDs(ids []string) (map[string][]model.CommentAttachment, error)
	DeleteByCommentID(commentID string) error
}

// commentTxRunner runs a closure that mutates a comment and its attachments
// inside a single transaction. The production adapter wires this to
// repository.TxManager; tests supply a passthrough that reuses fake repos.
type commentTxRunner interface {
	Do(fn func(CommentStore, CommentAttachmentStore) error) error
}

// todoScopeChecker resolves a todo within a space to reject cross-space access.
type todoScopeChecker interface {
	GetByID(id, spaceID string) (*model.Todo, error)
}

// CommentAttachmentInput is the service-level representation of one attachment
// riding along with a comment on create.
type CommentAttachmentInput struct {
	FileURL  string
	FileName *string
	FileSize *int64
	MimeType *string
}

type CommentService struct {
	commentRepo    CommentStore
	attachmentRepo CommentAttachmentStore
	todoRepo       todoScopeChecker
	access         TodoAccessChecker
	tx             commentTxRunner
}

func NewCommentService(
	commentRepo CommentStore,
	attachmentRepo CommentAttachmentStore,
	todoRepo todoScopeChecker,
	access TodoAccessChecker,
	tx commentTxRunner,
) *CommentService {
	return &CommentService{
		commentRepo:    commentRepo,
		attachmentRepo: attachmentRepo,
		todoRepo:       todoRepo,
		access:         access,
		tx:             tx,
	}
}

func (s *CommentService) CreateComment(
	todoID, spaceID, userID, content string,
	attachments []CommentAttachmentInput,
	sourceChannelID string,
) (*model.TodoComment, error) {
	content = strings.TrimSpace(content)
	if content == "" && len(attachments) == 0 {
		return nil, apperr.ValidationError("content or attachments required", "")
	}
	if len(content) > MaxContentLength {
		return nil, apperr.ValidationError("content too long", "content")
	}
	if len(attachments) > MaxAttachmentsPerComment {
		return nil, apperr.ValidationError("too many attachments", "attachments")
	}
	for _, a := range attachments {
		if a.FileURL == "" {
			return nil, apperr.ValidationError("file_url required", "attachments")
		}
		if a.FileSize != nil && *a.FileSize > MaxAttachmentSizeBytes {
			return nil, apperr.ValidationError("attachment too large", "attachments")
		}
	}

	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return nil, apperr.Forbidden("not authorized to access this todo")
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}
	c := &model.TodoComment{
		TodoID:  todoID,
		UserID:  userID,
		Content: contentPtr,
	}

	err = s.tx.Do(func(cs CommentStore, as CommentAttachmentStore) error {
		if err := cs.Create(c); err != nil {
			return err
		}
		if len(attachments) == 0 {
			return nil
		}
		atts := make([]*model.CommentAttachment, 0, len(attachments))
		for _, in := range attachments {
			atts = append(atts, &model.CommentAttachment{
				CommentID: c.ID,
				FileURL:   in.FileURL,
				FileName:  in.FileName,
				FileSize:  in.FileSize,
				MimeType:  in.MimeType,
			})
		}
		if err := as.CreateMany(atts); err != nil {
			return err
		}
		c.Attachments = make([]model.CommentAttachment, 0, len(atts))
		for _, a := range atts {
			c.Attachments = append(c.Attachments, *a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if c.Attachments == nil {
		c.Attachments = []model.CommentAttachment{}
	}
	return c, nil
}

func (s *CommentService) ListComments(
	todoID, spaceID, userID string,
	sourceChannelID string,
	cursor *string,
	limit int,
) ([]*model.TodoComment, bool, error) {
	todo, err := s.todoRepo.GetByID(todoID, spaceID)
	if err != nil {
		return nil, false, err
	}
	if !s.access.CanAccessTodo(todo, userID, sourceChannelID) {
		return nil, false, apperr.Forbidden("not authorized to access this todo")
	}
	comments, hasMore, err := s.commentRepo.ListByTodo(todoID, cursor, limit)
	if err != nil {
		return nil, false, err
	}
	if len(comments) == 0 {
		return comments, hasMore, nil
	}
	ids := make([]string, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
	}
	byComment, err := s.attachmentRepo.ListByCommentIDs(ids)
	if err != nil {
		return nil, false, err
	}
	for _, c := range comments {
		if atts, ok := byComment[c.ID]; ok {
			c.Attachments = atts
		} else {
			c.Attachments = []model.CommentAttachment{}
		}
	}
	return comments, hasMore, nil
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
	// FK ON DELETE CASCADE removes attachments, but we run both in a tx to
	// keep the intent explicit and to leave room for future side effects.
	return s.tx.Do(func(cs CommentStore, as CommentAttachmentStore) error {
		if err := as.DeleteByCommentID(id); err != nil {
			return err
		}
		return cs.Delete(id)
	})
}
