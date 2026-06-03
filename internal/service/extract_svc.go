package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm/promptstore"
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
	MessageID string `json:"message_id"`
	FromUID   string `json:"from_uid"`
	FromUname string `json:"from_uname"`
	Timestamp int64  `json:"timestamp"`
	Content   string `json:"content"`
	// ContentType is the IM message type (octo-lib ContentType). It is
	// optional and defaults to 0 ("unknown"); when set to 14 (RichText /
	// 图文混排) the Content string is a stringified RichText payload that must
	// be normalized to plain text before it reaches the LLM — see
	// messageDisplayContent. Older callers that omit this field still work:
	// a Content that is structurally a RichText block array is detected and
	// normalized regardless of the declared type, while ordinary text or
	// non-RichText JSON passes through verbatim (no silent field drop).
	ContentType int                        `json:"content_type,omitempty"`
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
	// Timezone is the IANA name (e.g. "Asia/Shanghai") that anchors the
	// prompt's "当前日期" field and the deadline year-range check. Empty
	// falls back to defaultExtractTimezone so a user typing "今天/本周五"
	// resolves in their calendar day, not UTC's. Unknown / unparseable
	// names also fall back rather than erroring — see resolveLocation.
	Timezone string
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

// activityStore is the narrow ActivityRepo surface used for best-effort
// activity recording.
type activityStore interface {
	Record(ctx context.Context, matterID, actorID, action string, detail interface{}) error
}

// ExtractService orchestrates an LLM-backed matter extraction from selected
// chat messages. The resulting matter is persisted via MatterService.
type ExtractService struct {
	llm       LLMToolCaller
	matterSvc *MatterService
	prompts   promptstore.Store
}

// ExtractOption configures an ExtractService at construction. The functional
// options pattern lets us add Langfuse / multi-store wiring later without
// breaking the existing two-arg constructor.
type ExtractOption func(*ExtractService)

// WithExtractPromptStore overrides the default embed-backed prompt source.
// Used by cmd/main.go to plug in a Langfuse-with-embed-fallback chain, and
// by tests that want to stub prompt content.
func WithExtractPromptStore(store promptstore.Store) ExtractOption {
	return func(s *ExtractService) { s.prompts = store }
}

func NewExtractService(llmClient LLMToolCaller, matterSvc *MatterService, opts ...ExtractOption) *ExtractService {
	s := &ExtractService{llm: llmClient, matterSvc: matterSvc, prompts: defaultPromptStore}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// extractToolArgs mirrors the JSON schema declared for `extract_matter`
// (defined in internal/llm/prompts/extract_matter.tool.json). The model
// returns:
//   - title / description: free text
//   - deadline: YYYY-MM-DD date string, or null if not inferable
//   - source_msg_ids: subset of input message IDs the matter is grounded in
//   - assignee_uids: subset of input from_uids identified as responsible
//
// Server-side validation in validateExtractArgs filters the lists to the
// input set so the model cannot fabricate references; an empty/all-invalid
// list falls back to safe defaults (all input msgs / [creator]).
type extractToolArgs struct {
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Deadline     flexibleDate `json:"deadline"`
	SourceMsgIDs []string     `json:"source_msg_ids"`
	AssigneeUIDs []string     `json:"assignee_uids"`
}

// flexibleDate decodes the model's `deadline` field tolerantly: a JSON string
// is captured as-is for downstream date parsing, while any other JSON shape
// (number, boolean, object, array) is silently treated as null. This is
// load-bearing during the int64-seconds → YYYY-MM-DD migration: a model that
// hasn't picked up the new schema and emits `"deadline": 1234567890` would
// otherwise blow up the entire `json.Unmarshal(raw, &args)` call and surface
// as `invalid arguments`, killing an otherwise-usable extraction (title /
// owners / source_msgs are still good). Failing soft here lets the matter
// land with `deadline=nil` instead.
//
// JSON-null and missing keys both decode to {raw=nil}; the value reaches
// parseLLMDeadline which already handles nil.
type flexibleDate struct {
	raw *string
}

// UnmarshalJSON accepts string-or-null, drops anything else to null. Note:
// we deliberately swallow the type-mismatch error rather than propagate it,
// because the caller already trusts json.RawMessage validity (we are still
// inside a well-formed JSON object). A genuinely malformed JSON document
// would have failed earlier at the encompassing Unmarshal call.
func (d *flexibleDate) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		d.raw = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		d.raw = &s
		return nil
	}
	d.raw = nil
	return nil
}

// The extract_matter tool schema lives in
// internal/llm/prompts/extract_matter.tool.json — loaded at runtime via
// the promptstore, not declared inline here.

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

	// defaultExtractTimezone anchors "当前日期" when the caller does not
	// supply ExtractInput.Timezone. Asia/Shanghai matches the primary
	// product user base; flipping this is a one-line change if we ever
	// need to default elsewhere.
	defaultExtractTimezone = "Asia/Shanghai"
)

