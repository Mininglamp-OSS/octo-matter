package service

import (
	"encoding/json"
	"strings"
)

// RichText (ContentType=14) 图文混排消息的抽取归一化。
//
// 背景：抽取入参 ExtractMessage.Content 是 flat string。type=14 图文混排消息
// 的正文是一段 stringified JSON（{"content":[...blocks...],"plain":"..."}）。
// 若把这段裸 JSON 直接塞给 LLM，会污染 prompt（JSON 噪声 → 抽取不完整/幻觉），
// 或在某些上游变成空串。本文件把 type=14 的 payload 重解析为纯文本：优先用
// 顶层 plain（server 权威生成），否则遍历 blocks 抽 text 拼接、image 注入占位符。
//
// 契约对齐 octo-lib/common/richtext.go（YUJ-2740/2745 已定基线）：字段名锁定为
// content（block 数组）+ plain（纯文本），image 占位符为 "[图片]"。这里刻意不
// 引入 octo-lib 依赖（其 gin 版本与本仓不一致，会拖动整条依赖树），而是自带一
// 份最小镜像实现，只覆盖展示/抽取所需的解析路径，并保持与 octo-lib 同语义。
const (
	// richTextContentType 是图文混排消息的 ContentType（=14）。
	richTextContentType = 14

	// richTextImagePlaceholder 在生成纯文本时替换 image block，对齐
	// octo-lib 的 RichTextImagePlaceholder。
	richTextImagePlaceholder = "[图片]"

	// richTextFallbackDisplay 是 type=14 payload 无法解析（裸 JSON 噪声 / 残缺）
	// 时的兜底展示文本。返回它而非原始 JSON，避免噪声进入 LLM prompt。
	richTextFallbackDisplay = "[富文本消息]"
)

// richTextBlock 是 content 数组中的单个 block。只取展示所需字段：text block 用
// Text，image block 用占位符（不关心 url/尺寸）。
//
// TODO(Phase 2): 目前只解析 type/text 两个字段，未来若引入带非文本语义的 block
// 类型（file / mention / link / card 等），需要在此显式扩展字段与
// buildRichTextPlain 的展示规则，避免被当作「未知 block」静默丢弃语义。
type richTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// buildRichTextPlain 遍历 blocks 生成纯文本，对齐 octo-lib BuildRichTextPlain：
// text block 取 Text；image block 注入占位符；未知 type 有 text 则写 text，
// 否则跳过（前向兼容二期扩展的 block 类型）。
func buildRichTextPlain(blocks []richTextBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch blk.Type {
		case "image":
			b.WriteString(richTextImagePlaceholder)
		case "text":
			b.WriteString(blk.Text)
		default:
			if blk.Text != "" {
				b.WriteString(blk.Text)
			}
		}
	}
	return b.String()
}

