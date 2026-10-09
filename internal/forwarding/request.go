// Package forwarding implements the input contract, independently of account routing.
package forwarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"workbuddy_manager/internal/jsondoc"
	"workbuddy_manager/internal/media"
)

const MaxRequestBytes = 32 << 20

type InvalidRequest struct{ Param, Message string }

func (e *InvalidRequest) Error() string { return e.Param + ": " + e.Message }
func invalid(p, m string) error         { return &InvalidRequest{p, m} }

type Request struct {
	Object         map[string]any
	Model          string
	Stream         bool
	N              int
	ConversationID string
}

// Parse validates known fields without filtering unknown extensions or repairing history.
func Parse(raw []byte) (*Request, error) {
	obj, err := jsondoc.Object(raw)
	if err != nil {
		return nil, invalid("body", err.Error())
	}
	r := &Request{Object: obj, N: 1}
	r.Model, _ = obj["model"].(string)
	if strings.TrimSpace(r.Model) == "" {
		return nil, invalid("model", "non-empty string required")
	}
	if v, ok := obj["stream"]; ok {
		r.Stream, ok = v.(bool)
		if !ok {
			return nil, invalid("stream", "boolean required")
		}
	}
	if v, ok := obj["n"]; ok {
		n, good := jsondoc.Int(v)
		if !good || n < 1 || n > 128 {
			return nil, invalid("n", "integer in [1,128] required")
		}
		r.N = int(n)
	}
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_thinking_tokens"} {
		if v, ok := obj[k]; ok && v != nil {
			n, good := jsondoc.Int(v)
			if !good || n <= 0 {
				return nil, invalid(k, "positive integer required")
			}
		}
	}
	if a, ok := obj["max_tokens"]; ok {
		if b, yes := obj["max_completion_tokens"]; yes {
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if string(aa) != string(bb) {
				return nil, invalid("max_completion_tokens", "conflicts with max_tokens")
			}
		}
	}
	for _, k := range []string{"parallel_tool_calls", "logprobs", "store"} {
		if v, ok := obj[k]; ok && v != nil {
			if _, ok := v.(bool); !ok {
				return nil, invalid(k, "boolean required")
			}
		}
	}
	for _, k := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty"} {
		if v, ok := obj[k]; ok && v != nil {
			if _, ok := v.(json.Number); !ok {
				return nil, invalid(k, "number required")
			}
		}
	}
	if v, ok := obj["stream_options"]; ok {
		if !r.Stream {
			return nil, invalid("stream_options", "only valid for streaming requests")
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, invalid("stream_options", "object required")
		}
		if v, ok := m["include_usage"]; ok {
			if _, ok := v.(bool); !ok {
				return nil, invalid("stream_options.include_usage", "boolean required")
			}
		}
	}
	for _, k := range []string{"functions", "function_call"} {
		if _, ok := obj[k]; ok {
			return nil, invalid(k, "legacy function protocol is unsupported; use tools/tool_calls")
		}
	}
	if v, ok := obj["prompt_cache_key"]; ok {
		s, good := v.(string)
		if !good || len(s) > 1024 {
			return nil, invalid("prompt_cache_key", "string up to 1024 bytes required")
		}
	}
	if err := reasoning(obj); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	if v, ok := obj["tools"]; ok {
		tools, ok := v.([]any)
		if !ok {
			return nil, invalid("tools", "array required")
		}
		for i, v := range tools {
			m, ok := v.(map[string]any)
			if !ok || m["type"] != "function" {
				return nil, invalid(fmt.Sprintf("tools[%d]", i), "function tool required")
			}
			fn, _ := m["function"].(map[string]any)
			name, _ := fn["name"].(string)
			if name == "" || names[name] {
				return nil, invalid("tools", "missing or duplicate function name")
			}
			names[name] = true
		}
	}
	if v, ok := obj["tool_choice"]; ok {
		name := ""
		switch tc := v.(type) {
		case string:
			if tc != "auto" && tc != "none" && tc != "required" {
				name = tc
			}
		case map[string]any:
			typ, _ := tc["type"].(string)
			switch typ {
			case "auto", "none", "required":
			case "function":
				fn, _ := tc["function"].(map[string]any)
				name, _ = fn["name"].(string)
				if name == "" {
					return nil, invalid("tool_choice", "function name required")
				}
			default:
				return nil, invalid("tool_choice", "unsupported choice")
			}
		default:
			return nil, invalid("tool_choice", "string or function choice object required")
		}
		if name != "" && !names[name] {
			return nil, invalid("tool_choice", "selected function is not defined")
		}
	}
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return nil, invalid("messages", "non-empty array required")
	}
	pending := map[string]bool{}
	for i, v := range msgs {
		p := fmt.Sprintf("messages[%d]", i)
		m, ok := v.(map[string]any)
		if !ok {
			return nil, invalid(p, "object required")
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return nil, invalid(p+".role", "unsupported role")
		}
		if _, ok := m["function_call"]; ok {
			return nil, invalid(p+".function_call", "use tool_calls")
		}
		if role == "tool" {
			id, _ := m["tool_call_id"].(string)
			if !pending[id] {
				return nil, invalid(p+".tool_call_id", "no matching unresolved tool call")
			}
			delete(pending, id)
		} else if len(pending) > 0 {
			return nil, invalid(p, "previous tool calls require results before continuing")
		}
		if content, ok := m["content"]; ok && content != nil {
			switch content.(type) {
			case string, []any:
			default:
				return nil, invalid(p+".content", "string, content array or null required")
			}
		}
		if calls, exists := m["tool_calls"]; exists {
			if role != "assistant" {
				return nil, invalid(p+".tool_calls", "only assistant can call tools")
			}
			a, ok := calls.([]any)
			if !ok {
				return nil, invalid(p+".tool_calls", "array required")
			}
			for _, v := range a {
				c, ok := v.(map[string]any)
				if !ok {
					return nil, invalid(p+".tool_calls", "object required")
				}
				id, _ := c["id"].(string)
				if id == "" || pending[id] {
					return nil, invalid(p+".tool_calls", "missing or duplicate call id")
				}
				fn, _ := c["function"].(map[string]any)
				name, _ := fn["name"].(string)
				args, ok := fn["arguments"].(string)
				if c["type"] != "function" || name == "" || !ok {
					return nil, invalid(p+".tool_calls", "function name and arguments required")
				}
				if _, err := jsondoc.Decode([]byte(args)); err != nil {
					return nil, invalid(p+".tool_calls.arguments", "valid JSON string required")
				}
				pending[id] = true
			}
		}
	}
	if len(pending) > 0 {
		return nil, invalid("messages", "unresolved tool calls")
	}
	// 媒体形状归一与资源校验（internal/media）：字符串 image_url → 对象形态、
	// 外链/非 data URL/超限图明确 400。这里同时覆盖原生 Chat 与跨协议桥接
	// （跨协议先转成 Chat 形态再走本函数），上游 prepareBody 的二次 Parse 幂等。
	if _, err := media.Normalize(obj); err != nil {
		var me *media.Error
		if errors.As(err, &me) {
			return nil, &InvalidRequest{Param: me.Param, Message: me.Message}
		}
		return nil, invalid("messages", err.Error())
	}
	ids := []any{obj["conversation_id"], obj["conversationId"]}
	if m, ok := obj["metadata"].(map[string]any); ok {
		ids = append(ids, m["conversation_id"], m["conversationId"])
	}
	for _, v := range ids {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || !ValidID(s) {
			return nil, invalid("conversation_id", "invalid identifier")
		}
		if r.ConversationID != "" && r.ConversationID != s {
			return nil, invalid("conversation_id", "conflicting conversation identifiers")
		}
		r.ConversationID = s
	}
	return r, nil
}

