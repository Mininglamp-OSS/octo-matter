package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// legacyBuildExtractSystemPrompt is a verbatim copy of the pre-smart_create
// prompt, kept here ONLY for the comparison harness so we can A/B the new
// prompt against the regression baseline. Do not call from production code.
// `now` is injected (not read from time.Now()) so the legacy and new prompts
// reference the same wall clock — otherwise A/B runs spanning midnight would
// disagree on the date and confound the comparison.
func legacyBuildExtractSystemPrompt(in ExtractInput, now time.Time) string {
	var b strings.Builder
	b.WriteString("你是事项抽取助手。根据群聊消息提取出一个结构化事项，必须通过 extract_matter 函数返回。\n")
	b.WriteString("字段约定：\n")
	b.WriteString("  - title / description：根据消息内容生成。\n")
	b.WriteString("  - deadline：消息中明确提到截止时间时，返回 Unix 秒时间戳；否则返回 null，不要编造。\n")
	b.WriteString("  - source_msg_ids：从输入消息的 message_id 中精选最相关的，不要编造或返回空数组。\n")
	b.WriteString("  - assignee_uids：从输入消息发言人的 from_uid 中识别负责人，不要编造；无法识别时返回空数组（服务端会回退到 creator）。\n")
	b.WriteString(fmt.Sprintf("当前时间：%s\n", now.Format(time.RFC3339)))
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

// TestCompare_LegacyVsSmart runs the same fixtures through both prompts and
// logs a side-by-side comparison. Gated by LLM_COMPARE=1 so it never runs in
// CI by accident (each fixture spends 2 model calls).
//
// Run example:
//
//	LLM_COMPARE=1 \
//	LLM_API_URL=https://your-gateway/v1 \
//	LLM_API_KEY=... \
//	LLM_MODEL=... \
//	go test ./internal/service -run TestCompare_LegacyVsSmart -v -timeout 5m
func TestCompare_LegacyVsSmart(t *testing.T) {
	if os.Getenv("LLM_COMPARE") != "1" {
		t.Skip("LLM_COMPARE not set; skipping A/B comparison")
	}
	url, key, model := os.Getenv("LLM_API_URL"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL")
	if url == "" || key == "" || model == "" {
		t.Skip("LLM_API_URL / LLM_API_KEY / LLM_MODEL must all be set")
	}
	c := llm.New(url, key, model, 90*time.Second)

	// Fixed reference time so deadline parsing is reproducible.
	// 2026-05-14 is a Thursday.
	refNow := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	type fixture struct {
		name   string
		focus  string
		in     ExtractInput
		expect string
	}

	fixtures := []fixture{
		{
			name:  "F1_BotSaysWoLai",
			focus: "Bot 喊\"我来\" 时不得被选为 assignee",
			in: ExtractInput{
				SpaceID: "sp", ChannelID: "ch", ChannelName: ptrStrCmp("产品研发-AI 视频群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f1_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "下周五前要把 Octo 智能事项的 Demo 视频录好，给董事会看。"),
					mkMsg("f1_2", "uid_bot_video", "VideoBot", refNow.Add(-90*time.Minute), "[bot] 我来负责脚本和录制。"),
					mkMsg("f1_3", "uid_carol", "Carol", refNow.Add(-30*time.Minute), "剪辑我来。"),
				},
			},
			expect: "assignee 应只包含 uid_wyl 和/或 uid_carol，绝不含 uid_bot_video",
		},
		{
			name:  "F2_NoDeadline",
			focus: "无任何时间线索 → deadline 必须 null（不能 +7 天）",
			in: ExtractInput{
				SpaceID: "sp", ChannelID: "ch", ChannelName: ptrStrCmp("产研内部群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f2_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "所有产研群以后必须用 Kano 模型排 P0/P1/P2，不能再拍脑门。"),
					mkMsg("f2_2", "uid_whm", "吴明辉", refNow.Add(-90*time.Minute), "同意，我来推。"),
				},
			},
			expect: "deadline=null（消息里完全没提时间）",
		},
		{
			name:  "F3_RelativeDate",
			focus: "相对日期\"下周三前\" + 当前日期(星期四)，新 prompt 应能算对",
			in: ExtractInput{
				SpaceID: "sp", ChannelID: "ch", ChannelName: ptrStrCmp("Octo 设计群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f3_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "Demo 视频下周三前出第一版给我看。"),
					mkMsg("f3_2", "uid_carol", "Carol", refNow.Add(-90*time.Minute), "好的，我来排进度。"),
				},
			},
			// 2026-05-14 is Thursday; 下周三 = 2026-05-20.
			expect: "deadline ≈ 2026-05-20（下周三）",
		},
		{
			name:  "F4_NoiseDominantDecisionBuried",
			focus: "大量闲聊 + 决策埋在最后一条，source_msg_ids 应剔除闲聊；title 不该用\"关于…\"前缀",
			in: ExtractInput{
				SpaceID: "sp", ChannelID: "ch", ChannelName: ptrStrCmp("财务对账群"),
				CreatorUID: "uid_lily",
				Messages: []ExtractMessage{
					mkMsg("f4_1", "uid_lily", "Lily", refNow.Add(-3*time.Hour), "中午吃啥"),
					mkMsg("f4_2", "uid_tom", "Tom", refNow.Add(-150*time.Minute), "火锅吧"),
					mkMsg("f4_3", "uid_lily", "Lily", refNow.Add(-140*time.Minute), "👍"),
					mkMsg("f4_4", "uid_tom", "Tom", refNow.Add(-130*time.Minute), "@all 周一前把 4 月对账单整理给财务，发到 finance@ 邮箱。Lily 你来汇总下。"),
					mkMsg("f4_5", "uid_lily", "Lily", refNow.Add(-120*time.Minute), "收到。"),
				},
			},
			expect: "title 动宾结构、不含\"关于\"；source_msg_ids 不含 f4_1/f4_2/f4_3/f4_5；assignee=uid_lily",
		},
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			oldArgs, oldErr := runOne(t, c, legacyBuildExtractSystemPrompt(fx.in, refNow), buildMessagesPrompt(fx.in.Messages))
			newArgs, newErr := runOne(t, c, buildExtractSystemPrompt(fx.in, refNow), buildMessagesPrompt(fx.in.Messages))

			t.Logf("========== %s ==========", fx.name)
			t.Logf("focus    : %s", fx.focus)
			t.Logf("expect   : %s", fx.expect)
			t.Logf("--- LEGACY ---")
			logArgs(t, oldArgs, oldErr)
			t.Logf("--- SMART  ---")
			logArgs(t, newArgs, newErr)
		})
	}
}

