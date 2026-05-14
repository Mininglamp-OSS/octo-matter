package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestValidateMessages_RejectsOversizedMessageID is the service-layer guard
// against unbounded message ids reaching matters.source_msg_ids (PR #43
// follow-up). The handler tag bounds the API surface; this guard ensures any
// internal caller that bypasses Gin binding is still rejected before the id
// hits the JSON column.
func TestValidateMessages_RejectsOversizedMessageID(t *testing.T) {
	long := strings.Repeat("x", ExtractMaxMessageIDLen+1)
	msgs := []ExtractMessage{
		{MessageID: "ok", FromUID: "u1"},
		{MessageID: long, FromUID: "u1"},
	}
	err := validateMessages(msgs)
	if err == nil {
		t.Fatalf("expected validation error for oversized message_id, got nil")
	}
	if !strings.Contains(err.Error(), "message_id") {
		t.Fatalf("expected error to mention message_id, got %v", err)
	}
}

func TestValidateMessages_AcceptsMaxLengthMessageID(t *testing.T) {
	maxLen := strings.Repeat("x", ExtractMaxMessageIDLen)
	msgs := []ExtractMessage{{MessageID: maxLen, FromUID: "u1"}}
	if err := validateMessages(msgs); err != nil {
		t.Fatalf("expected max-length id accepted, got %v", err)
	}
}

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
	// Deadline reference point: 2026-05-15 (well within ±5 years of 2026-05-14
	// "now" used below). UTC midnight is what parseLLMDeadline emits.
	dlTime := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

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
				Deadline:     flexDate("2026-05-15"),
			},
			wantSourceMsgs: []string{"m1", "m3"},
			wantAssignees:  []string{"u1", "u2"},
			wantDeadline:   &dlTime,
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
				Deadline:     flexibleDate{},
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline empty string treated as nil",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     flexDate(""),
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline whitespace treated as nil",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     flexDate("   "),
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline malformed (non-date) dropped",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     flexDate("next Friday"),
			},
			wantSourceMsgs: []string{"m1"},
			wantAssignees:  []string{"u1"},
			wantDeadline:   nil,
		},
		{
			name: "deadline as RFC3339 (model violated schema) dropped",
			args: extractToolArgs{
				SourceMsgIDs: []string{"m1"},
				AssigneeUIDs: []string{"u1"},
				Deadline:     flexDate("2026-05-15T18:00:00Z"),
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
			got := validateExtractArgs(tc.args, in, now)
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

// TestValidateExtractArgs_LengthCaps covers PR #34:
// LLM may emit title/description longer than DB column / handler binding
// allows, which would surface as a 500 at INSERT time. Service must clip
// to safe limits (or drop, depending on field) before persistence.
func TestValidateExtractArgs_LengthCaps(t *testing.T) {
	in := ExtractInput{
		CreatorUID: "creator",
		Messages:   []ExtractMessage{{MessageID: "m1", FromUID: "u1"}},
	}
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	t.Run("title exceeding cap is clipped to MaxLLMTitleLen runes", func(t *testing.T) {
		over := strings.Repeat("中", MaxLLMTitleLen+50)
		v := validateExtractArgs(extractToolArgs{
			Title:        over,
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
		}, in, now)
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
		}, in, now)
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
		}, in, now)
		got := []rune(v.Description)
		if len(got) != MaxLLMDescriptionLen {
			t.Errorf("description not clipped: got %d runes, want %d", len(got), MaxLLMDescriptionLen)
		}
	})

	t.Run("deadline year too far in future is dropped", func(t *testing.T) {
		// 2099 is > now.Year() + deadlineYearLookahead (5).
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
			Deadline:     flexDate("2099-12-31"),
		}, in, now)
		if v.Deadline != nil {
			t.Errorf("far-future deadline must be dropped, got %v", v.Deadline)
		}
	})

	t.Run("deadline year too far in past is dropped", func(t *testing.T) {
		// 2020 is < now.Year() - deadlineYearLookback (1).
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
			Deadline:     flexDate("2020-01-01"),
		}, in, now)
		if v.Deadline != nil {
			t.Errorf("far-past deadline must be dropped, got %v", v.Deadline)
		}
	})

	t.Run("deadline within reasonable range kept", func(t *testing.T) {
		v := validateExtractArgs(extractToolArgs{
			Title:        "ok",
			Description:  "ok",
			SourceMsgIDs: []string{"m1"},
			AssigneeUIDs: []string{"creator"},
			Deadline:     flexDate("2026-05-15"),
		}, in, now)
		if v.Deadline == nil {
			t.Errorf("reasonable deadline must be kept")
		}
	})
}

