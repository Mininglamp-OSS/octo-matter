package service

// v3.3.1 §B.2 RecordAgentActivity service-layer invariant tests.
//
// Validates that the missing-argument path (spaceID="" or matterRepo=nil)
// is fail-loud + drop, not fail-quiet + record. This is the position
// reversal from v3 — see commit "v3.3.1 §B.2" + PR comment Heads-up #4
// for the reasoning. The cross-space path (spaceID set, matter belongs
// to a different space) stays fail-quiet WARN, matching v3 §3.2.

import (
	"context"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestRecordAgentActivity_EmptySpaceID_DropsAndDoesNotRecord(t *testing.T) {
	// MatterService with a non-nil matterRepo + activity store, but the
	// caller passes spaceID="". The service-layer invariant must fail-loud
	// (ERROR log, not exercised here but exercised in production) AND
	// drop the call (no activity recorded).
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	svc.RecordAgentActivity(context.Background(),
		"some-matter-id", "", // spaceID empty — invariant violation
		"actor-uid", "agent_dispatched", nil)

	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded when spaceID is empty (v3.3.1 §B.2 fail-closed), got %d activities", got)
	}
}

func TestRecordAgentActivity_NilMatterRepo_DropsAndDoesNotRecord(t *testing.T) {
	// MatterService constructed with matterRepo=nil + non-nil spaceID.
	// matterRepo=nil indicates the service isn't set up to verify
	// matter↔space ownership — must fail-closed not silently record.
	activity := &fakeActivityRepo{}
	svc := NewMatterService(nil, newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	svc.RecordAgentActivity(context.Background(),
		"some-matter-id", "sp_a", // spaceID set, but matterRepo nil
		"actor-uid", "agent_dispatched", nil)

	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded when matterRepo is nil (v3.3.1 §B.2 fail-closed), got %d activities", got)
	}
}

func TestRecordAgentActivity_CrossSpaceMatter_DropsQuietly(t *testing.T) {
	// Cross-space (matter belongs to a different space than spaceID):
	// the v3 §3.2 fail-quiet WARN path. Activity is NOT recorded, but
	// no ERROR is logged (it's a real failure mode worth alerting on
	// but at WARN severity per v3 commit message).
	matter := &model.Matter{
		ID:      "matter-in-sp-b",
		SpaceID: "sp_b",
		Title:   "test",
		Status:  model.MatterStatusOpen,
	}
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(matter), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	svc.RecordAgentActivity(context.Background(),
		matter.ID, "sp_a", // matter is in sp_b, caller's space is sp_a
		"actor-uid", "agent_dispatched", nil)

	if got := len(activity.activities); got != 0 {
		t.Errorf("activity must NOT be recorded for cross-space (v3 §3.2), got %d activities", got)
	}
}

func TestRecordAgentActivity_ValidArgs_DoesRecord(t *testing.T) {
	// Positive control: when all invariants hold, activity IS recorded.
	matter := &model.Matter{
		ID:      "matter-in-sp-a",
		SpaceID: "sp_a",
		Title:   "test",
		Status:  model.MatterStatusOpen,
	}
	activity := &fakeActivityRepo{}
	svc := NewMatterService(newFakeMatterRepo(matter), newFakeAssigneeRepo(),
		fakeParticipantRepo{}, fakeChannelRepo{}, activity, noopTxRunner{}, nil)

	svc.RecordAgentActivity(context.Background(),
		matter.ID, "sp_a", // matter and caller in same space
		"actor-uid", "agent_dispatched", nil)

	if got := len(activity.activities); got != 1 {
		t.Errorf("activity should be recorded once for valid args, got %d", got)
	}
}
