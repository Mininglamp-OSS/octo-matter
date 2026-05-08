package service

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type denyAccessChecker struct{}

func (denyAccessChecker) CanAccessMatter(matter *model.Matter, callerUIDs []string, sourceChannelID string) (bool, error) {
	return false, nil
}

func TestCommentService_CreateComment_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), denyAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", []string{"stranger"}, "stranger", "hello", nil, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestCommentService_CreateComment_WithAttachments_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), denyAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", []string{"stranger"}, "stranger", "", []CommentAttachmentInput{
		{FileURL: "https://obj/x.png"},
	}, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestMatterService_GetMatter_DeniedByVisibility(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.GetMatter("t1", "sp1", []string{"stranger"}, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}
