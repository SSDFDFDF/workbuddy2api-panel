package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"workbuddy_manager/internal/media"
)

// 8 字节合法 PNG 头。
const testImage = "data:image/png;base64,iVBORw0KGgo="

func TestResponsesImageContentOrder(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[{"role":"user","content":[`+
		`{"type":"input_text","text":"前"},`+
		`{"type":"input_image","image_url":"`+testImage+`","detail":"high"},`+
		`{"type":"input_text","text":"后"}]}]}`)
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatal(msgs)
	}
	content, ok := msgs[0].(map[string]any)["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content=%#v", content)
	}
	if content[0].(map[string]any)["text"] != "前" || content[2].(map[string]any)["text"] != "后" {
		t.Fatalf("part order changed: %#v", content)
	}
	part := content[1].(map[string]any)
	if part["type"] != "image_url" {
		t.Fatalf("part=%#v", part)
	}
	imageURL := part["image_url"].(map[string]any)
	if imageURL["url"] != testImage || imageURL["detail"] != "high" {
		t.Fatalf("image_url=%#v", imageURL)
	}
}

func TestResponsesImageOnlyUserMessage(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"`+testImage+`"}]}]}`)
	content := r.Chat.Object["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "image_url" {
		t.Fatal(content)
	}
}

func TestResponsesImageRejections(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"assistant", `{"role":"assistant","content":[{"type":"input_image","image_url":"` + testImage + `"}]}`, "user messages"},
		{"system", `{"role":"system","content":[{"type":"input_image","image_url":"` + testImage + `"}]}`, "user messages"},
		{"file-id", `{"role":"user","content":[{"type":"input_image","file_id":"file-1"}]}`, "file_id"},
		{"external-url", `{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/x.png"}]}`, "external URLs"},
		{"object-form", `{"role":"user","content":[{"type":"input_image","image_url":{"url":"` + testImage + `"}}]}`, "string image_url required"},
		{"number-detail", `{"role":"user","content":[{"type":"input_image","image_url":"` + testImage + `","detail":5}]}`, "detail"},
		{"bad-image", `{"role":"user","content":[{"type":"input_image","image_url":"data:text/plain;base64,aGk="}]}`, "image/*"},
		{"tool-file", `{"type":"function_call_output","call_id":"a","output":[{"type":"input_file","file_id":"f"}]}`, "file results"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"x","store":false,"input":[` + tc.content + `]}`
			if strings.Contains(tc.content, "function_call_output") {
				body = `{"model":"x","store":false,"input":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}"},` + tc.content + `]}`
			}
			if _, err := Decode(Responses, []byte(body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

func TestAnthropicImageContent(t *testing.T) {
	r := mustDecode(t, Anthropic, `{"model":"x","max_tokens":10,"messages":[{"role":"user","content":[`+
		`{"type":"text","text":"看图"},`+
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}`)
	content, ok := r.Chat.Object["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content=%#v", content)
	}
	if content[0].(map[string]any)["text"] != "看图" {
		t.Fatal(content)
	}
	imageURL := content[1].(map[string]any)["image_url"].(map[string]any)
	if imageURL["url"] != testImage {
		t.Fatalf("image_url=%#v", imageURL)
	}
}

func TestAnthropicImageCacheControlDropped(t *testing.T) {
	r := mustDecode(t, Anthropic, `{"model":"x","max_tokens":10,"messages":[{"role":"user","content":[`+
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="},"cache_control":{"type":"ephemeral"}}]}]}`)
	raw, _ := json.Marshal(r.Chat.Object)
	if strings.Contains(string(raw), "cache_control") {
		t.Fatalf("cache_control forwarded: %s", raw)
	}
}

func TestAnthropicImageRejections(t *testing.T) {
	for _, tc := range []struct {
		name, message, want string
	}{
		{"url-source", `{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.test/x.png"}}]}`, "url image sources"},
		{"file-source", `{"role":"user","content":[{"type":"image","source":{"type":"file","file_id":"f"}}]}`, "file image sources"},
		{"assistant", `{"role":"assistant","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}`, "only supported in user messages"},
		{"bad-media-type", `{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"text/plain","data":"aGk="}}]}`, "image/*"},
		{"bad-base64", `{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!!!"}}]}`, "invalid base64"},
		{"tool-document", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"aGk="}}]}]}`, "no file part"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := ""
			if strings.Contains(tc.message, "tool_result") {
				prefix = `{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"f","input":{}}]},`
			}
			body := `{"model":"x","max_tokens":10,"messages":[` + prefix + tc.message + `]}`
			if _, err := Decode(Anthropic, []byte(body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

// Chat 原生路径同样走 forwarding.Parse 的媒体归一：字符串形态就地包装为对象，
// 对象形态一字不动（扩展字段保留）。
func TestChatImageShapeNormalized(t *testing.T) {
	r := mustDecode(t, Chat, `{"model":"x","messages":[`+
		`{"role":"user","content":[{"type":"image_url","image_url":"`+testImage+`"}]},`+
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"`+testImage+`","detail":"low","mime_type":"image/png"}}]}]}`)
	out, err := r.Chat.Encode("x")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	msgs := obj["messages"].([]any)
	first := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)
	if first["url"] != testImage || len(first) != 1 {
		t.Fatalf("string form not normalized on wire: %#v", first)
	}
	second := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)
	if second["detail"] != "low" || second["mime_type"] != "image/png" {
		t.Fatalf("object extensions lost: %#v", second)
	}
}