func ValidID(s string) bool {
	if len(s) > 256 {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

func (r *Request) ResolveHeaders(h http.Header) error {
	for _, key := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Root-Request-ID", "X-Parent-Conversation-ID", "X-Agent-Type", "X-Trace-ID"} {
		vs := h.Values(key)
		if len(vs) > 1 {
			return invalid(key, "multiple values are not allowed")
		}
		if !ValidID(h.Get(key)) {
			return invalid(key, "invalid identifier")
		}
	}
	if id := h.Get("X-Conversation-ID"); id != "" {
		if r.ConversationID != "" && r.ConversationID != id {
			return invalid("X-Conversation-ID", "conflicts with body")
		}
		r.ConversationID = id
	}
	return nil
}

func reasoning(obj map[string]any) error {
	effort := ""
	vals := []any{obj["reasoning_effort"], obj["reasoningEffort"]}
	if v, ok := obj["reasoning"]; ok {
		m, good := v.(map[string]any)
		if !good {
			return invalid("reasoning", "object required")
		}
		vals = append(vals, m["effort"])
	}
	for _, v := range vals {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			return invalid("reasoning_effort", "non-empty string required")
		}
		if effort != "" && effort != s {
			return invalid("reasoning_effort", "conflicting reasoning aliases")
		}
		effort = s
	}
	typ := ""
	if v, ok := obj["thinking"]; ok {
		m, good := v.(map[string]any)
		if !good {
			return invalid("thinking", "object required")
		}
		typ, _ = m["type"].(string)
		if typ != "enabled" && typ != "disabled" {
			return invalid("thinking.type", "enabled or disabled required")
		}
	}
	off := effort == "off" || effort == "none"
	if typ == "disabled" && effort != "" && !off || typ == "enabled" && off {
		return invalid("thinking", "conflicts with reasoning effort")
	}
	if _, budget := obj["max_thinking_tokens"]; budget && (off || typ == "disabled") {
		return invalid("max_thinking_tokens", "conflicts with disabled reasoning")
	}
	return nil
}

