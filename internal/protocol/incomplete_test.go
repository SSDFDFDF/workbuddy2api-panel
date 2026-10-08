package protocol

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func partialToolStream(args, finish string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"checking\"}}]}\n\n"+
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":%q}}]},\"finish_reason\":%q}]}\n\n"+
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", args, finish)
}

func TestObjectPrefix(t *testing.T) {
	for _, full := range []string{
		`{}`, ` {"a":[true,false,null,-1.25e-12,"a\\b\"c\u1234",{"n":9007199254740993}]} `,
		`{"nested":{"a":1},"again":{"a":2}}`,
		strings.Repeat(`{"a":`, jsondoc.MaxDepth) + `{}` + strings.Repeat(`}`, jsondoc.MaxDepth),
	} {
		for i := 0; i <= len(full); i++ {
			if err := objectPrefix(full[:i]); err != nil {
				t.Fatalf("valid prefix %q: %v", full[:i], err)
			}
		}
	}
	for _, raw := range []string{
		`[]`, `null`, `1`, `"x"`, `}{`, `{} {`, `{}x`, "\v", "\u00a0{}", "{\"x\":\"\xff", `{]`, `{"x":]`, `{"x":truX`,
		`{"x":1 "y"`, `{"x":1,}`, `{"x":[1,]}`, `{"x":01`, `{"x":"\q`,
		`{"x":1,"x":`, `{"x":1,"\u0078":2}`, `{"x":[{"a":1,"a":`,
		`{"x":` + strings.Repeat(`[`, jsondoc.MaxDepth+1),
	} {
		if err := objectPrefix(raw); err == nil {
			t.Errorf("invalid prefix accepted: %q", raw)
		}
	}
}

func TestIncompleteToolsDiagnosticOnly(t *testing.T) {
	for _, finish := range []string{"length", "content_filter"} {
		for _, args := range []string{"", "{", ` {"n":9007199254740993,"s":"unfinished`, `{"n":1}`, `{"x":tru`} {
			t.Run(finish+args, func(t *testing.T) {
				req := testRequest(t, Responses)
				raw := partialToolStream(args, finish)
				completion, err := Aggregate(strings.NewReader(raw), req)
				if err != nil {
					t.Fatal(err)
				}
				out, err := completion.Format(req)
				if err != nil {
					t.Fatal(err)
				}
				wantReason := "max_output_tokens"
				if finish == "content_filter" {
					wantReason = finish
				}
				if out["status"] != "incomplete" || out["incomplete_details"].(map[string]any)["reason"] != wantReason {
					t.Fatal(out)
				}
				call := out["output"].([]any)[1].(map[string]any)
				if call["arguments"] != args || call["status"] != "incomplete" || call["call_id"] != "call" || call["name"] != "lookup" {
					t.Fatal(call)
				}
				rec := httptest.NewRecorder()
				if err := Stream(rec, strings.NewReader(raw), req, nil); err != nil {
					t.Fatal(err)
				}
				es := events(t, rec.Body.String(), Responses)
				if es[len(es)-1]["type"] != "response.incomplete" {
					t.Fatal(es)
				}
				got := es[len(es)-1]["response"].(map[string]any)
				compareSemantic(t, got["output"], out["output"])
				compareSemantic(t, got["usage"], out["usage"])
				for _, e := range es {
					if strings.HasPrefix(e["type"].(string), "response.function_call") || e["type"] == "response.completed" {
						t.Fatal("executable tool event", e)
					}
					if item, ok := e["item"].(map[string]any); ok && item["type"] == "function_call" {
						t.Fatal("tool lifecycle event", e)
					}
				}
				// Even syntactically complete args in a cut-off turn are not a
				// completed tool call and cannot be replayed as completed history.
				next, _ := json.Marshal(map[string]any{"model": "x", "store": false, "input": out["output"]})
				if _, err := Decode(Responses, next); err == nil {
					t.Fatal("incomplete tool history accepted")
				}
				if _, err := completion.Format(testRequest(t, Anthropic)); err == nil {
					t.Fatal("Anthropic truncated tool accepted")
				}
				if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, Anthropic), nil); err == nil {
					t.Fatal("Anthropic truncated tool streamed")
				}
			})
		}
	}
}

func TestIncompleteDoesNotHideInvalidTools(t *testing.T) {
	base := partialToolStream(`{"x":`, "length")
	for _, raw := range []string{
		strings.Replace(base, `"name":"lookup"`, `"name":"look"`, 1),
		strings.Replace(base, `"name":"lookup"`, `"name":""`, 1),
		strings.Replace(base, `"id":"call"`, `"id":""`, 1),
		strings.Replace(base, `"type":"function"`, `"type":"custom"`, 1),
		strings.Replace(base, `"arguments":"{\"x\":"`, `"arguments":null`, 1),
		strings.Replace(base, `"arguments":"{\"x\":"`, `"extra":1`, 1),
		partialToolStream(`{"x":]`, "length"),
		partialToolStream(`{"x":1,"x":`, "content_filter"),
		partialToolStream(`[]`, "length"),
		strings.Replace(base, `"arguments":"{\"x\":"`, `"arguments":42`, 1),
		strings.Replace(base, `"name":"lookup"`, `"name":"lookup","extension":true`, 1),
		strings.Replace(base, `"id":"call"`, `"id":17`, 1),
		partialToolStream(`{`, "tool_calls"),
		partialToolStream(`{}`, "stop"),
		strings.Replace(base, `,"finish_reason":"length"`, "", 1),
		strings.Replace(base, "data: [DONE]", `data: {"error":{"message":"tail error"}}`, 1),
	} {
		req := testRequest(t, Responses)
		c, err := Aggregate(strings.NewReader(raw), req)
		if err == nil {
			_, err = c.Format(req)
		}
		if err == nil {
			t.Fatal("invalid tool became incomplete", raw)
		}
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), req, nil); err == nil {
			t.Fatal("invalid tool streamed", raw)
		}
		for _, marker := range []string{"event: response.incomplete", "event: response.completed", "event: response.function_call", `"type":"function_call"`} {
			if strings.Contains(rec.Body.String(), marker) {
				t.Fatal("invalid tool exposed", rec.Body)
			}
		}
	}
}

