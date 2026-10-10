package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Reference preserves the old JSON roundtrip, independently of the fast path.
func signatureJSONReference(content any) string {
	raw, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	st := strings.TrimSpace(string(raw))
	if st == "" || st == "null" {
		return ""
	}
	if st[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	if st[0] != '[' {
		return ""
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	nonText := false
	for _, raw := range parts {
		var p struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return ""
		}
		if p.Type == "" || p.Type == "text" {
			b.WriteString(p.Text)
			continue
		}
		nonText = true
		b.WriteString("\n[" + p.Type + "]\n")
	}
	if nonText {
		return strings.TrimSpace(b.String())
	}
	return b.String()
}

func TestContentSignatureMatchesJSONReference(t *testing.T) {
	cases := []string{
		`null`, `"  hello 世界\n"`, `42`, `{}`, `[]`, `[null]`,
		`[{"text":" a "},{"type":"text","text":"b\n"}]`,
		`[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]`,
		`[{"type":"text","text":" x "},{"type":"image_url"},{"type":"image_url"}]`,
		`[{"type":null,"text":null}]`, `[{"type":17,"text":"x"}]`,
		`[{"type":"image_url","text":false}]`, `["bad"]`, `[false]`, `[[]]`,
		`[{"Type":"image_url","Text":"x"}]`,
		`[{"TYPE":"text","type":"image_url","TEXT":" a ","text":null}]`,
		`[{"TYPE":17,"type":"text","text":"x"}]`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			var content any
			if err := json.Unmarshal([]byte(raw), &content); err != nil {
				t.Fatal(err)
			}
			if got, want := userContentSignature(content), signatureJSONReference(content); got != want {
				t.Fatalf("signature=%q want %q", got, want)
			}
		})
	}
}

func FuzzContentSignatureMatchesJSONReference(f *testing.F) {
	for _, raw := range []string{`"hello"`, `[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"abc"}}]`, `[null]`, `[{"Text":"x","text":null}]`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		var content any
		if json.Unmarshal([]byte(raw), &content) != nil {
			return
		}
		if got, want := userContentSignature(content), signatureJSONReference(content); got != want {
			t.Fatalf("signature=%q want %q for %s", got, want, raw)
		}
	})
}

func BenchmarkContentSignatureImage(b *testing.B) {
	for _, size := range []int{0, 1 << 20, 5 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			content := []any{map[string]any{"type": "text", "text": "describe"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": strings.Repeat("A", size)}}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if userContentSignature(content) == "" {
					b.Fatal("empty signature")
				}
			}
		})
	}
}
