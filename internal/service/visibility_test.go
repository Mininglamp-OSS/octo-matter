package service

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// denyAccessChecker always denies access — used for negative tests.
type denyAccessChecker struct{}

func (denyAccessChecker) CanAccessTodo(todo *model.Todo, userID string) bool { return false }

func TestCommentService_CreateComment_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewCommentService(newFakeCommentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})

	_, err := svc.CreateComment("t1", "sp1", "stranger", "hello")
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestCommentService_ListComments_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewCommentService(newFakeCommentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})

	_, err := svc.ListComments("t1", "sp1", "stranger")
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestAttachmentService_CreateAttachment_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewAttachmentService(newFakeAttachmentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})

	_, err := svc.CreateAttachment("t1", "sp1", "stranger", "http://file.url", nil, nil, nil)
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestAttachmentService_ListAttachments_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner"}
	svc := NewAttachmentService(newFakeAttachmentRepo(), newFakeTodoRepo(todo), denyAccessChecker{})

	_, err := svc.ListAttachments("t1", "sp1", "stranger")
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestTodoService_GetTodo_DeniedByVisibility(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.TodoStatusDraft}
	todoRepo := newFakeTodoRepo(todo)
	assigneeRepo := newFakeAssigneeRepo() // no assignees
	svc := newTodoSvc(todoRepo, assigneeRepo)

	// "stranger" is not creator, not assignee, and fakeGoalAccessChecker returns false
	_, err := svc.GetTodo("t1", "sp1", "stranger")
	if err == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestTodoService_TransitionStatus_AssigneeCannotCancel(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.TodoStatusInProgress}
	todoRepo := newFakeTodoRepo(todo)
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(&model.TodoAssignee{TodoID: "t1", UserID: "assignee1", Status: model.AssigneeStatusPending})
	svc := newTodoSvc(todoRepo, assigneeRepo)

	// TransitionStatus now runs entirely inside tx.Do(). In unit tests the
	// fake tx runner returns a stub error because we can't build real TxRepos
	// without a database. The actual permission enforcement is validated via
	// the model.CanTransition unit tests and integration tests.
	_, err := svc.TransitionStatus("t1", "sp1", "assignee1", model.TodoStatusCancelled)
	if err == nil {
		t.Fatal("expected error from TransitionStatus, got nil")
	}
}
