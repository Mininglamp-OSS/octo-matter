package service

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// Bounds on a single comment's attachments.
const (
	MaxAttachmentsPerComment = 10
	MaxAttachmentSizeBytes   = 100 << 20 // 100 MB
	MaxContentLength         = 10000
)

// CommentStore is the narrow CommentRepo surface CommentService depends on.
type CommentStore interface {
	Create(c *model.MatterComment) error
	GetByID(id string) (*model.MatterComment, error)
	Delete(id string) error
	ListByMatter(matterID string, cursor *string, limit int) ([]*model.MatterComment, bool, error)
}

// CommentAttachmentStore is the narrow CommentAttachmentRepo surface.
type CommentAttachmentStore interface {
	CreateMany(atts []*model.CommentAttachment) error
	ListByCommentIDs(ids []string) (map[string][]model.CommentAttachment, error)
	DeleteByCommentID(commentID string) error
}

// commentTxRunner runs a closure that mutates a comment and its attachments
// inside a single transaction.
type commentTxRunner interface {
	Do(fn func(CommentStore, CommentAttachmentStore) error) error
}

// matterScopeChecker resolves a matter within a space to reject cross-space access.
type matterScopeChecker interface {
	GetByID(id, spaceID string) (*model.Matter, error)
}

// CommentAttachmentInput is the service-level representation of one attachment.
type CommentAttachmentInput struct {
	FileURL  string
	FileName *string
	FileSize *int64
	MimeType *string
}

type CommentService struct {
	commentRepo    CommentStore
	attachmentRepo CommentAttachmentStore
	matterRepo     matterScopeChecker
	access         MatterAccessChecker
	tx             commentTxRunner
}

func NewCommentService(
	commentRepo CommentStore,
	attachmentRepo CommentAttachmentStore,
	matterRepo matterScopeChecker,
	access MatterAccessChecker,
	tx commentTxRunner,
) *CommentService {
	return &CommentService{
		commentRepo:    commentRepo,
		attachmentRepo: attachmentRepo,
		matterRepo:     matterRepo,
		access:         access,
		tx:             tx,
	}
}

func (s *CommentService) CreateComment(
	matterID, spaceID, userID, content string,
	attachments []CommentAttachmentInput,
	sourceChannelID string,
) (*model.MatterComment, error) {
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

	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.access.CanAccessMatter(matter, userID, sourceChannelID) {
		return nil, apperr.Forbidden("not authorized to access this matter")
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}
	c := &model.MatterComment{
		MatterID: matterID,
		UserID:   userID,
		Content:  contentPtr,
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
	matterID, spaceID, userID string,
	sourceChannelID string,
	cursor *string,
	limit int,
) ([]*model.MatterComment, bool, error) {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return nil, false, err
	}
	if !s.access.CanAccessMatter(matter, userID, sourceChannelID) {
		return nil, false, apperr.Forbidden("not authorized to access this matter")
	}
	comments, hasMore, err := s.commentRepo.ListByMatter(matterID, cursor, limit)
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
	matter, err := s.matterRepo.GetByID(c.MatterID, spaceID)
	if err != nil {
		return err
	}
	if !s.access.CanAccessMatter(matter, userID, sourceChannelID) {
		return apperr.Forbidden("not authorized to access this matter")
	}
	if c.UserID != userID {
		return apperr.ErrForbidden
	}
	return s.tx.Do(func(cs CommentStore, as CommentAttachmentStore) error {
		if err := as.DeleteByCommentID(id); err != nil {
			return err
		}
		return cs.Delete(id)
	})
}
