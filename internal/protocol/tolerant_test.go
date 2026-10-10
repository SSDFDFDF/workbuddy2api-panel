package protocol

import (
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/upstream"
)

// TestLegacyFunctionCallBridge pins the compatibility path that used to fail
// with "upstream message field function_call cannot be represented": a legacy
// single call from the Chat upstream is delivered as a native function call even
// though the client declared no tools.
func TestLegacyFunctionCallBridge(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"function_call":{"name":"ToolSearch","arguments":"{\"q\":"}}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"function_call":{"arguments":"\"mail\"}"}}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}` + "\n\ndata: [DONE]\n\n"
	open := mustDecode(t, Responses, `{"model":"x","store":false,"input":"ping"}`)
	completion, err := Aggregate(strings.NewReader(raw), open)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(open)
	if err != nil {
		t.Fatal(err)
	}
	items := out["output"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	call := items[1].(map[string]any)
	if call["type"] != "function_call" || call["name"] != "ToolSearch" || call["arguments"] != `{"q":"mail"}` || call["status"] != "completed" {
		t.Fatal(call)
	}
	rec := httptest.NewRecorder()
	if err := Stream(rec, strings.NewReader(raw), open, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"name":"ToolSearch"`) || !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatal(rec.Body.String())
	}
	// The same call reaches Anthropic as a tool_use block.
	anthropic := mustDecode(t, Anthropic, `{"model":"x","max_tokens":64,"messages":[{"role":"user","content":"ping"}]}`)
	aout, err := completion.Format(anthropic)
	if err != nil {
		t.Fatal(err)
	}
	block := aout["content"].([]any)[1].(map[string]any)
	if block["type"] != "tool_use" || block["name"] != "ToolSearch" {
		t.Fatal(block)
	}
	if block["input"].(map[string]any)["q"] != "mail" {
		t.Fatal(block)
	}
}

// TestLegacyFunctionCallSnapshot covers a single-frame legacy message, both
// through the translating consumer and through the direct Format path over
// native aggregation.
func TestLegacyFunctionCallSnapshot(t *testing.T) {
	raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","function_call":{"name":"ToolSearch","arguments":"{}"}},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	open := mustDecode(t, Responses, `{"model":"x","store":false,"input":"ping"}`)
	completion, err := Aggregate(strings.NewReader(raw), open)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(open)
	if err != nil {
		t.Fatal(err)
	}
	call := out["output"].([]any)[1].(map[string]any)
	if call["name"] != "ToolSearch" || call["arguments"] != "{}" || call["status"] != "completed" {
		t.Fatal(call)
	}
	native, err := upstream.Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := Format(open, native)
	if err != nil {
		t.Fatal(err)
	}
	item := direct["output"].([]any)[1].(map[string]any)
	if item["name"] != "ToolSearch" || item["arguments"] != "{}" {
		t.Fatal(item)
	}
}

// TestEmptyLegacyFunctionCallIsIgnored keeps an all-empty placeholder from
// becoming an unusable call or failing the turn.
func TestEmptyLegacyFunctionCallIsIgnored(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"content":"ok","function_call":{}},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	open := mustDecode(t, Responses, `{"model":"x","store":false,"input":"ping"}`)
	completion, err := Aggregate(strings.NewReader(raw), open)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(open)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["output"].([]any)) != 1 {
		t.Fatal(out["output"])
	}
}

