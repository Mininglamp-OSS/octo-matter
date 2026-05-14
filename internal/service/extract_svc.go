package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

const (
	// ExtractMaxMessages caps the per-request message count.
	ExtractMaxMessages = 200
	// ExtractMaxContentChars truncates each message body server-side.
	ExtractMaxContentChars = 500
	// ExtractMaxMessageIDLen bounds each input message_id. Validated server-side
	// since this id is persisted verbatim into matters.source_msg_ids; an
	// unbounded value would inflate the JSON column.
	ExtractMaxMessageIDLen = 255
)

// ExtractMessageAttachment is a single attachment carried by an input message.
type ExtractMessageAttachment struct {
	FileName string `json:"file_name"`
	FileURL  string `json:"file_url"`
}

// ExtractMessage is the frontend-supplied message payload.
type ExtractMessage struct {
	MessageID   string                     `json:"message_id"`
	FromUID     string                     `json:"from_uid"`
	FromUname   string                     `json:"from_uname"`
	Timestamp   int64                      `json:"timestamp"`
	Content     string                     `json:"content"`
	Attachments []ExtractMessageAttachment `json:"attachments,omitempty"`
}

// ExtractInput is the service-level input for matter extraction.
//
// CallerToken is the user's IM auth token (empty for bot callers). It is
// forwarded to octoim to verify the caller is a member of ChannelID before
// the source-channel link is written. Bot path
// (empty token) skips this check — see ExtractService.CreateFromMessages
// for the bot trust model rationale.
type ExtractInput struct {
	SpaceID     string
	ChannelType uint8
	ChannelID   string
	ChannelName *string
	CreatorUID  string
	CallerUIDs  []string
	CallerToken string
	Messages    []ExtractMessage
}

// ExtractResult mirrors the REST response body.
type ExtractResult struct {
	ID          string    `json:"id"`
	SeqNo       int       `json:"seq_no"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	SourceMsgs  []string  `json:"source_msgs"`
	Deadline    *int64    `json:"deadline"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// llmTool is the function-calling wrapper this service sends to the LLM gateway.
type llmCaller interface {
	CallTool(ctx context.Context, systemPrompt, userPrompt string, tool llm.Tool, opts ...llm.CallOption) (string, error)
}

// activityStore is the narrow ActivityRepo surface used for best-effort
// activity recording.
type activityStore interface {
	Record(ctx context.Context, matterID, actorID, action string, detail interface{}) error
}

// ExtractService orchestrates an LLM-backed matter extraction from selected
// chat messages. The resulting matter is persisted via MatterService.
type ExtractService struct {
	llm       llmCaller
	matterSvc *MatterService
}

func NewExtractService(llmClient llmCaller, matterSvc *MatterService) *ExtractService {
	return &ExtractService{llm: llmClient, matterSvc: matterSvc}
}

// extractToolArgs mirrors the JSON schema declared for `extract_matter`.
// Per design-v3.md §3.1, the model returns:
//   - title / description: free text
//   - deadline: Unix seconds, or null if not inferable
//   - source_msg_ids: subset of input message IDs the matter is grounded in
//   - assignee_uids: subset of input from_uids identified as responsible
//
// Server-side validation in validateExtractArgs filters the lists to the
// input set so the model cannot fabricate references; an empty/all-invalid
// list falls back to safe defaults (all input msgs / [creator]).
type extractToolArgs struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Deadline is a date in YYYY-MM-DD form (or null). We switched away from
	// Unix seconds because real models — even Claude Sonnet — emit wrong-year
	// timestamps (their internal year prior pre-dates the prompt's stated
	// "current date"). A date string forces them to spell the year out,
	// which they get right. Server-side parsing happens in validateExtractArgs.
	Deadline     *string  `json:"deadline"`
	SourceMsgIDs []string `json:"source_msg_ids"`
	AssigneeUIDs []string `json:"assignee_uids"`
}

