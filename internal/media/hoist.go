// hoist.go 工具结果图片的策略执行（passthrough / reject / hoist）。
//
// 只处理 role=="tool" 的消息，且只在真正命中图片时才改写：纯文本请求（包括
// 纯文本的 tool 结果）在 passthrough/reject 下零解析、零分配。
package media

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// HoistHeader 抬升后 user 消息的首个文本 part（与官方客户端
	// custom-model-tool-media-hoist 插件同文案）。
	HoistHeader = "Attached image(s) from tool result:"
	// hoistPlaceholder 图片被抽走后 tool 内容无剩余 part 时的占位（官方同文案）。
	hoistPlaceholder = "(see attached image)"
)

// ApplyToolPolicy 按策略处理 messages 里工具结果携带的图片，返回是否改写了消息序列。
//   - passthrough / auto：零改动（连 JSON 字符串形态也不解析）；
//   - reject：命中即返回 *Error，Param 指向第一个命中的 part；
//   - hoist：抽出图片，在**整批**连续 tool 消息之后插入一条 user 图片消息。
//
// 抬升的图片消息形态（官方插件同构）：
//
//	{"role":"user","content":[
//	  {"type":"text","text":"Attached image(s) from tool result:"},
//	  {"type":"text","text":"call <tool_call_id>:"},   // 仅当该 tool 消息有 id
//	  {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}
//	]}
//
// 图片按工具结果原顺序排列，并保留来源 tool_call_id 便于多工具排查；tool 消息的
// 非图片 part 原样保留（图片抽走后无剩余 part 时用占位文本，不产出空 content）。
func ApplyToolPolicy(obj map[string]any, policy ToolPolicy) (bool, error) {
	if policy == ToolPolicyPassthrough || policy == ToolPolicyAuto {
		return false, nil
	}
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return false, nil
	}
	n := newNormalizer()
	// out 惰性分配（copy-on-write）：无媒体请求（包括纯文本的 tool 结果）不复制
	// messages 切片、不构造任何中间结构，与 passthrough 同为 0 分配。
	var out []any
	emit := func(v any) {
		if out != nil {
			out = append(out, v)
		}
	}
	// mutate 在首次需要改写时分配 out，并把**当前批次之前**的消息原样拷入。
	// 传批次起点（i）而不是首条带图消息的下标：out 尚未建立时，i 之前的消息会被
	// emit 丢弃，只能在这里补回；而 msgs[i:first] 由下面的循环统一追加，两者不重叠
	//（早期版本传 first，会把批次前缀重复追加一次）。
	mutate := func(at int) {
		if out == nil {
			out = append(make([]any, 0, len(msgs)+4), msgs[:at]...)
		}
	}
	for i := 0; i < len(msgs); {
		msg, ok := msgs[i].(map[string]any)
		if !ok || msg["role"] != "tool" {
			emit(msgs[i])
			i++
			continue
		}
		// 定位连续 tool 消息批次（工具结果必须整批相邻：插入的 user 消息必须落在
		// 整批之后——插在批次中间会切断 tool_call 与后续结果的相邻顺序）。
		// 先做零分配探测，命中媒体才进入提取与改写。
		end, first := i, -1
		for ; end < len(msgs); end++ {
			tm, ok := msgs[end].(map[string]any)
			if !ok || tm["role"] != "tool" {
				break
			}
			if first < 0 && hasToolMedia(tm) {
				first = end
			}
		}
		if first < 0 {
			// 无媒体：原消息对象原样输出（同一对象，零改动）。
			for _, v := range msgs[i:end] {
				emit(v)
			}
			i = end
			continue
		}
		mutate(i)
		for k := i; k < first; k++ {
			out = append(out, msgs[k])
		}
		attached := []any{map[string]any{"type": "text", "text": HoistHeader}}
		for k := first; k < end; k++ {
			tm := msgs[k].(map[string]any)
			item, err := extractToolMedia(n, tm, k)
			if err != nil {
				return false, err
			}
			if item == nil {
				// 防御：hasToolMedia 已判定命中，此处不应为空。
				out = append(out, tm)
				continue
			}
			if policy == ToolPolicyReject {
				return false, invalid(fmt.Sprintf("messages[%d].content[%d].type", item.index, item.first), rejectMessage)
			}
			tm["content"] = item.contentAfterHoist()
			out = append(out, tm)
			if item.callID != "" {
				attached = append(attached, map[string]any{"type": "text", "text": "call " + item.callID + ":"})
			}
			attached = append(attached, item.media...)
		}
		out = append(out, map[string]any{"role": "user", "content": attached})
		i = end
	}
	if out == nil {
		return false, nil
	}
	obj["messages"] = out
	return true, nil
}

