package service

import (
	"context"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// Bounds on a single comment's attachments.
const (
	MaxAttachmentsPerComment = 10
	MaxAttachmentSizeBytes   = int64(100) << 20 // 100 MB
	MaxContentLength         = 10000
)

// CommentStore is the narrow CommentRepo surface CommentService depends on.
type CommentStore interface {
	Create(ctx context.Context, c *model.MatterComment) error
	GetByID(ctx context.Context, id string) (*model.MatterComment, error)
	Delete(ctx context.Context, id string) error
	ListByMatter(ctx context.Context, matterID string, cursor *string, limit int) ([]*model.MatterComment, bool, error)
}

// CommentAttachmentStore is the narrow CommentAttachmentRepo surface.
type CommentAttachmentStore interface {
	CreateMany(ctx context.Context, atts []*model.CommentAttachment) error
	ListByCommentIDs(ctx context.Context, ids []string) (map[string][]model.CommentAttachment, error)
	DeleteByCommentID(ctx context.Context, commentID string) error
}

// ParticipantUpserter is the single-method interface for upserting a participant
// within a transaction.
type ParticipantUpserter interface {
	Upsert(ctx context.Context, matterID, userID string) error
}

// commentTxRunner runs a closure that mutates a comment, its attachments,
// and participant records inside a single transaction.
type commentTxRunner interface {
	Do(ctx context.Context, fn func(CommentStore, CommentAttachmentStore, ParticipantUpserter) error) error
}

// matterScopeChecker resolves a matter within a space to reject cross-space access.
type matterScopeChecker interface {
	GetByID(ctx context.Context, id, spaceID string) (*model.Matter, error)
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

// CreateComment posts a comment authored by actorUID. Access is checked
// against the full callerUIDs set (so bots can post on behalf of their owner).
// On success, actorUID is upserted as a participant (GAP #8).
// NOTE: actorUID is not required to be in callerUIDs — this is intentional for
// delegation scenarios where a bot (in callerUIDs) posts on behalf of its owner
// (actorUID). The authorship is always attributed to actorUID.
func (s *CommentService) CreateComment(
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	actorUID, content string,
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

	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, sourceChannelID)
	if accessErr != nil {
		return nil, accessErr
	}
	if !ok {
		return nil, apperr.Forbidden("not authorized to access this matter")
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}
	c := &model.MatterComment{
		MatterID: matterID,
		UserID:   actorUID,
		Content:  contentPtr,
	}

	err = s.tx.Do(ctx, func(cs CommentStore, as CommentAttachmentStore, ps ParticipantUpserter) error {
		if err := cs.Create(ctx, c); err != nil {
			return err
		}
		if len(attachments) > 0 {
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
			if err := as.CreateMany(ctx, atts); err != nil {
				return err
			}
			c.Attachments = make([]model.CommentAttachment, 0, len(atts))
			for _, a := range atts {
				c.Attachments = append(c.Attachments, *a)
			}
		}
		// GAP #8: participant upsert inside tx for atomicity
		return ps.Upsert(ctx, matterID, actorUID)
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
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	sourceChannelID string,
	cursor *string,
	limit int,
) ([]*model.MatterComment, bool, error) {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, false, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, sourceChannelID)
	if accessErr != nil {
		return nil, false, accessErr
	}
	if !ok {
		return nil, false, apperr.Forbidden("not authorized to access this matter")
	}
	comments, hasMore, err := s.commentRepo.ListByMatter(ctx, matterID, cursor, limit)
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
	byComment, err := s.attachmentRepo.ListByCommentIDs(ctx, ids)
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

func (s *CommentService) DeleteComment(ctx context.Context, id, spaceID string, callerUIDs []string, actorUID, sourceChannelID string) error {
	c, err := s.commentRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	matter, err := s.matterRepo.GetByID(ctx, c.MatterID, spaceID)
	if err != nil {
		return err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, sourceChannelID)
	if accessErr != nil {
		return accessErr
	}
	if !ok {
		return apperr.Forbidden("not authorized to access this matter")
	}
	if c.UserID != actorUID {
		return apperr.ErrForbidden
	}
	return s.tx.Do(ctx, func(cs CommentStore, as CommentAttachmentStore, _ ParticipantUpserter) error {
		if err := as.DeleteByCommentID(ctx, id); err != nil {
			return err
		}
		return cs.Delete(ctx, id)
	})
}
