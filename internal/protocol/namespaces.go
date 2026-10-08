package protocol

import (
	"fmt"
	"strconv"
)

// toolIdentity is protocol-only metadata. The Chat wire sees only chatName.
// Namespace descriptions have no equivalent in Chat, so this subset requires
// an explicitly empty description rather than injecting it into child prompts.
type toolIdentity struct {
	name      string
	namespace string
}

type toolIndex struct {
	byChat     map[string]toolIdentity
	byIdentity map[toolIdentity]string
	byLocal    map[string][]string
}

func newToolIndex() *toolIndex {
	return &toolIndex{byChat: map[string]toolIdentity{}, byIdentity: map[toolIdentity]string{}, byLocal: map[string][]string{}}
}

// Length-delimited namespace names are injective, readable, stable across
// requests and independent of declaration order. Never truncate or guess a
// reverse split. Flat-name collisions fail instead of first-wins deduplication.
func namespaceChatName(namespace, name, path string) (string, error) {
	for _, s := range []string{namespace, name} {
		if len(s) == 0 || len(s) > 64 {
			return "", invalid(path, "namespace and child names must be 1..64 ASCII name characters")
		}
		for _, c := range []byte(s) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return "", invalid(path, "namespace and child names must use letters, digits, underscores or hyphens")
			}
		}
	}
	alias := "ns_" + strconv.Itoa(len(namespace)) + "_" + namespace + "_" + name
	if len(alias) > 64 {
		return "", invalid(path, "qualified namespace tool name exceeds Chat's 64-byte limit")
	}
	return alias, nil
}

func (idx *toolIndex) add(m map[string]any, namespace, path string) (map[string]any, error) {
	if err := fields(m, path, "type name description parameters strict"); err != nil {
		return nil, err
	}
	if m["type"] != "function" {
		return nil, invalid(path+".type", "only client function tools are supported")
	}
	if m["strict"] != false {
		return nil, invalid(path+".strict", "explicit strict:false required; schema-constrained generation is not verified")
	}
	t, err := function(m["name"], m["description"], m["parameters"], path)
	if err != nil {
		return nil, err
	}
	fn := t["function"].(map[string]any)
	id := toolIdentity{name: fn["name"].(string), namespace: namespace}
	alias := id.name
	if namespace != "" {
		alias, err = namespaceChatName(namespace, id.name, path+".name")
		if err != nil {
			return nil, err
		}
	}
	if _, exists := idx.byChat[alias]; exists {
		return nil, invalid(path+".name", "duplicate or colliding Chat tool name")
	}
	idx.byChat[alias], idx.byIdentity[id] = id, alias
	idx.byLocal[id.name] = append(idx.byLocal[id.name], alias)
	fn["name"] = alias
	return t, nil
}

func (idx *toolIndex) declarations(v any) ([]any, error) {
	a, ok := v.([]any)
	if !ok {
		return nil, invalid("tools", "array required")
	}
	out := make([]any, 0, len(a))
	namespaces := map[string]bool{}
	for i, v := range a {
		p := fmt.Sprintf("tools[%d]", i)
		m, err := object(v, p)
		if err != nil {
			return nil, err
		}
		if m["type"] != "namespace" {
			t, err := idx.add(m, "", p)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
			continue
		}
		if err := fields(m, p, "type name description tools"); err != nil {
			return nil, err
		}
		ns, err := nonempty(m["name"], p+".name")
		if err != nil {
			return nil, err
		}
		if namespaces[ns] {
			return nil, invalid(p+".name", "duplicate namespace declaration")
		}
		namespaces[ns] = true
		if m["description"] != "" {
			return nil, invalid(p+".description", "explicit empty namespace description required; group instructions cannot be represented by Chat")
		}
		children, ok := m["tools"].([]any)
		if !ok || len(children) == 0 {
			return nil, invalid(p+".tools", "non-empty function tool array required")
		}
		for j, child := range children {
			cp := fmt.Sprintf("%s.tools[%d]", p, j)
			m, err := object(child, cp)
			if err != nil {
				return nil, err
			}
			t, err := idx.add(m, ns, cp)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// The official function tool_choice has only name (no namespace field). Resolve
// a local name only when it identifies exactly one declaration; ambiguous local
// names must use auto/required or a narrower declaration set, not guessed aliases.
func (idx *toolIndex) choice(v any) (string, error) {
	name, err := nonempty(v, "tool_choice.name")
	if err != nil {
		return "", err
	}
	names := idx.byLocal[name]
	if len(names) != 1 {
		return "", invalid("tool_choice.name", "selected function must identify exactly one declared tool")
	}
	return names[0], nil
}

func (idx *toolIndex) history(name string, namespace any, path string) (string, error) {
	if namespace != nil {
		ns, err := nonempty(namespace, path+".namespace")
		if err != nil {
			return "", err
		}
		alias, ok := idx.byIdentity[toolIdentity{name: name, namespace: ns}]
		if !ok {
			return "", invalid(path+".namespace", "namespaced history requires its function declaration in this request")
		}
		return alias, nil
	}
	if id, ok := idx.byChat[name]; ok && id.namespace != "" {
		return "", invalid(path+".name", "Chat namespace alias is not a client tool identity; supply namespace and local name")
	}
	if _, flat := idx.byIdentity[toolIdentity{name: name}]; !flat && len(idx.byLocal[name]) > 0 {
		return "", invalid(path+".namespace", "explicit namespace required for namespaced tool history")
	}
	return name, nil // Existing flat historical tools need not be redeclared.
}

func (req *Request) responseToolIdentity(chatName string) toolIdentity {
	if req.tools != nil {
		if id, ok := req.tools.byChat[chatName]; ok {
			return id
		}
	}
	return toolIdentity{name: chatName}
}
