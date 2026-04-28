package service

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

type denyAccessChecker struct{}

func (denyAccessChecker) CanAccessTodo(todo *model.Todo, userID string) bool { return false }

func TestCommentService_CreateComment_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewCommentService(newFakeCommentRepo(), newFakeTodoRepo(todo), denyAccessChecker{}, nil)
	_, err := svc.CreateComment("t1", "sp1", "stranger", "hello")
	if err == nil || !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestAttachmentService_CreateAttachment_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewAttachmentService(newFakeAttachmentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})
	_, err := svc.CreateAttachment("t1", "sp1", "stranger", "http://file.url", nil, nil, nil)
	if err == nil || !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestTodoService_GetTodo_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.GetTodo("t1", "sp1", "stranger")
	if err == nil || !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}