func TestChatImageRejectedBeforeUpstream(t *testing.T) {
	if _, err := Decode(Chat, []byte(`{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.test/x.png"}]}]}`)); err == nil || !strings.Contains(err.Error(), "external URLs") {
		t.Fatalf("err=%v", err)
	}
	if _, err := Decode(Chat, []byte(`{"model":"x","messages":[{"role":"user","content":[{"type":"audio_url","audio_url":{"url":"data:audio/wav;base64,aGk="}}]}]}`)); err == nil || !strings.Contains(err.Error(), "audio_url") {
		t.Fatalf("err=%v", err)
	}
	// 兄弟协议的图片 part 名在原生 Chat 里定向报错（不误放给上游报 11101）。
	for _, part := range []string{
		`{"type":"input_image","image_url":"` + testImage + `"}`,
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}`,
	} {
		if _, err := Decode(Chat, []byte(`{"model":"x","messages":[{"role":"user","content":[`+part+`]}]}`)); err == nil || !strings.Contains(err.Error(), "not a Chat content part") {
			t.Fatalf("err=%v", err)
		}
	}
}

// setToolPolicy 临时覆盖进程级工具图片策略，测试结束恢复 auto。
func setToolPolicy(t *testing.T, p media.ToolPolicy) {
	t.Helper()
	prev := media.CurrentToolPolicy()
	if err := media.SetToolPolicy(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.SetToolPolicy(prev) })
}

// toolImagePartOf 取 content 里第一个图片 part 的 url（断言用）。
func toolImagePartOf(t *testing.T, content any) string {
	t.Helper()
	parts, ok := content.([]any)
	if !ok {
		t.Fatalf("content=%#v", content)
	}
	for _, rv := range parts {
		part, _ := rv.(map[string]any)
		if part["type"] != "image_url" {
			continue
		}
		imageURL, _ := part["image_url"].(map[string]any)
		url, _ := imageURL["url"].(string)
		return url
	}
	t.Fatalf("no image part in %#v", content)
	return ""
}

const toolImageBody = `{"type":"function_call_output","call_id":"call_1","output":[` +
	`{"type":"input_text","text":"screenshot saved"},` +
	`{"type":"input_image","image_url":"` + testImage + `"}]}`

// 桥接入口的工具结果图片默认抬升：tool 内容只留文本，图片改为工具批次之后的
// user 消息（官方 custom-model-tool-media-hoist 同构）。
func TestResponsesToolImageHoist(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[`+
		`{"role":"user","content":"read the file"},`+
		`{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"},`+toolImageBody+`]}`)
	if !r.Mutated {
		t.Fatal("hoist must mark the request as mutated")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages=%#v", msgs)
	}
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool message changed: %#v", tool)
	}
	content := tool["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "screenshot saved" {
		t.Fatalf("tool content must keep text only: %#v", content)
	}
	user := msgs[3].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("hoisted images must be a user message: %#v", user)
	}
	parts := user["content"].([]any)
	if len(parts) != 3 {
		t.Fatalf("user content=%#v", parts)
	}
	if parts[0].(map[string]any)["text"] != media.HoistHeader {
		t.Fatalf("header=%#v", parts[0])
	}
	if parts[1].(map[string]any)["text"] != "call call_1:" {
		t.Fatalf("provenance label=%#v", parts[1])
	}
	if got := toolImagePartOf(t, user["content"]); got != testImage {
		t.Fatalf("url=%q", got)
	}
}