// TestUndeclaredToolCallsPassThroughWhenNoToolSetIsAuthorized: the request
// declares no tools and has no tool history, so the observed call is delivered
// rather than failing a turn the client authorized no tools for.
func TestUndeclaredToolCallsPassThrough(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"WebFetch","arguments":"{\"url\":\"x\"}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	for _, kind := range []Kind{Responses, Anthropic} {
		var req *Request
		if kind == Responses {
			req = mustDecode(t, kind, `{"model":"x","store":false,"input":"hi"}`)
		} else {
			req = mustDecode(t, kind, `{"model":"x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
		}
		completion, err := Aggregate(strings.NewReader(raw), req)
		if err != nil {
			t.Fatal(kind, err)
		}
		out, err := completion.Format(req)
		if err != nil {
			t.Fatal(kind, err)
		}
		key := "output"
		if kind == Anthropic {
			key = "content"
		}
		if got := out[key].([]any)[0].(map[string]any)["name"]; got != "WebFetch" {
			t.Fatal(kind, got)
		}
	}
}

// TestMessageExtrasTolerated pins the remaining tolerant mappings: refusal
// becomes text when no content exists, annotations reach Responses, text-part
// arrays are concatenated, unknown finish labels stop the turn, and logprobs
// are ignored.
func TestMessageExtrasTolerated(t *testing.T) {
	// Refusal text is delivered when no visible content exists; text-part arrays
	// are concatenated; the unknown finish label stops the turn and the inline
	// logprobs value is ignored.
	for _, tc := range []struct{ raw, want string }{
		{`data: {"choices":[{"index":0,"delta":{"role":"assistant","refusal":"cannot help","logprobs":{"content":[]}}}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"eos"}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n", "cannot help"},
		{`data: {"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"part one "},{"type":"text","text":"part two"}]},"finish_reason":"eos"}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n", "part one part two"},
	} {
		for _, kind := range []Kind{Responses, Anthropic} {
			var req *Request
			if kind == Responses {
				req = mustDecode(t, kind, `{"model":"x","store":false,"input":"hi"}`)
			} else {
				req = mustDecode(t, kind, `{"model":"x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
			}
			completion, err := Aggregate(strings.NewReader(tc.raw), req)
			if err != nil {
				t.Fatal(kind, err)
			}
			out, err := completion.Format(req)
			if err != nil {
				t.Fatal(kind, err)
			}
			text := ""
			if kind == Responses {
				text = out["output"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
				if out["status"] != "completed" {
					t.Fatal(out["status"])
				}
			} else {
				text = out["content"].([]any)[0].(map[string]any)["text"].(string)
				if out["stop_reason"] != "end_turn" {
					t.Fatal(out["stop_reason"])
				}
			}
			if text != tc.want {
				t.Fatal(kind, text)
			}
		}
	}
}

// TestAnnotationsReachResponses keeps non-empty annotations instead of dropping
// them, since Responses output_text carries them natively.
func TestAnnotationsReachResponses(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"content":"see ","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"content":"this"},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	req := mustDecode(t, Responses, `{"model":"x","store":false,"input":"hi"}`)
	completion, err := Aggregate(strings.NewReader(raw), req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(req)
	if err != nil {
		t.Fatal(err)
	}
	part := out["output"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	annotations := part["annotations"].([]any)
	if len(annotations) != 1 || annotations[0].(map[string]any)["type"] != "url_citation" {
		t.Fatal(part)
	}
}

// TestSnapshotOnlyStreamsText: a stream that sends one full `message` frame
// instead of deltas must still deliver its text to a streaming client.
func TestSnapshotOnlyStreamsText(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"message":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	for _, kind := range []Kind{Responses, Anthropic} {
		var req *Request
		if kind == Responses {
			req = mustDecode(t, kind, `{"model":"x","store":false,"input":"hi"}`)
		} else {
			req = mustDecode(t, kind, `{"model":"x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
		}
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), req, nil); err != nil {
			t.Fatal(kind, err)
		}
		if !strings.Contains(rec.Body.String(), "hello") {
			t.Fatal(kind, "snapshot text lost:\n", rec.Body.String())
		}
	}
}

// TestSnapshotWithTextAndTool: a single snapshot frame carrying both text and a
// tool call keeps the text block and the call in one consistent plan.
func TestSnapshotWithTextAndTool(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"message":{"role":"assistant","content":"checking","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	req := testRequest(t, Responses)
	completion, err := Aggregate(strings.NewReader(raw), req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(req)
	if err != nil {
		t.Fatal(err)
	}
	items := out["output"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["type"] != "message" || items[1].(map[string]any)["type"] != "function_call" {
		t.Fatal(items)
	}
}

// TestModernCallsSupersedeLegacyEcho: when the upstream emits both a modern
// tool call and a legacy function_call echo, the call is delivered once.
func TestModernCallsSupersedeLegacyEcho(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"ToolSearch","arguments":"{}"}}],"function_call":{"name":"ToolSearch","arguments":"{}"}}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	open := mustDecode(t, Responses, `{"model":"x","store":false,"input":"ping"}`)
	completion, err := Aggregate(strings.NewReader(raw), open)
	if err != nil {
		t.Fatal(err)
	}
	out, err := completion.Format(open)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, item := range out["output"].([]any) {
		if item.(map[string]any)["type"] == "function_call" {
			calls++
		}
	}
	if calls != 1 {
		t.Fatal("duplicate call delivered", out["output"])
	}
}

// TestSnapshotContentAfterToolDeltas: a turn whose deltas carry only a tool call
// and whose final snapshot adds explanatory text must keep both in the plan.
func TestSnapshotContentAfterToolDeltas(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"message":{"role":"assistant","content":"because reasons"},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}` + "\n\ndata: [DONE]\n\n"
	req := testRequest(t, Responses)
	completion, err := Aggregate(strings.NewReader(raw), req)
	if err != nil {
		t.Fatal("aggregate:", err)
	}
	out, err := completion.Format(req)
	if err != nil {
		t.Fatal("format:", err)
	}
	items := out["output"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	part := items[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if part["text"] != "because reasons" {
		t.Fatal(part)
	}
}

// TestRefusalFallbackWithCall: visible text that only exists as a refusal (never
// a content delta) still produces a text block next to the tool call.
func TestRefusalFallbackWithCall(t *testing.T) {
	raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"cannot help","function_call":{"name":"ToolSearch","arguments":"{}"}},"finish_reason":"function_call"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	open := mustDecode(t, Responses, `{"model":"x","store":false,"input":"ping"}`)
	completion, err := Aggregate(strings.NewReader(raw), open)
	if err != nil {
		t.Fatal("aggregate:", err)
	}
	out, err := completion.Format(open)
	if err != nil {
		t.Fatal("format:", err)
	}
	items := out["output"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	part := items[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if part["text"] != "cannot help" {
		t.Fatal(part)
	}
}

// TestRefusalOnlyTextReachesStream: text that never arrived as deltas is still
// delivered inside the closing text block in both protocols.
func TestRefusalOnlyTextReachesStream(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"refusal":"cannot help"},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
	for _, kind := range []Kind{Responses, Anthropic} {
		var req *Request
		if kind == Responses {
			req = mustDecode(t, kind, `{"model":"x","store":false,"input":"hi"}`)
		} else {
			req = mustDecode(t, kind, `{"model":"x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
		}
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), req, nil); err != nil {
			t.Fatal(kind, "stream:", err)
		}
		if !strings.Contains(rec.Body.String(), "cannot help") {
			t.Fatal(kind, "refusal text lost in stream:\n", rec.Body.String())
		}
	}
}
