package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
)

func anthropicRequest(src map[string]any) (map[string]any, error) {
	if err := fields(src, "", "model messages system max_tokens stream temperature top_p tools tool_choice thinking stop_sequences"); err != nil {
		return nil, err
	}
	if n, ok := jsondoc.Int(src["max_tokens"]); !ok || n <= 0 {
		return nil, invalid("max_tokens", "positive integer required")
	}
	dst := map[string]any{"model": src["model"], "max_tokens": src["max_tokens"]}
	if v, ok := src["stream"]; ok {
		dst["stream"] = v
	}
	if err := sampling(src, dst, 1); err != nil {
		return nil, err
	}
	if v := src["thinking"]; v != nil {
		m, err := object(v, "thinking")
		if err != nil {
			return nil, err
		}
		if err = fields(m, "thinking", "type"); err != nil {
			return nil, err
		}
		if m["type"] != "disabled" {
			return nil, invalid("thinking", "only disabled is supported; signed thinking is unavailable")
		}
		dst["thinking"] = m
	}
	if v := src["stop_sequences"]; v != nil {
		a, ok := v.([]any)
		if !ok || len(a) != 0 {
			return nil, invalid("stop_sequences", "custom stop reasons cannot be recovered from the Chat upstream")
		}
	}
	if v := src["tools"]; v != nil {
		a, ok := v.([]any)
		if !ok {
			return nil, invalid("tools", "array required")
		}
		tools := make([]any, 0, len(a))
		for i, v := range a {
			p := fmt.Sprintf("tools[%d]", i)
			m, err := object(v, p)
			if err != nil {
				return nil, err
			}
			if err = fields(m, p, "type name description input_schema strict"); err != nil {
				return nil, err
			}
			if v := m["type"]; v != nil && v != "custom" {
				return nil, invalid(p+".type", "only client-defined tools are supported")
			}
			if err = optionalFalse(m, "strict"); err != nil {
				return nil, invalid(p+".strict", "schema-constrained generation is not verified")
			}
			t, err := function(m["name"], m["description"], m["input_schema"], p)
			if err != nil {
				return nil, err
			}
			tools = append(tools, t)
		}
		dst["tools"] = tools
	}
	if v := src["tool_choice"]; v != nil {
		m, err := object(v, "tool_choice")
		if err != nil {
			return nil, err
		}
		if err = fields(m, "tool_choice", "type name disable_parallel_tool_use"); err != nil {
			return nil, err
		}
		switch m["type"] {
		case "auto", "none":
			dst["tool_choice"] = m["type"]
		case "any":
			dst["tool_choice"] = "required"
		case "tool":
			n, err := nonempty(m["name"], "tool_choice.name")
			if err != nil {
				return nil, err
			}
			dst["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": n}}
		default:
			return nil, invalid("tool_choice.type", "auto, any, none or tool required")
		}
		if m["type"] != "tool" && m["name"] != nil {
			return nil, invalid("tool_choice.name", "only valid with type:tool")
		}
		if v, exists := m["disable_parallel_tool_use"]; exists {
			b, ok := v.(bool)
			if !ok {
				return nil, invalid("tool_choice.disable_parallel_tool_use", "boolean required")
			}
			dst["parallel_tool_calls"] = !b
		}
	}
	messages := []any{}
	if v := src["system"]; v != nil {
		content, err := textParts(v, "system", "text")
		if err != nil {
			return nil, err
		}
		messages = append(messages, map[string]any{"role": "system", "content": content})
	}
	a, ok := src["messages"].([]any)
	if !ok || len(a) == 0 {
		return nil, invalid("messages", "non-empty array required")
	}
	for i, v := range a {
		p := fmt.Sprintf("messages[%d]", i)
		m, err := object(v, p)
		if err != nil {
			return nil, err
		}
		if err = fields(m, p, "role content"); err != nil {
			return nil, err
		}
		role, _ := m["role"].(string)
		if role != "user" && role != "assistant" {
			return nil, invalid(p+".role", "user or assistant required")
		}
		msg := map[string]any{"role": role}
		if s, ok := m["content"].(string); ok {
			msg["content"] = s
			messages = append(messages, msg)
			continue
		}
		blocks, ok := m["content"].([]any)
		if !ok || len(blocks) == 0 {
			return nil, invalid(p+".content", "string or non-empty content array required")
		}
		parts, calls := []any{}, []any{}
		seenCall := false
		for j, v := range blocks {
			bp := fmt.Sprintf("%s.content[%d]", p, j)
			b, err := object(v, bp)
			if err != nil {
				return nil, err
			}
			switch b["type"] {
			case "text":
				if seenCall {
					return nil, invalid(bp, "text after tool_use cannot preserve block order in Chat")
				}
				part, err := textPartFrom(b, bp, "text")
				if err != nil {
					return nil, err
				}
				parts = append(parts, part)
			case "image":
				if role != "user" {
					return nil, invalid(bp, "image blocks are only supported in user messages")
				}
				part, err := anthropicImagePart(b, bp)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part)
			case "tool_use":
				if role != "assistant" {
					return nil, invalid(bp, "tool_use requires assistant role")
				}
				if err = fields(b, bp, "type id name input"); err != nil {
					return nil, err
				}
				args, err := object(b["input"], bp+".input")
				if err != nil {
					return nil, err
				}
				raw, err := json.Marshal(args)
				if err != nil {
					return nil, err
				}
				call, err := toolCall(b["id"], b["name"], string(raw), bp)
				if err != nil {
					return nil, err
				}
				calls = append(calls, call)
				seenCall = true
			case "tool_result":
				if role != "user" || len(parts) != 0 {
					return nil, invalid(bp, "tool_result must precede user text and images")
				}
				if err = fields(b, bp, "type tool_use_id content is_error"); err != nil {
					return nil, err
				}
				if err = optionalFalse(b, "is_error"); err != nil {
					return nil, invalid(bp+".is_error", "Chat has no equivalent tool failure flag")
				}
				id, err := nonempty(b["tool_use_id"], bp+".tool_use_id")
				if err != nil {
					return nil, err
				}
				content := any("")
				if v, exists := b["content"]; exists {
					content, err = anthropicToolResult(v, bp+".content")
					if err != nil {
						return nil, err
					}
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": content})
			default:
				return nil, invalid(bp+".type", "only text, image, tool_use and successful text/image tool_result are supported")
			}
		}
		if len(parts) > 0 || len(calls) > 0 {
			msg["content"] = nil
			if len(parts) > 0 {
				msg["content"] = parts
			}
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
		}
	}
	dst["messages"] = messages
	return dst, nil
}
