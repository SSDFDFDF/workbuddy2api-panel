package protocol

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func requestWithNames(t *testing.T, kind Kind, names []string) *Request {
	t.Helper()
	req := testRequest(t, kind)
	tools := []any{}
	for _, name := range names {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name, "parameters": map[string]any{"type": "object"}, "strict": false}})
	}
	req.Chat.Object["tools"] = tools
	return req
}

func nameChunks(chunks []any) string {
	var b strings.Builder
	for i, name := range chunks {
		fn := map[string]any{"name": name, "arguments": ""}
		call := map[string]any{"index": 0, "function": fn}
		if i == 0 {
			call["id"] = "call_1"
			call["type"] = "function"
			fn["arguments"] = "{}"
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{call}}}}})
		b.WriteString("data: " + string(raw) + "\n\n")
	}
	b.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	return b.String()
}

func TestToolNameDialects(t *testing.T) {
	for _, tt := range []struct {
		name   string
		names  []string
		chunks []any
		want   string
	}{
		{"whole", []string{"lookup"}, []any{"lookup"}, "lookup"},
		{"repeated", []string{"lookup"}, []any{"lookup", "lookup", "", nil, "lookup"}, "lookup"},
		{"incremental", []string{"lookup"}, []any{"lo", "ok", "up"}, "lookup"},
		{"cumulative", []string{"lookup"}, []any{"l", "look", "lookup"}, "lookup"},
		{"mixed", []string{"lookup"}, []any{"lo", "look", "up", "lookup"}, "lookup"},
		{"late", []string{"lookup"}, []any{"", nil, "look", "up"}, "lookup"},
		{"repeat-fragment", []string{"lookup"}, []any{"look", "look", "up"}, "lookup"},
		{"real-repeated-fragment", []string{"foofoo"}, []any{"foo", "foo"}, "foofoo"},
		{"ambiguous", []string{"foo", "foofoo"}, []any{"foo", "foo"}, ""},
		{"ambiguous-cumulative", []string{"look", "lolook"}, []any{"lo", "look"}, ""},
		{"unrelated-replacement", []string{"lookup", "other"}, []any{"lookup", "other"}, ""},
		{"undeclared", []string{"lookup"}, []any{"unknown"}, ""},
		{"not-a-prefix-repair", []string{"lookup"}, []any{"look"}, ""},
		{"missing-name", []string{"lookup"}, []any{"", nil}, ""},
		{"no-tools", nil, []any{"lookup"}, ""},
		{"invalid-type", []string{"lookup"}, []any{42}, ""},
	} {
		for _, kind := range []Kind{Responses, Anthropic} {
			t.Run(string(kind)+"/"+tt.name, func(t *testing.T) {
				req := requestWithNames(t, kind, tt.names)
				raw := nameChunks(tt.chunks)
				resp, err := Aggregate(strings.NewReader(raw), req)
				var out map[string]any
				if err == nil {
					out, err = resp.Format(req)
				}
				rec := httptest.NewRecorder()
				se := Stream(rec, strings.NewReader(raw), req, nil)
				if tt.want == "" {
					if err == nil || se == nil {
						t.Fatalf("expected rejection: aggregate=%v stream=%v", err, se)
					}
					for _, marker := range []string{"response.completed", "message_stop", `"type":"tool_use"`, "response.function_call_arguments.delta"} {
						if strings.Contains(rec.Body.String(), marker) {
							t.Fatalf("invalid tool emitted: %s", rec.Body)
						}
					}
					return
				}
				if err != nil || se != nil {
					t.Fatalf("aggregate=%v stream=%v", err, se)
				}
				key := "output"
				if kind == Anthropic {
					key = "content"
				}
				if got := out[key].([]any)[0].(map[string]any)["name"]; got != tt.want {
					t.Fatalf("got %v want %s", got, tt.want)
				}
				if !strings.Contains(rec.Body.String(), `"name":"`+tt.want+`"`) {
					t.Fatal(rec.Body.String())
				}
			})
		}
	}
}

func TestToolNameSnapshotAndFormatValidation(t *testing.T) {
	for _, kind := range []Kind{Responses, Anthropic} {
		req := requestWithNames(t, kind, []string{"lookup"})
		for _, name := range []string{"lookup", "unknown"} {
			raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + name + `","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
			native, err := upstream.Aggregate(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			_, fe := Format(req, native)
			_, ae := Aggregate(strings.NewReader(raw), req)
			se := Stream(httptest.NewRecorder(), strings.NewReader(raw), req, nil)
			if name == "lookup" && (fe != nil || ae != nil || se != nil) {
				t.Fatal(fe, ae, se)
			}
			if name != "lookup" && (fe == nil || ae == nil || se == nil) {
				t.Fatal("undeclared snapshot accepted", fe, ae, se)
			}
		}
	}
}

func TestNameResolutionDoesNotAlterNativeChatOrObservedFrames(t *testing.T) {
	raw := nameChunks([]any{"look", "lookup"})
	// Native Chat continues to concatenate literally, including with bridge-only options.
	for _, opts := range [][]upstream.StreamOption{nil, {upstream.WithDeclaredToolNames([]string{"lookup"})}} {
		resp, err := upstream.Aggregate(strings.NewReader(raw), opts...)
		if err != nil {
			t.Fatal(err)
		}
		call := resp["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
		if call["function"].(map[string]any)["name"] != "looklookup" {
			t.Fatal(call)
		}
	}
	var names []string
	observe := upstream.WithFrameObserver(func(frame map[string]any) {
		for _, v := range frame["choices"].([]any) {
			delta, _ := v.(map[string]any)["delta"].(map[string]any)
			calls, _ := delta["tool_calls"].([]any)
			for _, v := range calls {
				names = append(names, v.(map[string]any)["function"].(map[string]any)["name"].(string))
			}
		}
	})
	if _, err := Aggregate(strings.NewReader(raw), requestWithNames(t, Responses, []string{"lookup"}), observe); err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "look,lookup" {
		t.Fatal("observer saw rewritten frames", names)
	}
}
