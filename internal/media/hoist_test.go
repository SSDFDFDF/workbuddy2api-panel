package media

import (
	"strings"
	"testing"
)

func setPolicy(t *testing.T, p ToolPolicy) {
	t.Helper()
	prev := CurrentToolPolicy()
	if err := SetToolPolicy(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetToolPolicy(prev) })
}

func TestToolPolicyDefaults(t *testing.T) {
	setPolicy(t, ToolPolicyAuto)
	if got := ToolPolicyFor(false); got != ToolPolicyPassthrough {
		t.Fatalf("native Chat default=%q", got)
	}
	if got := ToolPolicyFor(true); got != ToolPolicyHoist {
		t.Fatalf("bridge default=%q", got)
	}
	setPolicy(t, ToolPolicyReject)
	if got := ToolPolicyFor(false); got != ToolPolicyReject || ToolPolicyFor(true) != ToolPolicyReject {
		t.Fatalf("explicit policy ignored: %q %q", ToolPolicyFor(false), ToolPolicyFor(true))
	}
	if err := SetToolPolicy("explode"); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if got := CurrentToolPolicy(); got != ToolPolicyReject {
		t.Fatalf("invalid policy changed state: %q", got)
	}
}

// toolObj 构造一条含图片的 tool 消息体（Chat 形态）。
func toolObj(callID string, content any) map[string]any {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": "shot", "arguments": "{}"}},
		}},
	}}
	if content != nil {
		obj["messages"] = append(obj["messages"].([]any), map[string]any{
			"role": "tool", "tool_call_id": callID, "content": content,
		})
	}
	return obj
}

func imagePart(url string) map[string]any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

func messagesOf(t *testing.T, obj map[string]any) []any {
	t.Helper()
	msgs, ok := obj["messages"].([]any)
	if !ok {
		t.Fatalf("messages=%#v", obj)
	}
	return msgs
}

func TestApplyToolPolicyHoistKeepsTextAndLabels(t *testing.T) {
	obj := toolObj("call_1", []any{
		map[string]any{"type": "text", "text": "captured"},
		imagePart(tinyPNG),
	})
	changed, err := ApplyToolPolicy(obj, ToolPolicyHoist)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	msgs := messagesOf(t, obj)
	if len(msgs) != 3 {
		t.Fatalf("messages=%#v", msgs)
	}
	tool := msgs[1].(map[string]any)
	keep, _ := tool["content"].([]any)
	if len(keep) != 1 || keep[0].(map[string]any)["text"] != "captured" {
		t.Fatalf("tool content=%#v", tool["content"])
	}
	user := msgs[2].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("hoisted=%#v", user)
	}
	parts := user["content"].([]any)
	if len(parts) != 3 || parts[0].(map[string]any)["text"] != HoistHeader || parts[1].(map[string]any)["text"] != "call call_1:" {
		t.Fatalf("parts=%#v", parts)
	}
	if url := parts[2].(map[string]any)["image_url"].(map[string]any)["url"]; url != tinyPNG {
		t.Fatalf("url=%v", url)
	}
}

func TestApplyToolPolicyHoistPlaceholder(t *testing.T) {
	obj := toolObj("c", []any{imagePart(tinyPNG)})
	if changed, err := ApplyToolPolicy(obj, ToolPolicyHoist); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	msgs := messagesOf(t, obj)
	if got := msgs[1].(map[string]any)["content"]; got != hoistPlaceholder {
		t.Fatalf("placeholder=%#v", got)
	}
	// 无 tool_call_id 时不产出空标注 part。
	obj = toolObj("", []any{imagePart(tinyPNG)})
	obj["messages"].([]any)[1].(map[string]any)["tool_call_id"] = ""
	if _, err := ApplyToolPolicy(obj, ToolPolicyHoist); err != nil {
		t.Fatal(err)
	}
	parts := messagesOf(t, obj)[2].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts=%#v", parts)
	}
}

