package service

// v3.3.1 §B.2 RecordAgentActivity service-layer invariant tests + v3.3.4
// §B3 lml2468 R7 P0 split-drop tests.
//
// Validates that:
//   - drop #1 (caller-bug invariant violation: spaceID="" or matterRepo=nil)
//     is fail-loud + drop + RETURN NIL (not error). Returning an error
//     would let daemon retry forever on a caller bug retry can't fix.
//   - drop #2 (matterRepo.GetByID returns ErrNotFound = cross-space OR
//     matter-deleted race) stays silent-drop + WARN log + RETURN NIL.
//   - drop #2 (other err from GetByID = transient DB) PROPAGATES UP wrapped
//     so handler returns 500 and daemon §H3 5xx→503 retries.

import (
	"context"
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestRecordAgentActivity_EmptySpaceID_DropsAndDoesNotRecord(t *testing.T) {
	// MatterService with a non-nil matterRepo + activity store, but the
	// caller passes spaceID="". The service-layer invariant must fail-loud
	// (ERROR log, not exercised here but exercised in production) AND
	// drop the call (no activity recorded). v3.3.4 §B3: returns nil so
	// daemon doesn't retry on a permanently buggy caller.
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	err := svc.RecordAgentActivity(context.Background(),
		"some-matter-id", "", // spaceID empty — invariant violation
		"actor-uid", "agent_dispatched", nil)

	if err != nil {
		t.Errorf("invariant violation must return nil (v3.3.4 §B3: caller-bug, retry-won't-help), got %v", err)
	}
	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded when spaceID is empty (v3.3.1 §B.2 fail-closed), got %d activities", got)
	}
}

func TestRecordAgentActivity_NilMatterRepo_DropsAndDoesNotRecord(t *testing.T) {
	// MatterService constructed with matterRepo=nil + non-nil spaceID.
	// matterRepo=nil indicates the service isn't set up to verify
	// matter↔space ownership — must fail-closed not silently record.
	// v3.3.4 §B3: returns nil so daemon doesn't retry on a misconfigured
	// service.
	activity := &fakeActivityRepo{}
	svc := NewMatterService(nil, newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	err := svc.RecordAgentActivity(context.Background(),
		"some-matter-id", "sp_a", // spaceID set, but matterRepo nil
		"actor-uid", "agent_dispatched", nil)

	if err != nil {
		t.Errorf("invariant violation must return nil (v3.3.4 §B3), got %v", err)
	}
	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded when matterRepo is nil (v3.3.1 §B.2 fail-closed), got %d activities", got)
	}
}

func TestRecordAgentActivity_CrossSpaceMatter_DropsQuietly(t *testing.T) {
	// Cross-space (matter belongs to a different space than spaceID):
	// the v3 §3.2 fail-quiet WARN path. Activity is NOT recorded, but
	// no ERROR is logged. v3.3.4 §B3: ErrNotFound stays silent drop +
	// RETURN NIL (by-design cross-space OR matter-deleted race — same
	// surface, see §B3 A4 acknowledge).
	matter := &model.Matter{
		ID:      "matter-in-sp-b",
		SpaceID: "sp_b",
		Title:   "test",
		Status:  model.MatterStatusOpen,
	}
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(matter), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	err := svc.RecordAgentActivity(context.Background(),
		matter.ID, "sp_a", // matter is in sp_b, caller's space is sp_a
		"actor-uid", "agent_dispatched", nil)

	if err != nil {
		t.Errorf("cross-space ErrNotFound must return nil (by-design drop, v3.3.4 §B3 A4), got %v", err)
	}
	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded for cross-space (v3 §3.2), got %d activities", got)
	}
}

// v3.3.4 §B3 lml2468 R7 P0 new case: transient DB error from GetByID
// (conn drop / deadlock / timeout / etc.) must PROPAGATE UP wrapped so
// the handler returns 500 and daemon §H3 maps to 503 + retries.
//
// This is the entire point of changing sig from void → error: without
// propagation, the handler silently 204s and daemon never retries the
// activity writeback, breaking the §H3 retry contract.
func TestRecordAgentActivity_TransientDBError_PropagatesUp(t *testing.T) {
	connDrop := errors.New("driver: bad connection")
	repo := &errReturningMatterRepo{
		fakeMatterRepo: *newFakeMatterRepo(),
		err:            connDrop,
	}
	activity := &fakeActivityRepo{}
	svc := NewMatterService(repo, newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	err := svc.RecordAgentActivity(context.Background(),
		"some-matter-id", "sp_a",
		"actor-uid", "agent_dispatched", nil)

	if err == nil {
		t.Fatalf("transient DB err must propagate up (v3.3.4 §B3), got nil")
	}
	// The error must wrap the underlying cause so callers can errors.Is.
	if !errors.Is(err, connDrop) {
		t.Errorf("propagated err must wrap underlying cause, got %v (no Is match for connDrop)", err)
	}
	// And it must NOT be ErrNotFound (that's the silent-drop path).
	if errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("propagated err must not match ErrNotFound (that's the silent-drop path), got %v", err)
	}
	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded when GetByID errs, got %d activities", got)
	}
}

// errReturningMatterRepo wraps fakeMatterRepo and forces GetByID to
// return a sentinel transient error instead of consulting the map. Used
// to exercise the v3.3.4 §B3 propagate-up branch without depending on a
// real DB.
type errReturningMatterRepo struct {
	fakeMatterRepo
	err error
}

func (r *errReturningMatterRepo) GetByID(_ context.Context, _id, _spaceID string) (*model.Matter, error) {
	return nil, r.err
}

func TestRecordAgentActivity_ValidArgs_DoesRecord(t *testing.T) {
	// Positive control: when all invariants hold, activity IS recorded
	// and the function returns nil.
	matter := &model.Matter{
		ID:      "matter-in-sp-a",
		SpaceID: "sp_a",
		Title:   "test",
		Status:  model.MatterStatusOpen,
	}
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(matter), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	err := svc.RecordAgentActivity(context.Background(),
		matter.ID, "sp_a", // matter and caller in same space
		"actor-uid", "agent_dispatched", nil)

	if err != nil {
		t.Errorf("happy path must return nil, got %v", err)
	}
	if got := len(activity.activities); got != 1 {
		t.Errorf("activity should be recorded once for valid args, got %d", got)
	}
}
