package upstream

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

const sseFixture = "data: {\"id\":\"x\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
	"data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"

func TestV2Completion(t *testing.T) {
	r, err := Aggregate(strings.NewReader(sseFixture))
	if err != nil {
		t.Fatal(err)
	}
	ch := r["choices"].([]any)[0].(map[string]any)
	if ch["message"].(map[string]any)["content"] != "hello" {
		t.Fatal(r)
	}
	if r["usage"].(map[string]any)["total_tokens"] != int64(5) {
		t.Fatal(r)
	}
	w := httptest.NewRecorder()
	if err := Stream(w, strings.NewReader(sseFixture)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(w.Body.String(), "[DONE]") != 1 {
		t.Fatal(w.Body.String())
	}
}
func TestV2FailureParity(t *testing.T) {
	cases := []string{"", "data: [DONE]\n\n", `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n", `data: {"error":{"code":6004,"message":"rate limited"}}` + "\n\n", "data: invalid\n\n", "data: null\n\n", "data: {\"choices\":[]", "data: [DONE]garbage\n\n"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := Aggregate(strings.NewReader(raw)); err == nil {
				t.Fatal("false aggregate success")
			}
			w := httptest.NewRecorder()
			if err := Stream(w, strings.NewReader(raw)); err == nil {
				t.Fatal("false stream success")
			}
			if !strings.Contains(w.Body.String(), "error") {
				t.Fatal(w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	err := Stream(w, strings.NewReader(cases[3]))
	var fe *FrameError
	if !errors.As(err, &fe) || w.Code != 429 {
		t.Fatalf("%d %v", w.Code, err)
	}
}
func TestV2ChoicesAndFields(t *testing.T) {
	raw := `data:{"extra":{"integer":9007199254740993},"choices":[{"index":1,"delta":{"content":"B","reasoning":"thought","annotations":[{"url":"x"}]},"logprobs":{"content":[{"token":"B"}]},"finish_reason":"length"},{"index":0,"delta":{"content":"A","refusal":"no"},"finish_reason":"stop"}]}` + "\r\n\r\ndata:[DONE]\r\n\r\n"
	r, err := Aggregate(strings.NewReader(raw), WithExpectedChoices(2))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	for _, s := range []string{"9007199254740993", "thought", "annotations", "logprobs", "refusal"} {
		if !strings.Contains(string(b), s) {
			t.Fatal(string(b))
		}
	}
	choices := r["choices"].([]any)
	if len(choices) != 2 || choices[0].(map[string]any)["message"].(map[string]any)["content"] != "A" {
		t.Fatal(r)
	}
	w := httptest.NewRecorder()
	if err := StreamHint(w, strings.NewReader(raw), nil, WithExpectedChoices(2)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatal(w.Body.String())
	}
	if _, err := Aggregate(strings.NewReader(sseFixture), WithExpectedChoices(2)); err == nil {
		t.Fatal("missing choice accepted")
	}
}
func TestV2DelayedAndFragmentedToolName(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"arguments":""}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"look","arguments":"{"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"up","arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	r, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	calls := r["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	if _, ok := call["index"]; ok {
		t.Fatal(call)
	}
	if call["function"].(map[string]any)["name"] != "lookup" {
		t.Fatal(call)
	}
	w := httptest.NewRecorder()
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"name":"look"`, `"name":"up"`} {
		if !strings.Contains(w.Body.String(), s) {
			t.Fatal(w.Body.String())
		}
	}
}
func TestV2LengthKeepsPartialTool(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"x","arguments":"{"}}]},"finish_reason":"length"}]}` + "\n\n"
	r, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "tool_calls") {
		t.Fatal(string(b))
	}
}
func TestV2UnknownConflict(t *testing.T) {
	raw := strings.Replace(sseFixture, `"model":"m"`, `"model":"m","extension":1`, 1)
	raw = strings.Replace(raw, `"delta":{}`, `"delta":{},"extension":2`, 1) // distinct choice-level field, not a conflict
	if _, err := Aggregate(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	raw = strings.Replace(sseFixture, `"model":"m"`, `"model":"m","extension":1`, 1)
	raw = strings.Replace(raw, `"usage":`, `"extension":2,"usage":`, 1)
	if _, err := Aggregate(strings.NewReader(raw)); err == nil {
		t.Fatal("ambiguous extension accepted")
	}
	w := httptest.NewRecorder()
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}
func TestV2MultilineFragmentedSSE(t *testing.T) {
	raw := "event: message\r\ndata: {\"choices\":\r\ndata: [{\"index\":0,\"delta\":{\"content\":\"你好\"},\"finish_reason\":\"stop\"}]}\r\n\r\n"
	_, err := Aggregate(&byteReader{raw: []byte(raw)})
	if err != nil {
		t.Fatal(err)
	}
}

type byteReader struct{ raw []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.raw) == 0 {
		return 0, io.EOF
	}
	p[0] = r.raw[0]
	r.raw = r.raw[1:]
	return 1, nil
}
func TestV2FullJSON(t *testing.T) {
	raw := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	if _, err := Aggregate(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}
func FuzzV2SSE(f *testing.F) {
	f.Add(sseFixture)
	f.Add("data: null\n\n")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<20 {
			return
		}
		_, _ = Aggregate(strings.NewReader(s))
	})
}