var extractMatterTool = llm.Tool{
	Type: "function",
	Function: llm.ToolFunction{
		Name:        "extract_matter",
		Description: "从聊天记录中提取事项信息",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "事项标题，简洁明确，不超过100字",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "事项描述/目标",
				},
				"deadline": map[string]interface{}{
					"type":        []interface{}{"string", "null"},
					"pattern":     `^\d{4}-\d{2}-\d{2}$`,
					"description": "截止日期，YYYY-MM-DD 格式（如 2026-05-15）。仅在消息中明确提到时间时返回；无法推断时返回 null，不要编造。",
				},
				"source_msg_ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "与事项直接相关的 message_id 列表，必须从输入 msgs 中选取，不要编造。",
				},
				"assignee_uids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "推断的负责人 uid 列表，必须来自输入消息的 from_uid，不要编造。无法识别时返回空数组。",
				},
			},
			"required": []string{"title", "description", "deadline", "source_msg_ids", "assignee_uids"},
		},
	},
}

// LLM output safety limits. Title and description are clipped (not rejected)
// to keep the request fail-soft; the LLM prompt already says "<=100字" for
// title so any overshoot is treated as a model glitch rather than user
// intent. Limits match the manual-create handler binding so LLM-backed and
// manual writes share the same DB-safe envelope.
const (
	MaxLLMTitleLen       = 500
	MaxLLMDescriptionLen = 10000
	// deadlineYearLookback / deadlineYearLookahead bound the parsed YYYY-MM-DD
	// to a sane window around the request time. Anything outside is treated
	// as a hallucination (e.g., model emits "0001-01-01" or "2099-12-31").
	// One year back tolerates models that occasionally lag a year behind the
	// prompt's stated date; five years forward covers any realistic deadline.
	deadlineYearLookback  = 1
	deadlineYearLookahead = 5

	// extractTemperature governs sampling for the matter-extract call. Low
	// temperature suits the task — we want consistent field choices, not
	// creative writing. Empirically 0.2 keeps title naming stable while still
	// letting the model vary phrasing across reruns of the same input.
	extractTemperature = 0.2

	// extractMaxTokens caps the model's response. A complete extract_matter
	// JSON payload is well under 800 tokens; 2048 leaves headroom for the
	// occasional long description without rewarding runaway output.
	extractMaxTokens = 2048
)

// extractValidated holds the cleaned, server-trusted derivation of the LLM
// extraction. Each field is sanitized against the input message set so the
// model cannot fabricate references.
type extractValidated struct {
	Title       string
	Description string
	SourceMsgs  []string
	Assignees   []string
	Deadline    *time.Time
}

// validateExtractArgs filters the LLM-emitted fields against the input,
// applies safe fallbacks (all input msgs / [creator]), and parses the deadline
// string. Pure function — no I/O, no time.Now() — so it is easily table-tested.
// `now` anchors the year-range sanity check for the deadline.
func validateExtractArgs(args extractToolArgs, in ExtractInput, now time.Time) extractValidated {
	inputMsgIDs := make(map[string]struct{}, len(in.Messages))
	inputUIDs := make(map[string]struct{}, len(in.Messages)+1)
	if in.CreatorUID != "" {
		inputUIDs[in.CreatorUID] = struct{}{}
	}
	for _, m := range in.Messages {
		inputMsgIDs[m.MessageID] = struct{}{}
		if m.FromUID != "" {
			inputUIDs[m.FromUID] = struct{}{}
		}
	}

	seenMsg := make(map[string]struct{}, len(args.SourceMsgIDs))
	msgs := make([]string, 0, len(args.SourceMsgIDs))
	for _, id := range args.SourceMsgIDs {
		if _, ok := inputMsgIDs[id]; !ok {
			continue
		}
		if _, dup := seenMsg[id]; dup {
			continue
		}
		seenMsg[id] = struct{}{}
		msgs = append(msgs, id)
	}
	if len(msgs) == 0 {
		msgs = make([]string, 0, len(in.Messages))
		for _, m := range in.Messages {
			msgs = append(msgs, m.MessageID)
		}
	}

	seenUID := make(map[string]struct{}, len(args.AssigneeUIDs))
	assignees := make([]string, 0, len(args.AssigneeUIDs))
	for _, uid := range args.AssigneeUIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, ok := inputUIDs[uid]; !ok {
			continue
		}
		if _, dup := seenUID[uid]; dup {
			continue
		}
		seenUID[uid] = struct{}{}
		assignees = append(assignees, uid)
	}
	if len(assignees) == 0 {
		assignees = []string{in.CreatorUID}
	}

	deadline := parseLLMDeadline(args.Deadline, now)

	return extractValidated{
		Title:       clipRunes(args.Title, MaxLLMTitleLen),
		Description: clipRunes(args.Description, MaxLLMDescriptionLen),
		SourceMsgs:  msgs,
		Assignees:   assignees,
		Deadline:    deadline,
	}
}

