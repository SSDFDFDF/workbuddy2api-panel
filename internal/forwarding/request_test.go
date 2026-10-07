package forwarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInvalidRequests(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"model":"m","messages":[]}`, `{"model":"a","model":"b","messages":[]}`, `{"model":"m","stream":1,"messages":[{"role":"user","content":"x"}]}`, `{"model":"m","messages":[{"role":"tool","tool_call_id":"missing","content":"x"}]}`, `{"model":"m","reasoning_effort":"low","reasoning":{"effort":"high"},"messages":[{"role":"user","content":"x"}]}`, `{"model":"m","thinking":{"type":"disabled"},"reasoning_effort":"high","messages":[{"role":"user","content":"x"}]}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
func TestValuePreservation(t *testing.T) {
	raw := `{"model":"deepseek-test","seed":9007199254740993,"metadata":{"large":9007199254740993},"temperature":0,"parallel_tool_calls":false,"custom":{"x":[1,null,false]},"messages":[{"role":"developer","content":"Do not change 11128 Main branch ` + "`whoami`" + `"},{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{\"port\":11128}"}}]},{"role":"tool","tool_call_id":"a","content":"11128"}]}`
	r, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Encode(r.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`9007199254740993`, `"port\":11128`, `"role":"developer"`, `"temperature":0`, `"parallel_tool_calls":false`} {
		if !strings.Contains(string(out), s) {
			t.Fatal(s, string(out))
		}
	}
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	if _, ok := obj["thinking"]; ok {
		t.Fatal("thinking injected")
	}
	if _, ok := obj["reasoning_effort"]; ok {
		t.Fatal("effort injected")
	}
}
func TestReasoningAliases(t *testing.T) {
	r, err := Parse([]byte(`{"model":"m","reasoningEffort":"low","reasoning":{"effort":"low","summary":"auto"},"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Encode("m")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"reasoning_effort":"low"`) || strings.Contains(string(out), "reasoningEffort") {
		t.Fatal(string(out))
	}
	r, err = Parse([]byte(`{"model":"m","reasoning_effort":"off","messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ = r.Encode("m")
	if strings.Contains(string(out), "high") || !strings.Contains(string(out), `"disabled"`) {
		t.Fatal(string(out))
	}
}
func TestConversationSources(t *testing.T) {
	r, err := Parse([]byte(`{"model":"m","metadata":{"conversation_id":"a"},"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Conversation-ID", "b")
	if r.ResolveHeaders(h) == nil {
		t.Fatal("conflict accepted")
	}
	h.Set("X-Conversation-ID", "a")
	if err := r.ResolveHeaders(h); err != nil {
		t.Fatal(err)
	}
}
func FuzzParse(f *testing.F) {
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	f.Add(`null`)
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<20 {
			return
		}
		r, err := Parse([]byte(s))
		if err == nil {
			if _, err = r.Encode(r.Model); err != nil {
				t.Fatal(err)
			}
		}
	})
}
