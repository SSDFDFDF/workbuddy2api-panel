// Package protocol implements the explicitly supported, stateless subset of
// Responses and Messages on top of the native Chat forwarding contract.
// It never repairs history, invents reasoning, or forwards foreign extensions.
package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/forwarding"
	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
)

type Kind string

const (
	Chat      Kind = "chat"
	Responses Kind = "responses"
	Anthropic Kind = "anthropic"
)

// Request keeps the wire request separate from protocol-only response metadata.
// Source is owned by this request; it is never sent to WorkBuddy.
type Request struct {
	Kind   Kind
	Chat   *forwarding.Request
	Source map[string]any
}

func invalid(path, message string) error {
	return &forwarding.InvalidRequest{Param: path, Message: message}
}

func fields(obj map[string]any, path, allowed string) error {
	set := map[string]bool{}
	for _, k := range strings.Fields(allowed) {
		set[k] = true
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !set[k] {
			if path != "" {
				k = path + "." + k
			}
			return invalid(k, "unsupported by the stateless Chat bridge")
		}
	}
	return nil
}

func object(v any, path string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid(path, "object required")
	}
	return m, nil
}

func nonempty(v any, path string) (string, error) {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", invalid(path, "non-empty string required")
	}
	return s, nil
}

func stringValue(v any, path string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", invalid(path, "string required")
	}
	return s, nil
}

func optionalFalse(obj map[string]any, key string) error {
	if v, ok := obj[key]; ok && v != nil && v != false {
		return invalid(key, "only false is supported")
	}
	return nil
}

func sampling(src, dst map[string]any, maxTemperature float64) error {
	for _, k := range []string{"temperature", "top_p"} {
		v, exists := src[k]
		if !exists || v == nil {
			continue
		}
		n, ok := v.(json.Number)
		if !ok {
			return invalid(k, "number required")
		}
		f, err := n.Float64()
		max := 1.0
		if k == "temperature" {
			max = maxTemperature
		}
		if err != nil || f < 0 || f > max {
			return invalid(k, fmt.Sprintf("number in [0,%g] required", max))
		}
		dst[k] = v
	}
	return nil
}

func textParts(v any, path string, allowedType string) (any, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return nil, invalid(path, "string or non-empty text content array required")
	}
	out := make([]any, 0, len(a))
	for i, v := range a {
		p := fmt.Sprintf("%s[%d]", path, i)
		m, err := object(v, p)
		if err != nil {
			return nil, err
		}
		allowed := "type text"
		if allowedType == "output_text" {
			allowed += " annotations logprobs"
			for _, key := range []string{"annotations", "logprobs"} {
				if v := m[key]; v != nil {
					a, ok := v.([]any)
					if !ok || len(a) != 0 {
						return nil, invalid(p+"."+key, "only an empty array is supported")
					}
				}
			}
		}
		if err = fields(m, p, allowed); err != nil {
			return nil, err
		}
		if m["type"] != allowedType {
			return nil, invalid(p+".type", "only "+allowedType+" is supported")
		}
		s, err := stringValue(m["text"], p+".text")
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"type": "text", "text": s})
	}
	return out, nil
}

// Decode delegates native Chat unchanged. Cross-protocol requests use an explicit
// allowlist, preserving numeric values and checking the resulting tool history
// with the exact same validator as native Chat.
func Decode(kind Kind, raw []byte) (*Request, error) {
	if kind == Chat {
		chat, err := forwarding.Parse(raw)
		return &Request{Kind: kind, Chat: chat}, err
	}
	src, err := jsondoc.Object(raw)
	if err != nil {
		return nil, invalid("body", err.Error())
	}
	var dst map[string]any
	switch kind {
	case Responses:
		dst, err = responsesRequest(src)
	case Anthropic:
		dst, err = anthropicRequest(src)
	default:
		return nil, invalid("protocol", "unsupported protocol")
	}
	if err != nil {
		return nil, err
	}
	if choice := dst["tool_choice"]; choice == "required" {
		if tools, _ := dst["tools"].([]any); len(tools) == 0 {
			return nil, invalid("tool_choice", "required/any requires at least one tool")
		}
	}
	encoded, err := json.Marshal(dst)
	if err != nil {
		return nil, err
	}
	chat, err := forwarding.Parse(encoded)
	if err != nil {
		return nil, err
	}
	return &Request{Kind: kind, Chat: chat, Source: src}, nil
}

func function(name, description, schema any, path string) (map[string]any, error) {
	n, err := nonempty(name, path+".name")
	if err != nil {
		return nil, err
	}
	params, err := object(schema, path+".parameters")
	if err != nil {
		return nil, err
	}
	if params["type"] != "object" {
		return nil, invalid(path+".parameters.type", "object schema required")
	}
	fn := map[string]any{"name": n, "parameters": params, "strict": false}
	if description != nil {
		d, err := stringValue(description, path+".description")
		if err != nil {
			return nil, err
		}
		fn["description"] = d
	}
	return map[string]any{"type": "function", "function": fn}, nil
}

func toolCall(id, name, args any, path string) (map[string]any, error) {
	cid, err := nonempty(id, path+".call_id")
	if err != nil {
		return nil, err
	}
	n, err := nonempty(name, path+".name")
	if err != nil {
		return nil, err
	}
	s, err := stringValue(args, path+".arguments")
	if err != nil {
		return nil, err
	}
	if _, err = jsondoc.Object([]byte(s)); err != nil {
		return nil, invalid(path+".arguments", "JSON object string required")
	}
	return map[string]any{"id": cid, "type": "function", "function": map[string]any{"name": n, "arguments": s}}, nil
}
