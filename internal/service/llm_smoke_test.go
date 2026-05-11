// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// LLM smoke tests run against a real OpenAI-compatible gateway. They are
// SKIPPED unless LLM_SMOKE=1, and require LLM_API_URL + LLM_API_KEY +
// LLM_MODEL in the environment. Used to validate that the v3 tool schemas
// (deadline / source_msg_ids / assignee_uids / status_suggestion) are
// actually accepted by the gateway and that real models return parseable
// JSON for them.
//
// Run example:
//
//	LLM_SMOKE=1 \
//	LLM_API_URL=https://api.example.com/v1 \
//	LLM_API_KEY=... \
//	LLM_MODEL=claude-sonnet-4-6 \
//	go test ./internal/service -run TestSmoke -v
func smokeClient(t *testing.T) *llm.Client {
	t.Helper()
	if os.Getenv("LLM_SMOKE") != "1" {
		t.Skip("LLM_SMOKE not set; skipping live LLM call")
	}
	url := os.Getenv("LLM_API_URL")
	key := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if url == "" || key == "" || model == "" {
		t.Skip("LLM_API_URL / LLM_API_KEY / LLM_MODEL must all be set for smoke tests")
	}
	return llm.New(url, key, model, 60*time.Second)
}

func TestSmoke_ExtractMatter_AcceptsV3Schema(t *testing.T) {
	c := smokeClient(t)
	in := ExtractInput{
		SpaceID:     "sp-smoke",
		ChannelType: 2,
		ChannelID:   "ch-smoke",
		ChannelName: strPtrSmoke("产品研发-智能事项群"),
		CreatorUID:  "uid_creator",
		Messages: []ExtractMessage{
			{
				MessageID: "m_001",
				FromUID:   "uid_alice",
				FromUname: "Alice",
				Timestamp: time.Now().Add(-2 * time.Hour).Unix(),
				Content:   "周五（5/15）之前要把 Octo 智能事项的 Demo 视频录好，给董事会看。",
			},
			{
				MessageID: "m_002",
				FromUID:   "uid_bob",
				FromUname: "Bob",
				Timestamp: time.Now().Add(-90 * time.Minute).Unix(),
				Content:   "我来负责脚本和录屏，Carol 帮忙做剪辑。",
			},
			{
				MessageID: "m_003",
				FromUID:   "uid_carol",
				FromUname: "Carol",
				Timestamp: time.Now().Add(-60 * time.Minute).Unix(),
				Content:   "OK，剪辑我来。需要 Alice 把上次的 PPT 发我。",
			},
			{
				MessageID: "m_004",
				FromUID:   "uid_alice",
				FromUname: "Alice",
				Timestamp: time.Now().Add(-30 * time.Minute).Unix(),
				Content:   "已发 Carol。我们这个事项的 deadline 就定 5/15 18:00。",
			},
		},
	}
	systemPrompt := buildExtractSystemPrompt(in)
	userPrompt := buildMessagesPrompt(in.Messages)

	raw, err := c.CallTool(context.Background(), systemPrompt, userPrompt, extractMatterTool)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	t.Logf("LLM raw arguments:\n%s", raw)

	var args extractToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("could not unmarshal LLM output into extractToolArgs: %v", err)
	}
	t.Logf("Parsed: title=%q description=%q deadline=%v source_msg_ids=%v assignee_uids=%v",
		args.Title, args.Description, args.Deadline, args.SourceMsgIDs, args.AssigneeUIDs)

	// Hard requirements:
	if strings.TrimSpace(args.Title) == "" {
		t.Errorf("title is empty — model returned no useful extraction")
	}
	if strings.TrimSpace(args.Description) == "" {
		t.Errorf("description is empty")
	}

	// Soft expectations — log warnings so we can see model behaviour without
	// failing the whole gate (the validate step covers fallbacks).
	if args.Deadline == nil {
		t.Logf("WARN: deadline is null — model didn't infer a date despite the explicit 5/15 mention")
	} else {
		when := time.Unix(*args.Deadline, 0).UTC()
		t.Logf("Deadline parsed: %s", when.Format(time.RFC3339))
	}
	if len(args.SourceMsgIDs) == 0 {
		t.Errorf("source_msg_ids is empty — model did not select supporting message ids")
	}
	if len(args.AssigneeUIDs) == 0 {
		t.Errorf("assignee_uids is empty — model did not identify any assignees")
	}

	// Validate the cleaning pipeline accepts what the real model returned:
	v := validateExtractArgs(args, in)
	t.Logf("After validation: assignees=%v source_msgs=%v deadline=%v",
		v.Assignees, v.SourceMsgs, v.Deadline)
	if len(v.Assignees) == 0 {
		t.Errorf("validated assignees empty — fallback also failed")
	}
	if len(v.SourceMsgs) == 0 {
		t.Errorf("validated source_msgs empty — fallback also failed")
	}
}

