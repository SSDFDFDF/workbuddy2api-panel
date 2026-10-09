package protocol

import (
	"reflect"
	"strings"
	"testing"

	"workbuddy_manager/internal/jsondoc"
)

func TestEmptyToolResults(t *testing.T) {
	for _, kind := range []Kind{Responses, Anthropic} {
		for _, tc := range []struct {
			label, value string
			want         any
			valid        bool
		}{
			{"empty-array", `[]`, []any{}, true},
			{"empty-string", `""`, "", true},
			{"empty-block", `[{"type":"TEXT_TYPE","text":""}]`, []any{map[string]any{"type": "text", "text": ""}}, true},
			{"null", `null`, nil, false},
			{"missing", ``, "", kind == Anthropic}, // Messages permits omitted content.
			{"wrong-type", `{}`, nil, false},
			{"bad-block", `[{"type":"TEXT_TYPE"}]`, nil, false},
			{"lost-extension", `[{"type":"TEXT_TYPE","text":"","cache_control":{}}]`, nil, false},
		} {
			t.Run(string(kind)+"/"+tc.label, func(t *testing.T) {
				field, blockType := "output", "input_text"
				if kind == Anthropic {
					field, blockType = "content", "text"
				}
				result := ""
				if tc.value != "" {
					result = `,"` + field + `":` + strings.ReplaceAll(tc.value, "TEXT_TYPE", blockType)
				}
				body := `{"model":"x","store":false,"input":[{"type":"function_call","call_id":"a","name":"old","arguments":"{}"},{"type":"function_call_output","call_id":"a"` + result + `}]}`
				if kind == Anthropic {
					body = `{"model":"x","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"old","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"` + result + `}]}]}`
				}
				r, err := Decode(kind, []byte(body))
				if (err == nil) != tc.valid {
					t.Fatal(tc, err)
				}
				if err != nil {
					return
				}
				encoded, err := r.Chat.Encode("x")
				if err != nil {
					t.Fatal(err)
				}
				wire, err := jsondoc.Object(encoded)
				if err != nil {
					t.Fatal(err)
				}
				msg := wire["messages"].([]any)[1].(map[string]any)
				if msg["role"] != "tool" || msg["tool_call_id"] != "a" || !reflect.DeepEqual(msg["content"], tc.want) {
					t.Fatal("empty result changed on wire", msg)
				}
			})
		}
	}
}

func TestEmptyToolResultDoesNotRelaxMessageOrHistoryValidation(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		body string
	}{
		{Responses, `{"model":"x","store":false,"input":[{"role":"user","content":[]}]}`},
		{Anthropic, `{"model":"x","max_tokens":100,"messages":[{"role":"user","content":[]}]}`},
		{Responses, `{"model":"x","store":false,"input":[{"type":"function_call_output","call_id":"missing","output":[]}]}`},
		{Anthropic, `{"model":"x","max_tokens":100,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":[]}]}]}`},
		{Responses, `{"model":"x","store":false,"input":[{"type":"function_call","call_id":"a","name":"old","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":[]},{"type":"function_call_output","call_id":"a","output":[]}]}`},
	} {
		if _, err := Decode(tc.kind, []byte(tc.body)); err == nil {
			t.Fatal("unrelated validation relaxed", tc.body)
		}
	}
	// Native Chat's permissive extension/null behavior is unchanged.
	native := mustDecode(t, Chat, `{"model":"x","messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"old","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":null,"extension":"keep"}]}`)
	msg := native.Chat.Object["messages"].([]any)[1].(map[string]any)
	if msg["content"] != nil || msg["extension"] != "keep" {
		t.Fatal(msg)
	}
}
