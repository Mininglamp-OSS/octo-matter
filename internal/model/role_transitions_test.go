package model

import (
	"testing"
)

func TestAllowedTransitionsForRole_CreatorGetsAll(t *testing.T) {
	got := AllowedTransitionsForRole(TodoStatusPlanned, true, false)
	// Creator can: start (in_progress) + cancel
	if len(got) != 2 {
		t.Fatalf("creator from planned: got %d transitions, want 2", len(got))
	}
}

func TestAllowedTransitionsForRole_AssigneeCanOnlyStartAndComplete(t *testing.T) {
	// From planned: assignee can start, but cannot cancel
	got := AllowedTransitionsForRole(TodoStatusPlanned, false, true)
	if len(got) != 1 {
		t.Fatalf("assignee from planned: got %d transitions, want 1 (start only)", len(got))
	}
	if got[0] != TodoStatusInProgress {
		t.Errorf("assignee from planned: got %v, want in_progress", got[0])
	}

	// From in_progress: assignee can complete, but cannot cancel
	got = AllowedTransitionsForRole(TodoStatusInProgress, false, true)
	if len(got) != 1 {
		t.Fatalf("assignee from in_progress: got %d transitions, want 1 (complete only)", len(got))
	}
	if got[0] != TodoStatusDone {
		t.Errorf("assignee from in_progress: got %v, want done", got[0])
	}
}

func TestAllowedTransitionsForRole_GoalMemberNeitherCreatorNorAssignee(t *testing.T) {
	// A user who can see the todo (via goal) but is neither creator nor assignee
	// should get zero allowed transitions
	got := AllowedTransitionsForRole(TodoStatusDraft, false, false)
	if len(got) != 0 {
		t.Errorf("non-creator/non-assignee from draft: got %v, want empty", got)
	}

	got = AllowedTransitionsForRole(TodoStatusPlanned, false, false)
	if len(got) != 0 {
		t.Errorf("non-creator/non-assignee from planned: got %v, want empty", got)
	}

	got = AllowedTransitionsForRole(TodoStatusInProgress, false, false)
	if len(got) != 0 {
		t.Errorf("non-creator/non-assignee from in_progress: got %v, want empty", got)
	}
}

func TestAllowedTransitionsForRole_DoneStatus(t *testing.T) {
	// Done has no transitions regardless of role
	got := AllowedTransitionsForRole(TodoStatusDone, true, true)
	if len(got) != 0 {
		t.Errorf("from done: got %v, want empty", got)
	}
}
