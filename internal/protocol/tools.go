package protocol

import (
	"fmt"

	"workbuddy_manager/internal/upstream"
)

func declaredToolNames(req *Request) []string {
	names := []string{}
	tools, _ := req.Chat.Object["tools"].([]any)
	for _, v := range tools {
		t, _ := v.(map[string]any)
		fn, _ := t["function"].(map[string]any)
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// strictToolNames reports whether upstream tool names must match the tools this
// request authorizes. A request that declares no tools and carries no tool
// history has nothing to contradict: the bridge passes observed names through
// instead of failing a tool-less turn (the upstream may still attempt one of
// its built-in tools). History alone keeps the strict check so retired
// identities never become callable (see toolIndex.wireOwners).
func (req *Request) strictToolNames() bool {
	if len(declaredToolNames(req)) > 0 {
		return true
	}
	return req.Kind == Responses && req.tools != nil && len(req.tools.wireOwners) > 0
}

// validateToolSelection enforces the translated request, not just declaration
// membership. A cutoff may explain a missing required tool, but cannot authorize
// forbidden calls, a different selected function or disallowed parallel calls.
func validateToolSelection(req *Request, calls []any, finish string) error {
	if req.Kind == Chat {
		return nil
	}
	obj := req.Chat.Object
	if obj["parallel_tool_calls"] == false && len(calls) > 1 {
		return fmt.Errorf("upstream returned parallel tools when parallel_tool_calls is false")
	}
	choice, _ := obj["tool_choice"].(string)
	selected := ""
	if tc, ok := obj["tool_choice"].(map[string]any); ok && tc["type"] == "function" {
		fn, _ := tc["function"].(map[string]any)
		selected, _ = fn["name"].(string)
	}
	if choice == "none" && len(calls) > 0 {
		return fmt.Errorf("upstream returned tools despite tool_choice:none")
	}
	if (choice == "required" || selected != "") && len(calls) == 0 && finish != "length" && finish != "content_filter" {
		return fmt.Errorf("upstream did not return the required tool call")
	}
	if selected != "" {
		for _, v := range calls {
			call, _ := v.(map[string]any)
			fn, _ := call["function"].(map[string]any)
			if fn["name"] != selected {
				return fmt.Errorf("upstream tool does not match selected function")
			}
		}
	}
	return nil
}

func completionOptions(req *Request, opts []upstream.StreamOption) []upstream.StreamOption {
	opts = append(opts, upstream.WithEmptyToolIdentityDeltas(), upstream.WithLegacyFunctionCalls())
	if req.strictToolNames() {
		opts = append(opts, upstream.WithDeclaredToolNames(declaredToolNames(req)))
	} else {
		// No tool set is authorized: dedup name dialects but pass observed
		// names through instead of failing a tool-less turn.
		opts = append(opts, upstream.WithToolNameDialects())
	}
	return opts
}
