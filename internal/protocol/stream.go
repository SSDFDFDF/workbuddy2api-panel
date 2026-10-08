package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// Stream relays text immediately but buffers tools until the native completion
// predicate and the destination schema both validate. Tool names can arrive in
// fragments, and emitting an executable call before validation is unsafe.
func Stream(w http.ResponseWriter, r io.Reader, req *Request, hint func(string) string, opts ...upstream.StreamOption) error {
	b := newBuilder(req)
	started, textStarted := false, false
	order := &outputOrder{}
	sequence := 0
	emit := func(name string, data map[string]any) error {
		data["type"] = name
		if req.Kind == Responses {
			data["sequence_number"] = sequence
			sequence++
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw); err != nil {
			return &upstream.DownstreamWriteError{Err: err}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return nil
	}
	start := func() error {
		if started {
			return nil
		}
		started = true
		if req.Kind == Responses {
			if err := emit("response.created", map[string]any{"response": b.responseEnvelope("in_progress", []any{}, nil)}); err != nil {
				return err
			}
			return emit("response.in_progress", map[string]any{"response": b.responseEnvelope("in_progress", []any{}, nil)})
		}
		// Zeroes here are provisional stream counters, never final accounting.
		// The real input/output counters are required and replace them at the end.
		return emit("message_start", map[string]any{"message": map[string]any{"id": b.id, "type": "message", "role": "assistant", "model": req.Chat.Model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}})
	}
	startText := func() error {
		if textStarted {
			return nil
		}
		textStarted = true
		if err := start(); err != nil {
			return err
		}
		if req.Kind == Anthropic {
			return emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		}
		if err := emit("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": b.messageID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}); err != nil {
			return err
		}
		return emit("response.content_part.added", map[string]any{"item_id": b.messageID, "output_index": 0, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}})
	}
	opts = completionOptions(req, opts)
	resp, err := upstream.ConsumeCompletion(r, func(chunk map[string]any) error {
		if err := order.observe(chunk); err != nil {
			return err
		}
		choices, _ := chunk["choices"].([]any)
		for _, v := range choices {
			ch := v.(map[string]any)
			m, _ := ch["delta"].(map[string]any)
			if m == nil {
				m, _ = ch["message"].(map[string]any)
			}
			if v := m["content"]; v != nil {
				text := v.(string)
				if text != "" {
					if err := startText(); err != nil {
						return err
					}
					if req.Kind == Anthropic {
						if err := emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": text}}); err != nil {
							return err
						}
					} else {
						if err := emit("response.output_text.delta", map[string]any{"item_id": b.messageID, "output_index": 0, "content_index": 0, "delta": text, "logprobs": []any{}}); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}, opts...)
	var result map[string]any
	if err == nil {
		result, err = b.format(resp)
	}
	if err != nil {
		var we *upstream.DownstreamWriteError
		if errors.As(err, &we) {
			return err
		}
		status, body := streamError(err, hint)
		if !started {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if e := json.NewEncoder(w).Encode(ErrorBody(req.Kind, status, body)); e != nil {
				return &upstream.DownstreamWriteError{Err: e}
			}
			return err
		}
		if req.Kind == Anthropic {
			if e := emit("error", ErrorBody(req.Kind, status, body)); e != nil {
				return e
			}
		} else {
			failed := b.responseEnvelope("failed", []any{}, nil)
			failed["error"] = body["error"]
			if e := emit("response.failed", map[string]any{"response": failed}); e != nil {
				return e
			}
		}
		return err
	}
	if err := start(); err != nil {
		return err
	}
	key := "output"
	if req.Kind == Anthropic {
		key = "content"
	}
	items := result[key].([]any)
	for i, v := range items {
		item := v.(map[string]any)
		if item["type"] == "text" || item["type"] == "message" {
			if err := startText(); err != nil {
				return err
			}
			if req.Kind == Anthropic {
				if err := emit("content_block_stop", map[string]any{"index": i}); err != nil {
					return err
				}
			} else {
				part := item["content"].([]any)[0].(map[string]any)
				if err := emit("response.output_text.done", map[string]any{"item_id": b.messageID, "output_index": i, "content_index": 0, "text": part["text"], "logprobs": []any{}}); err != nil {
					return err
				}
				if err := emit("response.content_part.done", map[string]any{"item_id": b.messageID, "output_index": i, "content_index": 0, "part": part}); err != nil {
					return err
				}
				if err := emit("response.output_item.done", map[string]any{"output_index": i, "item": item}); err != nil {
					return err
				}
			}
			continue
		}
		if req.Kind == Anthropic {
			if err := emit("content_block_start", map[string]any{"index": i, "content_block": map[string]any{"type": "tool_use", "id": item["id"], "name": item["name"], "input": map[string]any{}}}); err != nil {
				return err
			}
			raw, err := json.Marshal(item["input"])
			if err != nil {
				return err
			}
			if err := emit("content_block_delta", map[string]any{"index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw)}}); err != nil {
				return err
			}
			if err := emit("content_block_stop", map[string]any{"index": i}); err != nil {
				return err
			}
		} else {
			initial := map[string]any{"id": item["id"], "type": "function_call", "call_id": item["call_id"], "name": item["name"], "arguments": "", "status": "in_progress"}
			if err := emit("response.output_item.added", map[string]any{"output_index": i, "item": initial}); err != nil {
				return err
			}
			if err := emit("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": i, "delta": item["arguments"]}); err != nil {
				return err
			}
			if err := emit("response.function_call_arguments.done", map[string]any{"item_id": item["id"], "output_index": i, "name": item["name"], "arguments": item["arguments"]}); err != nil {
				return err
			}
			if err := emit("response.output_item.done", map[string]any{"output_index": i, "item": item}); err != nil {
				return err
			}
		}
	}
	if req.Kind == Anthropic {
		if err := emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": result["stop_reason"], "stop_sequence": nil}, "usage": result["usage"]}); err != nil {
			return err
		}
		return emit("message_stop", map[string]any{})
	}
	terminal := "response.completed"
	if result["status"] == "incomplete" {
		terminal = "response.incomplete"
	}
	return emit(terminal, map[string]any{"response": result})
}
