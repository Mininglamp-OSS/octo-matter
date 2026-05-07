package service

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type denyAccessChecker struct{}

func (denyAccessChecker) CanAccessTodo(todo *model.Todo, userID string, sourceChannelID string) bool {
	return false
}

func TestCommentService_CreateComment_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", "stranger", "hello", nil, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestCommentService_CreateComment_WithAttachments_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", "stranger", "", []CommentAttachmentInput{
		{FileURL: "https://obj/x.png"},
	}, "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestTodoService_GetTodo_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.GetTodo("t1", "sp1", "stranger", "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}
