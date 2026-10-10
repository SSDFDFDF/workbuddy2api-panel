package protocol

import (
	"fmt"
	"io"

	"workbuddy_manager/internal/upstream"
)

type blockKind uint8

const (
	textBlock blockKind = iota
	toolBlock
)

// blockRef refers into the native completion without copying text or arguments.
// Tools retain native index order; arrival order of parallel parameter fragments
// is not a declaration of content order.
type blockRef struct {
	kind blockKind
	tool int
}

// Completion keeps native accounting separate from the destination block plan.
// Never put gateway-only state in the upstream JSON document.
type Completion struct {
	Raw    map[string]any
	blocks []blockRef
}

// validatePlan catches stale/mutated Raw documents rather than silently omitting
// newly added content. Only the current one-text-prefix subset can be emitted.
func (c *Completion) validatePlan(text string, calls int) error {
	nextTool, texts := 0, 0
	for i, block := range c.blocks {
		switch block.kind {
		case textBlock:
			texts++
			if i != 0 || texts != 1 {
				return fmt.Errorf("unsupported text block order")
			}
		case toolBlock:
			if block.tool != nextTool {
				return fmt.Errorf("tool block no longer matches completion")
			}
			nextTool++
		default:
			return fmt.Errorf("unsupported output block kind")
		}
	}
	if nextTool != calls || (text != "" || calls == 0) && texts != 1 {
		return fmt.Errorf("block plan no longer matches completion")
	}
	return nil
}

func (c *Completion) Format(req *Request) (map[string]any, error) {
	if req.Kind == Chat {
		return c.Raw, nil
	}
	return newBuilder(req).formatCompletion(c)
}

// outputState is shared by SSE and JSON consumers. It establishes text blocks
// while observing validated frames, then binds tools only after upstream finish.
// Text and tool deltas may interleave: text is delivered through one block
// (order between separately delivered blocks cannot be replayed by the
// destination protocols, but content is never dropped).
type outputState struct {
	blocks   []blockRef
	streamed bool
}

func (o *outputState) observe(chunk map[string]any, text func(int, string) error) error {
	choices, _ := chunk["choices"].([]any)
	for _, v := range choices {
		ch := v.(map[string]any)
		delta, hasDelta := ch["delta"].(map[string]any)
		m := delta
		if m == nil {
			m, _ = ch["message"].(map[string]any)
		}
		if err := checkMessage(m); err != nil {
			return err
		}
		s := messageText(m)
		if s == "" {
			continue
		}
		if len(o.blocks) == 0 {
			o.blocks = append(o.blocks, blockRef{kind: textBlock})
		}
		if text == nil {
			continue
		}
		// A full message snapshot may carry the whole text at once. Stream it
		// only when no text has been streamed yet; otherwise the deltas already
		// delivered it (the final formatted text still comes from the aggregated
		// message, so a snapshot is never lost downstream).
		if !hasDelta {
			if o.streamed {
				continue
			}
			o.streamed = true
		}
		if err := text(len(o.blocks)-1, s); err != nil {
			return err
		}
	}
	return nil
}

func (o *outputState) complete(raw map[string]any) (*Completion, error) {
	m, _, err := responseParts(raw)
	if err != nil {
		return nil, err
	}
	calls, _ := m["tool_calls"].([]any)
	// The final message decides the plan, not the frames that happened to carry
	// text: a refusal fallback (or any snapshot text) may be visible here without
	// ever having arrived as a content delta. Without a text block the plan and
	// the formatted message disagree and the turn would fail in validatePlan.
	if len(o.blocks) == 0 && (len(calls) == 0 || visibleText(m) != "") {
		o.blocks = append(o.blocks, blockRef{kind: textBlock})
	}
	for i := range calls {
		o.blocks = append(o.blocks, blockRef{kind: toolBlock, tool: i})
	}
	return &Completion{Raw: raw, blocks: o.blocks}, nil
}

func collect(r io.Reader, req *Request, text func(int, string) error, opts ...upstream.StreamOption) (*Completion, error) {
	state := &outputState{}
	raw, err := upstream.ConsumeCompletion(r, func(chunk map[string]any) error {
		return state.observe(chunk, text)
	}, completionOptions(req, opts)...)
	if err != nil {
		return nil, err
	}
	return state.complete(raw)
}

func Aggregate(r io.Reader, req *Request, opts ...upstream.StreamOption) (*Completion, error) {
	return collect(r, req, nil, opts...)
}
