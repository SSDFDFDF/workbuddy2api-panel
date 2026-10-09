package upstream

import (
	"fmt"
	"strings"
	"testing"

	"workbuddy_manager/internal/jsondoc"
)

func TestStringBuffersPreserveSnapshotSemantics(t *testing.T) {
	for _, tt := range []struct {
		first, snapshot, want string
		reject                bool
	}{
		{`""`, `"snap"`, "snap!", false},
		{`null`, `"snap"`, "snap!", false},
		{`"same"`, `"same"`, "same!", false},
		{`"same"`, `"different"`, "", true},
		{`""`, `null`, "!", false},
	} {
		raw := `data: {"choices":[{"index":0,"delta":{"content":` + tt.first + `}}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"message":{"content":` + tt.snapshot + `}}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}]}` + "\n\n"
		out, err := Aggregate(strings.NewReader(raw))
		if tt.reject {
			if err == nil {
				t.Fatal("conflicting snapshot accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"]
		if got != tt.want {
			t.Fatalf("got %v want %s", got, tt.want)
		}
	}
}

func TestStringBuffersKeepRetainedPrefixesImmutable(t *testing.T) {
	s := newCompletion(1, true)
	retained := []string{}
	for i := 0; i < 400; i++ {
		frame, err := jsondoc.Object([]byte(`{"choices":[{"index":0,"delta":{"content":"你","reasoning":"思","refusal":"不","tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"f","arguments":"x"}}]}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.add(frame); err != nil {
			t.Fatal(err)
		}
		if frame["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"] != "你" {
			t.Fatal("frame mutated")
		}
		c := s.choices[0]
		retained = append(retained, c.message["content"].(string))
		fn := c.tools[0]["function"].(map[string]any)
		if fn["arguments"] != strings.Repeat("x", i+1) || fn["name"] != strings.Repeat("f", i+1) {
			t.Fatal(fn)
		}
		if c.message["reasoning"] != strings.Repeat("思", i+1) || c.message["refusal"] != strings.Repeat("不", i+1) {
			t.Fatal(c.message)
		}
	}
	for i, text := range retained {
		if text != strings.Repeat("你", i+1) {
			t.Fatalf("prefix %d mutated", i)
		}
	}
}

func TestNativeStreamReleasesDeliveredBuffers(t *testing.T) {
	// Exercise the consume path directly so retained buffer ownership is observable.
	s := newCompletion(1, false)
	frame := `data: {"choices":[{"index":0,"delta":{"content":"abc","reasoning":"def","refusal":"ghi","annotations":[]}}]}` + "\n\n"
	raw := strings.Repeat(frame, 1000) + `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	frames := 0
	if err := consume(strings.NewReader(raw), s, func(_ sseEvent, _ map[string]any) error { frames++; return nil }, streamOptions{expected: 1}); err != nil {
		t.Fatal(err)
	}
	if frames != 1001 {
		t.Fatal(frames)
	}
	if len(s.choices[0].messageStrings) != 0 {
		t.Fatal("delivered text buffer retained")
	}
	for _, key := range []string{"content", "reasoning", "refusal", "annotations"} {
		if _, ok := s.choices[0].message[key]; ok {
			t.Fatal("delivered field retained", key)
		}
	}
}

func TestStringBufferContentTypesAndPresence(t *testing.T) {
	for _, text := range []string{`null`, `""`, `42`, `[]`} {
		raw := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":%s},\"finish_reason\":\"stop\"}]}\n\n", text)
		out, err := Aggregate(strings.NewReader(raw))
		if text == `42` || text == `[]` {
			if err == nil {
				t.Fatal("accepted invalid delta")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		m := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		v, exists := m["reasoning"]
		if text == `null` && exists || text == `""` && (!exists || v != "") {
			t.Fatal(m)
		}
	}
}