// resolveLocation maps a caller-supplied IANA name to a *time.Location,
// falling back to defaultExtractTimezone — and then to UTC — when the name
// is empty or unknown. We deliberately avoid surfacing the error: a stale
// or typo'd timezone should not block extraction; an "off-by-one-day"
// prompt is preferable to a 5xx.
func resolveLocation(tz string) *time.Location {
	if tz == "" {
		tz = defaultExtractTimezone
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	if loc, err := time.LoadLocation(defaultExtractTimezone); err == nil {
		return loc
	}
	return time.UTC
}

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

	deadline := parseLLMDeadline(args.Deadline.raw, now)

	return extractValidated{
		Title:       clipRunes(args.Title, MaxLLMTitleLen),
		Description: clipRunes(args.Description, MaxLLMDescriptionLen),
		SourceMsgs:  msgs,
		Assignees:   assignees,
		Deadline:    deadline,
	}
}

// parseLLMDeadline turns the model's YYYY-MM-DD string into a midnight
// *time.Time anchored to `now.Location()`, with a year-range sanity check.
// Returns nil for any of: nil pointer, empty/whitespace, malformed date,
// parse error, or year outside the configured window. The window is
// deliberately narrow because the model has no business outputting
// "0001-01-01" or "2099-12-31" — those signal a hallucination.
//
// Timezone anchor matters: a user in Asia/Shanghai who types "5/15" expects
// the deadline boundary to land at 2026-05-15 00:00 +08:00, not at UTC
// midnight (which is 08:00 Shanghai). Using time.ParseInLocation with
// now.Location() keeps the calendar-day semantics consistent end-to-end —
// the same date the model sees in the prompt's "当前日期" is the wall-clock
// date that lands in the database.
//
// Note: the prompt also tells the model "若解析出的日期早于当前日期 >30 天，
// 则用次年" (typical use case: user says "5/15" in November). That rule is
// best-effort prompt-level only — this function does NOT roll a 4-months-stale
// date forward. An overdue deadline can be legitimate (catching up on a missed
// task), so silently mutating the year would do more harm than good. If we
// later need to enforce the rule, do it here, not in the model.
func parseLLMDeadline(raw *string, now time.Time) *time.Time {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
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

	// Anchor "now" to the caller's timezone so the prompt's "当前日期" and
	// the deadline year-range check both agree with the user's calendar day.
	now := time.Now().In(resolveLocation(in.Timezone))

	prompt, err := s.prompts.Get(ctx, promptExtractMatter)
	if err != nil {
		return nil, fmt.Errorf("load extract_matter prompt: %w", err)
	}
	systemPrompt, err := renderExtractSystemPrompt(prompt, in, now)
	if err != nil {
		return nil, fmt.Errorf("render extract_matter prompt: %w", err)
	}
	userPrompt := buildMessagesPrompt(in.Messages)

	raw, err := s.llm.CallTool(ctx, systemPrompt, userPrompt, prompt.Tool,
		llm.WithTemperature(extractTemperature),
		llm.WithMaxTokens(extractMaxTokens),
		llm.WithSystemPromptCache(),
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

// extractPromptData is the data shape consumed by extract_matter.md.
// Mirror any field rename here in the template, or Render will fail loudly
// (missingkey=error).
type extractPromptData struct {
	CurrentDate string
	Weekday     string
	ChannelName string
	ChannelID   string
	CreatorUID  string
	Senders     []senderInfo
}

// buildExtractSystemPrompt is a thin wrapper over renderExtractSystemPrompt
// for callers (tests, smoke harness) that don't want to handle the error
// branch. The prompt is embedded at build time and validated by init() at
// process start, so a render error here is a programmer mistake — we panic
// rather than propagate. Production code uses renderExtractSystemPrompt
// directly via CreateFromMessages and wraps the error properly.
//
// `now` is injected (not pulled from time.Now()) so the prompt is
// deterministic under test. Production callers pass
// `time.Now().In(resolveLocation(in.Timezone))` so the "当前日期" line
// matches the user's calendar day, not UTC's.
//
// This helper always reads from the package-level defaultPromptStore and
// IGNORES any per-service override set via WithExtractPromptStore.
// Production code must go through ExtractService.CreateFromMessages,
// which honours the injected store; this helper exists only so test code
// (anchor tests, golden tests, smoke harness) does not need a full
// ExtractService instance to render the prompt for an LLM call.
func buildExtractSystemPrompt(in ExtractInput, now time.Time) string {
	prompt, err := defaultPromptStore.Get(context.Background(), promptExtractMatter)
	if err != nil {
		panic("service: extract_matter prompt unavailable: " + err.Error())
	}
	out, err := renderExtractSystemPrompt(prompt, in, now)
	if err != nil {
		panic("service: extract_matter prompt render: " + err.Error())
	}
	return out
}

func renderExtractSystemPrompt(prompt promptstore.Prompt, in ExtractInput, now time.Time) (string, error) {
	data := extractPromptData{
		CurrentDate: now.Format("2006-01-02"),
		Weekday:     weekdayCN(now.Weekday()),
		ChannelID:   in.ChannelID,
		CreatorUID:  in.CreatorUID,
		Senders:     uniqueSenders(in.Messages),
	}
	if in.ChannelName != nil {
		data.ChannelName = *in.ChannelName
	}
	return prompt.Render(data)
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
		content := messageDisplayContent(m)
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
