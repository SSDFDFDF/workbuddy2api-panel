package protocol

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"workbuddy_manager/internal/jsondoc"
)

func namespaceFixture(t *testing.T, ns, name string) map[string]any {
	t.Helper()
	fn, err := jsondoc.Object([]byte(nsFunction))
	if err != nil {
		t.Fatal(err)
	}
	fn["name"] = name
	return map[string]any{"type": "namespace", "name": ns, "description": "", "tools": []any{fn}}
}

func decodeFixture(t *testing.T, body map[string]any) *Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return mustDecode(t, Responses, string(raw))
}

func historyFixture(name string, ns any, id string) []any {
	call := map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": ` {"n":9007199254740993} `}
	if ns != nil {
		call["namespace"] = ns
	}
	return []any{call, map[string]any{"type": "function_call_output", "call_id": id, "output": []any{}}}
}

func TestCompactNamespaceLifecycleAndRetiredReplay(t *testing.T) {
	ns := strings.Repeat("c", 64)
	name := "lookup"
	alias, err := namespaceChatName(ns, name, "test")
	// Versioned golden value ensures alias stability across process restarts and
	// releases; the digest uses all 256 bits rather than truncating the identity.
	if err != nil || alias != "nsh_a5a9H0ShJNLfYwTpuH8hgP5pqmDgXwoD0LZ95bRz2Uw" {
		t.Fatal(alias, err)
	}
	req := decodeFixture(t, map[string]any{"model": "x", "store": false, "input": "hi", "tools": []any{namespaceFixture(t, ns, name)}, "tool_choice": map[string]any{"type": "function", "name": name}})
	if req.Chat.Object["tool_choice"].(map[string]any)["function"].(map[string]any)["name"] != alias {
		t.Fatal("forced choice not mapped")
	}
	for _, finish := range []string{"tool_calls", "length"} {
		raw := strings.Replace(nameChunks([]any{alias, alias}), `"finish_reason":"tool_calls"`, `"finish_reason":"`+finish+`"`, 1)
		completion, err := Aggregate(strings.NewReader(raw), req)
		if err != nil {
			t.Fatal(err)
		}
		out, err := completion.Format(req)
		if err != nil {
			t.Fatal(err)
		}
		direct, err := Format(req, completion.Raw)
		if err != nil {
			t.Fatal(err)
		}
		compareSemantic(t, out["output"], direct["output"])
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), req, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Body.String(), alias) {
			t.Fatal("internal compact alias leaked", rec.Body)
		}
		es := events(t, rec.Body.String(), Responses)
		compareSemantic(t, es[len(es)-1]["response"].(map[string]any)["output"], out["output"])
		for _, e := range es {
			if item, ok := e["item"].(map[string]any); ok && (item["name"] != name || item["namespace"] != ns) {
				t.Fatal("lifecycle lost compact identity", e)
			}
			if finish == "length" && (e["type"] == "response.output_item.added" || strings.HasPrefix(e["type"].(string), "response.function_call")) {
				t.Fatal("cutoff tool delivered", e)
			}
		}
		call := out["output"].([]any)[0].(map[string]any)
		if call["name"] != name || call["namespace"] != ns {
			t.Fatal(call)
		}
		history := []any{call, map[string]any{"type": "function_call_output", "call_id": call["call_id"], "output": []any{}}}
		encoded, _ := json.Marshal(map[string]any{"model": "x", "store": false, "input": history})
		next, err := Decode(Responses, encoded)
		if finish == "length" {
			if err == nil {
				t.Fatal("incomplete retired call accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		msgs := next.Chat.Object["messages"].([]any)
		fn := msgs[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
		if fn["name"] != alias || fn["arguments"] != call["arguments"] || len(declaredToolNames(next)) != 0 || !reflect.DeepEqual(msgs[1].(map[string]any)["content"], []any{}) {
			t.Fatal("stateless retired replay changed", next.Chat.Object)
		}
	}
}

func TestHistoricalIdentitiesDoNotGrantTools(t *testing.T) {
	for _, declared := range []any{nil, []any{}, []any{namespaceFixture(t, "fs", "lookup")}, []any{namespaceFixture(t, "crm", "other")}} {
		body := map[string]any{"model": "x", "store": false, "input": historyFixture("lookup", "crm", "a")}
		if declared != nil {
			body["tools"] = declared
		}
		r := decodeFixture(t, body)
		if _, ok := r.tools.byChat["ns_3_crm_lookup"]; ok {
			t.Fatal("history entered callable index")
		}
		if _, err := r.tools.choice("lookup"); (err == nil) != (len(declaredToolNames(r)) == 1 && declaredToolNames(r)[0] == "ns_2_fs_lookup") {
			t.Fatal("historical tool affected choice", err)
		}
		raw := nameChunks([]any{"ns_3_crm_lookup"})
		if c, err := Aggregate(strings.NewReader(raw), r); err == nil {
			if _, err := c.Format(r); err == nil {
				t.Fatal("retired tool delivered")
			}
		}
		snapshot, _ := jsondoc.Object([]byte(`{"choices":[{"index":0,"message":{"tool_calls":[{"id":"new","type":"function","function":{"name":"ns_3_crm_lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		if _, err := Format(r, snapshot); err == nil {
			t.Fatal("snapshot authorized retired tool")
		}
		rec := httptest.NewRecorder()
		if err := Stream(rec, strings.NewReader(raw), r, nil); err == nil || strings.Contains(rec.Body.String(), "response.function_call") || strings.Contains(rec.Body.String(), "response.output_item.added") {
			t.Fatal("retired tool streamed", rec.Body, err)
		}
	}
	// Missing namespace unambiguously denotes flat history, not today's same
	// local name in another namespace. Old flat history need not be redeclared.
	r := decodeFixture(t, map[string]any{"model": "x", "store": false, "input": historyFixture("lookup", nil, "a"), "tools": []any{namespaceFixture(t, "crm", "lookup")}})
	call := r.Chat.Object["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["function"].(map[string]any)["name"] != "lookup" || len(r.tools.byLocal["lookup"]) != 1 {
		t.Fatal("flat history remapped or affected current choice", call)
	}
}

func TestNamespaceCollisionsAcrossDeclarationsAndHistory(t *testing.T) {
	for _, ns := range []string{"crm", strings.Repeat("c", 64)} {
		alias, _ := namespaceChatName(ns, "lookup", "test")
		group := namespaceFixture(t, ns, "lookup")
		flat, _ := jsondoc.Object([]byte(nsFunction))
		flat["name"] = alias
		bodies := []map[string]any{
			{"input": "hi", "tools": []any{flat, group}},
			{"input": "hi", "tools": []any{group, flat}},
			{"input": historyFixture(alias, nil, "a"), "tools": []any{group}},
			{"input": historyFixture("lookup", ns, "a"), "tools": []any{flat}},
			{"input": append(historyFixture(alias, nil, "a"), historyFixture("lookup", ns, "b")...)},
			{"input": append(historyFixture("lookup", ns, "a"), historyFixture(alias, nil, "b")...)},
		}
		for _, body := range bodies {
			body["model"], body["store"] = "x", false
			raw, _ := json.Marshal(body)
			if _, err := Decode(Responses, raw); err == nil {
				t.Fatal("wire identity collision accepted", string(raw))
			}
		}
		// Repeating the same retired identity isn't a collision.
		r := decodeFixture(t, map[string]any{"model": "x", "store": false, "input": append(historyFixture("lookup", ns, "a"), historyFixture("lookup", ns, "b")...)})
		if len(r.tools.wireOwners) != 1 || len(r.tools.byChat) != 0 {
			t.Fatal("repeated history changed identity index")
		}
	}
	// Force a digest collision in the index to test fail-closed behavior without
	// weakening the production hash or relying on a practically impossible pair.
	idx := newToolIndex()
	ns := strings.Repeat("c", 64)
	alias, _ := namespaceChatName(ns, "lookup", "test")
	idx.wireOwners[alias] = toolIdentity{name: "other", namespace: ns}
	if _, err := idx.history("lookup", ns, "test"); err == nil {
		t.Fatal("digest collision ignored")
	}
}