// 图片是工具结果的全部内容时，tool 内容用占位文本（不产出空 content）。
func TestToolImageOnlyUsesPlaceholder(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[`+
		`{"type":"function_call","call_id":"c","name":"shot","arguments":"{}"},`+
		`{"type":"function_call_output","call_id":"c","output":[{"type":"input_image","image_url":"`+testImage+`"}]}]}`)
	msgs := r.Chat.Object["messages"].([]any)
	if got := msgs[1].(map[string]any)["content"]; got != "(see attached image)" {
		t.Fatalf("placeholder=%#v", got)
	}
	if len(msgs) != 3 || msgs[2].(map[string]any)["role"] != "user" {
		t.Fatalf("messages=%#v", msgs)
	}
}

// 连续多个 tool 消息：user 图片消息只插在整批之后，不切断配对顺序，且按批次顺序
// 保留来源标注。
func TestToolImageHoistKeepsToolBatchOrder(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[`+
		`{"type":"function_call","call_id":"a","name":"shot","arguments":"{}"},`+
		`{"type":"function_call","call_id":"b","name":"shot","arguments":"{}"},`+
		`{"type":"function_call_output","call_id":"a","output":[{"type":"input_image","image_url":"`+testImage+`"}]},`+
		`{"type":"function_call_output","call_id":"b","output":[{"type":"input_text","text":"b text"},{"type":"input_image","image_url":"`+testImage+`"}]},`+
		`{"role":"user","content":"continue"}]}`)
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages=%#v", msgs)
	}
	if msgs[1].(map[string]any)["tool_call_id"] != "a" || msgs[2].(map[string]any)["tool_call_id"] != "b" {
		t.Fatalf("tool order changed: %#v", msgs)
	}
	if msgs[3].(map[string]any)["role"] != "user" {
		t.Fatalf("user image message must follow the whole batch: %#v", msgs)
	}
	parts := msgs[3].(map[string]any)["content"].([]any)
	if len(parts) != 5 { // header + (label,image) + (label,image)
		t.Fatalf("parts=%#v", parts)
	}
	if parts[1].(map[string]any)["text"] != "call a:" || parts[3].(map[string]any)["text"] != "call b:" {
		t.Fatalf("labels=%#v", parts)
	}
	if parts[0].(map[string]any)["text"] != media.HoistHeader {
		t.Fatalf("header=%#v", parts[0])
	}
	// 原尾随 user 消息必须还在（内容未被吞掉）。
	if tail := msgs[4].(map[string]any); tail["role"] != "user" || tail["content"] != "continue" {
		t.Fatalf("trailing user message lost: %#v", tail)
	}
}

