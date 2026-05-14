package service

import (
	"strings"
	"testing"
	"time"
)

// TestBuildExtractSystemPrompt_Anchors locks down the smart-create-style
// sections we now require in the extract system prompt. Each anchor maps to a
// behavior the old prompt was missing (and that the LLM was regressing on):
//
//   - role framing — "起草事项" not "做总结"
//   - title naming  — 动宾 + ≤20 字 + 禁套话
//   - description shape — 50–150 字 + 保留原话
//   - assignee priority — 显式认领 > 被指派 > creator fallback
//   - deadline policy — relative-date parsing + null on absence (NO "+7 day" default)
//   - source filtering — drop greetings/reactions/previews
//   - current date + Chinese weekday — model needs both to resolve "本周五"
//
// If any of these anchors disappear, prompt quality has regressed even if
// every existing test still passes; the LLM is the silent victim.
func TestBuildExtractSystemPrompt_Anchors(t *testing.T) {
	name := "#Octo设计群"
	in := ExtractInput{
		ChannelID:   "ch-1",
		ChannelName: &name,
		CreatorUID:  "creator-uid",
		Messages: []ExtractMessage{
			{MessageID: "m1", FromUID: "u1", FromUname: "王宜林"},
			{MessageID: "m2", FromUID: "u2", FromUname: "吴明辉"},
		},
	}
	// 2026-05-14 is a Thursday in the Gregorian calendar.
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	got := buildExtractSystemPrompt(in, now)

	musts := []string{
		// role
		"事项起草助手",
		"立事项",
		// title rules
		"动宾",
		"20 汉字",
		"关于",
		// description rules
		"50–150 汉字",
		"保留原话",
		// deadline rules
		"YYYY-MM-DD",
		"2026-05-15",
		"返回 null",
		"次年",
		// source filtering — P2.2 explicit token list
		"寒暄",
		"收到",
		"好的",
		"OK",
		"嗯",
		"👍",
		"加入/退出群通知",
		"若把这条消息从 timeline 里删掉",
		// assignee priority
		"显式认领",
		"@X 你来",
		// constraint table
		"禁止虚构",
		"输出语言跟随消息",
		// runtime context
		"当前日期：2026-05-14 (星期四)",
		"频道名称：#Octo设计群",
		"频道 ID：ch-1",
		"Creator UID：creator-uid",
		"u1 → 王宜林",
		"u2 → 吴明辉",
	}
	for _, m := range musts {
		if !strings.Contains(got, m) {
			t.Errorf("system prompt missing anchor %q\n--- got ---\n%s\n--- end ---", m, got)
		}
	}

	// Negative anchors: behaviors we deliberately do NOT want.
	mustNots := []string{
		// the old prompt used RFC3339 UTC — bad for relative-date parsing.
		"T10:00:00Z",
		// smart_create suggests a "+7 day" default; we explicitly rejected it.
		"默认一周",
		"+ 7 天",
		// P2.1: Unix-second deadline is gone; model must emit a date string.
		"Unix 秒时间戳",
		"Unix 时间戳（秒）",
	}
	for _, m := range mustNots {
		if strings.Contains(got, m) {
			t.Errorf("system prompt contains forbidden substring %q", m)
		}
	}
}

func TestBuildExtractSystemPrompt_WeekdayCoversAllDays(t *testing.T) {
	// 2026-05-10 is Sunday, 2026-05-11 Monday, ... 2026-05-16 Saturday.
	cases := []struct {
		day  int
		want string
	}{
		{10, "星期日"},
		{11, "星期一"},
		{12, "星期二"},
		{13, "星期三"},
		{14, "星期四"},
		{15, "星期五"},
		{16, "星期六"},
	}
	in := ExtractInput{ChannelID: "ch", CreatorUID: "c"}
	for _, tc := range cases {
		now := time.Date(2026, 5, tc.day, 9, 0, 0, 0, time.UTC)
		got := buildExtractSystemPrompt(in, now)
		if !strings.Contains(got, tc.want) {
			t.Errorf("day %d: expected weekday %q in prompt", tc.day, tc.want)
		}
	}
}

func TestBuildExtractSystemPrompt_NoChannelName(t *testing.T) {
	in := ExtractInput{ChannelID: "ch-1", CreatorUID: "c"} // ChannelName nil
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	got := buildExtractSystemPrompt(in, now)
	if strings.Contains(got, "频道名称：") {
		t.Errorf("prompt should omit channel-name line when name is nil:\n%s", got)
	}
	if !strings.Contains(got, "频道 ID：ch-1") {
		t.Errorf("prompt should still include channel ID")
	}
}
