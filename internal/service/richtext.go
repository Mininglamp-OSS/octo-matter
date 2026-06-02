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

// richTextDisplayText 尝试把一段（可能是 RichText=14 的）content 解析为展示纯
// 文本。返回 (text, true) 表示 payload 被识别为 RichText 形态并已归一化；返回
// (_, false) 表示它不是 RichText payload，调用方应按原始 content 处理。
//
// 识别规则（避免对普通 JSON 文本误判）：必须是 JSON object 且至少含 content 或
// plain 字段之一，才算 RichText 形态。
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

	var blocks []richTextBlock
	if hasContent {
		if err := json.Unmarshal(raw.Content, &blocks); err != nil {
			// content 不是 block 数组：回退兼容老版本「content 为纯字符串」。
			var cs string
			if err2 := json.Unmarshal(raw.Content, &cs); err2 == nil {
				if raw.Plain != nil && strings.TrimSpace(*raw.Plain) != "" {
					return *raw.Plain, true
				}
				if strings.TrimSpace(cs) != "" {
					return cs, true
				}
				return richTextFallbackDisplay, true
			}
			// content 既非数组又非字符串：形态不可信，不当 RichText 处理。
			return "", false
		}
	}

	// 优先顶层 plain（server 权威），其次按 blocks 现场拼接。
	if raw.Plain != nil && strings.TrimSpace(*raw.Plain) != "" {
		return *raw.Plain, true
	}
	if plain := buildRichTextPlain(blocks); strings.TrimSpace(plain) != "" {
		return plain, true
	}
	// 识别为 RichText 但内容为空 → 兜底占位，避免空串/裸 JSON。
	return richTextFallbackDisplay, true
}

// messageDisplayContent 返回一条抽取入参消息用于 LLM prompt 的纯文本正文。
//
//   - 显式 ContentType==14，或 content 在结构上就是 RichText payload（向后兼容
//     前端尚未透传 type 的旧路径）→ 走 RichText 归一化。
//   - 显式标记为 14 但 payload 无法解析为合法 RichText（裸 JSON 噪声）→ 返回
//     兜底占位，拒绝把原始 JSON 噪声塞给 LLM。
//   - 其它一律按原始 content 返回（旧 Content 路径完全不变）。
func messageDisplayContent(m ExtractMessage) string {
	if text, ok := richTextDisplayText(m.Content); ok {
		return text
	}
	if m.ContentType == richTextContentType {
		// 类型声明是图文混排，但 payload 不是合法 RichText 形态：避免 JSON 噪声。
		return richTextFallbackDisplay
	}
	return m.Content
}
