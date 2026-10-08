package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompletionBlockPlan(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want []blockRef
	}{
		{textStream, []blockRef{{kind: textBlock}}},
		{toolsStream, []blockRef{{kind: textBlock}, {kind: toolBlock, tool: 0}, {kind: toolBlock, tool: 1}}},
		{emptyIdentityStream, []blockRef{{kind: toolBlock, tool: 0}, {kind: toolBlock, tool: 1}}},
		{`{"object":"chat.completion","choices":[{"index":0,"message":{"content":""},"finish_reason":"stop"}]}`, []blockRef{{kind: textBlock}}},
	} {
		var indices []int
		var text strings.Builder
		req := testRequest(t, Responses)
		c, err := collect(strings.NewReader(tt.raw), req, func(index int, delta string) error {
			indices = append(indices, index)
			text.WriteString(delta)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.blocks, tt.want) {
			t.Fatalf("blocks=%v want=%v", c.blocks, tt.want)
		}
		for _, index := range indices {
			if c.blocks[index].kind != textBlock {
				t.Fatal("text delta bound to wrong block")
			}
		}
		m, _, _ := responseParts(c.Raw)
		if s, _ := m["content"].(string); s != text.String() {
			t.Fatal("streamed text differs from final block", s, text.String())
		}
		out, err := c.Format(req)
		if err != nil || len(out["output"].([]any)) != len(tt.want) {
			t.Fatal(out, err)
		}
		for key := range c.Raw {
			if strings.Contains(key, "block") || strings.Contains(key, "gateway") {
				t.Fatal("protocol state leaked into raw accounting", key)
			}
		}
	}
}

func TestStaleBlockPlanIsRejected(t *testing.T) {
	for _, mutate := range []func(*Completion){
		func(c *Completion) { c.blocks = nil },
		func(c *Completion) { c.blocks = append(c.blocks, blockRef{kind: toolBlock, tool: 0}) },
		func(c *Completion) { c.blocks[0].kind = blockKind(99) },
		func(c *Completion) { c.blocks[1].tool = -1 },
	} {
		c, err := Aggregate(strings.NewReader(toolsStream), testRequest(t, Responses))
		if err != nil {
			t.Fatal(err)
		}
		mutate(c)
		if _, err := c.Format(testRequest(t, Responses)); err == nil {
			t.Fatal("stale block plan silently lost content")
		}
	}
}

func TestParallelToolIndexOrderNotArrivalOrder(t *testing.T) {
	raw := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"id":"b","type":"function","function":{"name":"other","arguments":"{}"}},{"index":2,"id":"a","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	c, err := Aggregate(strings.NewReader(raw), testRequest(t, Responses))
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Format(testRequest(t, Responses))
	if err != nil {
		t.Fatal(err)
	}
	items := out["output"].([]any)
	if items[0].(map[string]any)["call_id"] != "a" || items[1].(map[string]any)["call_id"] != "b" {
		t.Fatal("native index order changed", items)
	}
}
