package protocol

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/upstream"
)

func TestToolSelectionContract(t *testing.T) {
	forced := func(name string) any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	one := nameChunks([]any{"lookup"})
	for _, kind := range []Kind{Responses, Anthropic} {
		for _, tt := range []struct {
			name     string
			choice   any
			parallel any
			raw      string
			ok       bool
		}{
			{"auto-text", "auto", nil, textStream, true},
			{"auto-tools", "auto", nil, toolsStream, true},
			{"none-text", "none", nil, textStream, true},
			{"none-tools", "none", nil, toolsStream, false},
			{"required-text", "required", nil, textStream, false},
			{"required-tools", "required", nil, toolsStream, true},
			{"forced-text", forced("lookup"), nil, textStream, false},
			{"forced-other", forced("other"), nil, one, false},
			{"forced-match", forced("lookup"), false, one, true},
			{"no-parallel", "auto", false, toolsStream, false},
			{"parallel-cutoff", "auto", false, strings.Replace(toolsStream, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), false},
			{"forced-cutoff-match", forced("lookup"), false, strings.Replace(one, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), kind == Responses},
			{"parallel-allowed", "auto", true, toolsStream, true},
			{"required-cutoff", "required", false, strings.Replace(textStream, `"stop"`, `"length"`, 1), true},
			{"forced-filtered", forced("lookup"), false, strings.Replace(textStream, `"stop"`, `"content_filter"`, 1), true},
			{"none-cutoff-tool", "none", nil, strings.Replace(one, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), false},
			{"forced-cutoff-wrong", forced("other"), nil, strings.Replace(one, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), false},
		} {
			t.Run(string(kind)+"/"+tt.name, func(t *testing.T) {
				req := testRequest(t, kind)
				req.Chat.Object["tool_choice"] = tt.choice
				if tt.parallel != nil {
					req.Chat.Object["parallel_tool_calls"] = tt.parallel
				}
				c, err := Aggregate(strings.NewReader(tt.raw), req)
				if err == nil {
					_, err = c.Format(req)
				}
				rec := httptest.NewRecorder()
				se := Stream(rec, strings.NewReader(tt.raw), req, nil)
				if (err == nil) != tt.ok || (se == nil) != tt.ok {
					t.Fatal("wrong decision", err, se)
				}
				if !tt.ok {
					for _, marker := range []string{"response.completed", "message_stop", "response.function_call_arguments", `"type":"tool_use"`} {
						if strings.Contains(rec.Body.String(), marker) {
							t.Fatal("tool escaped before selection validation", rec.Body)
						}
					}
				}
				// Snapshot/direct formatting cannot bypass the request constraints.
				native, ne := upstream.Aggregate(strings.NewReader(tt.raw))
				if ne == nil {
					_, fe := Format(req, native)
					if (fe == nil) != tt.ok {
						t.Fatal("direct Format disagrees", fe)
					}
				}
			})
		}
	}
}

func FuzzToolSelection(f *testing.F) {
	f.Add(byte(0), byte(0), byte(0), true)
	f.Add(byte(2), byte(1), byte(1), false)
	f.Fuzz(func(t *testing.T, selection, toolCount, termination byte, parallel bool) {
		choice := []any{"auto", "none", "required", map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}}[int(selection)%4]
		finish := []string{"stop", "tool_calls", "length", "content_filter"}[int(termination)%4]
		var calls []any
		for i := 0; i < int(toolCount)%3; i++ {
			name := []string{"lookup", "other"}[i]
			calls = append(calls, map[string]any{"index": i, "id": fmt.Sprint(i), "type": "function", "function": map[string]any{"name": name, "arguments": "{}"}})
		}
		delta := map[string]any{"content": "checking"}
		if len(calls) > 0 {
			delta["tool_calls"] = calls
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1}})
		raw := "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
		for _, kind := range []Kind{Responses, Anthropic} {
			req := testRequest(t, kind)
			req.Chat.Object["tool_choice"], req.Chat.Object["parallel_tool_calls"] = choice, parallel
			c, err := Aggregate(strings.NewReader(raw), req)
			if err == nil {
				_, err = c.Format(req)
			}
			rec := httptest.NewRecorder()
			se := Stream(rec, strings.NewReader(raw), req, nil)
			if (err == nil) != (se == nil) {
				t.Fatal("selection decision differs", err, se)
			}
			if err != nil || finish != "tool_calls" {
				for _, marker := range []string{"response.function_call_arguments", `"type":"tool_use"`} {
					if strings.Contains(rec.Body.String(), marker) {
						t.Fatal("unauthorized tool delivery", rec.Body)
					}
				}
			}
		}
	})
}

func TestToolSelectionDoesNotChangeNativeChat(t *testing.T) {
	req := mustDecode(t, Chat, `{"model":"x","messages":[{"role":"user","content":"hi"}],"tool_choice":"none","parallel_tool_calls":false,"tools":[{"type":"function","function":{"name":"lookup"}}]}`)
	native, err := upstream.Aggregate(strings.NewReader(toolsStream))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Format(req, native)
	if err != nil || fmt.Sprint(out) != fmt.Sprint(native) {
		t.Fatal("native constraints changed", err)
	}
}
