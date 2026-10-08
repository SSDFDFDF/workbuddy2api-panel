// Package media implements the upstream image wire contract shared by the native
// Chat passthrough and the Responses / Messages bridges.
//
// WorkBuddy 上游只接受一种媒体输入（逆向记录见
// docs/WORKBUDDY_MULTIMODAL_ATTACHMENT_PROTOCOL.md）：
//
//		messages[].content[] = {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}
//
//	  - image_url 必须是**对象**：字符串形态上游返回 400 code=11101
//	    ("cannot unmarshal string into Go value of type v2.ImageContent")；
//	  - url 必须是**内联 data URL**：外链没有已验证的上游支持，网关**有意不代抓**
//	    （网关不是图片托管服务；代抓要引入 SSRF/隐私/尾延迟成本，且本项目客户端不产生
//	    该形态），请求明确 400 并请客户端内联字节。若日后真遇官方自家 CDN 外链，正确
//	    修法是**白名单透传**（不下载、只放行），而不是代抓。
//
// 本包只做形状归一、资源校验与已声明的图片变换（media.image_transcode /
// media.image_max_dimension，默认全关）：不抓远端、不为上游无法表达的 part 编造
// 降级路径（失败方向是明确报错）。
//
// 错误类型是本包自有的 *Error：media 不能 import forwarding（forwarding.Parse 需要
// 调用本包做归一），调用方（forwarding / protocol）负责把 Param/Message 转成各自的
// 协议错误信封。
package media

import (
	"fmt"
	"strings"
)

const (
	// MaxImageBytes 单张图片 base64 解码后的字节上限。5 MiB 是官方客户端
	// ImageUtils.prepareImageDataUrl 的硬限制（5.6.2 / 5.7.6 一致），超出会在
	// 客户端侧直接判 invalid base64 payload；网关提前拒绝并给出可操作提示。
	MaxImageBytes = 5 << 20

	// MaxImagesPerRequest 单请求图片数量上限。用于封顶请求体与解码内存放大；
	// "too many images and documents" 是上游真实错误形态，20 张足以覆盖一轮
	// 截图/多图问答。
	MaxImagesPerRequest = 20

	// MaxTotalImageBytes 单请求图片解码后累计字节上限（并行大图的内存封顶）。
	MaxTotalImageBytes = 24 << 20
)

// Error 媒体校验失败（Param 是面向客户端的参数路径，Message 是可操作说明）。
type Error struct{ Param, Message string }

func (e *Error) Error() string { return e.Param + ": " + e.Message }

func invalid(param, message string) error { return &Error{Param: param, Message: message} }

// Part 构造上游唯一接受的 Chat 图片 part。detail 为空时省略该字段（不补默认值，
// 由上游按其默认语义处理）。
func Part(url, detail string) map[string]any {
	imageURL := map[string]any{"url": url}
	if detail != "" {
		imageURL["detail"] = detail
	}
	return map[string]any{"type": "image_url", "image_url": imageURL}
}

// DataURL 校验一个内联图片 data URL 并返回其 base64 解码后的字节数。
// path 是错误信息里面向客户端的参数路径。只接受 data:image/*;base64,<payload>；
// 外链、非 image 媒体类型、非法 base64、空 payload、超过 MaxImageBytes 一律拒绝。
func DataURL(url, path string) (int, error) {
	_, payload, err := splitDataURL(url, path)
	if err != nil {
		return 0, err
	}
	return base64Size(payload)
}

