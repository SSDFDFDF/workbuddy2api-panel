package upstream

import (
	"fmt"
	"strings"
)

// toolNameState is opt-in for translated responses only. Native Chat retains
// literal delta concatenation. A candidate is always an observed name prefix,
// never a name guessed just because the request declares a single tool.
//
// Each non-empty chunk can extend a prefix, repeat that entire prefix, or be a
// cumulative prefix. Keep every viable interpretation; only a unique *complete*
// declared name may be emitted. Unrelated replacements are never accepted.
type toolNameState struct {
	declared map[string]bool
	prefixes map[string]bool
}

const maxToolNameCandidates = 128
const maxToolNameCandidateBytes = 64 << 10

func (s *toolNameState) add(v any) error {
	if v == nil {
		return nil
	}
	chunk, ok := v.(string)
	if !ok {
		return fmt.Errorf("invalid string delta name")
	}
	if chunk == "" {
		return nil
	}
	if len(chunk) > maxToolNameCandidateBytes {
		return fmt.Errorf("tool name exceeds bounded name state limit")
	}
	next := map[string]bool{}
	bytes := 0
	add := func(prefix string) error {
		if next[prefix] {
			return nil
		}
		for name := range s.declared {
			if strings.HasPrefix(name, prefix) {
				if len(next) >= maxToolNameCandidates || len(prefix) > maxToolNameCandidateBytes-bytes {
					return fmt.Errorf("tool name exceeds bounded name state limit")
				}
				next[prefix] = true
				bytes += len(prefix)
				break
			}
		}
		return nil
	}
	if len(s.prefixes) == 0 {
		if err := add(chunk); err != nil {
			return err
		}
	} else {
		for prefix := range s.prefixes {
			if len(prefix)+len(chunk) > maxToolNameCandidateBytes {
				// Do not silently discard an over-limit interpretation and
				// mistake a remaining candidate for an unambiguous result.
				return fmt.Errorf("tool name exceeds bounded name state limit")
			}
			if err := add(prefix + chunk); err != nil {
				return err
			}
			if strings.HasPrefix(chunk, prefix) {
				if err := add(chunk); err != nil {
					return err
				}
			}
		}
	}
	if len(next) == 0 {
		return fmt.Errorf("upstream tool name does not match declared tools")
	}
	s.prefixes = next
	return nil
}

func (s *toolNameState) name() (string, error) {
	name := ""
	for prefix := range s.prefixes {
		if s.declared[prefix] {
			if name != "" && name != prefix {
				return "", fmt.Errorf("ambiguous upstream tool name")
			}
			name = prefix
		}
	}
	if name == "" {
		return "", fmt.Errorf("incomplete or undeclared upstream tool name")
	}
	return name, nil
}

// WithDeclaredToolNames enables name-dialect resolution for ConsumeCompletion.
// Even an empty declaration set enables validation (no tools may be invented).
// The copied set is request-local; native Aggregate and StreamHint ignore it.
func WithDeclaredToolNames(names []string) StreamOption {
	declared := make(map[string]bool, len(names))
	for _, name := range names {
		declared[name] = true
	}
	return func(o *streamOptions) { o.declaredToolNames = declared }
}
