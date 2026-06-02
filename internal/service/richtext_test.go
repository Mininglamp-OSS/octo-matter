package service

import (
	"strings"
	"testing"
)

// TestMessageDisplayContent_RichType14 locks the type=14 (图文混排) extraction
// path: the LLM must see clean plain text, never the raw stringified JSON
// payload (which previously leaked through Content verbatim and either blanked
// out or polluted the prompt with JSON noise → incomplete/hallucinated matters).
func TestMessageDisplayContent_RichType14(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		contentType int
		want        string
	}{
		{
			name:        "prefer server plain",
			content:     `{"content":[{"type":"text","text":"上线方案"},{"type":"image","url":"https://x/y.png","width":10,"height":10}],"plain":"上线方案[图片]"}`,
			contentType: 14,
			want:        "上线方案[图片]",
		},
		{
			name:        "build from blocks when plain empty",
			content:     `{"content":[{"type":"text","text":"先看图"},{"type":"image","url":"https://x/y.png","width":10,"height":10},{"type":"text","text":"再讨论"}],"plain":""}`,
			contentType: 14,
			want:        "先看图[图片]再讨论",
		},
		{
			name:        "image only collapses to placeholder",
			content:     `{"content":[{"type":"image","url":"https://x/y.png","width":10,"height":10}],"plain":""}`,
			contentType: 14,
			want:        "[图片]",
		},
		{
			name:        "legacy string content payload",
			content:     `{"content":"纯文本旧格式"}`,
			contentType: 14,
			want:        "纯文本旧格式",
		},
		{
			name:        "unknown block type keeps its text",
			content:     `{"content":[{"type":"text","text":"A"},{"type":"divider","text":"--"},{"type":"text","text":"B"}],"plain":""}`,
			contentType: 14,
			want:        "A--B",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := messageDisplayContent(ExtractMessage{Content: tc.content, ContentType: tc.contentType})
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMessageDisplayContent_RichTextAutoDetect verifies backward compat: even
// when the caller does NOT set ContentType=14 (older octo-web), a Content that
// is structurally a RichText payload is still normalized rather than passed as
// raw JSON.
func TestMessageDisplayContent_RichTextAutoDetect(t *testing.T) {
	content := `{"content":[{"type":"text","text":"自动识别"}],"plain":"自动识别"}`
	got := messageDisplayContent(ExtractMessage{Content: content}) // ContentType unset (0)
	if got != "自动识别" {
		t.Fatalf("auto-detect failed: got %q", got)
	}
}

// TestMessageDisplayContent_Type14RejectsJSONNoise ensures a message declared
// as type=14 but carrying a non-RichText JSON blob (noise) does not leak the
// raw JSON into the prompt — it collapses to a safe placeholder instead.
func TestMessageDisplayContent_Type14RejectsJSONNoise(t *testing.T) {
	noise := `{"foo":"bar","baz":[1,2,3]}`
	got := messageDisplayContent(ExtractMessage{Content: noise, ContentType: 14})
	if got != richTextFallbackDisplay {
		t.Fatalf("expected fallback placeholder for noisy type=14 payload, got %q", got)
	}
	if strings.Contains(got, "foo") || strings.Contains(got, "[1,2,3]") {
		t.Fatalf("raw JSON noise leaked into prompt content: %q", got)
	}
}

// TestMessageDisplayContent_PlainTextUntouched guarantees the legacy flat
// string path is fully backward compatible: a normal text message (no type, no
// JSON shape) is returned verbatim.
func TestMessageDisplayContent_PlainTextUntouched(t *testing.T) {
	cases := []string{
		"这是一条普通文本消息",
		"包含 { 花括号 } 但不是 JSON",
		"",
		"   ",
	}
	for _, c := range cases {
		if got := messageDisplayContent(ExtractMessage{Content: c}); got != c {
			t.Fatalf("plain content mutated: input %q got %q", c, got)
		}
	}
}

// TestMessageDisplayContent_NonRichJSONUntypedUntouched verifies that a JSON
// object that is NOT a RichText payload AND is not declared type=14 is returned
// verbatim (we must not hijack arbitrary JSON-looking text).
func TestMessageDisplayContent_NonRichJSONUntypedUntouched(t *testing.T) {
	noise := `{"foo":"bar"}`
	if got := messageDisplayContent(ExtractMessage{Content: noise}); got != noise {
		t.Fatalf("non-richtext untyped JSON should pass through: got %q", got)
	}
}

// TestMessageDisplayContent_UntypedContentStringNotHijacked is the core
// regression for the PR #64 review blocker: an untyped (ContentType=0) JSON
// object whose "content" is a STRING (not a block array) must NOT be auto
// detected as RichText. The old code parsed it as legacy string content and
// returned only "deploy", silently dropping "source":"ops". Auto-detect is now
// narrowed to block-array shape only, so the whole object passes through
// verbatim.
func TestMessageDisplayContent_UntypedContentStringNotHijacked(t *testing.T) {
	cases := []string{
		`{"content":"deploy","source":"ops"}`,
		`{"content":"deploy","extra":"field"}`,
		`{"plain":"hi","other":"x"}`,
		`{"content":"only"}`,
		// Object array that is NOT RichText blocks (elements lack a "type"):
		// must not be hijacked — this is the symmetric data-loss hole.
		`{"content":[{"title":"deploy"}],"source":"ops"}`,
	}
	for _, c := range cases {
		if got := messageDisplayContent(ExtractMessage{Content: c}); got != c {
			t.Fatalf("untyped string-content JSON was hijacked: input %q got %q", c, got)
		}
	}
}

// TestMessageDisplayContent_NonType14ContentStringNotHijacked covers the most
// clearly-wrong case from the review: a caller that explicitly declares a
// non-14 ContentType (e.g. 1 = plain text) whose body happens to be a RichText
// shaped string-content JSON must NOT be shape-sniffed and truncated. It is
// returned verbatim.
func TestMessageDisplayContent_NonType14ContentStringNotHijacked(t *testing.T) {
	body := `{"content":"deploy","source":"ops"}`
	got := messageDisplayContent(ExtractMessage{Content: body, ContentType: 1})
	if got != body {
		t.Fatalf("explicit non-14 message was hijacked: got %q want %q", got, body)
	}
}

// TestMessageDisplayContent_NonType14BlockArrayAutoDetected documents the
// intentional behavior endorsed by the reviewers: the block-array shape is
// structurally distinctive enough to auto-detect for ANY non-14 message,
// because a JSON block array is unambiguously RichText and normalizing it loses
// no data (the alternative is dumping raw block-array JSON into the prompt —
// exactly the bug this PR fixes). Only the broad string-content / plain-only
// legacy handling is reserved for explicit ContentType==14.
func TestMessageDisplayContent_NonType14BlockArrayAutoDetected(t *testing.T) {
	body := `{"content":[{"type":"text","text":"hi"}],"plain":"hi"}`
	got := messageDisplayContent(ExtractMessage{Content: body, ContentType: 1})
	if got != "hi" {
		t.Fatalf("non-14 block-array should be normalized: got %q want %q", got, "hi")
	}
}

// TestMessageDisplayContent_Type14StringContentStillWorks pins that the broad
// legacy handling (content-as-string) is preserved for EXPLICIT ContentType=14,
// even after auto-detect was narrowed. Only the auto-detect path was tightened.
func TestMessageDisplayContent_Type14StringContentStillWorks(t *testing.T) {
	got := messageDisplayContent(ExtractMessage{Content: `{"content":"纯文本旧格式"}`, ContentType: 14})
	if got != "纯文本旧格式" {
		t.Fatalf("explicit type=14 legacy string content broke: got %q", got)
	}
}

// TestRichTextDisplayTextBlocksOnly_RejectsStringContent unit-tests the new
// narrowed detector directly: it must reject every shape that is not a JSON
// object with a block-array "content", so the untyped auto-detect path can rely
// on it without hijacking ordinary JSON.
func TestRichTextDisplayTextBlocksOnly_RejectsStringContent(t *testing.T) {
	reject := []string{
		`{"content":"deploy","source":"ops"}`, // string content
		`{"plain":"hi"}`,                      // plain only
		`{"foo":"bar"}`,                       // neither
		`{"content":null}`,                    // null content
		`plain text`,                          // not JSON
		``,                                    // empty
		`{"content":[{"title":"deploy"}],"source":"ops"}`, // object array, not RichText blocks (no type)
		`{"content":[]}`,      // empty array
		`{"content":[1,2,3]}`, // scalar array, unmarshal error
	}
	for _, c := range reject {
		if _, ok := richTextDisplayTextBlocksOnly(c); ok {
			t.Fatalf("blocks-only detector should reject %q", c)
		}
	}

	// Accepts the block-array shape and normalizes it.
	got, ok := richTextDisplayTextBlocksOnly(`{"content":[{"type":"text","text":"hi"}],"plain":"hi"}`)
	if !ok || got != "hi" {
		t.Fatalf("blocks-only detector should accept block array: ok=%v got=%q", ok, got)
	}
}

// TestRichTextDisplayText_EmptyContentArrayFallsBack ensures a RichText payload
// recognized by shape but with empty content/plain collapses to a placeholder
// rather than an empty string.
func TestRichTextDisplayText_EmptyContentArrayFallsBack(t *testing.T) {
	got, ok := richTextDisplayText(`{"content":[],"plain":""}`)
	if !ok {
		t.Fatalf("expected payload to be recognized as RichText")
	}
	if got != richTextFallbackDisplay {
		t.Fatalf("expected fallback placeholder, got %q", got)
	}
}