// Anthropic tool_result 里的图片同样抬升。
func TestAnthropicToolImageHoist(t *testing.T) {
	r := mustDecode(t, Anthropic, `{"model":"x","max_tokens":10,"messages":[`+
		`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"shot","input":{}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[`+
		`{"type":"text","text":"screen"},`+
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}]}`)
	if !r.Mutated {
		t.Fatal("hoist must mark the request as mutated")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%#v", msgs)
	}
	tool := msgs[1].(map[string]any)
	content := tool["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "screen" {
		t.Fatalf("tool content=%#v", content)
	}
	if got := toolImagePartOf(t, msgs[2].(map[string]any)["content"]); got != testImage {
		t.Fatalf("hoisted url=%q", got)
	}
}

// 显式 reject：命中即 400，参数指向第一个图片 part。
func TestToolImageRejectPolicy(t *testing.T) {
	setToolPolicy(t, media.ToolPolicyReject)
	body := `{"model":"x","store":false,"input":[` +
		`{"type":"function_call","call_id":"c","name":"shot","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"c","output":[{"type":"input_text","text":"t"},{"type":"input_image","image_url":"` + testImage + `"}]}]}`
	_, err := Decode(Responses, []byte(body))
	if err == nil || !strings.Contains(err.Error(), "reject") || !strings.Contains(err.Error(), ".content[1]") {
		t.Fatalf("err=%v", err)
	}
}

// 显式 passthrough：图片留在 tool 消息里（未经验证的上游形状，交给上游判定）。
func TestToolImagePassthroughPolicy(t *testing.T) {
	setToolPolicy(t, media.ToolPolicyPassthrough)
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[`+
		`{"type":"function_call","call_id":"c","name":"shot","arguments":"{}"},`+
		`{"type":"function_call_output","call_id":"c","output":[{"type":"input_text","text":"t"},{"type":"input_image","image_url":"`+testImage+`"}]}]}`)
	if r.Mutated {
		t.Fatal("passthrough must not mutate")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages=%#v", msgs)
	}
	if got := toolImagePartOf(t, msgs[1].(map[string]any)["content"]); got != testImage {
		t.Fatalf("url=%q", got)
	}
}

// 原生 Chat 默认 passthrough：既有转发行为不变（tool 图片原样保留、不重新序列化）。
func TestChatToolImageDefaultPassthrough(t *testing.T) {
	raw := `{"model":"x","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"shot","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":[{"type":"image_url","image_url":{"url":"` + testImage + `"}}],"custom":9007199254740993}]}`
	r := mustDecode(t, Chat, raw)
	if r.Mutated {
		t.Fatal("native Chat default must not rewrite tool images")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if got := toolImagePartOf(t, msgs[1].(map[string]any)["content"]); got != testImage {
		t.Fatalf("url=%q", got)
	}
}

// 原生 Chat 显式 hoist：图片抬升、Request.Mutated 置位（供 encodedRequest 重新序列化）。
func TestChatToolImageHoistWhenConfigured(t *testing.T) {
	setToolPolicy(t, media.ToolPolicyHoist)
	raw := `{"model":"x","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"shot","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":[{"type":"image_url","image_url":{"url":"` + testImage + `"}}],"custom":9007199254740993}]}`
	r := mustDecode(t, Chat, raw)
	if !r.Mutated {
		t.Fatal("explicit hoist must mark the request as mutated")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 3 || msgs[2].(map[string]any)["role"] != "user" {
		t.Fatalf("messages=%#v", msgs)
	}
	if got := msgs[1].(map[string]any)["content"]; got != "(see attached image)" {
		t.Fatalf("placeholder=%#v", got)
	}
}

// 官方客户端形态：tool 消息的 content 是「JSON 字符串装 parts 数组」时，显式 hoist
// 策略下同样抬升。
func TestToolImageJSONStringContent(t *testing.T) {
	setToolPolicy(t, media.ToolPolicyHoist)
	inner := `[{\"type\":\"image_url\",\"image_url\":{\"url\":\"` + testImage + `\"}}]`
	raw := `{"model":"x","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"Read","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"c","content":"` + inner + `"}]}`
	r := mustDecode(t, Chat, raw)
	if !r.Mutated {
		t.Fatal("JSON string parts must be hoisted")
	}
	msgs := r.Chat.Object["messages"].([]any)
	if got := msgs[1].(map[string]any)["content"]; got != "(see attached image)" {
		t.Fatalf("placeholder=%#v", got)
	}
	if got := toolImagePartOf(t, msgs[2].(map[string]any)["content"]); got != testImage {
		t.Fatalf("hoisted url=%q", got)
	}
}

// 工具返回的普通 JSON 文本（含数组、含 image_url 字段但非内联图片）不得被误当 parts。
func TestToolImageJSONStringOpaque(t *testing.T) {
	setToolPolicy(t, media.ToolPolicyHoist)
	for _, content := range []string{
		`[{\"type\":\"image_url\",\"image_url\":{\"url\":\"https://example.test/x.png\"}}]`,
		`[{\"type\":\"photo\",\"data\":\"not parts\"}]`,
		`{\"type\":\"image_url\"}`,
		`not json at all data:image/png;base64,aGk=`,
	} {
		raw := `{"model":"x","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":` + mustJSON(t, content) + `}]}`
		r, err := Decode(Chat, []byte(raw))
		if err != nil {
			t.Fatalf("err=%v content=%s", err, content)
		}
		if r.Mutated {
			t.Fatalf("opaque tool text was reinterpreted: %s", content)
		}
		if got := r.Chat.Object["messages"].([]any)[1].(map[string]any)["content"]; got != content {
			t.Fatalf("content changed: %#v", got)
		}
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 批次内首条 tool 无图、次条有图：抬升后每条工具结果只出现一次，且配对顺序不变。
// 回归：早期实现会重复批次前缀（同一 tool_call_id 出现两次），使这份合法请求被
// 网关自己的「tool_call_id 无匹配」校验拒掉。
func TestToolImageHoistBatchPrefixNotDuplicated(t *testing.T) {
	r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[`+
		`{"type":"function_call","call_id":"a","name":"shot","arguments":"{}"},`+
		`{"type":"function_call","call_id":"b","name":"shot","arguments":"{}"},`+
		`{"type":"function_call_output","call_id":"a","output":"text only"},`+
		`{"type":"function_call_output","call_id":"b","output":[{"type":"input_text","text":"b"},{"type":"input_image","image_url":"`+testImage+`"}]}]}`)
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages=%#v", msgs)
	}
	var ids []string
	for _, v := range msgs {
		if id, ok := v.(map[string]any)["tool_call_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("tool results duplicated or reordered: %v", ids)
	}
	// 网关自己的配对校验必须仍然接受这份改写后的历史（tool 结果不重复、不缺配）。
	raw, err := json.Marshal(r.Chat.Object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(Chat, raw); err != nil {
		t.Fatalf("rewritten history rejected by the gateway validator: %v", err)
	}
}