// EncodeObject 对 obj（调用方拥有的可变副本）就地应用声明的 WorkBuddy 线格式转换。
// 不序列化、不改动调用方原始文档。
//
// 为什么拆出对象版本：旧实现的 Encode 靠 json.Marshal → jsondoc.Object 拿一份副本，
// 等于为「不污染入参」这件小事付出了整包序列化 + 整包解析的代价；而调用方往往
// 紧接着还要再做一次改写（缓存键注入）并序列化——合并到同一次序列化里就省下一遍。
func EncodeObject(obj map[string]any, model string) {
	obj["model"] = model
	obj["stream"] = true
	if _, ok := obj["stream_options"]; !ok {
		obj["stream_options"] = map[string]any{"include_usage": true}
	}
	if _, ok := obj["reasoning_summary"]; !ok {
		obj["reasoning_summary"] = "auto"
	}
	// WorkBuddy's native output budget is max_tokens. Retain exact numeric value.
	if v, ok := obj["max_completion_tokens"]; ok {
		obj["max_tokens"] = v
		delete(obj, "max_completion_tokens")
	}
	if tc, ok := obj["tool_choice"].(map[string]any); ok {
		if typ, _ := tc["type"].(string); typ == "function" {
			if fn, ok := tc["function"].(map[string]any); ok {
				obj["tool_choice"] = fn["name"]
			}
		} else {
			obj["tool_choice"] = typ
		}
	}
	effort, _ := obj["reasoning_effort"].(string)
	if effort == "" {
		effort, _ = obj["reasoningEffort"].(string)
	}
	if m, ok := obj["reasoning"].(map[string]any); ok {
		if effort == "" {
			effort, _ = m["effort"].(string)
		}
		delete(m, "effort")
		if len(m) == 0 {
			delete(obj, "reasoning")
		}
	}
	delete(obj, "reasoningEffort")
	if effort == "off" || effort == "none" {
		delete(obj, "reasoning_effort")
		if th, ok := obj["thinking"].(map[string]any); ok {
			th["type"] = "disabled"
		} else {
			obj["thinking"] = map[string]any{"type": "disabled"}
		}
	} else if effort != "" {
		obj["reasoning_effort"] = effort
	}
}

// Encode performs only declared WorkBuddy wire conversions. No model-name heuristics.
func (r *Request) Encode(model string) ([]byte, error) {
	// The document belongs to this request; each encoding starts from a fresh copy.
	obj := jsondoc.CopyObject(r.Object)
	EncodeObject(obj, model)
	return json.Marshal(obj)
}