// parseLLMDeadline turns the model's YYYY-MM-DD string into a UTC midnight
// *time.Time, with a year-range sanity check against `now`. Returns nil for
// any of: nil pointer, empty/whitespace, malformed date, parse error, or year
// outside the configured window. The window is deliberately narrow because
// the model has no business outputting "0001-01-01" or "2099-12-31" — those
// signal a hallucination, not a real deadline.
func parseLLMDeadline(raw *string, now time.Time) *time.Time {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	minYear := now.Year() - deadlineYearLookback
	maxYear := now.Year() + deadlineYearLookahead
	if t.Year() < minYear || t.Year() > maxYear {
		return nil
	}
	return &t
}

// clipRunes returns s truncated to at most max runes (NOT bytes), preserving
// UTF-8 validity. Empty input or non-positive max yields s unchanged / "".
func clipRunes(s string, max int) string {
	if s == "" || max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// CreateFromMessages runs the LLM extraction, persists the matter + channel
// link via MatterService, and records a best-effort `created` activity.
//
// Source-channel link gate: user path must be IM-verified as a member of
// ChannelID before any matter_channels row is written; bot path is allowed
// (one-shot trust at matter creation, see MatterService.RequireChannelMember
// for the rationale).
func (s *ExtractService) CreateFromMessages(ctx context.Context, in ExtractInput) (*ExtractResult, error) {
	if err := validateMessages(in.Messages); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.CreatorUID) == "" {
		return nil, apperr.ValidationError("creator_uid required", "creator_uid")
	}
	if strings.TrimSpace(in.ChannelID) == "" {
		return nil, apperr.ValidationError("channel_id required", "channel_id")
	}
	if err := s.matterSvc.RequireChannelMember(ctx, in.CallerToken, in.ChannelID, in.CallerUIDs); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	systemPrompt := buildExtractSystemPrompt(in, now)
	userPrompt := buildMessagesPrompt(in.Messages)

	raw, err := s.llm.CallTool(ctx, systemPrompt, userPrompt, extractMatterTool,
		llm.WithTemperature(extractTemperature),
		llm.WithMaxTokens(extractMaxTokens),
	)
	if err != nil {
		return nil, fmt.Errorf("llm extract_matter: %w", err)
	}
	var args extractToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("llm extract_matter: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Title) == "" {
		return nil, fmt.Errorf("llm extract_matter: empty title: %w", llm.ErrEmptyToolCall)
	}

	v := validateExtractArgs(args, in, now)

	chType := in.ChannelType
	channelIDCopy := in.ChannelID
	var chTypePtr *uint8
	if chType != 0 {
		ct := chType
		chTypePtr = &ct
	}
	var descPtr *string
	if desc := strings.TrimSpace(v.Description); desc != "" {
		descPtr = &desc
	}

	matter := &model.Matter{
		SpaceID:           in.SpaceID,
		Title:             v.Title,
		Description:       descPtr,
		CreatorID:         in.CreatorUID,
		Status:            model.MatterStatusOpen,
		Deadline:          v.Deadline,
		SourceChannelID:   &channelIDCopy,
		SourceChannelType: chTypePtr,
		SourceName:        in.ChannelName,
		SourceMsgIDs:      model.JSONStringSlice(v.SourceMsgs),
	}

	detail, err := s.matterSvc.CreateMatterWithAssignees(ctx, matter, v.Assignees)
	if err != nil {
		return nil, err
	}

	// Note: CreateMatterWithAssignees already records the "created" activity;
	// do NOT record a second one here.

	var deadlineTS *int64
	if v.Deadline != nil {
		ts := v.Deadline.Unix()
		deadlineTS = &ts
	}

	return &ExtractResult{
		ID:          detail.ID,
		SeqNo:       detail.SeqNo,
		Title:       detail.Title,
		Description: stringPtrOrEmpty(detail.Description),
		SourceMsgs:  v.SourceMsgs,
		Deadline:    deadlineTS,
		Status:      string(detail.Status),
		CreatedAt:   detail.CreatedAt,
	}, nil
}

