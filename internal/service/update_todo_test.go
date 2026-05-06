package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestUpdateTodo_PersistsDeadlineAndRemindAt(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	deadline := "2026-06-01T12:00:00Z"
	remind := "2026-05-30T09:00:00Z"
	updated, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("x"), nil, nil, &deadline, &remind)
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}
	if updated.Deadline == nil || !updated.Deadline.Equal(mustParse(t, deadline)) {
		t.Errorf("deadline not persisted: got %v, want %s", updated.Deadline, deadline)
	}
	if updated.RemindAt == nil || !updated.RemindAt.Equal(mustParse(t, remind)) {
		t.Errorf("remind_at not persisted: got %v, want %s", updated.RemindAt, remind)
	}
}

func TestUpdateTodo_AssigneeCanUpdate(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "owner", Title: "x", Status: model.TodoStatusOpen}
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(&model.TodoAssignee{TodoID: "t1", UserID: "assignee-1"})
	svc := newTodoSvc(newFakeTodoRepo(todo), assigneeRepo)

	updated, err := svc.UpdateTodo("t1", "space-A", "assignee-1", strPtr("new title"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("assignee should be able to update todo: %v", err)
	}
	if updated.Title != "new title" {
		t.Errorf("title not updated: got %q, want %q", updated.Title, "new title")
	}
}

func TestUpdateTodo_NonCreatorNonAssigneeForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "owner", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	_, err := svc.UpdateTodo("t1", "space-A", "stranger", strPtr("hack"), nil, nil, nil, nil)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator non-assignee should get ErrForbidden, got %v", err)
	}
}

func TestUpdateTodo_EmptyStringClearsTimestamp(t *testing.T) {
	existing := mustParse(t, "2026-06-01T12:00:00Z")
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen, Deadline: &existing}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	empty := ""
	updated, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("x"), nil, nil, &empty, nil)
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}
	if updated.Deadline != nil {
		t.Errorf("empty deadline should clear the field, got %v", updated.Deadline)
	}
}

func TestUpdateTodo_NilPointerLeavesTimestampUntouched(t *testing.T) {
	existing := mustParse(t, "2026-06-01T12:00:00Z")
	desc := "keep me"
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen, Deadline: &existing, Description: &desc}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	updated, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("x"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}
	if updated.Deadline == nil || !updated.Deadline.Equal(existing) {
		t.Errorf("nil deadline should leave existing value, got %v", updated.Deadline)
	}
	// Verify description is also preserved when nil (regression: previously cleared unconditionally).
	if updated.Description == nil || *updated.Description != "keep me" {
		t.Errorf("nil description should leave existing value, got %v", updated.Description)
	}
}

func TestUpdateTodo_InvalidDeadlineReturnsInvalidInput(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	bad := "not-a-date"
	_, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("x"), nil, nil, &bad, nil)
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("bad deadline should return ErrInvalidInput, got %v", err)
	}
}

func TestUpdateTodo_NilGoalIDPreservesExisting(t *testing.T) {
	gid := "goal-123"
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen, GoalID: &gid}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	// nil goalID means "don't change it"
	updated, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("new title"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}
	if updated.GoalID == nil || *updated.GoalID != "goal-123" {
		t.Errorf("nil goalID should preserve existing, got %v", updated.GoalID)
	}
}

func TestUpdateTodo_EmptyGoalIDClearsGoal(t *testing.T) {
	gid := "goal-123"
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen, GoalID: &gid}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())

	// Empty string goalID means "clear the goal"
	emptyGoal := ""
	updated, err := svc.UpdateTodo("t1", "space-A", "u1", strPtr("x"), nil, &emptyGoal, nil, nil)
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}
	if updated.GoalID == nil || *updated.GoalID != "" {
		t.Errorf("empty goalID should clear goal, got %v", updated.GoalID)
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustParse(%q): %v", s, err)
	}
	return v
}

