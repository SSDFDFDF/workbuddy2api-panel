package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

const textStream = "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你\"}}]}\n\n" +
	"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":7}}}\n\ndata: [DONE]\n\n"

const toolsStream = `data: {"choices":[{"index":0,"delta":{"content":"checking"}}]}` + "\n\n" +
	`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"arguments":"{","name":"look"}},{"index":1,"id":"b","type":"function","function":{"name":"other","arguments":"{"}}]}}]}` + "\n\n" +
	`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"n\":2}"}},{"index":0,"function":{"name":"up","arguments":"\"n\":9007199254740993}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
	`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4}}` + "\n\ndata: [DONE]\n\n"

func testRequest(t *testing.T, kind Kind) *Request {
	if kind == Responses {
		return mustDecode(t, kind, `{"model":"cn:demo","store":false,"input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false},{"type":"function","name":"other","parameters":{"type":"object"},"strict":false}]}`)
	}
	return mustDecode(t, kind, `{"model":"cn:demo","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}},{"name":"other","input_schema":{"type":"object"}}]}`)
}

func events(t *testing.T, raw string, kind Kind) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for i, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("invalid SSE: %q", block)
		}
		var obj map[string]any
		d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(lines[1], "data: ")))
		d.UseNumber()
		if err := d.Decode(&obj); err != nil {
			t.Fatal(err)
		}
		if obj["type"] != strings.TrimPrefix(lines[0], "event: ") {
			t.Fatal("event/data type mismatch")
		}
		if kind == Responses && obj["sequence_number"] != json.Number(fmt.Sprint(i)) {
			t.Fatalf("non-monotonic sequence: %v", obj)
		}
		out = append(out, obj)
	}
	return out
}

func TestTextAndToolStreams(t *testing.T) {
	for _, kind := range []Kind{Responses, Anthropic} {
		for _, raw := range []string{textStream, toolsStream} {
			t.Run(string(kind)+fmt.Sprint(len(raw)), func(t *testing.T) {
				r := testRequest(t, kind)
				rec := httptest.NewRecorder()
				if err := Stream(rec, strings.NewReader(raw), r, nil); err != nil {
					t.Fatal(err)
				}
				es := events(t, rec.Body.String(), kind)
				if strings.Contains(rec.Body.String(), "[DONE]") {
					t.Fatal("Chat terminator leaked")
				}
				resp, err := Aggregate(strings.NewReader(raw), r)
				if err != nil {
					t.Fatal(err)
				}
				formatted, err := resp.Format(r)
				if err != nil {
					t.Fatal(err)
				}
				if kind == Responses {
					terminal := es[len(es)-1]
					if terminal["type"] != "response.completed" {
						t.Fatal(terminal)
					}
					got := terminal["response"].(map[string]any)
					if got["model"] != "cn:demo" {
						t.Fatal(got)
					}
					// Ignore generated identities/timestamps, but require content and
					// usage to be identical to non-streaming aggregation.
					compareSemantic(t, got["output"], formatted["output"])
					compareSemantic(t, got["usage"], formatted["usage"])
					added, done := map[string]bool{}, map[string]bool{}
					for _, e := range es {
						if e["type"] == "response.output_item.added" || e["type"] == "response.output_item.done" {
							item := e["item"].(map[string]any)
							id := item["id"].(string)
							if e["type"] == "response.output_item.added" {
								if added[id] {
									t.Fatal("duplicate added")
								}
								added[id] = true
							} else {
								if !added[id] || done[id] {
									t.Fatal("bad done")
								}
								done[id] = true
							}
						}
					}
					if len(added) != len(done) {
						t.Fatal("unclosed items")
					}
				} else {
					if es[len(es)-1]["type"] != "message_stop" {
						t.Fatal(es)
					}
					blocks := []any{}
					args := map[int]string{}
					closed := map[int]bool{}
					for _, e := range es {
						idx := 0
						if n, ok := e["index"].(json.Number); ok {
							x, _ := n.Int64()
							idx = int(x)
						}
						switch e["type"] {
						case "content_block_start":
							if idx != len(blocks) {
								t.Fatal("index reuse/gap")
							}
							blocks = append(blocks, e["content_block"])
						case "content_block_delta":
							if idx >= len(blocks) || closed[idx] {
								t.Fatal("delta outside block")
							}
							d := e["delta"].(map[string]any)
							b := blocks[idx].(map[string]any)
							if d["type"] == "text_delta" {
								b["text"] = b["text"].(string) + d["text"].(string)
							} else {
								args[idx] += d["partial_json"].(string)
							}
						case "content_block_stop":
							if idx >= len(blocks) || closed[idx] {
								t.Fatal("duplicate stop")
							}
							closed[idx] = true
							if s := args[idx]; s != "" {
								var input any
								d := json.NewDecoder(strings.NewReader(s))
								d.UseNumber()
								if err := d.Decode(&input); err != nil {
									t.Fatal(err)
								}
								blocks[idx].(map[string]any)["input"] = input
							}
						case "message_delta":
							compareSemantic(t, e["usage"], formatted["usage"])
						}
					}
					if len(closed) != len(blocks) {
						t.Fatal("unclosed blocks")
					}
					compareSemantic(t, blocks, formatted["content"])
				}
			})
		}
	}
}

