// Package jsondoc decodes bounded JSON without losing integer precision or accepting
// ambiguous duplicate keys. It is shared by all request/response rewrite paths.
package jsondoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const MaxDepth = 128

func Decode(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON value")
	}
	return v, nil
}

func Object(raw []byte) (map[string]any, error) {
	v, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return m, nil
}

func value(d *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d", MaxDepth)
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := t.(json.Delim)
	if !compound {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			v, err := value(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("unterminated JSON object")
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := value(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("unterminated JSON array")
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

func Int(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := n.Int64()
		return i, e == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n >= -9007199254740991 && n <= 9007199254740991 && n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}
