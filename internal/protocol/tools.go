package protocol

import "github.com/linguo2625469/workbuddy2api-panel/internal/upstream"

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

func completionOptions(req *Request, opts []upstream.StreamOption) []upstream.StreamOption {
	return append(opts, upstream.WithEmptyToolIdentityDeltas(), upstream.WithDeclaredToolNames(declaredToolNames(req)))
}
