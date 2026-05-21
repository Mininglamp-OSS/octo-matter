package service

import (
	"strings"
	"testing"
)

// TestValidateTimelineArgs covers the v3 LLM-output validation pipeline for
// extract_matter_progress: filter related_uids / source_msg_ids to the input
// set so the model cannot fabricate references; coerce status_suggestion to
// the allowed enum {open, done, nil}; default to safe server-side derivation
// when the LLM returns nothing.
func TestValidateTimelineArgs(t *testing.T) {
	in := []ExtractMessage{
		{MessageID: "m1", FromUID: "u1"},
		{MessageID: "m2", FromUID: "u2"},
		{MessageID: "m3", FromUID: "u1"}, // duplicate from_uid
	}

	cases := []struct {
		name              string
		args              timelineToolArgs
		wantSourceMsgs    []string
		wantRelatedUIDs   []string
		wantStatusSuggest *string
	}{
		{
			name: "all valid",
			args: timelineToolArgs{
				SourceMsgIDs:     []string{"m1", "m3"},
				RelatedUIDs:      []string{"u1", "u2"},
				StatusSuggestion: strPtrOrNil("done"),
			},
			wantSourceMsgs:    []string{"m1", "m3"},
			wantRelatedUIDs:   []string{"u1", "u2"},
			wantStatusSuggest: strPtrOrNil("done"),
		},
		{
			name: "fabricated source ids dropped, fallback to all",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"m1", "fake"},
				RelatedUIDs:  []string{"u1"},
			},
			wantSourceMsgs:  []string{"m1"},
			wantRelatedUIDs: []string{"u1"},
		},
		{
			name: "all source ids invalid → fallback to input set",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"fake1", "fake2"},
				RelatedUIDs:  []string{"u1"},
			},
			wantSourceMsgs:  []string{"m1", "m2", "m3"},
			wantRelatedUIDs: []string{"u1"},
		},
		{
			name: "fabricated uids dropped",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"m1"},
				RelatedUIDs:  []string{"u1", "stranger", "u2"},
			},
			wantSourceMsgs:  []string{"m1"},
			wantRelatedUIDs: []string{"u1", "u2"},
		},
		{
			name: "all related uids invalid → fallback to deduped from_uids",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"m1"},
				RelatedUIDs:  []string{"stranger1", "stranger2"},
			},
			wantSourceMsgs: []string{"m1"},
			// Falls back to unique input from_uids in first-appearance order.
			wantRelatedUIDs: []string{"u1", "u2"},
		},
		{
			name: "duplicate uids deduped, order preserved",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"m1"},
				RelatedUIDs:  []string{"u2", "u1", "u2"},
			},
			wantSourceMsgs:  []string{"m1"},
			wantRelatedUIDs: []string{"u2", "u1"},
		},
		{
			name: "empty/whitespace uids skipped",
			args: timelineToolArgs{
				SourceMsgIDs: []string{"m1"},
				RelatedUIDs:  []string{"", "  ", "u1"},
			},
			wantSourceMsgs:  []string{"m1"},
			wantRelatedUIDs: []string{"u1"},
		},
		{
			name: "status suggestion nil",
			args: timelineToolArgs{
				SourceMsgIDs:     []string{"m1"},
				RelatedUIDs:      []string{"u1"},
				StatusSuggestion: nil,
			},
			wantSourceMsgs:    []string{"m1"},
			wantRelatedUIDs:   []string{"u1"},
			wantStatusSuggest: nil,
		},
		{
			name: "status suggestion 'open' allowed",
			args: timelineToolArgs{
				SourceMsgIDs:     []string{"m1"},
				RelatedUIDs:      []string{"u1"},
				StatusSuggestion: strPtrOrNil("open"),
			},
			wantSourceMsgs:    []string{"m1"},
			wantRelatedUIDs:   []string{"u1"},
			wantStatusSuggest: strPtrOrNil("open"),
		},
		{
			name: "status suggestion 'archived' rejected → nil",
			args: timelineToolArgs{
				SourceMsgIDs:     []string{"m1"},
				RelatedUIDs:      []string{"u1"},
				StatusSuggestion: strPtrOrNil("archived"),
			},
			wantSourceMsgs:    []string{"m1"},
			wantRelatedUIDs:   []string{"u1"},
			wantStatusSuggest: nil,
		},
		{
			name: "status suggestion garbage → nil",
			args: timelineToolArgs{
				SourceMsgIDs:     []string{"m1"},
				RelatedUIDs:      []string{"u1"},
				StatusSuggestion: strPtrOrNil("nonsense"),
			},
			wantSourceMsgs:    []string{"m1"},
			wantRelatedUIDs:   []string{"u1"},
			wantStatusSuggest: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateTimelineArgs(tc.args, in)
			if !equalStringSlices(got.SourceMsgs, tc.wantSourceMsgs) {
				t.Errorf("SourceMsgs: got %v, want %v", got.SourceMsgs, tc.wantSourceMsgs)
			}
			if !equalStringSlices(got.RelatedUIDs, tc.wantRelatedUIDs) {
				t.Errorf("RelatedUIDs: got %v, want %v", got.RelatedUIDs, tc.wantRelatedUIDs)
			}
			if !equalStrPtr(got.StatusSuggestion, tc.wantStatusSuggest) {
				t.Errorf("StatusSuggestion: got %v, want %v", got.StatusSuggestion, tc.wantStatusSuggest)
			}
		})
	}
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func equalStrPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// TestValidateTimelineArgs_ContentCap covers PR #34:
// LLM-emitted content must obey the same MaxContentLength cap as the direct
// timeline write path; otherwise INSERT into matter_timelines.content (TEXT
// but bound by handler max=10000 in the direct path) fails opaque 5xx.
func TestValidateTimelineArgs_ContentCap(t *testing.T) {
	in := []ExtractMessage{{MessageID: "m1", FromUID: "u1"}}

	t.Run("over-cap content clipped to MaxContentLength runes", func(t *testing.T) {
		over := strings.Repeat("段", MaxContentLength+50)
		v := validateTimelineArgs(timelineToolArgs{
			Content:      over,
			SourceMsgIDs: []string{"m1"},
			RelatedUIDs:  []string{"u1"},
		}, in)
		got := []rune(v.Content)
		if len(got) != MaxContentLength {
			t.Errorf("content not clipped: got %d runes, want %d", len(got), MaxContentLength)
		}
	})

	t.Run("HasExplicitCitations set when LLM cites a valid msg", func(t *testing.T) {
		v := validateTimelineArgs(timelineToolArgs{
			SourceMsgIDs: []string{"m1"},
		}, in)
		if !v.HasExplicitCitations {
			t.Errorf("HasExplicitCitations should be true when a valid msg id is cited")
		}
	})

	t.Run("HasExplicitCitations cleared on empty / all-invalid citations", func(t *testing.T) {
		// Empty.
		v := validateTimelineArgs(timelineToolArgs{SourceMsgIDs: nil}, in)
		if v.HasExplicitCitations {
			t.Errorf("HasExplicitCitations must be false when LLM cited nothing — attachment privacy depends on it")
		}
		// All invalid → filtered to empty → fallback to all msgs, but the
		// flag MUST stay false. buildLLMAttachments relies on this to fail
		// closed.
		v = validateTimelineArgs(timelineToolArgs{SourceMsgIDs: []string{"fake1", "fake2"}}, in)
		if v.HasExplicitCitations {
			t.Errorf("HasExplicitCitations must be false when every citation was filtered out")
		}
	})

	t.Run("within-cap content untouched", func(t *testing.T) {
		short := strings.Repeat("a", 100)
		v := validateTimelineArgs(timelineToolArgs{
			Content:      short,
			SourceMsgIDs: []string{"m1"},
			RelatedUIDs:  []string{"u1"},
		}, in)
		if v.Content != short {
			t.Errorf("short content modified: %q vs %q", v.Content, short)
		}
	})
}
