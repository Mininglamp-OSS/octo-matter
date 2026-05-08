package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestUpdateMatter_PersistsDeadlineAndRemindAt(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())

	deadline := "2026-06-01T12:00:00Z"
	remind := "2026-05-30T09:00:00Z"
	updated, err := svc.UpdateMatter("t1", "space-A", "u1", strPtr("x"), nil, &deadline, &remind)
	if err != nil {
		t.Fatalf("UpdateMatter failed: %v", err)
	}
	if updated.Deadline == nil || !updated.Deadline.Equal(mustParse(t, deadline)) {
		t.Errorf("deadline not persisted: got %v, want %s", updated.Deadline, deadline)
	}
	if updated.RemindAt == nil || !updated.RemindAt.Equal(mustParse(t, remind)) {
		t.Errorf("remind_at not persisted: got %v, want %s", updated.RemindAt, remind)
	}
}

func TestUpdateMatter_AssigneeCanUpdate(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "owner", Title: "x", Status: model.MatterStatusOpen}
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(&model.MatterAssignee{MatterID: "t1", UserID: "assignee-1"})
	svc := newMatterSvc(newFakeMatterRepo(matter), assigneeRepo)

	updated, err := svc.UpdateMatter("t1", "space-A", "assignee-1", strPtr("new title"), nil, nil, nil)
	if err != nil {
		t.Fatalf("assignee should be able to update matter: %v", err)
	}
	if updated.Title != "new title" {
		t.Errorf("title not updated: got %q, want %q", updated.Title, "new title")
	}
}

func TestUpdateMatter_NonCreatorNonAssigneeForbidden(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "owner", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())

	_, err := svc.UpdateMatter("t1", "space-A", "stranger", strPtr("hack"), nil, nil, nil)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator non-assignee should get ErrForbidden, got %v", err)
	}
}

func TestUpdateMatter_EmptyStringClearsTimestamp(t *testing.T) {
	existing := mustParse(t, "2026-06-01T12:00:00Z")
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen, Deadline: &existing}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())

	empty := ""
	updated, err := svc.UpdateMatter("t1", "space-A", "u1", strPtr("x"), nil, &empty, nil)
	if err != nil {
		t.Fatalf("UpdateMatter failed: %v", err)
	}
	if updated.Deadline != nil {
		t.Errorf("empty deadline should clear the field, got %v", updated.Deadline)
	}
}

func TestUpdateMatter_NilPointerLeavesTimestampUntouched(t *testing.T) {
	existing := mustParse(t, "2026-06-01T12:00:00Z")
	desc := "keep me"
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen, Deadline: &existing, Description: &desc}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())

	updated, err := svc.UpdateMatter("t1", "space-A", "u1", strPtr("x"), nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateMatter failed: %v", err)
	}
	if updated.Deadline == nil || !updated.Deadline.Equal(existing) {
		t.Errorf("nil deadline should leave existing value, got %v", updated.Deadline)
	}
	if updated.Description == nil || *updated.Description != "keep me" {
		t.Errorf("nil description should leave existing value, got %v", updated.Description)
	}
}

func TestUpdateMatter_InvalidDeadlineReturnsInvalidInput(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())

	bad := "not-a-date"
	_, err := svc.UpdateMatter("t1", "space-A", "u1", strPtr("x"), nil, &bad, nil)
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("bad deadline should return ErrInvalidInput, got %v", err)
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
