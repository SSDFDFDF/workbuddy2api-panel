package protocol

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

const emptyIdentityStream = `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"","arguments":"{"}},{"index":1,"id":"b","type":"function","function":{"name":"other","arguments":"{}"}}]}}]}` + "\n\n" +
	`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":"","function":{"name":"lookup","arguments":"\"n\":1}"}},{"index":1,"id":"","function":{"name":"","arguments":""}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
	`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4}}` + "\n\ndata: [DONE]\n\n"

func TestEmptyToolIdentityContinuations(t *testing.T) {
	resp, err := Aggregate(strings.NewReader(emptyIdentityStream), testRequest(t, Responses))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{Responses, Anthropic} {
		out, err := Format(testRequest(t, kind), resp)
		if err != nil {
			t.Fatal(err)
		}
		key := "output"
		if kind == Anthropic {
			key = "content"
		}
		items := out[key].([]any)
		if len(items) != 2 || items[0].(map[string]any)["name"] != "lookup" || items[1].(map[string]any)["name"] != "other" {
			t.Fatal(items)
		}
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(emptyIdentityStream), testRequest(t, kind), nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Body.String(), `"name":""`) || strings.Contains(rec.Body.String(), `"call_id":""`) {
			t.Fatal(rec.Body.String())
		}
	}
	// This compatibility opt-in does not change native Chat's contract.
	if _, err := upstream.Aggregate(strings.NewReader(emptyIdentityStream)); err == nil {
		t.Fatal("native aggregate behavior changed")
	}
	if err := upstream.Stream(httptest.NewRecorder(), strings.NewReader(emptyIdentityStream)); err == nil {
		t.Fatal("native stream behavior changed")
	}
	for _, raw := range []string{
		strings.Replace(emptyIdentityStream, `"id":"","type":""`, `"id":"conflict","type":""`, 1),
		strings.Replace(emptyIdentityStream, `"index":0,"id":"","type":""`, `"id":"","type":""`, 1),
	} {
		if _, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses)); err == nil {
			t.Fatal("conflicting/ambiguous identity accepted")
		}
		for _, kind := range []Kind{Responses, Anthropic} {
			if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
				t.Fatal("conflicting/ambiguous identity streamed")
			}
		}
	}
}

func TestCacheAliasesAndPrecision(t *testing.T) {
	for _, tt := range []struct {
		name, counters   string
		hit, write       int64
		hasHit, hasWrite bool
	}{
		{"missing", ``, 0, 0, false, false},
		{"deepseek", `,"prompt_cache_hit_tokens":7,"prompt_cache_miss_tokens":13`, 7, 0, true, false},
		{"zero", `,"cache_read_input_tokens":0,"cache_creation_input_tokens":0`, 0, 0, true, true},
		{"zero-alias", `,"cache_read_input_tokens":0,"cached_tokens":0,"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":2}`, 7, 2, true, true},
		{"input-details", `,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":2}`, 7, 2, true, true},
		{"same-aliases", `,"cache_read_input_tokens":7,"prompt_cache_hit_tokens":7,"cache_creation_input_tokens":2,"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":2}`, 7, 2, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u, err := jsondoc.Object([]byte(`{"prompt_tokens":20,"completion_tokens":3` + tt.counters + `}`))
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []Kind{Responses, Anthropic} {
				v, err := usageFor(kind, u)
				if err != nil {
					t.Fatal(err)
				}
				got := v.(map[string]any)
				if kind == Anthropic {
					want := map[string]any{"input_tokens": 20 - tt.hit - tt.write, "output_tokens": 3}
					if tt.hasHit {
						want["cache_read_input_tokens"] = tt.hit
					}
					if tt.hasWrite {
						want["cache_creation_input_tokens"] = tt.write
					}
					compareSemantic(t, got, want)
				} else {
					want := map[string]any{"input_tokens": 20, "output_tokens": 3, "total_tokens": 23}
					if tt.hasHit || tt.hasWrite {
						d := map[string]any{}
						if tt.hasHit {
							d["cached_tokens"] = tt.hit
						}
						if tt.hasWrite {
							d["cache_write_tokens"] = tt.write
						}
						want["input_tokens_details"] = d
					}
					compareSemantic(t, got, want)
				}
			}
		})
	}
	u, _ := jsondoc.Object([]byte(`{"prompt_tokens":9007199254740995,"completion_tokens":1,"prompt_cache_hit_tokens":9007199254740993}`))
	v, err := usageFor(Anthropic, u)
	if err != nil || v.(map[string]any)["input_tokens"] != int64(2) {
		t.Fatalf("lost cache precision: %v %v", v, err)
	}
	for _, counters := range []string{
		`"cached_tokens":-1`, `"cached_tokens":1.5`, `"cached_tokens":"2"`,
		`"cached_tokens":21`, `"cached_tokens":18,"cache_creation_input_tokens":3`,
		`"cached_tokens":2,"prompt_cache_hit_tokens":3`,
		`"cache_creation_input_tokens":1,"prompt_tokens_details":{"cache_write_tokens":2}`,
	} {
		u, _ := jsondoc.Object([]byte(`{"prompt_tokens":20,"completion_tokens":3,` + counters + `}`))
		for _, kind := range []Kind{Responses, Anthropic} {
			if _, err := usageFor(kind, u); err == nil {
				t.Fatalf("accepted invalid counters: %s", counters)
			}
		}
	}
}

