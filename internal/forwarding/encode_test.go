package forwarding

import (
	"encoding/json"
	"testing"

	"workbuddy_manager/internal/jsondoc"
)

// referenceEncode 旧实现的语义参照：marshal → 重新解析 → 就地转换 → marshal。
// 新的 Encode 用深拷贝替代这条往返，输出必须与之逐字节一致。
func referenceEncode(t *testing.T, obj map[string]any, model string) []byte {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := jsondoc.Object(raw)
	if err != nil {
		t.Fatal(err)
	}
	EncodeObject(fresh, model)
	out, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEncodeMatchesReferenceAcrossForms 覆盖线格式转换的全部形态：
// 嵌套 tool_choice、扁平 tool_choice 字符串、max_completion_tokens、三种
// reasoning 别名、thinking 冲突归一、以及未声明字段的保留。
func TestEncodeMatchesReferenceAcrossForms(t *testing.T) {
	cases := map[string]string{
		"最小":                   `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`,
		"函数工具选择":               `{"model":"m","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}},"messages":[{"role":"user","content":"hi"}]}`,
		"字符串工具选择":              `{"model":"m","tool_choice":"auto","messages":[{"role":"user","content":"hi"}]}`,
		"budget 改名":            `{"model":"m","max_completion_tokens":4096,"messages":[{"role":"user","content":"hi"}]}`,
		"effort 别名":            `{"model":"m","reasoningEffort":"high","messages":[{"role":"user","content":"hi"}]}`,
		"reasoning 对象":         `{"model":"m","reasoning":{"effort":"low","summary":"auto"},"messages":[{"role":"user","content":"hi"}]}`,
		"effort 关闭补 thinking":  `{"model":"m","reasoning_effort":"off","messages":[{"role":"user","content":"hi"}]}`,
		"thinking 启用保持":        `{"model":"m","reasoning_effort":"high","thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi"}]}`,
		"未知字段保留":               `{"model":"m","x_vendor":{"deep":[1,2,{"k":"v"}]},"messages":[{"role":"user","content":"hi"}]}`,
		"大整数与高精度小数":            `{"model":"m","seed":1234567890123456789,"temp":0.30000000000000004,"messages":[{"role":"user","content":"hi"}]}`,
		"已有 stream_options":    `{"model":"m","stream":true,"stream_options":{"include_usage":false},"messages":[{"role":"user","content":"hi"}]}`,
		"已有 reasoning_summary": `{"model":"m","reasoning_summary":"concise","messages":[{"role":"user","content":"hi"}]}`,
		"图片 part":              `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"看图"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"low"}}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := Parse([]byte(body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			before := jsondoc.CopyObject(r.Object) // 用于验证 Encode 不改动原文档
			got, err := r.Encode("cn:test-model")
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			want := referenceEncode(t, r.Object, "cn:test-model")
			if string(got) != string(want) {
				t.Fatalf("Encode 与旧实现语义不一致:\n got=%s\nwant=%s", got, want)
			}
			// 原文档不得被改动（Encode 必须从副本出发）。
			origJSON, _ := json.Marshal(r.Object)
			beforeJSON, _ := json.Marshal(before)
			if string(origJSON) != string(beforeJSON) {
				t.Fatalf("Encode 污染了调用方文档:\n got=%s\nwant=%s", origJSON, beforeJSON)
			}
		})
	}
}

// TestEncodeObjectAppliesWireConversions 直接断言转换结果（不依赖参照实现），
// 钉住出站契约本身：stream 恒 true、stream_options 补齐、model 被替换。
func TestEncodeObjectAppliesWireConversions(t *testing.T) {
	r, err := Parse([]byte(`{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := jsondoc.CopyObject(r.Object)
	EncodeObject(obj, "glm-5.2")
	if obj["model"] != "glm-5.2" {
		t.Errorf("model=%v", obj["model"])
	}
	if obj["stream"] != true {
		t.Errorf("stream=%v want true（出站恒流式）", obj["stream"])
	}
	so, ok := obj["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Errorf("stream_options=%v want {include_usage:true}", obj["stream_options"])
	}
	if obj["reasoning_summary"] != "auto" {
		t.Errorf("reasoning_summary=%v want auto", obj["reasoning_summary"])
	}
	// 原文档保持原样（调用方持有的是副本）
	if r.Object["model"] != "claude-x" || r.Object["stream"] != nil {
		t.Errorf("原文档被 EncodeObject 改动: %v", r.Object)
	}
}

// TestEncodeSizeStableForLargePayload 大 payload（base64 图片）下 Encode 不得
// 复制/改写图片字节本身：输出中的 data URL 必须与输入完全一致。
func TestEncodeSizeStableForLargePayload(t *testing.T) {
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	url := "data:image/jpeg;base64," + base64Std(payload)
	body := `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + url + `"}}]}]}`
	r, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Encode("m2")
	if err != nil {
		t.Fatal(err)
	}
	// 输出必须仍含完整原始 data URL（逐字节比对图片部分）
	if !containsBytes(out, []byte(url)) {
		t.Fatal("Encode 输出的图片 data URL 与输入不一致")
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func base64Std(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		v := uint32(chunk[0])<<16 | uint32(chunk[1])<<8 | uint32(chunk[2])
		out = append(out,
			alphabet[(v>>18)&63], alphabet[(v>>12)&63],
			alphabet[(v>>6)&63], alphabet[v&63])
		if n < 3 {
			for k := n; k < 3; k++ {
				out[len(out)-(3-k)] = '='
			}
		}
	}
	return string(out)
}
