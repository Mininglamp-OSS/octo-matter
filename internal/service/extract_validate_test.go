package service

import (
	"strings"
	"testing"
	"time"
)

// TestValidateExtractArgs covers the v3 LLM-output validation pipeline:
// LLM may freely emit deadline / source_msg_ids / assignee_uids, and the
// server must (a) clip the message-id and uid lists to the input set so the
// model cannot fabricate references, (b) coerce a missing-or-empty result
// into safe defaults, and (c) parse the deadline timestamp into a *time.Time.
func TestValidateExtractArgs(t *testing.T) {
	in := ExtractInput{
		CreatorUID: "creator",
		Messages: []ExtractMessage{
			{MessageID: "m1", FromUID: "u1"},
			{MessageID: "m2", FromUID: "u2"},
			{MessageID: "m3", FromUID: "u1"}, // duplicate from_uid
		},
	}
	t1234 := int64(1234567890)
	t1234Time := time.Unix(t1234, 0).UTC()

	cases := []struct {
		name           string
		args           extractToolArgs
		wantSourceMsgs []string
		wantAssignees  []string
		wantDeadline   *time.Time
	}{
		{
			name: "all valid",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1", "m3"},
				AssigneeUIDs: []string{"u1", "u2"},
				Deadline:     &t1234,
			},
			wantSourceMsgs: []string{"m1", "m3"},
			wantAssignees:  []string{"u1", "u2"},
			wantDeadline:   &t1234Time,
		},
		{
			name: "fabricated source ids dropped",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1", "fake-id", "m99"},
				AssigneeUIDs: []string{"u1"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
		},
		{
			name: "all source ids invalid falls back to all input",
			args: extractToolArgs{
				SourceMsgIDs: []string{"fake-1", "fake-2"},
				AssigneeUIDs: []string{"u1"},
			},
			wantSourceMsgs: []string{"m1", "m2", "m3"},
			wantAssignees:  []string{"u1"},
		},
		{
			name: "fabricated assignees dropped",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1", "outsider", "u2"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1", "u2"},
		},
		{
			name: "all assignees invalid falls back to creator",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"outsider1", "outsider2"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"creator"},
		},
		{
			name: "creator allowed as assignee",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"creator", "u1"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"creator", "u1"},
		},
		{
			name: "duplicate source ids deduped, order preserved",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m2", "m1", "m2", "m1"},
				AssigneeUIDs: []string{"u1"},
			},
			wantSourceMsgs: []string{"m2", "m1"},
			wantAssignees:  []string{"u1"},
		},
		{
			name: "duplicate assignees deduped, order preserved",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u2", "u1", "u2"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u2", "u1"},
		},
		{
			name: "empty/whitespace assignees skipped",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"", "   ", "u1"},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
		},
		{
			name: "deadline nil",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     nil,
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline zero treated as nil",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     int64Ptr(0),
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline negative treated as nil",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     int64Ptr(-1),
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "empty assignee + empty source_msgs",
			args: extractToolArgs{
				SourceMsgIDs: nil,
				AssigneeUIDs: nil,
			},
			wantSourceMsgs: []string{"m1", "m2", "m3"},
			wantAssignees:  []string{"creator"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateExtractArgs(tc.args, in)
			if !equalStringSlices(got.SourceMsgs, tc.wantSourceMsgs) {
				t.Errorf("SourceMsgs: got %v, want %v", got.SourceMsgs, tc.wantSourceMsgs)
			}
			if !equalStringSlices(got.Assignees, tc.wantAssignees) {
				t.Errorf("Assignees: got %v, want %v", got.Assignees, tc.wantAssignees)
			}
			if !equalTimePtr(got.Deadline, tc.wantDeadline) {
				t.Errorf("Deadline: got %v, want %v", got.Deadline, tc.wantDeadline)
			}
		})
	}
}

func int64Ptr(v int64) *int64 { return &v }

// TestValidateExtractArgs_LengthCaps covers PR #34 review r4259102520:
// LLM may emit title/description longer than DB column / handler binding
// allows, which would surface as a 500 at INSERT time. Service must clip
// to safe limits (or drop, depending on field) before persistence.
func TestValidateExtractArgs_LengthCaps(t *testing.T) {
	in := ExtractInput{
		CreatorUID: "creator",
		Messages:   []ExtractMessage{{MessageID: "m1", FromUID: "u1"}},
	}

	t.Run("title exceeding cap is clipped to MaxLLMTitleLen runes", func(t *testing.T) {
		over := strings.Repeat("中", MaxLLMTitleLen+50)
		v := validateExtractArgs(extractToolArgs{
			Title:        over,
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
		}, in)
		got := []rune(v.Title)
		if len(got) != MaxLLMTitleLen {
			t.Errorf("title not clipped: got %d runes, want %d", len(got), MaxLLMTitleLen)
		}
	})

	t.Run("title within cap untouched", func(t *testing.T) {
		short := strings.Repeat("a", 100)
		v := validateExtractArgs(extractToolArgs{
			Title:        short,
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
		}, in)
		if v.Title != short {
			t.Errorf("short title was modified: %q vs %q", v.Title, short)
		}
	})

	t.Run("description exceeding cap is clipped to MaxLLMDescriptionLen runes", func(t *testing.T) {
		over := strings.Repeat("文", MaxLLMDescriptionLen+50)
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  over,
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
		}, in)
		got := []rune(v.Description)
		if len(got) != MaxLLMDescriptionLen {
			t.Errorf("description not clipped: got %d runes, want %d", len(got), MaxLLMDescriptionLen)
		}
	})

	t.Run("deadline absurdly far in future is dropped", func(t *testing.T) {
		// year 9999 unix ≈ 253402300799; well past maxReasonableDeadlineUnix.
		over := int64(253_402_300_799)
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
			Deadline:     &over,
		}, in)
		if v.Deadline != nil {
			t.Errorf("absurdly far deadline must be dropped, got %v", v.Deadline)
		}
	})

	t.Run("deadline within reasonable range kept", func(t *testing.T) {
		ts := int64(1747332000) // 2025-05-15 reasonable
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
			Deadline:     &ts,
		}, in)
		if v.Deadline == nil {
			t.Errorf("reasonable deadline must be kept")
		}
	})
}


func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}
