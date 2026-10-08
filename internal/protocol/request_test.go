package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, kind Kind, body string) *Request {
	t.Helper()
	r, err := Decode(kind, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRequestMapping(t *testing.T) {
	res := mustDecode(t, Responses, `{"model":"cn:test","store":false,"stream":true,"temperature":0,"parallel_tool_calls":false,"max_output_tokens":9007199254740993,"instructions":"rules","metadata":{"conversation_id":"not-routing"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"id":{"type":"integer","default":9007199254740993}}},"strict":false}],"tool_choice":{"type":"function","name":"lookup"},"input":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"output_text","text":"checking","annotations":[],"logprobs":[]}]},{"type":"function_call","id":"fc_other","call_id":"a","name":"lookup","arguments":"{\"id\":9007199254740993}"},{"type":"function_call","call_id":"b","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"b","output":"second"},{"type":"function_call_output","call_id":"a","output":"first"}]}`)
	if res.Chat.ConversationID != "" {
		t.Fatal("client metadata became routing state")
	}
	msgs := res.Chat.Object["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages=%#v", msgs)
	}
	calls := msgs[2].(map[string]any)["tool_calls"].([]any)
	if len(calls) != 2 || calls[0].(map[string]any)["id"] != "a" {
		t.Fatal(calls)
	}
	raw, _ := json.Marshal(res.Chat.Object)
	for _, want := range []string{`9007199254740993`, `"temperature":0`, `"parallel_tool_calls":false`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("lost %s: %s", want, raw)
		}
	}
	for _, foreign := range []string{"input", "instructions", "store", "metadata"} {
		if _, ok := res.Chat.Object[foreign]; ok {
			t.Fatalf("foreign field %s forwarded", foreign)
		}
	}
}

func TestAnthropicToolHistory(t *testing.T) {
	r := mustDecode(t, Anthropic, `{"model":"test","max_tokens":100,"system":[{"type":"text","text":"rules"}],"tools":[{"name":"f","input_schema":{"type":"object"}}],"tool_choice":{"type":"any","disable_parallel_tool_use":true},"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"check"},{"type":"tool_use","id":"a","name":"f","input":{"n":9007199254740993}},{"type":"tool_use","id":"b","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"b","content":"B"},{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"A"}],"is_error":false},{"type":"text","text":"continue"}]}]}`)
	if r.Chat.Object["tool_choice"] != "required" || r.Chat.Object["parallel_tool_calls"] != false {
		t.Fatal(r.Chat.Object)
	}
	msgs := r.Chat.Object["messages"].([]any)
	if len(msgs) != 6 {
		t.Fatal(msgs)
	}
	if msgs[3].(map[string]any)["tool_call_id"] != "b" || msgs[5].(map[string]any)["role"] != "user" {
		t.Fatal(msgs)
	}
}

func TestUnsupportedAndInvalidRequests(t *testing.T) {
	cases := []struct {
		kind        Kind
		body, param string
	}{
		{Responses, `{"model":"x","input":"hi"}`, "store"},
		{Responses, `{"model":"x","store":true,"input":"hi"}`, "store"},
		{Responses, `{"model":"x","store":false,"input":"hi","previous_response_id":"resp_x"}`, "previous_response_id"},
		{Responses, `{"model":"x","store":false,"input":"hi","background":true}`, "background"},
		{Responses, `{"model":"x","store":false,"input":"hi","reasoning":{"effort":"high"}}`, "reasoning"},
		{Responses, `{"model":"x","store":false,"input":"hi","text":{"format":{"type":"json_object"}}}`, "text.format"},
		{Responses, `{"model":"x","store":false,"input":"hi","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`, "strict"},
		{Responses, `{"model":"x","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"http://x"}]}]}`, "input"},
		{Responses, `{"model":"x","store":false,"input":[{"type":"function_call_output","call_id":"missing","output":"ok"}]}`, "tool_call_id"},
		{Responses, `{"model":"x","store":false,"input":"hi","unexpected":false}`, "unexpected"},
		{Responses, `{"model":"x","store":false,"input":"hi","temperature":3}`, "temperature"},
		{Responses, `{"model":"x","store":false,"input":"hi","n":2}`, "n"},
		{Responses, `{"model":"x","model":"y","store":false,"input":"hi"}`, "duplicate"},
		{Anthropic, `{"model":"x","messages":[{"role":"user","content":"hi"}]}`, "max_tokens"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"system","content":"hi"}]}`, "role"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1}}`, "thinking"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral"}}]}`, "cache_control"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stop_sequences":["END"]}`, "stop_sequences"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"bad","is_error":true}]}]}`, "is_error"},
		{Anthropic, `{"model":"x","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"f","input":{}},{"type":"text","text":"after"}]}]}`, "order"},
	}
	for _, tt := range cases {
		t.Run(string(tt.kind)+"/"+tt.param, func(t *testing.T) {
			_, err := Decode(tt.kind, []byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.param) {
				t.Fatalf("err=%v want %s", err, tt.param)
			}
		})
	}
}

