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

// Copy returns a deep copy of a document produced by Decode/Object.
//
// 为什么需要它：出站链路需要在「不污染调用方文档」的前提下就地应用改写
// （线格式转换、指纹改写、缓存键注入）。此前的做法是 json.Marshal → 再解析一次
// 来拿副本——对一个 5 MiB 图片请求来说，这一次往返就是数十毫秒与数十 MB 分配。
// 深拷贝只复制容器（map/slice），叶子值（json.Number/string/bool/nil）按值共享：
// 它们本身不可变，共享是安全的，且这让大 payload 的拷贝成本与字节数**无关**
// （base64 图片仍是同一个 string，不做 memcpy）。
//
// 未识别的类型原样返回（防御：Decode 只产出上述类型，但调用方可能塞入别的值）。
func Copy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = Copy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Copy(e)
		}
		return out
	default:
		return v
	}
}

// CopyObject deep-copies an object document（nil 安全）。
func CopyObject(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = Copy(v)
	}
	return out
}
