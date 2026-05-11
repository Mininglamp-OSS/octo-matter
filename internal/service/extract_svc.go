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
	CallTool(ctx context.Context, systemPrompt, userPrompt string, tool llm.Tool) (string, error)
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
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Deadline     *int64   `json:"deadline"`
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
					"type":        []interface{}{"number", "null"},
					"description": "Unix 时间戳（秒）。从消息中推断的截止时间。无法推断时返回 null，不要编造。",
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
	// maxReasonableDeadlineUnix bounds an inferred deadline to year 2100;
	// anything past that is almost certainly a hallucination (and 9999-year
	// timestamps break some downstream date pickers).
	maxReasonableDeadlineUnix = int64(4_102_444_800) // 2100-01-01 UTC
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
// timestamp. Pure function — no I/O — so it is easily table-tested.
func validateExtractArgs(args extractToolArgs, in ExtractInput) extractValidated {
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

	var deadline *time.Time
	if args.Deadline != nil && *args.Deadline > 0 && *args.Deadline < maxReasonableDeadlineUnix {
		t := time.Unix(*args.Deadline, 0).UTC()
		deadline = &t
	}

	return extractValidated{
		Title:       clipRunes(args.Title, MaxLLMTitleLen),
		Description: clipRunes(args.Description, MaxLLMDescriptionLen),
		SourceMsgs:  msgs,
		Assignees:   assignees,
		Deadline:    deadline,
	}
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

	systemPrompt := buildExtractSystemPrompt(in)
	userPrompt := buildMessagesPrompt(in.Messages)

	raw, err := s.llm.CallTool(ctx, systemPrompt, userPrompt, extractMatterTool)
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

	v := validateExtractArgs(args, in)

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

func buildExtractSystemPrompt(in ExtractInput) string {
	var b strings.Builder
	b.WriteString("你是事项抽取助手。根据群聊消息提取出一个结构化事项，必须通过 extract_matter 函数返回。\n")
	b.WriteString("字段约定：\n")
	b.WriteString("  - title / description：根据消息内容生成。\n")
	b.WriteString("  - deadline：消息中明确提到截止时间时，返回 Unix 秒时间戳；否则返回 null，不要编造。\n")
	b.WriteString("  - source_msg_ids：从输入消息的 message_id 中精选最相关的，不要编造或返回空数组。\n")
	b.WriteString("  - assignee_uids：从输入消息发言人的 from_uid 中识别负责人，不要编造；无法识别时返回空数组（服务端会回退到 creator）。\n")
	b.WriteString(fmt.Sprintf("当前时间：%s\n", time.Now().UTC().Format(time.RFC3339)))
	if in.ChannelName != nil && *in.ChannelName != "" {
		b.WriteString(fmt.Sprintf("频道名称：%s\n", *in.ChannelName))
	}
	b.WriteString(fmt.Sprintf("频道 ID：%s\n", in.ChannelID))
	b.WriteString(fmt.Sprintf("Creator UID：%s\n", in.CreatorUID))
	b.WriteString("参与者（uid → 姓名）：\n")
	for _, u := range uniqueSenders(in.Messages) {
		b.WriteString(fmt.Sprintf("  - %s → %s\n", u.UID, u.Name))
	}
	return b.String()
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
