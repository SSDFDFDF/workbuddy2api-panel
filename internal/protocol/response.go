package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"workbuddy_manager/internal/jsondoc"
	"workbuddy_manager/internal/upstream"
)

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

// ErrorBody serializes already-sanitized errors in the client's protocol.
func ErrorBody(kind Kind, status int, body map[string]any) map[string]any {
	if kind != Anthropic {
		return body
	}
	e, _ := body["error"].(map[string]any)
	message, _ := e["message"].(string)
	if message == "" {
		message = "request failed"
	}
	typ := "api_error"
	switch status {
	case 400, 404, 405, 422:
		typ = "invalid_request_error"
	case 401:
		typ = "authentication_error"
	case 403:
		typ = "permission_error"
	case 413:
		typ = "request_too_large"
	case 429:
		typ = "rate_limit_error"
	case 503, 529:
		typ = "overloaded_error"
	}
	return map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": message}}
}

func streamError(err error, hint func(string) string) (int, map[string]any) {
	var fe *upstream.FrameError
	if errors.As(err, &fe) {
		body := upstream.ErrorResponse(fe.Kind, fe.Payload)
		if hint != nil {
			if h := hint(fe.Payload); h != "" {
				body["error"].(map[string]any)["gateway_hint"] = h
			}
		}
		return fe.Status, body
	}
	return 502, map[string]any{"error": map[string]any{"type": "upstream_error", "code": "upstream_protocol_error", "message": err.Error()}}
}

// Unsupported response information is an upstream protocol error, not a reason
// to emit a plausible but lossy successful result.
func checkMessage(m map[string]any) error {
	for k, v := range m {
		if v == nil {
			continue
		}
		switch k {
		case "content": // Checked as text by the order guard and final builder.
		case "role":
			if v != "assistant" {
				return fmt.Errorf("unexpected upstream message role")
			}
		case "tool_calls":
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("invalid upstream tool_calls array")
			}
		case "annotations":
			if a, ok := v.([]any); !ok || len(a) > 0 {
				return fmt.Errorf("upstream annotations are unsupported")
			}
		case "reasoning", "reasoning_content", "refusal":
			if v != "" {
				return fmt.Errorf("upstream %s cannot be represented by this text/tool bridge", k)
			}
		default:
			return fmt.Errorf("upstream message field %s cannot be represented by this text/tool bridge", k)
		}
	}
	return nil
}

func responseParts(resp map[string]any) (map[string]any, string, error) {
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) != 1 {
		return nil, "", fmt.Errorf("exactly one upstream choice required")
	}
	ch, ok := choices[0].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("invalid upstream choice")
	}
	if v := ch["logprobs"]; v != nil {
		return nil, "", fmt.Errorf("upstream logprobs are unsupported")
	}
	m, ok := ch["message"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("missing upstream message")
	}
	if err := checkMessage(m); err != nil {
		return nil, "", err
	}
	finish, _ := ch["finish_reason"].(string)
	switch finish {
	case "stop", "length", "tool_calls", "content_filter":
	default:
		return nil, "", fmt.Errorf("unsupported upstream finish_reason %q", finish)
	}
	return m, finish, nil
}

// responseBuilder is request-scoped; stream and JSON share final object creation.
type responseBuilder struct {
	req       *Request
	id        string
	messageID string
	created   int64
}

func newBuilder(req *Request) *responseBuilder {
	prefix := "resp_"
	if req.Kind == Anthropic {
		prefix = "msg_"
	}
	return &responseBuilder{req: req, id: newID(prefix), messageID: newID("msg_"), created: time.Now().Unix()}
}

func (b *responseBuilder) responseEnvelope(status string, output []any, usage any) map[string]any {
	r := map[string]any{
		"id": b.id, "object": "response", "created_at": b.created, "model": b.req.Chat.Model,
		"status": status, "output": output, "error": nil, "incomplete_details": nil,
		"usage": usage, "store": false, "background": false, "previous_response_id": nil,
		"instructions": nil, "max_output_tokens": nil, "metadata": map[string]any{},
		"tools": []any{}, "tool_choice": "auto", "parallel_tool_calls": true,
		"temperature": nil, "top_p": nil, "reasoning": nil,
		"text": map[string]any{"format": map[string]any{"type": "text"}}, "truncation": "disabled",
	}
	for _, k := range []string{"instructions", "max_output_tokens", "metadata", "tools", "tool_choice", "parallel_tool_calls", "temperature", "top_p", "reasoning", "text"} {
		if v := b.req.Source[k]; v != nil {
			r[k] = v
		}
	}
	return r
}

