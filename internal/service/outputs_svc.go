package service

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// outputsReader is the narrow surface OutputsService needs from
// TimelineAttachmentRepo — kept here, where it is used, per the project's
// "accept interfaces" convention.
type outputsReader interface {
	ListOutputs(ctx context.Context, f repository.OutputsFilter) ([]*model.MatterOutput, bool, error)
}

// OutputsService lists deduplicated file outputs for a matter.
//
// Access policy mirrors ActivityService.ListActivities: outputs are a
// matter-global view aggregating files from every linked channel, so visibility
// is restricted to creator / assignees / participants. The channel-membership
// branch of CanAccessMatter is short-circuited (empty channelID + token) to
// avoid leaking outputs from channels the caller is not a member of.
type OutputsService struct {
	matterRepo matterScopeChecker
	access     MatterAccessChecker
	reader     outputsReader
}

func NewOutputsService(matterRepo matterScopeChecker, access MatterAccessChecker, reader outputsReader) *OutputsService {
	return &OutputsService{matterRepo: matterRepo, access: access, reader: reader}
}

// ListOutputs returns paginated, deduplicated attachments for a matter.
func (s *OutputsService) ListOutputs(
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	q string,
	cursor *string,
	limit int,
) ([]*model.MatterOutput, bool, error) {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, false, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, "", "")
	if accessErr != nil {
		return nil, false, accessErr
	}
	if !ok {
		return nil, false, apperr.Forbidden(i18n.KeyMatterAccess)
	}
	return s.reader.ListOutputs(ctx, repository.OutputsFilter{
		MatterID: matterID,
		Q:        q,
		Cursor:   cursor,
		Limit:    limit,
	})
}
