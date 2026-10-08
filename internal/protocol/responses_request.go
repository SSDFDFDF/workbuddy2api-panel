package protocol

import "fmt"

func responsesRequest(src map[string]any, tools *toolIndex) (map[string]any, error) {
	if err := fields(src, "", "model input instructions stream store previous_response_id conversation background tools tool_choice parallel_tool_calls max_output_tokens temperature top_p metadata text reasoning include truncation"); err != nil {
		return nil, err
	}
	// The official default is true. Requiring an explicit opt-out avoids falsely
	// acknowledging persistence on a stateless gateway.
	if src["store"] != false {
		return nil, invalid("store", "explicit store:false is required; response storage is not implemented")
	}
	for _, k := range []string{"previous_response_id", "conversation"} {
		if src[k] != nil {
			return nil, invalid(k, "server-side conversation state is not implemented; send complete input history")
		}
	}
	if err := optionalFalse(src, "background"); err != nil {
		return nil, err
	}
	if v := src["truncation"]; v != nil && v != "disabled" {
		return nil, invalid("truncation", "only disabled is supported")
	}
	if v := src["include"]; v != nil {
		a, ok := v.([]any)
		if !ok || len(a) != 0 {
			return nil, invalid("include", "only an empty array is supported")
		}
	}
	if v := src["text"]; v != nil {
		m, err := object(v, "text")
		if err != nil {
			return nil, err
		}
		if err = fields(m, "text", "format"); err != nil {
			return nil, err
		}
		if v := m["format"]; v != nil {
			f, err := object(v, "text.format")
			if err != nil {
				return nil, err
			}
			if err = fields(f, "text.format", "type"); err != nil {
				return nil, err
			}
			if f["type"] != "text" {
				return nil, invalid("text.format", "only plain text is supported; structured output is not verified")
			}
		}
	}
	dst := map[string]any{"model": src["model"]}
	if v, ok := src["stream"]; ok {
		dst["stream"] = v
	}
	if v := src["reasoning"]; v != nil {
		m, err := object(v, "reasoning")
		if err != nil {
			return nil, err
		}
		if err = fields(m, "reasoning", "effort"); err != nil {
			return nil, err
		}
		if m["effort"] != "none" {
			return nil, invalid("reasoning", "only effort:none is supported; reasoning state is not translatable")
		}
		dst["reasoning_effort"] = "none"
	}
	if v := src["max_output_tokens"]; v != nil {
		dst["max_tokens"] = v
	}
	if err := sampling(src, dst, 2); err != nil {
		return nil, err
	}
	if v := src["metadata"]; v != nil {
		m, err := object(v, "metadata")
		if err != nil {
			return nil, err
		}
		if len(m) > 16 {
			return nil, invalid("metadata", "at most 16 entries allowed")
		}
		for k, v := range m {
			s, ok := v.(string)
			if !ok || len(k) > 64 || len(s) > 512 {
				return nil, invalid("metadata", "string keys up to 64 bytes and values up to 512 bytes required")
			}
		}
		// Client metadata is echoed only, not interpreted as WorkBuddy routing.
	}
	if v := src["tools"]; v != nil {
		declared, err := tools.declarations(v)
		if err != nil {
			return nil, err
		}
		dst["tools"] = declared
	}
	if v := src["parallel_tool_calls"]; v != nil {
		dst["parallel_tool_calls"] = v
	}
	if v := src["tool_choice"]; v != nil {
		if s, ok := v.(string); ok {
			if s != "auto" && s != "none" && s != "required" {
				return nil, invalid("tool_choice", "auto, none or required expected")
			}
			dst["tool_choice"] = s
		} else {
			m, err := object(v, "tool_choice")
			if err != nil {
				return nil, err
			}
			if err = fields(m, "tool_choice", "type name"); err != nil {
				return nil, err
			}
			if m["type"] != "function" {
				return nil, invalid("tool_choice.type", "function required")
			}
			name, err := tools.choice(m["name"])
			if err != nil {
				return nil, err
			}
			dst["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
	}
	messages := []any{}
	if v := src["instructions"]; v != nil {
		s, err := stringValue(v, "instructions")
		if err != nil {
			return nil, err
		}
		messages = append(messages, map[string]any{"role": "system", "content": s})
	}
	switch input := src["input"].(type) {
	case string:
		messages = append(messages, map[string]any{"role": "user", "content": input})
	case []any:
		// Group adjacent function calls and preceding assistant text into the same
		// Chat turn. Never move an item across a user or tool-result boundary.
		var assistant map[string]any
		for i, v := range input {
			p := fmt.Sprintf("input[%d]", i)
			m, err := object(v, p)
			if err != nil {
				return nil, err
			}
			switch m["type"] {
			case nil, "message":
				if err = fields(m, p, "type role content id status"); err != nil {
					return nil, err
				}
				if v := m["id"]; v != nil {
					if _, err = nonempty(v, p+".id"); err != nil {
						return nil, err
					}
				}
				if v := m["status"]; v != nil && v != "completed" {
					return nil, invalid(p+".status", "only completed history items are supported")
				}
				role, _ := m["role"].(string)
				if role != "user" && role != "assistant" && role != "system" && role != "developer" {
					return nil, invalid(p+".role", "unsupported message role")
				}
				// responsesContent 按 role 决定允许的 part 类型：assistant 只收文本
				// （input_text / output_text，replayed ResponseOutputMessage 无损回放），
				// user 额外收 input_image，system/developer 只收 input_text。
				content, err := responsesContent(m["content"], p+".content", role)
				if err != nil {
					return nil, err
				}
				msg := map[string]any{"role": role, "content": content}
				messages = append(messages, msg)
				assistant = nil
				if role == "assistant" {
					assistant = msg
				}
			case "function_call":
				if err = fields(m, p, "type id call_id name namespace arguments status"); err != nil {
					return nil, err
				}
				if v := m["id"]; v != nil {
					if _, err = nonempty(v, p+".id"); err != nil {
						return nil, err
					}
				}
				if v := m["status"]; v != nil && v != "completed" {
					return nil, invalid(p+".status", "only completed tool history is supported")
				}
				name, err := nonempty(m["name"], p+".name")
				if err != nil {
					return nil, err
				}
				name, err = tools.history(name, m["namespace"], p)
				if err != nil {
					return nil, err
				}
				call, err := toolCall(m["call_id"], name, m["arguments"], p)
				if err != nil {
					return nil, err
				}
				if assistant == nil {
					assistant = map[string]any{"role": "assistant", "content": nil}
					messages = append(messages, assistant)
				}
				calls, _ := assistant["tool_calls"].([]any)
				assistant["tool_calls"] = append(calls, call)
			case "function_call_output":
				if err = fields(m, p, "type call_id output id status"); err != nil {
					return nil, err
				}
				if v := m["status"]; v != nil && v != "completed" {
					return nil, invalid(p+".status", "only completed tool results are supported")
				}
				if v := m["id"]; v != nil {
					if _, err = nonempty(v, p+".id"); err != nil {
						return nil, err
					}
				}
				id, err := nonempty(m["call_id"], p+".call_id")
				if err != nil {
					return nil, err
				}
				output, err := responsesToolOutput(m["output"], p+".output")
				if err != nil {
					return nil, err
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": output})
				assistant = nil
			default:
				return nil, invalid(p+".type", "unsupported input item")
			}
		}
	default:
		return nil, invalid("input", "string or array required")
	}
	dst["messages"] = messages
	return dst, nil
}