func FuzzResponsePipeline(f *testing.F) {
	f.Add(textStream)
	f.Add(toolsStream)
	f.Add(emptyIdentityStream)
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<16 {
			t.Skip()
		}
		completion, aggregateErr := Aggregate(strings.NewReader(raw), testRequest(t, Responses))
		for _, kind := range []Kind{Responses, Anthropic} {
			req := testRequest(t, kind)
			err := aggregateErr
			if err == nil {
				_, err = Format(req, completion)
			}
			streamErr := Stream(httptest.NewRecorder(), strings.NewReader(raw), req, nil)
			if (err == nil) != (streamErr == nil) {
				t.Fatalf("stream/aggregate success mismatch: %v / %v", streamErr, err)
			}
		}
	})
}

func TestUnrepresentableResponseFields(t *testing.T) {
	for _, field := range []string{
		`"audio":{"data":"abc"}`, `"citations":["source"]`,
		`"custom_extension":{"x":1}`, `"role":"user"`,
		`"annotations":{"text":"not-an-array"}`,
	} {
		raw := `data: {"choices":[{"index":0,"delta":{"content":"hi",` + field + `},"finish_reason":"stop"}]}` + "\n\n"
		if _, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses)); err == nil {
			t.Fatalf("silently lost response data: %s", field)
		}
		for _, kind := range []Kind{Responses, Anthropic} {
			if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
				t.Fatalf("silently streamed unsupported data: %s", field)
			}
		}
	}
	for _, raw := range []string{
		`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"message":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"content":"hi"},"message":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\n",
	} {
		if _, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses)); err == nil {
			t.Fatal("mixed snapshot and delta accepted")
		}
		for _, kind := range []Kind{Responses, Anthropic} {
			if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
				t.Fatal("snapshot repeated as text delta")
			}
		}
	}
}

func TestInvalidToolsAreNotDroppedOrInvented(t *testing.T) {
	for _, replacement := range []string{`"name":" "`, `"name":""`} {
		raw := strings.Replace(toolsStream, `"name":"other"`, replacement, 1)
		for _, kind := range []Kind{Responses, Anthropic} {
			rec := httptest.NewRecorder()
			if err := Stream(rec, strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
				t.Fatal("invalid tool accepted")
			}
			if strings.Contains(rec.Body.String(), "event: response.output_item.done") || strings.Contains(rec.Body.String(), `"type":"tool_use"`) {
				t.Fatal("partial tool success emitted", rec.Body.String())
			}
		}
	}
	resp, _ := Aggregate(strings.NewReader(toolsStream), testRequest(t, Responses))
	calls := resp["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)
	calls[0].(map[string]any)["type"] = "custom"
	for _, kind := range []Kind{Responses, Anthropic} {
		if _, err := Format(testRequest(t, kind), resp); err == nil {
			t.Fatal("non-function tool converted")
		}
	}
	legacy := `{"object":"chat.completion","choices":[{"index":0,"message":{"content":"","function_call":{"name":"x","arguments":"{}"}},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	if _, err := Aggregate(strings.NewReader(legacy), testRequest(t, Responses)); err == nil {
		t.Fatal("legacy tool silently lost")
	}
	// Keep JSON fixtures syntactically checked even when a validation path rejects early.
	var v any
	if err := json.Unmarshal([]byte(legacy), &v); err != nil {
		t.Fatal(err)
	}
}