func TestApplyToolPolicyHoistBatchInsertsOnce(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "tool", "tool_call_id": "a", "content": []any{imagePart(tinyPNG)}},
		map[string]any{"role": "tool", "tool_call_id": "b", "content": []any{
			map[string]any{"type": "text", "text": "b"},
			imagePart(tinyPNG),
		}},
		map[string]any{"role": "user", "content": "continue"},
	}}
	changed, err := ApplyToolPolicy(obj, ToolPolicyHoist)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	msgs := messagesOf(t, obj)
	if len(msgs) != 4 {
		t.Fatalf("messages=%#v", msgs)
	}
	if msgs[0].(map[string]any)["tool_call_id"] != "a" || msgs[1].(map[string]any)["tool_call_id"] != "b" {
		t.Fatalf("tool order changed: %#v", msgs)
	}
	if msgs[2].(map[string]any)["role"] != "user" || msgs[3].(map[string]any)["content"] != "continue" {
		t.Fatalf("messages=%#v", msgs)
	}
	parts := msgs[2].(map[string]any)["content"].([]any)
	if len(parts) != 5 || parts[1].(map[string]any)["text"] != "call a:" || parts[3].(map[string]any)["text"] != "call b:" {
		t.Fatalf("parts=%#v", parts)
	}
}

// 非 tool 消息打断批次：每段各自插入一条，不把两段合成一条（避免把图片挪到不相邻
// 的工具结果之后）。
func TestApplyToolPolicyHoistSeparatesRuns(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "tool", "tool_call_id": "a", "content": []any{imagePart(tinyPNG)}},
		map[string]any{"role": "assistant", "content": nil},
		map[string]any{"role": "tool", "tool_call_id": "b", "content": []any{imagePart(tinyPNG)}},
	}}
	if _, err := ApplyToolPolicy(obj, ToolPolicyHoist); err != nil {
		t.Fatal(err)
	}
	msgs := messagesOf(t, obj)
	if len(msgs) != 5 {
		t.Fatalf("messages=%#v", msgs)
	}
	for _, i := range []int{1, 4} {
		if msgs[i].(map[string]any)["role"] != "user" {
			t.Fatalf("index %d=%#v", i, msgs[i])
		}
	}
}