// hasToolMedia 零分配探测一条 tool 消息是否携带图片：数组形态直接扫 part 类型；
// 字符串形态走与解析同一套窄判据（只在含 "data:image/" 时才真正解析 JSON，且
// 判据成立即意味着确实有内联图片，属罕见路径）。
func hasToolMedia(m map[string]any) bool {
	switch v := m["content"].(type) {
	case []any:
		for _, rv := range v {
			if part, ok := rv.(map[string]any); ok && part["type"] == "image_url" {
				return true
			}
		}
		return false
	case string:
		_, ok := parseToolPartsString(v)
		return ok
	}
	return false
}

// rejectMessage 工具结果图片在 reject 策略下的客户端提示（含替代做法）。
const rejectMessage = "tool result images are not forwarded under media.tool_images=reject; set media.tool_images=hoist to attach them as a user message, or send the image as a user message"

// toolMedia 一条 tool 消息里抽出的图片与保留内容。
type toolMedia struct {
	index  int    // messages 下标（错误路径用）
	callID string // tool_call_id（抬升后的来源标注）
	media  []any  // image_url part（对象形态，顺序保持）
	keep   []any  // 非媒体 part（原顺序，原样保留）
	first  int    // 首个媒体 part 在 content 中的下标（错误路径用）
}

// extractToolMedia 从一条 tool 消息的 content 里抽出图片 part。
// 返回 (nil, nil) 表示没有可处理的媒体（content 非数组/非 parts 字符串，或其中
// 没有图片）；形态非法返回 *Error（只有 hoist/reject 策略会走到这里）。
//
// index 是该方法在 messages 中的下标。
func extractToolMedia(n *normalizer, m map[string]any, index int) (*toolMedia, error) {
	content, ok := m["content"]
	if !ok || content == nil {
		return nil, nil
	}
	switch v := content.(type) {
	case []any:
		return toolMediaFromParts(n, m, index, v, false)
	case string:
		// 只有窄判据命中的「JSON 字符串装 parts」才继续解析（形状/资源校验见
		// parseToolPartsString 与 toolMediaFromParts）。
		parts, ok := parseToolPartsString(v)
		if !ok {
			return nil, nil
		}
		return toolMediaFromParts(n, m, index, parts, true)
	}
	return nil, nil
}