func runOne(t *testing.T, c *llm.Client, sys, usr string, opts ...llm.CallOption) (extractToolArgs, error) {
	t.Helper()
	raw, err := c.CallTool(context.Background(), sys, usr, extractMatterTool, opts...)
	if err != nil {
		return extractToolArgs{}, err
	}
	var args extractToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return extractToolArgs{}, fmt.Errorf("unmarshal: %w; raw=%s", err, raw)
	}
	return args, nil
}

// TestCompare_LegacyVsSmart_NRuns is the statistical counterpart to
// TestCompare_LegacyVsSmart: each fixture × each prompt is sampled N times,
// then per-rule pass rates are reported as "x/N". This is what lets us claim
// "P2 improved bot-exclusion from 4/10 to 10/10" with a straight face — single
// runs would be sampling noise.
//
// LEGACY uses gateway-default temperature (the pre-P2.6 baseline). SMART uses
// the production-aligned WithTemperature(0.2) + WithMaxTokens. So the delta
// captures the *combined* effect of P2.1 (date string) + P2.2 (filter words)
// + P2.6 (low temperature) — i.e., what the next deploy actually ships.
//
// Run example:
//
//	LLM_COMPARE=1 LLM_COMPARE_N=10 \
//	LLM_API_URL=... LLM_API_KEY=... LLM_MODEL=... \
//	go test ./internal/service -run TestCompare_LegacyVsSmart_NRuns -v -timeout 20m
func TestCompare_LegacyVsSmart_NRuns(t *testing.T) {
	if os.Getenv("LLM_COMPARE") != "1" {
		t.Skip("LLM_COMPARE not set; skipping N-run A/B comparison")
	}
	url, key, model := os.Getenv("LLM_API_URL"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL")
	if url == "" || key == "" || model == "" {
		t.Skip("LLM_API_URL / LLM_API_KEY / LLM_MODEL must all be set")
	}
	n := envIntDefault("LLM_COMPARE_N", 10)
	concurrency := envIntDefault("LLM_COMPARE_CONCURRENCY", 4)
	c := llm.New(url, key, model, 120*time.Second)

	refNow := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	type fixture struct {
		name  string
		in    ExtractInput
		check func(a extractToolArgs) (rules map[string]bool) // rule → pass
	}

	fixtures := []fixture{
		{
			name: "F1_BotExcluded",
			in: ExtractInput{
				ChannelID: "ch", ChannelName: ptrStrCmp("产品研发-AI 视频群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f1_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "下周五前要把 Octo 智能事项的 Demo 视频录好，给董事会看。"),
					mkMsg("f1_2", "uid_bot_video", "VideoBot", refNow.Add(-90*time.Minute), "[bot] 我来负责脚本和录制。"),
					mkMsg("f1_3", "uid_carol", "Carol", refNow.Add(-30*time.Minute), "剪辑我来。"),
				},
			},
			check: func(a extractToolArgs) map[string]bool {
				botPicked := false
				for _, u := range a.AssigneeUIDs {
					if u == "uid_bot_video" {
						botPicked = true
					}
				}
				return map[string]bool{"bot 不在 assignee": !botPicked}
			},
		},
		{
			name: "F2_DeadlineNullWhenMissing",
			in: ExtractInput{
				ChannelID: "ch", ChannelName: ptrStrCmp("产研内部群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f2_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "所有产研群以后必须用 Kano 模型排 P0/P1/P2，不能再拍脑门。"),
					mkMsg("f2_2", "uid_whm", "吴明辉", refNow.Add(-90*time.Minute), "同意，我来推。"),
				},
			},
			check: func(a extractToolArgs) map[string]bool {
				return map[string]bool{"deadline=null": a.Deadline == nil}
			},
		},
		{
			name: "F3_RelativeDateCorrect",
			in: ExtractInput{
				ChannelID: "ch", ChannelName: ptrStrCmp("Octo 设计群"),
				CreatorUID: "uid_wyl",
				Messages: []ExtractMessage{
					mkMsg("f3_1", "uid_wyl", "王宜林", refNow.Add(-2*time.Hour), "Demo 视频下周三前出第一版给我看。"),
					mkMsg("f3_2", "uid_carol", "Carol", refNow.Add(-90*time.Minute), "好的，我来排进度。"),
				},
			},
			check: func(a extractToolArgs) map[string]bool {
				// refNow = 2026-05-14 Thu → 下周三 = 2026-05-20.
				correctYear := false
				exactDate := false
				if a.Deadline != nil {
					correctYear = strings.HasPrefix(*a.Deadline, "2026-")
					exactDate = *a.Deadline == "2026-05-20"
				}
				return map[string]bool{
					"deadline 年份=2026":    correctYear,
					"deadline=2026-05-20": exactDate,
				}
			},
		},
		{
			name: "F4_NoiseFilteredAndTitleClean",
			in: ExtractInput{
				ChannelID: "ch", ChannelName: ptrStrCmp("财务对账群"),
				CreatorUID: "uid_lily",
				Messages: []ExtractMessage{
					mkMsg("f4_1", "uid_lily", "Lily", refNow.Add(-3*time.Hour), "中午吃啥"),
					mkMsg("f4_2", "uid_tom", "Tom", refNow.Add(-150*time.Minute), "火锅吧"),
					mkMsg("f4_3", "uid_lily", "Lily", refNow.Add(-140*time.Minute), "👍"),
					mkMsg("f4_4", "uid_tom", "Tom", refNow.Add(-130*time.Minute), "@all 周一前把 4 月对账单整理给财务，发到 finance@ 邮箱。Lily 你来汇总下。"),
					mkMsg("f4_5", "uid_lily", "Lily", refNow.Add(-120*time.Minute), "收到。"),
				},
			},
			check: func(a extractToolArgs) map[string]bool {
				noise := map[string]bool{"f4_1": true, "f4_2": true, "f4_3": true, "f4_5": true}
				noiseLeak := false
				for _, id := range a.SourceMsgIDs {
					if noise[id] {
						noiseLeak = true
					}
				}
				titleFiller := false
				for _, p := range []string{"关于", "讨论", "针对"} {
					if strings.HasPrefix(strings.TrimSpace(a.Title), p) {
						titleFiller = true
					}
				}
				assigneeOK := false
				for _, u := range a.AssigneeUIDs {
					if u == "uid_lily" {
						assigneeOK = true
					}
				}
				return map[string]bool{
					"source 不含噪声":   !noiseLeak,
					"title 无空泛前缀":   !titleFiller,
					"assignee=Lily": assigneeOK,
				}
			},
		},
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			oldResults, oldErr := runMany(t, c, legacyBuildExtractSystemPrompt(fx.in, refNow), buildMessagesPrompt(fx.in.Messages), n, concurrency)
			newResults, newErr := runMany(t, c, buildExtractSystemPrompt(fx.in, refNow), buildMessagesPrompt(fx.in.Messages), n, concurrency,
				llm.WithTemperature(extractTemperature), llm.WithMaxTokens(extractMaxTokens))

			oldPass := tally(oldResults, fx.check)
			newPass := tally(newResults, fx.check)

			t.Logf("========== %s (N=%d) ==========", fx.name, n)
			t.Logf("                                             LEGACY     SMART")
			ruleNames := orderedRuleKeys(oldPass, newPass)
			for _, rule := range ruleNames {
				t.Logf("  %-40s   %2d/%d      %2d/%d", rule, oldPass[rule], n-oldErr, newPass[rule], n-newErr)
			}
			if oldErr > 0 || newErr > 0 {
				t.Logf("  (errored calls: LEGACY=%d, SMART=%d)", oldErr, newErr)
			}
		})
	}
}

