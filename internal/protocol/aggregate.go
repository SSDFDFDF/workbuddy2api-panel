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
// This foundation deliberately retains the strict text-before-tools subset:
// accepting interleaving here also needs a lossless Chat history representation.
type outputState struct {
	blocks      []blockRef
	sawTools    bool
	sawMessage  bool
	sawSnapshot bool
}

func (o *outputState) observe(chunk map[string]any, text func(int, string) error) error {
	choices, _ := chunk["choices"].([]any)
	for _, v := range choices {
		ch := v.(map[string]any)
		if ch["logprobs"] != nil {
			return fmt.Errorf("upstream logprobs are unsupported")
		}
		_, hasDelta := ch["delta"]
		_, hasMessage := ch["message"]
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
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("only upstream text is supported")
			}
			if s != "" {
				if o.sawTools {
					return fmt.Errorf("text after tool deltas cannot preserve output order in this bridge")
				}
				if len(o.blocks) == 0 {
					o.blocks = append(o.blocks, blockRef{kind: textBlock})
				}
				if text != nil {
					if err := text(len(o.blocks)-1, s); err != nil {
						return err
					}
				}
			}
		}
		if a, ok := m["tool_calls"].([]any); ok && len(a) > 0 {
			o.sawTools = true
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
	if len(o.blocks) == 0 && len(calls) == 0 {
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