func TestIncompleteNamesAndNativeIsolation(t *testing.T) {
	for _, names := range [][]any{{"look", "lookup"}, {"lookup", "lookup"}, {"lo", "ok", "up"}} {
		raw := strings.Replace(nameChunks(names), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
		req := requestWithNames(t, Responses, []string{"lookup"})
		c, err := Aggregate(strings.NewReader(raw), req)
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.Format(req)
		if err != nil || out["output"].([]any)[0].(map[string]any)["name"] != "lookup" {
			t.Fatal(out, err)
		}
		native, err := upstream.Aggregate(strings.NewReader(raw), upstream.WithDeclaredToolNames([]string{"lookup"}))
		if err != nil {
			t.Fatal(err)
		}
		call := native["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
		var literal string
		for _, n := range names {
			literal += n.(string)
		}
		if call["function"].(map[string]any)["name"] != literal {
			t.Fatal("native name changed", call)
		}
	}
	ambiguous := strings.Replace(nameChunks([]any{"lookup", "lookup"}), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
	if _, err := Aggregate(strings.NewReader(ambiguous), requestWithNames(t, Responses, []string{"lookup", "lookuplookup"})); err == nil {
		t.Fatal("ambiguous truncated name accepted")
	}
	// Snapshot/direct formatting follows the same prefix validation as deltas.
	raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{\"n\":"}}]},"finish_reason":"length"}]}`
	c, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Format(testRequest(t, Responses), c.Raw); err != nil {
		t.Fatal(err)
	}
	if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, Responses), nil); err != nil {
		t.Fatal(err)
	}
}

func TestIncompleteParallelTurnIsAtomic(t *testing.T) {
	raw := strings.Replace(toolsStream, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
	// One valid completed argument object and one unfinished object do not
	// authorize either tool in a cutoff turn.
	raw = strings.Replace(raw, `\"n\":2}`, `\"n\":`, 1)
	req := testRequest(t, Responses)
	c, err := Aggregate(strings.NewReader(raw), req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Format(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range out["output"].([]any) {
		if item.(map[string]any)["status"] != "incomplete" {
			t.Fatal("partial turn upgraded a tool", item)
		}
	}
	rec := httptest.NewRecorder()
	if err := Stream(rec, strings.NewReader(raw), req, nil); err != nil {
		t.Fatal(err)
	}
	for _, e := range events(t, rec.Body.String(), Responses) {
		if item, ok := e["item"].(map[string]any); ok && item["type"] == "function_call" {
			t.Fatal("partial turn emitted tool lifecycle", e)
		}
	}
	// Duplicate call IDs must still fail even with an explicit cutoff.
	raw = strings.Replace(raw, `"id":"b"`, `"id":"a"`, 1)
	c, err = Aggregate(strings.NewReader(raw), req)
	if err == nil {
		_, err = c.Format(req)
	}
	if err == nil {
		t.Fatal("duplicate truncated tool IDs accepted")
	}
}

func FuzzToolCutoff(f *testing.F) {
	f.Add(`{"n":`, byte(0))
	f.Add(`{"x":]`, byte(1))
	f.Add(`{"n":9007199254740993}`, byte(2))
	f.Fuzz(func(t *testing.T, args string, selector byte) {
		if len(args) > 1024 {
			t.Skip()
		}
		finish := []string{"length", "content_filter", "tool_calls", ""}[int(selector)%4]
		raw := partialToolStream(args, finish)
		req := testRequest(t, Responses)
		c, err := Aggregate(strings.NewReader(raw), req)
		var out map[string]any
		if err == nil {
			out, err = c.Format(req)
		}
		rec := httptest.NewRecorder()
		se := Stream(rec, strings.NewReader(raw), req, nil)
		if (err == nil) != (se == nil) {
			t.Fatal("stream/aggregate mismatch", err, se)
		}
		if err != nil || finish != "tool_calls" {
			if strings.Contains(rec.Body.String(), "response.function_call_arguments") {
				t.Fatal("non-completed tool exposed", rec.Body)
			}
		}
		if err == nil {
			es := events(t, rec.Body.String(), Responses)
			terminal := es[len(es)-1]["response"].(map[string]any)
			compareSemantic(t, terminal["output"], out["output"])
		}
	})
}

func FuzzObjectPrefix(f *testing.F) {
	for _, seed := range []string{`{}`, `{"x":`, `{"a":[null,true,-1.2e3,"a\\b",{}]}`, `{"x":1,"x":2}`, `{"x":]`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		err := objectPrefix(raw)
		if json.Valid([]byte(raw)) {
			_, expected := jsondoc.Object([]byte(raw))
			if (err == nil) != (expected == nil) {
				t.Fatalf("complete JSON validation differs: %q: %v / %v", raw, err, expected)
			}
			if expected == nil {
				// Invalid UTF-8 at the end of an arbitrary byte prefix is not a
				// legal SSE argument string; test only character boundaries.
				for i := 0; i <= len(raw); i++ {
					if !utf8.ValidString(raw[:i]) {
						continue
					}
					if err := objectPrefix(raw[:i]); err != nil {
						t.Fatalf("valid JSON prefix rejected: %q: %v", raw[:i], err)
					}
				}
			}
		}
	})
}