// Normalize 原地归一 messages[].content[] 里的图片 part，并执行已声明的资源校验与
// （策略开启时的）像素级转码/压缩：
//   - 字符串形态 "image_url":"data:..." → {"url":"data:..."}（上游只认对象形态）；
//   - 对象形态只校验/替换 url（detail / mime_type 等扩展字段不动）；
//   - 上游无法表达的媒体 part（input_audio / audio_url / video_url / file_url /
//     input_file）明确拒绝，而不是让上游报难以定位的 11101。
//
// 返回请求内的图片数量。0 表示无媒体（纯文本路径零改动）；策略关闭时图片也不重编码。
func Normalize(obj map[string]any) (int, error) {
	n := newNormalizer()
	msgs, _ := obj["messages"].([]any)
	count, total := 0, 0
	for i, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for j, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := part["type"].(string)
			switch typ {
			case "image_url":
				path := fmt.Sprintf("messages[%d].content[%d]", i, j)
				url, err := imageURL(part, path)
				if err != nil {
					return count, err
				}
				// n.image 同时完成形状/大小校验与（策略开启时的）转码/压缩，
				// 并回报解码字节数：这里不再单独调 DataURL，避免同一 URL 解析多遍。
				processed, size, err := n.image(url, path+".image_url.url")
				if err != nil {
					return count, err
				}
				if processed != url {
					setImageURL(part, processed)
				}
				count++
				total += size
				if count > MaxImagesPerRequest {
					return count, invalid(path, fmt.Sprintf("at most %d images per request are supported", MaxImagesPerRequest))
				}
				if total > MaxTotalImageBytes {
					return count, invalid(path, fmt.Sprintf("images in one request exceed the %d-byte total limit", MaxTotalImageBytes))
				}
			case "input_audio", "audio_url", "video_url", "file_url", "input_file":
				// 纯文本热路径零分配：路径字符串只在命中媒体 part 时构造。
				return count, invalid(fmt.Sprintf("messages[%d].content[%d].type", i, j), "the upstream chat API has no "+typ+" part; only text and image_url are supported")
			case "input_image", "image":
				// 兄弟协议的图片 part 名（Responses / Messages）：上游 Chat 只认
				// image_url 对象形态，这里给出定向提示而不是让上游报 11101。
				return count, invalid(fmt.Sprintf("messages[%d].content[%d].type", i, j), typ+" is not a Chat content part; use {\"type\":\"image_url\",\"image_url\":{\"url\":\"data:image/*;base64,...\"}}")
			}
		}
	}
	return count, nil
}

// setImageURL 把处理后的 URL 写回 part（字符串与对象两种形态都兼容）。
func setImageURL(part map[string]any, url string) {
	if obj, ok := part["image_url"].(map[string]any); ok {
		obj["url"] = url
		return
	}
	part["image_url"] = map[string]any{"url": url}
}

// imageURL 归一单个图片 part 的 image_url 字段并返回待校验的 url。
// 字符串形态就地包装为对象；对象形态只取 url（其余字段一字不动）。
func imageURL(part map[string]any, path string) (string, error) {
	raw, ok := part["image_url"]
	if !ok {
		return "", invalid(path+".image_url", "missing image_url")
	}
	if s, isString := raw.(string); isString {
		if strings.TrimSpace(s) == "" {
			return "", invalid(path+".image_url", "non-empty data:image/*;base64 URL required")
		}
		part["image_url"] = map[string]any{"url": s}
		return s, nil
	}
	obj, isObject := raw.(map[string]any)
	if !isObject {
		return "", invalid(path+".image_url", "image_url object (or string form) required")
	}
	url, _ := obj["url"].(string)
	if strings.TrimSpace(url) == "" {
		return "", invalid(path+".image_url.url", "non-empty data:image/*;base64 URL required")
	}
	return url, nil
}

// HasImages 报告 messages[].content[] 是否携带 image_url part。用于 error.gateway_hint
// 的"请求确实带图"判定：基于已解析对象扫描，不受同一请求里字符串 content 消息
// 干扰（旧实现按 body 反序列化，混合形态会整体失败并漏判）。
func HasImages(obj map[string]any) bool {
	msgs, _ := obj["messages"].([]any)
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			if part, ok := rawPart.(map[string]any); ok && part["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

// base64Size 计算 base64 payload 的解码字节数，兼作格式校验（不实际解码，避免为
// 校验再分配一份图片内存）。容忍 MIME 习惯的换行/空白；标准字母表 + 可选 '=' 填充。
func base64Size(payload string) (int, error) {
	data, pad := 0, 0
	padded := false
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			if padded {
				return 0, errBase64
			}
			data++
		case c == '=':
			padded = true
			pad++
			if pad > 2 {
				return 0, errBase64
			}
		case c == '\n' || c == '\r' || c == ' ' || c == '\t':
			// 折行/空白不计入长度。
		default:
			return 0, errBase64
		}
	}
	total := data + pad
	if pad > 0 && total%4 != 0 {
		return 0, errBase64
	}
	rem := total % 4
	if rem == 1 {
		return 0, errBase64
	}
	size := total/4*3 + remBytes(rem) - pad
	if size < 0 {
		return 0, errBase64
	}
	return size, nil
}

func remBytes(rem int) int {
	switch rem {
	case 2:
		return 1
	case 3:
		return 2
	}
	return 0
}

var errBase64 = fmt.Errorf("invalid base64")