func validateMessages(msgs []ExtractMessage) error {
	if len(msgs) == 0 {
		return apperr.ValidationError("msgs required", "msgs")
	}
	if len(msgs) > ExtractMaxMessages {
		return apperr.ValidationError(fmt.Sprintf("msgs exceeds limit of %d", ExtractMaxMessages), "msgs")
	}
	for i, m := range msgs {
		if len(m.MessageID) > ExtractMaxMessageIDLen {
			return apperr.ValidationError(
				fmt.Sprintf("msgs[%d].message_id exceeds limit of %d", i, ExtractMaxMessageIDLen),
				"msgs.message_id",
			)
		}
	}
	return nil
}

// buildExtractSystemPrompt assembles the system prompt for the extract_matter
// tool call. The `now` parameter is injected (not read from time.Now()) so the
// prompt is deterministic under test — production callers pass time.Now().UTC().
//
// Sections, in order:
//  1. Role + framing ("起草事项" not "做总结")
//  2. Field-level rules: title naming, description shape, deadline parsing,
//     source filtering, assignee priority (incl. agent/bot exclusion)
//  3. Defensive constraints (no fabrication, UID-only, agent ≠ owner)
//  4. Runtime context: current date (YYYY-MM-DD + weekday), channel, creator,
//     and the sender list the model must pick assignees from.
func buildExtractSystemPrompt(in ExtractInput, now time.Time) string {
	var b strings.Builder
	b.WriteString("你是 Octo Matter 的事项起草助手。用户从群聊里多选若干条关键消息，交给你蒸馏出一条新事项的字段。\n")
	b.WriteString("你的任务不是做总结，而是\"立事项\"：读完消息后判断这件事是什么、目标是什么、谁牵头、什么时候要。\n")
	b.WriteString("必须通过 extract_matter 函数返回，不要在普通文本里输出结果。\n\n")

	b.WriteString("字段规则：\n")
	b.WriteString("  - title：短、具体、可检索，≤ 20 汉字，优先动宾结构（如\"董事会 PPT 打磨\"\"Kano 模型推广到所有产研群\"）。\n")
	b.WriteString("    禁止套话前缀（\"关于…的讨论\"\"针对…的安排\"）、结尾标点、emoji。\n")
	b.WriteString("  - description：一段话讲清\"要做成什么 + 关键约束/重点\"，50–150 汉字。\n")
	b.WriteString("    必须保留原话的专有名词、数字、口号（tagline、代号、具体数值）。\n")
	b.WriteString("    反面例子：\"讨论了 PPT 相关事宜\"（空洞）、\"张三说 A，李四说 B\"（流水账）。\n")
	b.WriteString("  - deadline：消息中明确提到截止时间时，返回 YYYY-MM-DD 格式的日期字符串（如 \"2026-05-15\"）；解析\"周五前/下周三/月底\"等相对表达时基于下方\"当前日期\"。\n")
	b.WriteString("    消息里只给月/日时，使用当前日期的年份；若解析出的日期早于当前日期 >30 天，则用次年。\n")
	b.WriteString("    无任何时间线索时返回 null，不要编造、不要默认填\"一周后\"。\n")
	b.WriteString("    不要返回 Unix 时间戳、ISO datetime（含 T 和时区）、自然语言（\"下周三\"），必须是 \"YYYY-MM-DD\" 这种 10 字符日期。\n")
	b.WriteString("  - source_msg_ids：从输入 message_id 中精选最能支撑该事项的若干条，不要编造、不要返回空数组。\n")
	b.WriteString("    以下消息**一律不得**出现在 source_msg_ids，即使只有这几条也宁可少选：\n")
	b.WriteString("      · 寒暄、客套（\"早\"\"在吗\"\"打扰一下\"\"辛苦了\"）\n")
	b.WriteString("      · 单字/单词确认（\"收到\"\"好的\"\"嗯\"\"OK\"\"ok\"\"明白\"\"可以\"\"没问题\"\"行\"\"好\"\"对\"\"是\"）\n")
	b.WriteString("      · 纯表情 / 纯 emoji（\"👍\"\"✅\"\"😂\"\"哈哈\"\"[表情]\"）\n")
	b.WriteString("      · 文件预览截图、自动回执、加入/退出群通知\n")
	b.WriteString("    判断标准：若把这条消息从 timeline 里删掉，事项的 title / description / deadline / assignee 全都不变，则它就是噪声。\n")
	b.WriteString("  - assignee_uids：从下方\"参与者\"列表的 uid 中识别负责人，不要编造、不要输出表里没有的 uid。\n")
	b.WriteString("    优先级：(1) 显式认领（\"我来 / 我牵头 / 我负责 / 我接\"）的发言者 > (2) 被指派者（\"@X 你来 / 交给 X\"）> (3) 都没有则返回空数组（服务端回退到 creator）。\n")
	b.WriteString("    Bot / agent 账号（PPTBot、ResearchBot、HRBot、PMBot 等自动体）是工具，**不得作为 assignee**，即便它发了消息也不行。\n\n")

	b.WriteString("关键约束：\n")
	b.WriteString("  ❌ 禁止虚构 message_id 或 uid，未出现在输入中的一律不要输出。\n")
	b.WriteString("  ❌ 禁止把 bot / agent 当 assignee。\n")
	b.WriteString("  ❌ 禁止 title 出现\"讨论/关于/事宜\"等空泛词或结尾标点 emoji。\n")
	b.WriteString("  ❌ 禁止 deadline 编造（消息没说截止时间就返回 null）。\n")
	b.WriteString("  ✅ 输出语言跟随消息：中文消息 → 中文字段；英文消息 → 英文字段。\n\n")

	b.WriteString(fmt.Sprintf("当前日期：%s (%s)\n", now.Format("2006-01-02"), weekdayCN(now.Weekday())))
	if in.ChannelName != nil && *in.ChannelName != "" {
		b.WriteString(fmt.Sprintf("频道名称：%s\n", *in.ChannelName))
	}
	b.WriteString(fmt.Sprintf("频道 ID：%s\n", in.ChannelID))
	b.WriteString(fmt.Sprintf("Creator UID：%s\n", in.CreatorUID))
	b.WriteString("参与者（uid → 姓名，assignee_uids 只能来自这个列表）：\n")
	for _, u := range uniqueSenders(in.Messages) {
		b.WriteString(fmt.Sprintf("  - %s → %s\n", u.UID, u.Name))
	}
	return b.String()
}

