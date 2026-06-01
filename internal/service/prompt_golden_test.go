package service

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// TestBuildExtractSystemPrompt_GoldenWhitespace pins the byte-exact
// whitespace produced by extract_matter.md → renderExtractSystemPrompt.
// Substantive content is covered by TestBuildExtractSystemPrompt_Anchors
// in extract_prompt_test.go; this test exists specifically to catch
// whitespace drift introduced by template edits (a missing {{-}} trim
// would add a blank line that the LLM might interpret as section break).
func TestBuildExtractSystemPrompt_GoldenWhitespace(t *testing.T) {
	channelName := "design"
	in := ExtractInput{
		ChannelID:   "c1",
		ChannelName: &channelName,
		CreatorUID:  "creator",
		Messages: []ExtractMessage{
			{MessageID: "m1", FromUID: "u1", FromUname: "Alice"},
			{MessageID: "m2", FromUID: "u2", FromUname: "Bob"},
		},
	}
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	got := buildExtractSystemPrompt(in, now)

	// Whitespace contract: every line ends with \n; section breaks are a
	// single blank line; no double blank lines; the participant list ends
	// with one trailing newline (not two). The model relies on these
	// boundaries to scope its "字段规则" / "关键约束" parsing.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("prompt contains a triple newline (would render as a double blank line)")
	}
	if !strings.HasSuffix(got, "Bob\n") {
		t.Errorf("prompt must end with the last sender + single newline, got tail: %q", tailN(got, 30))
	}

	// Locked structural anchors (different from the content anchors covered
	// elsewhere): these prove the {{if}} / {{- range}} machinery in the
	// template produces exactly the lines we expect, in order.
	wantLines := []string{
		"当前日期：2026-05-14 (星期四)",
		"频道名称：design",
		"频道 ID：c1",
		"Creator UID：creator",
		"参与者（uid → 姓名，assignee_uids 只能来自这个列表）：",
		"  - u1 → Alice",
		"  - u2 → Bob",
	}
	prev := -1
	for _, line := range wantLines {
		idx := strings.Index(got, line)
		if idx < 0 {
			t.Errorf("missing required line %q", line)
			continue
		}
		if idx < prev {
			t.Errorf("line %q out of order (expected after previous anchor)", line)
		}
		prev = idx
	}
}

// TestBuildExtractSystemPrompt_GoldenEmptyStringChannelName covers the
// boundary where the caller passes a pointer to "" instead of nil. The
// template's {{if .ChannelName}} treats the empty string as falsy, so
// the rendered output must omit the "频道名称：" line entirely.
func TestBuildExtractSystemPrompt_GoldenEmptyStringChannelName(t *testing.T) {
	empty := ""
	in := ExtractInput{
		ChannelID:   "c1",
		ChannelName: &empty,
		CreatorUID:  "creator",
		Messages: []ExtractMessage{
			{MessageID: "m1", FromUID: "u1", FromUname: "Alice"},
		},
	}
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	got := buildExtractSystemPrompt(in, now)
	if strings.Contains(got, "频道名称：") {
		t.Errorf("prompt should omit '频道名称：' line when ChannelName points to empty string, got:\n%s", got)
	}
	if !strings.Contains(got, "频道 ID：c1") {
		t.Errorf("'频道 ID：' line should still be present")
	}
}

// TestBuildTimelineSystemPrompt_Golden pins the timeline-extraction prompt
// against a known-rich input. Covers every conditional branch: description
// present, deadline present, assignees present, channel name present,
// recent entries present.
func TestBuildTimelineSystemPrompt_Golden(t *testing.T) {
	deadline := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	desc := "design v3 rollout"
	matter := &model.Matter{
		Title:       "Rollout",
		Description: &desc,
		Status:      model.MatterStatusOpen,
		Deadline:    &deadline,
	}
	assignees := []*model.MatterAssignee{
		{UserID: "u1"},
		{UserID: "u2"},
	}
	createdAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	recentContent := "kicked off"
	recent := []*model.TimelineEntry{
		{Content: &recentContent, CreatedAt: createdAt},
	}
	channelName := "design"
	in := TimelineInput{ChannelName: &channelName}

	got := normalizeNow(buildTimelineSystemPrompt(matter, assignees, recent, in))
	want := "你是事项进展抽取助手。根据群聊消息提炼与目标事项相关的进展，必须通过 extract_matter_progress 函数返回。\n" +
		"字段约定：\n" +
		"  - content：一段话概括最新进展。\n" +
		"  - related_uids：本次进展涉及的人员 uid，必须从输入消息的 from_uid 中选取，不要编造。\n" +
		"  - source_msg_ids：从输入消息的 message_id 中选取支撑此进展的消息，不要编造或返回空数组。\n" +
		"  - status_suggestion：如果消息明显表达了完成（done）或重新打开（open），则返回相应字符串；否则返回 null。\n" +
		"当前时间：<NOW>\n" +
		"\n" +
		"【目标事项】\n" +
		"  标题：Rollout\n" +
		"  描述：design v3 rollout\n" +
		"  当前状态：open\n" +
		"  截止时间：2026-06-01T00:00:00Z\n" +
		"  负责人 UID：u1, u2\n" +
		"  频道：design\n" +
		"\n" +
		"【已有进展（最近 3 条）】\n" +
		"  - [2026-05-01T12:00:00Z] kicked off\n" +
		"\n" +
		"请基于以下新消息，提取本次新增的进展，避免重复已有进展。\n"
	if got != want {
		t.Errorf("prompt mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestBuildTimelineSystemPrompt_GoldenMinimal exercises the all-empty
// branch: no description, no deadline, no assignees, no channel name,
// no recent entries. The output must still be well-formed (no stray
// "描述：" or "负责人 UID：" lines).
func TestBuildTimelineSystemPrompt_GoldenMinimal(t *testing.T) {
	matter := &model.Matter{
		Title:  "Bare",
		Status: model.MatterStatusOpen,
	}
	got := normalizeNow(buildTimelineSystemPrompt(matter, nil, nil, TimelineInput{}))
	want := "你是事项进展抽取助手。根据群聊消息提炼与目标事项相关的进展，必须通过 extract_matter_progress 函数返回。\n" +
		"字段约定：\n" +
		"  - content：一段话概括最新进展。\n" +
		"  - related_uids：本次进展涉及的人员 uid，必须从输入消息的 from_uid 中选取，不要编造。\n" +
		"  - source_msg_ids：从输入消息的 message_id 中选取支撑此进展的消息，不要编造或返回空数组。\n" +
		"  - status_suggestion：如果消息明显表达了完成（done）或重新打开（open），则返回相应字符串；否则返回 null。\n" +
		"当前时间：<NOW>\n" +
		"\n" +
		"【目标事项】\n" +
		"  标题：Bare\n" +
		"  当前状态：open\n" +
		"\n" +
		"请基于以下新消息，提取本次新增的进展，避免重复已有进展。\n"
	if got != want {
		t.Errorf("prompt mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
}

var nowLineRE = regexp.MustCompile(`当前时间：[^\n]+`)

func normalizeNow(s string) string {
	return nowLineRE.ReplaceAllString(s, "当前时间：<NOW>")
}

func tailN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