// richTextDisplayText 把一段 content 解析为展示纯文本，覆盖完整的宽松形态：
// block 数组、legacy 字符串 content、仅 plain。返回 (text, true) 表示已识别并
// 归一化；(_, false) 表示不是 RichText payload，调用方应按原始 content 处理。
//
// 适用范围：仅用于「显式 ContentType==14」路径。其宽松识别（含 content 为字符串
// 的 legacy 形态）会与普通业务 JSON 混淆，不能用于自动识别 —— 未声明 14 的消息
// 走 richTextDisplayTextBlocksOnly 的收窄白名单。
//
// 识别规则：必须是 JSON object 且至少含 content 或 plain 字段之一。
//
// 解析优先级（plain 优先契约）：plain → block 数组 → legacy 字符串 content →
// 兜底占位。顶层非空 plain 是 server 权威文本，必须在尝试解析（并可能失败于）
// 任何 content 形态之前先返回 —— content 形态不可信（对象 / 标量 / 坏数据）绝不
// 能盖过已存在的 plain，否则会丢字塌陷到兜底占位。
//
// 信任边界：本函数走「已落库消息的展示/抽取路径」，与 octo-lib
// GetRichTextDisplayText 一致地信任 server 生成的 plain；入站写入校验不在本函数
// 职责内（那条路径在 octo-server 上）。
func richTextDisplayText(payload string) (string, bool) {
	s := strings.TrimSpace(payload)
	if s == "" || s[0] != '{' {
		return "", false
	}
	var raw struct {
		Content json.RawMessage `json:"content"`
		Plain   *string         `json:"plain"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return "", false
	}
	hasContent := len(raw.Content) > 0 && string(raw.Content) != "null"
	if !hasContent && raw.Plain == nil {
		// 既无 content 又无 plain：不是 RichText 形态，交回原始路径。
		return "", false
	}

	// plain 优先：顶层非空 plain 是 server 权威文本。必须在解析 content 之前返回，
	// 这样 content 是对象 / 标量 / 坏形态导致的解析失败都不会盖过 plain（丢字）。
	if raw.Plain != nil && strings.TrimSpace(*raw.Plain) != "" {
		return *raw.Plain, true
	}

	// 无可用 plain，再尝试从 content 还原文本。
	if hasContent {
		var blocks []richTextBlock
		if err := json.Unmarshal(raw.Content, &blocks); err == nil {
			if plain := buildRichTextPlain(blocks); strings.TrimSpace(plain) != "" {
				return plain, true
			}
			// 识别为 RichText 但 blocks 拼接为空 → 兜底占位，避免空串/裸 JSON。
			return richTextFallbackDisplay, true
		}
		// content 不是 block 数组：回退兼容老版本「content 为纯字符串」。
		var cs string
		if err := json.Unmarshal(raw.Content, &cs); err == nil {
			if strings.TrimSpace(cs) != "" {
				return cs, true
			}
			return richTextFallbackDisplay, true
		}
		// content 既非数组又非字符串（对象 / 标量 / 坏形态），且无 plain 可用：
		// 类型已声明为 14，兜底占位而非泄漏裸 JSON 噪声。
		return richTextFallbackDisplay, true
	}

	// 仅有 plain 字段但为空串：识别为 RichText 但内容为空 → 兜底占位。
	return richTextFallbackDisplay, true
}

// richTextDisplayTextBlocksOnly 是 richTextDisplayText 的「收窄版」，仅用于
// 未显式声明 ContentType==14 的自动识别路径。
//
// 为什么要收窄：richTextDisplayText 把「content 为纯字符串」的 {"content":"..."}
// 也当作 legacy RichText 解析，但这种形态与普通业务 JSON（如
// {"content":"deploy","source":"ops"}）无法结构性区分 —— 自动识别它会把
// source 等字段静默丢掉，违背「非 RichText JSON 原样透传」的向后兼容承诺。
//
// 因此本函数只认结构上足够独特、不会误伤普通 JSON 的 block 数组形态：
// content 必须是 JSON 数组（[{type,text},...]）。content 是字符串 / 缺失 /
// 仅有 plain 等宽松形态一律返回 (_, false)，交回原始 content 路径，
// 这些宽松处理只保留给显式 ContentType==14。
func richTextDisplayTextBlocksOnly(payload string) (string, bool) {
	s := strings.TrimSpace(payload)
	if s == "" || s[0] != '{' {
		return "", false
	}
	var raw struct {
		Content json.RawMessage `json:"content"`
		Plain   *string         `json:"plain"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return "", false
	}
	// 必须有 content，且 content 必须解析为 block 数组 —— 这是唯一足够独特、
	// 自动识别不会误吞普通 JSON 的结构。
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return "", false
	}
	var blocks []richTextBlock
	if err := json.Unmarshal(raw.Content, &blocks); err != nil {
		// content 不是数组（可能是字符串/对象）→ 不在收窄白名单内。
		return "", false
	}
	// 数组元素必须真的长得像 RichText block（每个元素都有非空 type）。否则
	// 普通业务 JSON 的对象数组（如 {"content":[{"title":"x"}],"source":"ops"}）
	// 也能 unmarshal 成零值 block，自动识别会把它当 RichText 吞掉 source 等字段
	// —— 这正是收窄要避免的对称数据丢失。空数组也不算 RichText（无从判断）。
	if len(blocks) == 0 {
		return "", false
	}
	for _, blk := range blocks {
		if blk.Type == "" {
			return "", false
		}
	}

	// 走到这里已确认是 RichText block 数组：优先 server 权威 plain，其次按 blocks
	// 拼接，仍为空则兜底占位（结构已是 RichText，不应回退到原始 JSON）。
	if raw.Plain != nil && strings.TrimSpace(*raw.Plain) != "" {
		return *raw.Plain, true
	}
	if plain := buildRichTextPlain(blocks); strings.TrimSpace(plain) != "" {
		return plain, true
	}
	return richTextFallbackDisplay, true
}

// messageDisplayContent 返回一条抽取入参消息用于 LLM prompt 的纯文本正文。
//
//   - 显式 ContentType==14：走完整 RichText 归一化（plain → blocks → legacy
//     string content → 兜底占位）。payload 无法解析为合法 RichText（裸 JSON
//     噪声）→ 返回兜底占位，拒绝把原始 JSON 噪声塞给 LLM。
//   - 非 14 / 省略 ContentType：只对结构上足够独特的 block 数组形态做自动识别
//     归一化；content 为字符串、仅 plain、或普通业务 JSON 一律原样透传，
//     避免静默吞掉 source 等用户字段（向后兼容承诺）。
//   - 其它一律按原始 content 返回（旧 Content 路径完全不变）。
func messageDisplayContent(m ExtractMessage) string {
	if m.ContentType == richTextContentType {
		if text, ok := richTextDisplayText(m.Content); ok {
			return text
		}
		// 类型声明是图文混排，但 payload 不是合法 RichText 形态：避免 JSON 噪声。
		return richTextFallbackDisplay
	}
	// 未显式声明 14：仅自动识别 block 数组形态，普通文本/JSON 原样透传。
	if text, ok := richTextDisplayTextBlocksOnly(m.Content); ok {
		return text
	}
	return m.Content
}