// cacheCounter reads aliases without float conversion or summation. WorkBuddy
// may emit zero compatibility aliases beside a real hit count. Distinct positive
// counts are ambiguous and fail rather than silently choosing a billing value.
func cacheCounter(u map[string]any, paths ...[]string) (int64, bool, error) {
	var count int64
	seen := false
	for _, path := range paths {
		var v any = u
		for _, key := range path {
			m, _ := v.(map[string]any)
			v = m[key]
		}
		if v == nil {
			continue
		}
		n, ok := jsondoc.Int(v)
		if !ok || n < 0 {
			return 0, false, fmt.Errorf("invalid upstream cache counter %s", strings.Join(path, "."))
		}
		seen = true
		if count > 0 && n > 0 && count != n {
			return 0, false, fmt.Errorf("conflicting upstream cache counters")
		}
		if n > 0 {
			count = n
		}
	}
	return count, seen, nil
}

func usageFor(kind Kind, raw any) (any, error) {
	u, ok := raw.(map[string]any)
	if !ok {
		if kind == Responses {
			return nil, nil
		}
		return nil, fmt.Errorf("upstream usage is required for Anthropic output")
	}
	p, pok := jsondoc.Int(u["prompt_tokens"])
	c, cok := jsondoc.Int(u["completion_tokens"])
	if !pok || !cok || p < 0 || c < 0 || p > int64(^uint64(0)>>1)-c {
		return nil, fmt.Errorf("invalid or missing upstream token counts")
	}
	out := map[string]any{"input_tokens": p, "output_tokens": c}
	if kind == Responses {
		out["total_tokens"] = p + c
	}
	// Only emit observed cache counters. Never treat a cache miss as a cache write.
	hit, hasHit, err := cacheCounter(u, []string{"prompt_tokens_details", "cached_tokens"},
		[]string{"prompt_cache_hit_tokens"}, []string{"cache_read_input_tokens"},
		[]string{"cached_tokens"}, []string{"input_tokens_details", "cached_tokens"})
	if err != nil {
		return nil, err
	}
	created, hasCreated, err := cacheCounter(u, []string{"prompt_tokens_details", "cache_write_tokens"},
		[]string{"input_tokens_details", "cache_write_tokens"}, []string{"cache_creation_input_tokens"})
	if err != nil {
		return nil, err
	}
	if hit > p || created > p-hit {
		return nil, fmt.Errorf("upstream cache counters exceed prompt tokens")
	}
	if kind == Anthropic {
		if hasHit {
			out["cache_read_input_tokens"] = hit
		}
		if hasCreated {
			out["cache_creation_input_tokens"] = created
		}
		out["input_tokens"] = p - hit - created
	} else {
		if hasHit || hasCreated {
			details := map[string]any{}
			if hasHit {
				details["cached_tokens"] = hit
			}
			if hasCreated {
				// Gateway extension: Responses has no standard cache-write counter.
				details["cache_write_tokens"] = created
			}
			out["input_tokens_details"] = details
		}
	}
	if details, ok := u["completion_tokens_details"].(map[string]any); kind == Responses && ok {
		if v, exists := details["reasoning_tokens"]; exists {
			n, ok := jsondoc.Int(v)
			if !ok || n < 0 || n > c {
				return nil, fmt.Errorf("invalid reasoning token count")
			}
			out["output_tokens_details"] = map[string]any{"reasoning_tokens": n}
		}
	}
	return out, nil
}

func (b *responseBuilder) format(resp map[string]any) (map[string]any, error) {
	// Direct snapshot formatting has no stream chronology. Match the existing
	// Chat snapshot contract: content first, then tool array order.
	m, _, err := responseParts(resp)
	if err != nil {
		return nil, err
	}
	s := &outputState{}
	if text, _ := m["content"].(string); text != "" {
		s.blocks = append(s.blocks, blockRef{kind: textBlock})
	}
	c, err := s.complete(resp)
	if err != nil {
		return nil, err
	}
	return b.formatCompletion(c)
}