func TestSmoke_ExtractMatterProgress_AcceptsV3Schema(t *testing.T) {
	c := smokeClient(t)
	msgs := []ExtractMessage{
		{
			MessageID: "p_001",
			FromUID:   "uid_bob",
			FromUname: "Bob",
			Timestamp: time.Now().Add(-2 * time.Hour).Unix(),
			Content:   "Demo 脚本初稿写完了，发给 Alice review。",
		},
		{
			MessageID: "p_002",
			FromUID:   "uid_alice",
			FromUname: "Alice",
			Timestamp: time.Now().Add(-90 * time.Minute).Unix(),
			Content:   "review 过了，整体 OK，第3段建议精简一下。",
		},
		{
			MessageID: "p_003",
			FromUID:   "uid_bob",
			FromUname: "Bob",
			Timestamp: time.Now().Add(-30 * time.Minute).Unix(),
			Content:   "改完了，已提交终稿。事项可以标记完成了。",
		},
	}

	// Build a synthetic system prompt similar to buildTimelineSystemPrompt
	// but minimal — the tool schema is what we want to test.
	sp := strings.Builder{}
	sp.WriteString("你是事项进展抽取助手。根据群聊消息提炼与目标事项相关的进展，必须通过 extract_matter_progress 函数返回。\n")
	sp.WriteString("字段约定：\n")
	sp.WriteString("  - content：一段话概括最新进展。\n")
	sp.WriteString("  - related_uids：本次进展涉及的人员 uid，必须从输入消息的 from_uid 中选取，不要编造。\n")
	sp.WriteString("  - source_msg_ids：从输入消息的 message_id 中选取支撑此进展的消息，不要编造或返回空数组。\n")
	sp.WriteString("  - status_suggestion：如果消息明显表达了完成（done）或重新打开（open），则返回相应字符串；否则返回 null。\n")
	sp.WriteString("【目标事项】\n  标题：Octo 智能事项 Demo 视频\n  当前状态：open\n")

	raw, err := c.CallTool(context.Background(), sp.String(), buildMessagesPrompt(msgs), extractProgressTool)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	t.Logf("LLM raw arguments:\n%s", raw)

	var args timelineToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("could not unmarshal LLM output into timelineToolArgs: %v", err)
	}
	statusStr := "<nil>"
	if args.StatusSuggestion != nil {
		statusStr = fmt.Sprintf("%q", *args.StatusSuggestion)
	}
	t.Logf("Parsed: content=%q related_uids=%v source_msg_ids=%v status_suggestion=%s",
		args.Content, args.RelatedUIDs, args.SourceMsgIDs, statusStr)

	if strings.TrimSpace(args.Content) == "" {
		t.Errorf("content is empty")
	}
	if len(args.RelatedUIDs) == 0 {
		t.Errorf("related_uids is empty")
	}
	if len(args.SourceMsgIDs) == 0 {
		t.Errorf("source_msg_ids is empty")
	}
	if args.StatusSuggestion == nil {
		t.Logf("WARN: status_suggestion is nil; expected 'done' given Bob said '可以标记完成了'")
	} else if *args.StatusSuggestion != "done" {
		t.Logf("WARN: status_suggestion=%q; expected 'done'", *args.StatusSuggestion)
	}

	v := validateTimelineArgs(args, msgs)
	statusStr2 := "<nil>"
	if v.StatusSuggestion != nil {
		statusStr2 = fmt.Sprintf("%q", *v.StatusSuggestion)
	}
	t.Logf("After validation: related_uids=%v source_msgs=%v status_suggestion=%s",
		v.RelatedUIDs, v.SourceMsgs, statusStr2)
}

func strPtrSmoke(s string) *string { return &s }