func TestApplyToolPolicyReject(t *testing.T) {
	obj := toolObj("c", []any{map[string]any{"type": "text", "text": "t"}, imagePart(tinyPNG)})
	_, err := ApplyToolPolicy(obj, ToolPolicyReject)
	if err == nil || !strings.Contains(err.Error(), "messages[1].content[1]") {
		t.Fatalf("err=%v", err)
	}
	// 未命中媒体的消息不报错（策略只作用于工具结果图片）。
	obj = toolObj("c", "plain text")
	if changed, err := ApplyToolPolicy(obj, ToolPolicyReject); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestApplyToolPolicyPassThrough(t *testing.T) {
	content := []any{map[string]any{"type": "text", "text": "t"}, imagePart(tinyPNG)}
	obj := toolObj("c", content)
	for _, p := range []ToolPolicy{ToolPolicyPassthrough, ToolPolicyAuto} {
		if changed, err := ApplyToolPolicy(obj, p); err != nil || changed {
			t.Fatalf("policy=%q changed=%v err=%v", p, changed, err)
		}
	}
	if got := messagesOf(t, obj)[1].(map[string]any)["content"].([]any); len(got) != 2 {
		t.Fatalf("content changed: %#v", got)
	}
}

func TestApplyToolPolicyJSONStringParts(t *testing.T) {
	inner := `[{"type":"text","text":"before"},{"type":"image_url","image_url":"` + tinyPNG + `"}]`
	obj := toolObj("c", inner)
	changed, err := ApplyToolPolicy(obj, ToolPolicyHoist)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	msgs := messagesOf(t, obj)
	keep, _ := msgs[1].(map[string]any)["content"].([]any)
	if len(keep) != 1 || keep[0].(map[string]any)["text"] != "before" {
		t.Fatalf("string parts not converted: %#v", msgs[1])
	}
	if parts := msgs[2].(map[string]any)["content"].([]any); len(parts) != 3 {
		t.Fatalf("hoisted=%#v", parts)
	}
	// 字符串形态里的未知 part 类型明确报错（写回 tool content 会绕过 Normalize）。
	obj = toolObj("c", `[{"type":"image_blob_ref","blob_id":"x"},{"type":"image_url","image_url":"`+tinyPNG+`"}]`)
	if _, err := ApplyToolPolicy(obj, ToolPolicyHoist); err == nil || !strings.Contains(err.Error(), "unsupported tool result part") {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyToolPolicyOpaqueStringsUntouched(t *testing.T) {
	for _, content := range []string{
		`[{"type":"image_url","image_url":{"url":"https://example.test/x.png"}}]`,
		`[{"type":"photo","data":"whatever"}]`,
		`{"type":"image_url"}`,
		`random text with data:image/png;base64,aGk= inside`,
		`[{"type":"text","text":"no image"}]`,
	} {
		obj := toolObj("c", content)
		if changed, err := ApplyToolPolicy(obj, ToolPolicyHoist); err != nil || changed {
			t.Fatalf("content=%s changed=%v err=%v", content, changed, err)
		}
		if got := messagesOf(t, obj)[1].(map[string]any)["content"]; got != content {
			t.Fatalf("content rewritten: %#v", got)
		}
	}
}

// 无媒体请求（含纯文本 tool 结果）在两种策略下都不分配、不改写。
func TestApplyToolPolicyTextOnlyAllocations(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "rules"},
		map[string]any{"role": "assistant", "tool_calls": []any{}},
		map[string]any{"role": "tool", "tool_call_id": "a", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
		map[string]any{"role": "user", "content": "hi"},
	}}
	for _, p := range []ToolPolicy{ToolPolicyPassthrough, ToolPolicyHoist, ToolPolicyReject} {
		allocs := testing.AllocsPerRun(100, func() {
			if changed, err := ApplyToolPolicy(obj, p); err != nil || changed {
				t.Fatalf("policy=%q changed=%v err=%v", p, changed, err)
			}
		})
		if allocs != 0 && !raceEnabled {
			t.Fatalf("policy=%q allocates %.1f objects per run", p, allocs)
		}
	}
}

// 批次内第一条 tool 无图、后续才有图：批次前缀只能出现一次。
// 回归：早期实现把批次前缀既由 mutate(msgs[:first]) 拷入、又由循环追加一遍，
// 同一 tool_call_id 出现两次 → 网关自己的配对校验会拒掉这份请求。
func TestApplyToolPolicyHoistBatchPrefixOnce(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "tool_calls": []any{}},
		map[string]any{"role": "tool", "tool_call_id": "a", "content": "text only"},
		map[string]any{"role": "tool", "tool_call_id": "b", "content": []any{imagePart(tinyPNG)}},
		map[string]any{"role": "user", "content": "continue"},
	}}
	changed, err := ApplyToolPolicy(obj, ToolPolicyHoist)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	msgs := messagesOf(t, obj)
	if len(msgs) != 5 {
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
	if got := msgs[1].(map[string]any)["content"]; got != "text only" {
		t.Fatalf("text-only tool result must stay untouched: %#v", got)
	}
	if msgs[3].(map[string]any)["role"] != "user" || msgs[4].(map[string]any)["content"] != "continue" {
		t.Fatalf("hoisted message placement: %#v", msgs)
	}
}

// 同上，但前面已有一个被抬升的批次（out 已建立）：批次前缀同样只能出现一次。
func TestApplyToolPolicyHoistBatchPrefixOnceAfterMutation(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "tool", "tool_call_id": "a", "content": []any{imagePart(tinyPNG)}},
		map[string]any{"role": "user", "content": "next"},
		map[string]any{"role": "tool", "tool_call_id": "b", "content": "text only"},
		map[string]any{"role": "tool", "tool_call_id": "c", "content": []any{imagePart(tinyPNG)}},
	}}
	if _, err := ApplyToolPolicy(obj, ToolPolicyHoist); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, v := range messagesOf(t, obj) {
		if id, ok := v.(map[string]any)["tool_call_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("tool results duplicated or reordered: %v", ids)
	}
}

// 字符串形态里的畸形 text 块明确报错（不写出畸形 part）。
func TestApplyToolPolicyJSONStringMalformedText(t *testing.T) {
	for _, content := range []string{
		`[{"type":"text"},{"type":"image_url","image_url":"` + tinyPNG + `"}]`,
		`[{"type":"text","text":5},{"type":"image_url","image_url":"` + tinyPNG + `"}]`,
	} {
		obj := toolObj("c", content)
		if _, err := ApplyToolPolicy(obj, ToolPolicyHoist); err == nil || !strings.Contains(err.Error(), "string text required") {
			t.Fatalf("content=%s err=%v", content, err)
		}
	}
}