// runMany fans out n CallTool requests at the given concurrency and returns
// the successful args plus the count of errored calls. The error count is
// load-bearing for the statistical report: pass-rate denominators must be
// (n - errored), not n, or a partially-failed batch looks like a clean batch.
func runMany(t *testing.T, c *llm.Client, sys, usr string, n, concurrency int, opts ...llm.CallOption) (ok []extractToolArgs, errored int) {
	t.Helper()
	results := make([]extractToolArgs, n)
	errs := make([]error, n)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			args, err := runOne(t, c, sys, usr, opts...)
			results[i] = args
			errs[i] = err
		}()
	}
	wg.Wait()
	ok = make([]extractToolArgs, 0, n)
	for i, e := range errs {
		if e != nil {
			t.Logf("call %d errored: %v", i, e)
			errored++
			continue
		}
		ok = append(ok, results[i])
	}
	return ok, errored
}

// tally counts per-rule passes across `results`. Always ensures every rule
// produced by `check` appears in the map with at least 0, so a "0/N" row is
// not silently dropped from the report.
func tally(results []extractToolArgs, check func(extractToolArgs) map[string]bool) map[string]int {
	pass := map[string]int{}
	for _, r := range results {
		for k, v := range check(r) {
			if _, ok := pass[k]; !ok {
				pass[k] = 0
			}
			if v {
				pass[k]++
			}
		}
	}
	return pass
}

// orderedRuleKeys returns the union of keys across both maps in a deterministic
// order (sorted alphabetically), so the printed comparison table is stable
// across runs — otherwise Go's randomised map iteration makes runs hard to
// eyeball-compare.
func orderedRuleKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	out := []string{}
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func envIntDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func logArgs(t *testing.T, a extractToolArgs, err error) {
	t.Helper()
	if err != nil {
		t.Logf("  ERROR: %v", err)
		return
	}
	dl := "<nil>"
	if a.Deadline != nil {
		dl = *a.Deadline
	}
	t.Logf("  title       : %q", a.Title)
	t.Logf("  description : %q", a.Description)
	t.Logf("  deadline    : %s", dl)
	t.Logf("  source_msgs : %v", a.SourceMsgIDs)
	t.Logf("  assignees   : %v", a.AssigneeUIDs)
}

func mkMsg(id, uid, name string, ts time.Time, body string) ExtractMessage {
	return ExtractMessage{
		MessageID: id, FromUID: uid, FromUname: name,
		Timestamp: ts.Unix(), Content: body,
	}
}

func ptrStrCmp(s string) *string { return &s }
