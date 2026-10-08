package protocol

import (
	"errors"
	"fmt"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/media"
)

// mediaError 把 media 包的校验错误转成协议侧的统一 400 信封（Param/Message 原样），
// 让三个入口的错误路径与参数指向一致。
func mediaError(err error) error {
	var me *media.Error
	if errors.As(err, &me) {
		return invalid(me.Param, me.Message)
	}
	return err
}

// textPartFrom 校验一个文本类内容块并构造 Chat 文本 part。三种协议的 text 系
// 块（Responses input_text/output_text、Messages text）共用同一字段契约：
// output_text 额外允许空 annotations/logprobs。
func textPartFrom(m map[string]any, path, typ string) (map[string]any, error) {
	allowed := "type text"
	if typ == "output_text" {
		allowed += " annotations logprobs"
		for _, key := range []string{"annotations", "logprobs"} {
			if v := m[key]; v != nil {
				a, ok := v.([]any)
				if !ok || len(a) != 0 {
					return nil, invalid(path+"."+key, "only an empty array is supported")
				}
			}
		}
	}
	if err := fields(m, path, allowed); err != nil {
		return nil, err
	}
	s, err := stringValue(m["text"], path+".text")
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "text", "text": s}, nil
}

// responsesContent 把一个 Responses message 的 content 值转换为 Chat content：
// 文本 part 保持原顺序，只有 user 消息额外接受 input_image。
//
// assistant 历史与 system/developer 消息里的 input_image 明确拒绝：Chat 上游对
// 非 user 角色的图片 part 没有已验证形态，宁缺勿滥（客户端可把图片作为 user
// 输入重放）。
func responsesContent(v any, path, role string) (any, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return nil, invalid(path, "string or non-empty content array required")
	}
	assistant := role == "assistant"
	allowedText := "input_text and input_image"
	if assistant {
		allowedText = "input_text and output_text"
	} else if role != "user" {
		allowedText = "input_text"
	}
	out := make([]any, 0, len(a))
	for i, rv := range a {
		p := fmt.Sprintf("%s[%d]", path, i)
		m, err := object(rv, p)
		if err != nil {
			return nil, err
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "input_text", "output_text":
			if typ == "output_text" && !assistant {
				return nil, invalid(p+".type", "output_text is only valid in assistant history")
			}
			part, err := textPartFrom(m, p, typ)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "input_image":
			if role != "user" {
				return nil, invalid(p+".type", "images are only supported in user messages")
			}
			part, err := responsesImagePart(m, p)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		default:
			return nil, invalid(p+".type", "only "+allowedText+" are supported")
		}
	}
	return out, nil
}

// responsesImagePart 转换 Responses 的 input_image 内容块。官方要求的字段形态是
// 扁平字符串 image_url（SDK: ResponseInputImageParam.image_url: str），这里只接受
// 该形态；file_id / prompt_cache_breakpoint 等本网关无法表达的能力明确拒绝。
func responsesImagePart(m map[string]any, path string) (map[string]any, error) {
	if v := m["file_id"]; v != nil {
		return nil, invalid(path+".file_id", "file ids are not available in this stateless gateway; inline the image as a data URL")
	}
	if err := fields(m, path, "type image_url detail"); err != nil {
		return nil, err
	}
	url, ok := m["image_url"].(string)
	if !ok {
		return nil, invalid(path+".image_url", "string image_url required (data:image/*;base64 URL)")
	}
	detail := ""
	if v := m["detail"]; v != nil {
		s, err := stringValue(v, path+".detail")
		if err != nil {
			return nil, err
		}
		detail = s
	}
	if _, err := media.DataURL(url, path+".image_url"); err != nil {
		return nil, mediaError(err)
	}
	return media.Part(url, detail), nil
}

// anthropicImagePart 转换 Messages 的 image 内容块。上游只接受内联字节，因此只支持
// base64 source；url / file source 明确拒绝并给出替代做法（客户端内联）。
func anthropicImagePart(b map[string]any, path string) (map[string]any, error) {
	src, err := object(b["source"], path+".source")
	if err != nil {
		return nil, err
	}
	switch src["type"] {
	case "base64":
	case "url":
		return nil, invalid(path+".source.type", "url image sources are not supported by this gateway; inline the image as a base64 source")
	case "file":
		return nil, invalid(path+".source.type", "file image sources are not available in this stateless gateway; inline the image as a base64 source")
	default:
		return nil, invalid(path+".source.type", "base64 source required")
	}
	if err := fields(b, path, "type source"); err != nil {
		return nil, err
	}
	if err := fields(src, path+".source", "type media_type data"); err != nil {
		return nil, err
	}
	mimeType, err := stringValue(src["media_type"], path+".source.media_type")
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return nil, invalid(path+".source.media_type", "image/* media_type required")
	}
	data, err := stringValue(src["data"], path+".source.data")
	if err != nil {
		return nil, err
	}
	url := "data:" + mimeType + ";base64," + data
	if _, err := media.DataURL(url, path+".source.data"); err != nil {
		return nil, mediaError(err)
	}
	return media.Part(url, ""), nil
}

// responsesToolOutput 转换 Responses 工具结果 output：字符串与文本数组的形态、
// 空白与空结果语义与 toolResultText 一致（数组逐块转换，不拼接）；含 input_image
// 时转成 Chat 的 image_url part，由 media.ApplyToolPolicy 按策略抬升或透传。
func responsesToolOutput(v any, path string) (any, error) {
	a, ok := v.([]any)
	if !ok {
		// 字符串 / 非数组：沿用原有形态校验与字符串原样语义。
		return toolResultText(v, path, "input_text")
	}
	out := make([]any, 0, len(a))
	for i, rv := range a {
		p := fmt.Sprintf("%s[%d]", path, i)
		m, err := object(rv, p)
		if err != nil {
			return nil, err
		}
		switch m["type"] {
		case "input_text":
			part, err := textPartFrom(m, p, "input_text")
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "input_image":
			part, err := responsesImagePart(m, p)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "input_file":
			return nil, invalid(p+".type", "file results are not supported; the upstream chat API has no file part")
		default:
			return nil, invalid(p+".type", "only input_text and input_image are supported in tool results")
		}
	}
	return out, nil
}

// anthropicToolResult 转换 Messages 的 tool_result.content：字符串与文本块的形态、
// 空白与空结果语义与 toolResultText 一致（数组逐块转换，不拼接）；含 image 块时转
// 为 Chat 的 image_url part，其中图片只接受 base64 source（上游无外链/文件形态），
// 随后由 media.ApplyToolPolicy 按策略抬升或透传。
func anthropicToolResult(v any, path string) (any, error) {
	a, ok := v.([]any)
	if !ok {
		return toolResultText(v, path, "text")
	}
	out := make([]any, 0, len(a))
	for i, rv := range a {
		p := fmt.Sprintf("%s[%d]", path, i)
		m, err := object(rv, p)
		if err != nil {
			return nil, err
		}
		switch m["type"] {
		case "text":
			part, err := textPartFrom(m, p, "text")
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "image":
			part, err := anthropicImagePart(m, p)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "document":
			return nil, invalid(p+".type", "document results are not supported; the upstream chat API has no file part")
		default:
			return nil, invalid(p+".type", "only text and image are supported in tool results")
		}
	}
	return out, nil
}