// TestExtractToolArgs_FlexibleDeadline locks down the migration-fail-soft
// contract on the `deadline` field. The new schema is YYYY-MM-DD string,
// but a model that hasn't picked up the change yet (or a buggy gateway) may
// still emit a number, bool, or other type. The whole extractToolArgs must
// still decode — `deadline` falls through to nil for any non-string shape.
// This is the difference between "matter created with deadline=nil" and
// "422 LLM_EMPTY_EXTRACTION" for the user.
func TestExtractToolArgs_FlexibleDeadline(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantDLNil   bool
		wantDLValue string // only checked when wantDLNil == false
		wantTitle   string
	}{
		{
			name:        "string YYYY-MM-DD survives",
			raw:         `{"title":"t","description":"d","deadline":"2026-05-15","source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil:   false,
			wantDLValue: "2026-05-15",
			wantTitle:   "t",
		},
		{
			name:      "explicit null → nil",
			raw:       `{"title":"t","description":"d","deadline":null,"source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil: true,
			wantTitle: "t",
		},
		{
			// Legacy number format: model echoes the old Unix-seconds schema.
			// Must NOT fail Unmarshal — drop only the deadline, keep title.
			name:      "legacy unix-seconds number → silently nil",
			raw:       `{"title":"t","description":"d","deadline":1234567890,"source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil: true,
			wantTitle: "t",
		},
		{
			name:      "boolean shape → silently nil",
			raw:       `{"title":"t","description":"d","deadline":true,"source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil: true,
			wantTitle: "t",
		},
		{
			name:      "object shape → silently nil",
			raw:       `{"title":"t","description":"d","deadline":{"year":2026},"source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil: true,
			wantTitle: "t",
		},
		{
			name:      "missing key → nil (zero-value)",
			raw:       `{"title":"t","description":"d","source_msg_ids":[],"assignee_uids":[]}`,
			wantDLNil: true,
			wantTitle: "t",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got extractToolArgs
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("Unmarshal should fail-soft, got err: %v", err)
			}
			if got.Title != tc.wantTitle {
				t.Errorf("title: got %q want %q", got.Title, tc.wantTitle)
			}
			if tc.wantDLNil {
				if got.Deadline.raw != nil {
					t.Errorf("deadline expected nil, got %q", *got.Deadline.raw)
				}
				return
			}
			if got.Deadline.raw == nil {
				t.Errorf("deadline expected %q, got nil", tc.wantDLValue)
			} else if *got.Deadline.raw != tc.wantDLValue {
				t.Errorf("deadline: got %q want %q", *got.Deadline.raw, tc.wantDLValue)
			}
		})
	}
}

// TestResolveLocation covers Asia/Shanghai default, override happy path,
// unknown name fallback, and empty-string default.
func TestResolveLocation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // expected location name
	}{
		{"empty → Asia/Shanghai", "", "Asia/Shanghai"},
		{"explicit Asia/Shanghai", "Asia/Shanghai", "Asia/Shanghai"},
		{"explicit UTC", "UTC", "UTC"},
		{"unknown → default", "Fake/Notreal", "Asia/Shanghai"},
		{"empty after garbage", "Mars/Olympus", "Asia/Shanghai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveLocation(tc.in)
			if got.String() != tc.want {
				t.Errorf("got %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestBuildExtractSystemPrompt_HonorsTimezone confirms the user-facing
// "当前日期" line reflects the resolved timezone, so a user at 00:30
// Asia/Shanghai sees today's local date in the prompt, not yesterday's UTC.
func TestBuildExtractSystemPrompt_HonorsTimezone(t *testing.T) {
	// 2026-05-14T16:30:00Z is 2026-05-15 00:30 in Asia/Shanghai (UTC+8).
	asiaUTC := time.Date(2026, 5, 14, 16, 30, 0, 0, time.UTC)
	loc := resolveLocation("Asia/Shanghai")
	local := asiaUTC.In(loc)

	in := ExtractInput{ChannelID: "ch", CreatorUID: "c"}
	got := buildExtractSystemPrompt(in, local)
	if !strings.Contains(got, "当前日期：2026-05-15") {
		t.Errorf("expected Asia/Shanghai-local date in prompt, got:\n%s", got)
	}
	// Same instant, UTC-anchored, should read as the previous day.
	gotUTC := buildExtractSystemPrompt(in, asiaUTC)
	if !strings.Contains(gotUTC, "当前日期：2026-05-14") {
		t.Errorf("UTC anchor should read as 2026-05-14, got:\n%s", gotUTC)
	}
}

// TestParseLLMDeadline_HonorsTimezone is the regression test for review #7's
// UTC-midnight blocker: a model emits "2026-05-15" in a non-UTC conversation,
// and the persisted *time.Time must be midnight in that timezone, not in UTC
// (which would render as the prior day to a negative-offset user).
//
// Concretely, for America/Los_Angeles (UTC-7 during DST, UTC-8 otherwise),
// using bare time.Parse would store 2026-05-15T00:00:00Z = 2026-05-14T17:00
// local, so the "May 15" deadline shows up as "May 14" in the user's
// calendar. ParseInLocation with now.Location() prevents that.
func TestParseLLMDeadline_HonorsTimezone(t *testing.T) {
	cases := []struct {
		name    string
		tz      string
		raw     string
		wantDay int
		// expected wall-clock hour in target tz (always 0 for date-only inputs)
		wantHour int
	}{
		{
			name:     "Asia/Shanghai date is local midnight",
			tz:       "Asia/Shanghai",
			raw:      "2026-05-15",
			wantDay:  15,
			wantHour: 0,
		},
		{
			name:     "America/Los_Angeles date is local midnight, not UTC midnight",
			tz:       "America/Los_Angeles",
			raw:      "2026-05-15",
			wantDay:  15,
			wantHour: 0,
		},
		{
			name:     "UTC date is UTC midnight",
			tz:       "UTC",
			raw:      "2026-05-15",
			wantDay:  15,
			wantHour: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := resolveLocation(tc.tz)
			now := time.Date(2026, 5, 14, 12, 0, 0, 0, loc)
			got := parseLLMDeadline(&tc.raw, now)
			if got == nil {
				t.Fatalf("expected parsed deadline, got nil")
			}
			// Inspect the wall-clock representation in the target tz —
			// .In(loc) is a no-op for already-located times, but it documents
			// intent: we care about local-tz semantics, not absolute instant.
			gotLocal := got.In(loc)
			if gotLocal.Day() != tc.wantDay || gotLocal.Hour() != tc.wantHour {
				t.Errorf("expected day=%d hour=%d in %s, got day=%d hour=%d (instant=%s)",
					tc.wantDay, tc.wantHour, tc.tz, gotLocal.Day(), gotLocal.Hour(), got.Format(time.RFC3339))
			}
			// Belt-and-braces: location of the parsed deadline must match
			// now's location. Using bare time.Parse would put it in UTC.
			if got.Location().String() != loc.String() {
				t.Errorf("expected deadline location %q, got %q", loc.String(), got.Location().String())
			}
		})
	}
}

// flexDate wraps a date string in the test-scope flexibleDate type. Empty
// string yields a nil-raw flexibleDate (matching the "model returned null"
// case), so call sites can express both "no deadline" and "deadline=X" in
// a single helper.
func flexDate(s string) flexibleDate {
	if s == "" {
		return flexibleDate{}
	}
	return flexibleDate{raw: &s}
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
