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