func TestResponsesAssistantTextForms(t *testing.T) {
	for _, content := range []string{
		`"你好"`,
		`[{"type":"input_text","text":"你好"}]`,
		`[{"type":"output_text","text":"你好","annotations":[],"logprobs":[]}]`,
		`[{"type":"input_text","text":"你"},{"type":"output_text","text":"好"}]`,
	} {
		r := mustDecode(t, Responses, `{"model":"x","store":false,"input":[{"role":"user","content":"hi"},{"role":"assistant","content":`+content+`},{"role":"user","content":"continue"}]}`)
		msgs := r.Chat.Object["messages"].([]any)
		if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
			t.Fatal(msgs)
		}
		v := msgs[1].(map[string]any)["content"]
		var text string
		switch v := v.(type) {
		case string:
			text = v
		case []any:
			for _, part := range v {
				p := part.(map[string]any)
				if p["type"] != "text" {
					t.Fatal(p)
				}
				text += p["text"].(string)
			}
		}
		if text != "你好" {
			t.Fatal(text)
		}
	}
	for _, item := range []string{
		`{"role":"user","content":[{"type":"output_text","text":"hi"}]}`,
		`{"role":"assistant","content":[{"type":"input_text","text":"hi","annotations":[]}]}`,
		`{"role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[{"url":"x"}]}]}`,
		`{"role":"assistant","content":[{"type":"input_image","image_url":"https://example.com/x.png"}]}`,
		`{"role":"assistant","content":[{"type":"input_text","text":42}]}`,
		`{"role":"assistant","content":[{"type":"input_text","text":"hi","cache_control":{"type":"ephemeral"}}]}`,
		`{"role":"assistant","phase":"commentary","content":[{"type":"input_text","text":"hi"}]}`,
	} {
		if _, err := Decode(Responses, []byte(`{"model":"x","store":false,"input":[`+item+`]}`)); err == nil {
			t.Fatal("lost unsupported history information", item)
		}
	}
}

func TestNativeRequestStillPreservesExtensions(t *testing.T) {
	r := mustDecode(t, Chat, `{"model":"x","messages":[{"role":"user","content":"hi"}],"custom":{"n":9007199254740993}}`)
	if r.Chat.Object["custom"] == nil {
		t.Fatal("native extension lost")
	}
}

func FuzzRequestDecode(f *testing.F) {
	f.Add(`{"model":"x","store":false,"input":"hi"}`)
	f.Add(`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
	f.Add(`{"model":"x","store":false,"input":[{"role":"assistant","content":[{"type":"input_text","text":"hi"},{"type":"output_text","text":"there","annotations":[]}]}]}`)
	f.Add(`{"model":"x","store":false,"input":"hi","tools":` + nsTools + `}`)
	f.Add(`{"model":"x","store":false,"input":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":[{"type":"input_text","text":"ok"}]}]}`)
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 1<<16 {
			t.Skip()
		}
		for _, kind := range []Kind{Responses, Anthropic} {
			_, _ = Decode(kind, []byte(body))
		}
	})
}
