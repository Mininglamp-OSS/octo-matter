package model

import (
	"sort"
	"testing"
)

func sortStatuses(s []TodoStatus) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func TestAllowedTransitions(t *testing.T) {
	tests := []struct {
		status   TodoStatus
		expected []TodoStatus
	}{
		{TodoStatusDraft, []TodoStatus{TodoStatusPlanned, TodoStatusCancelled}},
		{TodoStatusPlanned, []TodoStatus{TodoStatusInProgress, TodoStatusCancelled}},
		{TodoStatusInProgress, []TodoStatus{TodoStatusDone, TodoStatusCancelled}},
		{TodoStatusCancelled, []TodoStatus{TodoStatusDraft}},
		{TodoStatusDone, nil},
	}

	for _, tt := range tests {
		got := AllowedTransitions(tt.status)
		sortStatuses(got)
		expected := make([]TodoStatus, len(tt.expected))
		copy(expected, tt.expected)
		sortStatuses(expected)

		if len(got) != len(expected) {
			t.Errorf("AllowedTransitions(%q): got %v, want %v", tt.status, got, expected)
			continue
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Errorf("AllowedTransitions(%q): got %v, want %v", tt.status, got, expected)
				break
			}
		}
	}
}

func TestCanTransition_ValidAsCreator(t *testing.T) {
	valid := []struct{ from, to TodoStatus }{
		{TodoStatusDraft, TodoStatusPlanned},
		{TodoStatusDraft, TodoStatusCancelled},
		{TodoStatusPlanned, TodoStatusInProgress},
		{TodoStatusPlanned, TodoStatusCancelled},
		{TodoStatusInProgress, TodoStatusDone},
		{TodoStatusInProgress, TodoStatusCancelled},
		{TodoStatusCancelled, TodoStatusDraft}, // reactivate
	}
	for _, tt := range valid {
		if !CanTransition(tt.from, tt.to, true, false) {
			t.Errorf("CanTransition(%q->%q, creator) = false, want true", tt.from, tt.to)
		}
	}
}

func TestCanTransition_AssigneePermissions(t *testing.T) {
	// Assignee CAN: start (planned->in_progress), complete (in_progress->done)
	if !CanTransition(TodoStatusPlanned, TodoStatusInProgress, false, true) {
		t.Error("Assignee should be able to start (planned->in_progress)")
	}
	if !CanTransition(TodoStatusInProgress, TodoStatusDone, false, true) {
		t.Error("Assignee should be able to complete (in_progress->done)")
	}

	// Assignee CANNOT: plan, cancel, reactivate
	if CanTransition(TodoStatusDraft, TodoStatusPlanned, false, true) {
		t.Error("Assignee should NOT be able to plan (draft->planned)")
	}
	if CanTransition(TodoStatusPlanned, TodoStatusCancelled, false, true) {
		t.Error("Assignee should NOT be able to cancel (planned->cancelled)")
	}
	if CanTransition(TodoStatusInProgress, TodoStatusCancelled, false, true) {
		t.Error("Assignee should NOT be able to cancel (in_progress->cancelled)")
	}
	if CanTransition(TodoStatusCancelled, TodoStatusDraft, false, true) {
		t.Error("Assignee should NOT be able to reactivate (cancelled->draft)")
	}
}

func TestCanTransition_InvalidTransitions(t *testing.T) {
	invalid := []struct{ from, to TodoStatus }{
		{TodoStatusDraft, TodoStatusDone},
		{TodoStatusDraft, TodoStatusInProgress},
		{TodoStatusPlanned, TodoStatusDone},
		{TodoStatusPlanned, TodoStatusDraft},
		{TodoStatusInProgress, TodoStatusDraft},
		{TodoStatusInProgress, TodoStatusPlanned},
		{TodoStatusDone, TodoStatusDraft},
		{TodoStatusDone, TodoStatusPlanned},
		{TodoStatusDone, TodoStatusCancelled},
		{TodoStatusCancelled, TodoStatusPlanned},
		{TodoStatusCancelled, TodoStatusInProgress},
		{TodoStatusCancelled, TodoStatusDone},
	}
	for _, tt := range invalid {
		if CanTransition(tt.from, tt.to, true, true) {
			t.Errorf("CanTransition(%q->%q) should be false even for creator+assignee", tt.from, tt.to)
		}
	}
}

func TestCanTransition_NeitherCreatorNorAssignee(t *testing.T) {
	// Valid transition but user has no role → should fail
	if CanTransition(TodoStatusDraft, TodoStatusPlanned, false, false) {
		t.Error("User with no role should not be able to transition")
	}
}

func TestCanTransition_UnknownStatus(t *testing.T) {
	if CanTransition("unknown", TodoStatusDraft, true, true) {
		t.Error("Transition from unknown status should fail")
	}
	if CanTransition(TodoStatusDraft, "unknown", true, true) {
		t.Error("Transition to unknown status should fail")
	}
}