func compareSemantic(t *testing.T, a, b any) {
	t.Helper()
	normalize := func(v any) any {
		raw, _ := json.Marshal(v)
		var x any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		_ = d.Decode(&x)
		return x
	}
	a, b = normalize(a), normalize(b)
	stripIDs(a)
	stripIDs(b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("semantic mismatch\n%#v\n%#v", a, b)
	}
}
func stripIDs(v any) {
	switch x := v.(type) {
	case map[string]any:
		if x["type"] == "message" || x["type"] == "function_call" {
			delete(x, "id")
		}
		for _, v := range x {
			stripIDs(v)
		}
	case []any:
		for _, v := range x {
			stripIDs(v)
		}
	}
}

func TestStreamFailuresAndIncomplete(t *testing.T) {
	prefix := `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
	for _, kind := range []Kind{Responses, Anthropic} {
		for _, raw := range []string{"", "data: [DONE]\n\n", prefix, prefix + "data: not-json\n\n", prefix + `data: {"error":{"code":6004,"message":"rate limited"}}` + "\n\n", `data: {"choices":[{"index":0,"delta":{"reasoning_content":"secret"},"finish_reason":"stop"}]}` + "\n\n", strings.Replace(strings.Replace(toolsStream, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), `"name":"other"`, `"name":"unknown"`, 1)} {
			rec := httptest.NewRecorder()
			if err := Stream(rec, strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
				t.Fatalf("accepted invalid stream %s", raw)
			}
			body := rec.Body.String()
			if strings.Contains(body, "event: response.completed") || strings.Contains(body, "event: message_stop") || strings.Contains(body, "event: response.output_item.done") {
				t.Fatalf("false success: %s", body)
			}
		}
		raw := strings.Replace(textStream, `"stop"`, `"length"`, 1)
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), testRequest(t, kind), nil); err != nil {
			t.Fatal(err)
		}
		want := "event: response.incomplete"
		if kind == Anthropic {
			want = `"stop_reason":"max_tokens"`
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatal(rec.Body.String())
		}
	}
}

func TestAggregateStreamRejectSameOrdering(t *testing.T) {
	raw := strings.Replace(toolsStream, `"finish_reason":"tool_calls"`, `"finish_reason":null`, 1)
	raw = strings.Replace(raw, "data: [DONE]", `data: {"choices":[{"index":0,"delta":{"content":"late"},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]", 1)
	if _, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses)); err == nil {
		t.Fatal("aggregate reordered text")
	}
	for _, kind := range []Kind{Responses, Anthropic} {
		if err := Stream(httptest.NewRecorder(), strings.NewReader(raw), testRequest(t, kind), nil); err == nil {
			t.Fatal("stream reordered text")
		}
	}
}

func TestJSONSnapshotAndMissingUsage(t *testing.T) {
	raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`
	for _, kind := range []Kind{Responses, Anthropic} {
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), testRequest(t, kind), nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rec.Body.String(), `"hi"`) {
			t.Fatal(rec.Body.String())
		}
	}
	resp, err := Aggregate(strings.NewReader(`{"object":"chat.completion","choices":[{"index":0,"message":{"content":"hi"},"finish_reason":"stop"}]}`), testRequest(t, Responses))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.Format(testRequest(t, Anthropic)); err == nil {
		t.Fatal("fabricated Anthropic usage")
	}
	out, err := resp.Format(testRequest(t, Responses))
	if err != nil || out["usage"] != nil {
		t.Fatal(out, err)
	}
}

type byteReader struct{ r io.Reader }

func (b byteReader) Read(p []byte) (int, error) { return b.r.Read(p[:1]) }
func TestUTF8SingleByteReads(t *testing.T) {
	if err := Stream(httptest.NewRecorder(), byteReader{strings.NewReader(textStream)}, testRequest(t, Responses), nil); err != nil {
		t.Fatal(err)
	}
}

type failingWriter struct{ header http.Header }

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) WriteHeader(int)           {}
func (w *failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestDownstreamWriteFailure(t *testing.T) {
	err := Stream(&failingWriter{http.Header{}}, strings.NewReader(textStream), testRequest(t, Responses), nil)
	var we *upstream.DownstreamWriteError
	if !errors.As(err, &we) {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestOwnOutputCanBeReplayed(t *testing.T) {
	for _, kind := range []Kind{Responses, Anthropic} {
		r := testRequest(t, kind)
		resp, err := Aggregate(strings.NewReader(toolsStream), r)
		if err != nil {
			t.Fatal(err)
		}
		out, err := resp.Format(r)
		if err != nil {
			t.Fatal(err)
		}
		var next map[string]any
		if kind == Responses {
			input := []any{map[string]any{"role": "user", "content": "hi"}}
			input = append(input, out["output"].([]any)...)
			for _, id := range []string{"a", "b"} {
				input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": "ok"})
			}
			next = map[string]any{"model": "cn:demo", "store": false, "input": input}
		} else {
			next = map[string]any{"model": "cn:demo", "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "hi"}, map[string]any{"role": "assistant", "content": out["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "a", "content": "ok"}, map[string]any{"type": "tool_result", "tool_use_id": "b", "content": "ok"}}}}}
		}
		raw, _ := json.Marshal(next)
		if _, err := Decode(kind, raw); err != nil {
			t.Fatalf("own output cannot be replayed: %s: %v", raw, err)
		}
	}
}