func (b *responseBuilder) formatCompletion(completion *Completion) (map[string]any, error) {
	resp := completion.Raw
	m, finish, err := responseParts(resp)
	if err != nil {
		return nil, err
	}
	usage, err := usageFor(b.req.Kind, resp["usage"])
	if err != nil {
		return nil, err
	}
	text := ""
	if v := m["content"]; v != nil {
		var ok bool
		text, ok = v.(string)
		if !ok {
			return nil, fmt.Errorf("only upstream text content is supported")
		}
	}
	calls, _ := m["tool_calls"].([]any)
	incomplete := finish == "length" || finish == "content_filter"
	if len(calls) > 0 && finish != "tool_calls" && !(b.req.Kind == Responses && incomplete) {
		return nil, fmt.Errorf("unfinished or inconsistently terminated upstream tool calls")
	}
	if finish == "tool_calls" && len(calls) == 0 {
		return nil, fmt.Errorf("tool_calls finish without calls")
	}
	if err := validateToolSelection(b.req, calls, finish); err != nil {
		return nil, err
	}
	if err := completion.validatePlan(text, len(calls)); err != nil {
		return nil, err
	}
	output := []any{}
	status := "completed"
	if incomplete {
		status = "incomplete"
	}
	ids := map[string]bool{}
	declared := map[string]bool{}
	for _, name := range declaredToolNames(b.req) {
		declared[name] = true
	}
	for _, block := range completion.blocks {
		if block.kind == textBlock {
			if b.req.Kind == Responses {
				output = append(output, map[string]any{"id": b.messageID, "type": "message", "role": "assistant", "status": status,
					"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}}})
			} else {
				output = append(output, map[string]any{"type": "text", "text": text})
			}
			continue
		}
		if block.tool >= len(calls) {
			return nil, fmt.Errorf("tool block no longer matches completion")
		}
		call, ok := calls[block.tool].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid upstream tool call")
		}
		if err := fields(call, "upstream.tool_call", "id type function"); err != nil {
			return nil, err
		}
		fn, _ := call["function"].(map[string]any)
		if err := fields(fn, "upstream.tool_call.function", "name arguments"); err != nil {
			return nil, err
		}
		id, _ := call["id"].(string)
		name, _ := fn["name"].(string)
		args, hasArgs := fn["arguments"].(string)
		if !hasArgs {
			return nil, fmt.Errorf("missing upstream tool arguments")
		}
		if v := call["type"]; v != nil && v != "function" {
			return nil, fmt.Errorf("unsupported upstream tool type")
		}
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" || ids[id] {
			return nil, fmt.Errorf("missing or duplicate upstream tool identity")
		}
		if !declared[name] {
			return nil, fmt.Errorf("upstream tool name does not match declared tools")
		}
		ids[id] = true
		var input map[string]any
		if incomplete {
			err = objectPrefix(args)
		} else {
			input, err = jsondoc.Object([]byte(args))
		}
		if err != nil {
			return nil, fmt.Errorf("invalid upstream tool input: %w", err)
		}
		if b.req.Kind == Responses {
			identity := b.req.responseToolIdentity(name)
			item := map[string]any{"type": "function_call", "id": newID("fc_"), "call_id": id, "name": identity.name, "arguments": args, "status": status}
			if identity.namespace != "" {
				item["namespace"] = identity.namespace
			}
			output = append(output, item)
		} else {
			output = append(output, map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
		}
	}
	if b.req.Kind == Responses {
		r := b.responseEnvelope(status, output, usage)
		if status == "incomplete" {
			reason := "max_output_tokens"
			if finish == "content_filter" {
				reason = "content_filter"
			}
			r["incomplete_details"] = map[string]any{"reason": reason}
		}
		return r, nil
	}
	stop := map[string]string{"stop": "end_turn", "length": "max_tokens", "tool_calls": "tool_use", "content_filter": "refusal"}[finish]
	return map[string]any{"id": b.id, "type": "message", "role": "assistant", "model": b.req.Chat.Model,
		"content": output, "stop_reason": stop, "stop_sequence": nil, "usage": usage}, nil
}

func Format(req *Request, resp map[string]any) (map[string]any, error) {
	if req.Kind == Chat {
		return resp, nil
	}
	return newBuilder(req).format(resp)
}
