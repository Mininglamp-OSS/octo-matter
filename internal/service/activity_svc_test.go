// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// --- fake activity repo ---------------------------------------------------

type fakeActivityRepo struct {
	activities []*model.MatterActivity
	err        error
	recordErr  error
}

func (f *fakeActivityRepo) Record(_ context.Context, matterID, actorID, action string, detail interface{}) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	var raw model.JSONDetail
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		raw = model.JSONDetail(b)
	}
	f.activities = append(f.activities, &model.MatterActivity{
		ID:        "act-" + action,
		MatterID:  matterID,
		ActorID:   actorID,
		Action:    action,
		Detail:    raw,
		CreatedAt: time.Now(),
	})
	return nil
}

func (f *fakeActivityRepo) ListByMatter(_ context.Context, matterID string, cursor *string, limit int) ([]*model.MatterActivity, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	var out []*model.MatterActivity
	for _, a := range f.activities {
		if a.MatterID == matterID {
			out = append(out, a)
		}
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// Verify fakeActivityRepo satisfies both the write and read interfaces.
var _ activityStore = (*fakeActivityRepo)(nil)
var _ activityReader = (*fakeActivityRepo)(nil)

// errorAccessChecker returns a hard error on CanAccessMatter (simulates IM
// or DB failure in the access path).
type errorAccessChecker struct{ err error }

func (e errorAccessChecker) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, _, _ string) (bool, error) {
	return false, e.err
}

// recordingAccessChecker captures the channelID + callerToken args so we can
// assert ListActivities never enables the channel-membership branch (PR #39
// review r4259484583 — would leak cross-channel audit events otherwise).
type recordingAccessChecker struct {
	gotChannelID string
	gotToken     string
	allow        bool
}

func (r *recordingAccessChecker) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, channelID, token string) (bool, error) {
	r.gotChannelID = channelID
	r.gotToken = token
	return r.allow, nil
}

// --- helper to build an ActivityService for tests -------------------------

func newActivitySvc(matter *model.Matter, actRepo *fakeActivityRepo) *ActivityService {
	return NewActivityService(
		newFakeMatterRepo(matter),
		fakeAccessChecker{},
		actRepo,
	)
}

// --- Tests ----------------------------------------------------------------

func TestActivityService_ListActivities_HappyPath(t *testing.T) {
	matter := &model.Matter{ID: "m1", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	detail := map[string]interface{}{"from": "open", "to": "closed"}
	_ = actRepo.Record(context.Background(), "m1", "u1", "status_changed", detail)

	svc := newActivitySvc(matter, actRepo)
	items, hasMore, err := svc.ListActivities(context.Background(), "m1", "sp1", []string{"u1"}, nil, 50)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if hasMore {
		t.Errorf("expected hasMore=false, got true")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(items))
	}
	if items[0].Action != "status_changed" {
		t.Errorf("expected action=status_changed, got %q", items[0].Action)
	}
	if items[0].Detail == nil {
		t.Errorf("expected non-nil detail")
	}
}

func TestActivityService_ListActivities_EmptyList(t *testing.T) {
	matter := &model.Matter{ID: "m2", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}

	svc := newActivitySvc(matter, actRepo)
	items, hasMore, err := svc.ListActivities(context.Background(), "m2", "sp1", []string{"u1"}, nil, 50)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if hasMore {
		t.Errorf("expected hasMore=false")
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestActivityService_ListActivities_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "m3", SpaceID: "sp-A", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}

	svc := newActivitySvc(matter, actRepo)
	_, _, err := svc.ListActivities(context.Background(), "m3", "sp-B", []string{"u1"}, nil, 50)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space: expected ErrNotFound, got %v", err)
	}
}

func TestActivityService_ListActivities_UnauthorizedCallerDenied(t *testing.T) {
	matter := &model.Matter{ID: "m4", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	// Use a deny-all access checker via a custom struct.
	svc := NewActivityService(newFakeMatterRepo(matter), denyAccessChecker{}, actRepo)

	_, _, err := svc.ListActivities(context.Background(), "m4", "sp1", []string{"stranger"}, nil, 50)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("unauthorized: expected ErrForbidden, got %v", err)
	}
}

func TestActivityService_ListActivities_RepoError(t *testing.T) {
	matter := &model.Matter{ID: "m5", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{err: errors.New("db down")}

	svc := newActivitySvc(matter, actRepo)
	_, _, err := svc.ListActivities(context.Background(), "m5", "sp1", []string{"u1"}, nil, 50)
	if err == nil {
		t.Fatal("expected error from repo, got nil")
	}
}

func TestActivityService_ListActivities_HasMore(t *testing.T) {
	matter := &model.Matter{ID: "m6", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	// Insert 3 activities, request limit=2.
	for i := 0; i < 3; i++ {
		actRepo.activities = append(actRepo.activities, &model.MatterActivity{
			ID:        "a" + string(rune('0'+i)),
			MatterID:  "m6",
			ActorID:   "u1",
			Action:    "created",
			CreatedAt: time.Now(),
		})
	}

	svc := newActivitySvc(matter, actRepo)
	items, hasMore, err := svc.ListActivities(context.Background(), "m6", "sp1", []string{"u1"}, nil, 2)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if !hasMore {
		t.Errorf("expected hasMore=true when result exceeds limit")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestActivityService_ListActivities_AccessCheckerError_PropagatesUpstream(t *testing.T) {
	matter := &model.Matter{ID: "m8", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	errAccessChecker := errorAccessChecker{err: errors.New("IM down")}
	svc := NewActivityService(newFakeMatterRepo(matter), errAccessChecker, actRepo)

	_, _, err := svc.ListActivities(context.Background(), "m8", "sp1", []string{"u1"}, nil, 50)
	if err == nil {
		t.Fatal("expected error when access checker fails, got nil")
	}
}

func TestActivityService_ListActivities_NilDetailSerializesAsNull(t *testing.T) {
	matter := &model.Matter{ID: "m7", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	actRepo.activities = append(actRepo.activities, &model.MatterActivity{
		ID:        "a1",
		MatterID:  "m7",
		ActorID:   "u1",
		Action:    "created",
		Detail:    nil, // nil detail
		CreatedAt: time.Now(),
	})

	svc := newActivitySvc(matter, actRepo)
	items, _, err := svc.ListActivities(context.Background(), "m7", "sp1", []string{"u1"}, nil, 50)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Detail != nil {
		t.Errorf("expected nil detail, got %s", items[0].Detail)
	}
}

// TestActivityService_ListActivities_DoesNotEnableChannelMembership is the
// regression guard for PR #39 review r4259484583. ListActivities must invoke
// CanAccessMatter with empty channelID + callerToken so a caller that is
// merely a member of a linked channel (and not creator/assignee/participant)
// cannot read the matter-global audit log.
func TestActivityService_ListActivities_DoesNotEnableChannelMembership(t *testing.T) {
	matter := &model.Matter{ID: "m9", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	actRepo := &fakeActivityRepo{}
	access := &recordingAccessChecker{allow: true}
	svc := NewActivityService(newFakeMatterRepo(matter), access, actRepo)

	_, _, err := svc.ListActivities(context.Background(), "m9", "sp1", []string{"channel-only-user"}, nil, 50)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if access.gotChannelID != "" {
		t.Errorf("CanAccessMatter channelID: got %q, want empty", access.gotChannelID)
	}
	if access.gotToken != "" {
		t.Errorf("CanAccessMatter callerToken: got %q, want empty", access.gotToken)
	}
}