// weekdayCN renders a time.Weekday as the Chinese form ("星期一" .. "星期日"),
// matching the convention used by smart_create_matter_prompt.md so the model
// has a stable anchor when parsing relative dates like "本周五".
func weekdayCN(w time.Weekday) string {
	names := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	return names[w]
}

func buildMessagesPrompt(msgs []ExtractMessage) string {
	var b strings.Builder
	b.WriteString("以下是与目标事项相关的聊天消息：\n\n")
	for _, m := range msgs {
		content := m.Content
		if r := []rune(content); len(r) > ExtractMaxContentChars {
			content = string(r[:ExtractMaxContentChars])
		}
		ts := time.Unix(m.Timestamp, 0).UTC().Format(time.RFC3339)
		b.WriteString(fmt.Sprintf("[%s] %s(%s) | msg=%s\n%s\n",
			ts, m.FromUname, m.FromUID, m.MessageID, content))
		if len(m.Attachments) > 0 {
			for _, a := range m.Attachments {
				b.WriteString(fmt.Sprintf("  附件：%s\n", a.FileName))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

type senderInfo struct {
	UID  string
	Name string
}

func uniqueSenders(msgs []ExtractMessage) []senderInfo {
	seen := map[string]bool{}
	out := make([]senderInfo, 0, len(msgs))
	for _, m := range msgs {
		if seen[m.FromUID] {
			continue
		}
		seen[m.FromUID] = true
		out = append(out, senderInfo{UID: m.FromUID, Name: m.FromUname})
	}
	return out
}

func stringPtrOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
