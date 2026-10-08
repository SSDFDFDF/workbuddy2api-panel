package protocol

import (
	"fmt"
	"io"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// outputOrder guards a limitation of Chat aggregation: after tools begin, text
// cannot be collapsed back in front of them without changing content order.
// Both delivery modes apply the same rule to the same validated event stream.
type outputOrder struct {
	sawTools    bool
	sawMessage  bool
	sawSnapshot bool
}

func (o *outputOrder) observe(chunk map[string]any) error {
	choices, _ := chunk["choices"].([]any)
	for _, v := range choices {
		ch := v.(map[string]any)
		if ch["logprobs"] != nil {
			return fmt.Errorf("upstream logprobs are unsupported")
		}
		_, hasDelta := ch["delta"]
		_, hasMessage := ch["message"]
		// A full message is a snapshot, not another text delta. Only the first
		// event may be a snapshot; mixing forms cannot preserve stream semantics.
		if hasMessage && (hasDelta || o.sawMessage) || hasDelta && o.sawSnapshot {
			return fmt.Errorf("mixed upstream message snapshots and deltas are unsupported")
		}
		if hasMessage {
			o.sawSnapshot = true
		}
		m, _ := ch["delta"].(map[string]any)
		if m == nil {
			m, _ = ch["message"].(map[string]any)
		}
		if m != nil {
			o.sawMessage = true
		}
		if err := checkMessage(m); err != nil {
			return err
		}
		if v := m["content"]; v != nil {
			text, ok := v.(string)
			if !ok {
				return fmt.Errorf("only upstream text is supported")
			}
			if text != "" && o.sawTools {
				return fmt.Errorf("text after tool deltas cannot preserve output order in this bridge")
			}
		}
		if a, ok := m["tool_calls"].([]any); ok && len(a) > 0 {
			o.sawTools = true
		}
	}
	return nil
}

func Aggregate(r io.Reader, req *Request, opts ...upstream.StreamOption) (map[string]any, error) {
	order := &outputOrder{}
	return upstream.ConsumeCompletion(r, order.observe, completionOptions(req, opts)...)
}