// toolMediaFromParts 按 part 类型拆分 media / keep。media 一律规范化为上游接受的
// 对象形态并复跑资源校验（字符串形态此前未经过 Normalize）。
func toolMediaFromParts(n *normalizer, m map[string]any, index int, parts []any, fromString bool) (*toolMedia, error) {
	out := &toolMedia{index: index}
	out.callID, _ = m["tool_call_id"].(string)
	out.first = -1
	for i, rv := range parts {
		part, ok := rv.(map[string]any)
		if !ok {
			if fromString {
				return nil, invalid(fmt.Sprintf("messages[%d].content[%d]", index, i), "tool result parts must be objects")
			}
			out.keep = append(out.keep, rv)
			continue
		}
		typ, _ := part["type"].(string)
		if typ != "image_url" {
			if fromString {
				// 字符串形态只放行本次能无损转换的两种 part，且 text 必须带字符串正文：
				// 其余类型（image_blob_ref / image / 未知）与畸形 text 一并拒绝——它们会被
				// 写回 tool content，绕过 Normalize 的类型检查（宁缺勿滥，不把畸形 part
				// 送上上游换一个难定位的 11101）。
				if typ != "text" {
					return nil, invalid(fmt.Sprintf("messages[%d].content[%d].type", index, i), "unsupported tool result part "+typ+" in JSON string content")
				}
				if _, ok := part["text"].(string); !ok {
					return nil, invalid(fmt.Sprintf("messages[%d].content[%d].text", index, i), "text part with string text required in JSON string content")
				}
			}
			out.keep = append(out.keep, part)
			continue
		}
		normalized, err := normalizeToolImagePart(n, part, fmt.Sprintf("messages[%d].content[%d]", index, i))
		if err != nil {
			return nil, err
		}
		if out.first < 0 {
			out.first = i
		}
		out.media = append(out.media, normalized)
	}
	if len(out.media) == 0 {
		return nil, nil
	}
	return out, nil
}

// normalizeToolImagePart 规范化并校验工具结果里的单个 image_url part，并在图片
// 策略开启时做转码/压缩（与用户图片走同一套 normalizer 与预算）。
func normalizeToolImagePart(n *normalizer, part map[string]any, path string) (map[string]any, error) {
	raw, ok := part["image_url"]
	if !ok {
		return nil, invalid(path+".image_url", "missing image_url")
	}
	if s, isString := raw.(string); isString {
		processed, _, err := n.image(s, path+".image_url")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": processed}}, nil
	}
	obj, isObject := raw.(map[string]any)
	if !isObject {
		return nil, invalid(path+".image_url", "image_url object (or string form) required")
	}
	url, _ := obj["url"].(string)
	processed, _, err := n.image(url, path+".image_url.url")
	if err != nil {
		return nil, err
	}
	// 对象形态保留扩展字段（detail / mime_type 等），只替换 url。
	if processed != url {
		obj["url"] = processed
	}
	return part, nil
}

// contentAfterHoist 返回图片抽走后 tool 消息应保留的 content：
// 有剩余 part → 保留原顺序的 parts 数组；全部是图片 → 占位文本（不产出空 content）。
func (e *toolMedia) contentAfterHoist() any {
	if len(e.keep) == 0 {
		return hoistPlaceholder
	}
	return e.keep
}

// parseToolPartsString 把「JSON 字符串装 parts 数组」解析成 parts。
//
// 官方客户端（Read 工具、MCP 工具结果）产出的就是这种形态，出站前由客户端自己
// JSON.parse 后规范化（逆向记录 §4.3）。网关侧判据刻意收窄，避免把工具返回的
// 普通 JSON 文本误当 parts 造成数据丢失：
//   - trim 后以 '[' 开头，且包含 "data:image/"；
//   - 必须是合法 JSON 数组、元素皆对象且带非空字符串 type；
//   - 至少一个 image_url part 的 url 是 data: 内联图片。
//
// 任一条不满足 → (nil,false)：content 按普通文本原样保留（不解析、不改写）。
func parseToolPartsString(s string) ([]any, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "[") || !strings.Contains(trimmed, "data:image/") {
		return nil, false
	}
	var parts []any
	if err := json.Unmarshal([]byte(trimmed), &parts); err != nil || len(parts) == 0 {
		return nil, false
	}
	hasInline := false
	for _, rv := range parts {
		part, ok := rv.(map[string]any)
		if !ok {
			return nil, false
		}
		typ, ok := part["type"].(string)
		if !ok || typ == "" {
			return nil, false
		}
		if typ != "image_url" {
			continue
		}
		url, _ := part["image_url"].(string)
		if url == "" {
			if obj, ok := part["image_url"].(map[string]any); ok {
				url, _ = obj["url"].(string)
			}
		}
		if strings.HasPrefix(url, "data:image/") {
			hasInline = true
		}
	}
	if !hasInline {
		return nil, false
	}
	return parts, true
}
