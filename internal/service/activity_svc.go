// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// activityReader is the narrow ActivityRepo surface ActivityService depends on
// for read access.
type activityReader interface {
	ListByMatter(ctx context.Context, matterID string, cursor *string, limit int) ([]*model.MatterActivity, bool, error)
}

// ActivityService lists matter activities with space + visibility checks.
type ActivityService struct {
	matterRepo  matterScopeChecker
	access      MatterAccessChecker
	activityRepo activityReader
}

// NewActivityService wires the activity service.
func NewActivityService(
	matterRepo matterScopeChecker,
	access MatterAccessChecker,
	activityRepo activityReader,
) *ActivityService {
	return &ActivityService{
		matterRepo:   matterRepo,
		access:       access,
		activityRepo: activityRepo,
	}
}

// ListActivities returns paginated activities for a matter.
//
// Access policy DIFFERS from TimelineService.ListEntries by design:
// activities are a matter-global audit log (assignee changes, status/title/
// description history, channel link/unlink across all linked channels) and
// the repo has no per-channel scope. Allowing channel-member-only callers
// here would leak cross-channel activity, so visibility is restricted to
// creator / assignees / participants — CanAccessMatter is called with empty
// channelID + callerToken to short-circuit the channel-membership branch.
// See PR #39 review r4259484583.
func (s *ActivityService) ListActivities(
	ctx context.Context,
	matterID, spaceID string,
	callerUIDs []string,
	cursor *string,
	limit int,
) ([]*model.MatterActivity, bool, error) {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, false, err
	}
	ok, accessErr := s.access.CanAccessMatter(ctx, matter, callerUIDs, "", "")
	if accessErr != nil {
		return nil, false, accessErr
	}
	if !ok {
		return nil, false, apperr.Forbidden("not authorized to access this matter")
	}
	return s.activityRepo.ListByMatter(ctx, matterID, cursor, limit)
}
