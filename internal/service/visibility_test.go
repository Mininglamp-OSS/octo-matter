// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type denyAccessChecker struct{}

func (denyAccessChecker) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, _, _ string) (bool, error) {
	return false, nil
}

func TestTimelineService_CreateEntry_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), denyAccessChecker{})
	_, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"stranger"}, "stranger", "hello", nil, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestTimelineService_CreateEntry_WithAttachments_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), denyAccessChecker{})
	_, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"stranger"}, "stranger", "", []TimelineAttachmentInput{
		{FileURL: "https://obj/x.png"},
	}, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestMatterService_GetMatter_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.GetMatter(context.Background(), "t1", "sp1", []string{"stranger"}, "", "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}
