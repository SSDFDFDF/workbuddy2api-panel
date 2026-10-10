package session

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func TestHeaderSessionKey(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"claude code", hdr("X-Claude-Code-Session-Id", "cc-1"), "claude:cc-1"},
		{"codex kebab", hdr("Session-Id", "cx-1"), "codex:cx-1"},
		{"codex snake (non-canonical key)", func() http.Header {
			// http.Header.Set 会把 Session_id 规范化成 Session-Id；绕过 Set
			// 直接赋值模拟非规范化头名（代理/中间件转发形态）。
			h := http.Header{}
			h["Session_id"] = []string{"cx-2"}
			return h
		}(), "codex:cx-2"},
		{"antigravity", hdr("X-Http-Session-Id", "agy-1"), "agy:agy-1"},
		{"generic", hdr("X-Session-ID", "gen-1"), "hdr:gen-1"},
		{"opencode", hdr("X-Session-Affinity", "ses_abc"), "affinity:ses_abc"},
		{"pi slot", hdr("X-Slot-Session-Id", "slot-1"), "slot:slot-1"},
		{"task", hdr("X-Task-Id", "t-1"), "task:t-1"},
		{"thread", hdr("Thread-Id", "th-1"), "thread:th-1"},
		{"pi client", hdr("X-Client-Request-Id", "pi-1"), "clientreq:pi-1"},
		{"priority claude over codex", hdr("X-Claude-Code-Session-Id", "cc", "Session-Id", "cx"), "claude:cc"},
		{"priority codex over generic", hdr("Session-Id", "cx", "X-Session-ID", "gen"), "codex:cx"},
		{"none", hdr(), ""},
		{"blank ignored", hdr("X-Session-ID", "  "), ""},
		{"control chars rejected", hdr("X-Session-ID", "a\x00b"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HeaderSessionKey(c.header)
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestBodyKey(t *testing.T) {
	derivedBody := `{"model":"m","messages":[{"role":"user","content":"你好"}]}`
	derived := ExtractKey([]byte(derivedBody))
	if derived == "" {
		t.Fatal("fixture: derived key must be non-empty")
	}
	cases := []struct {
		name string
		body string
		pck  string
		want string
	}{
		// Claude Code 旧格式：user_{hash}_session_{uuid} → session 段
		{"claude legacy user_id", `{"metadata":{"user_id":"user_a1b2c3_session_550e8400-e29b-41d4-a716-446655440000"}}`, "", "claude:550e8400-e29b-41d4-a716-446655440000"},
		// Claude Code 新格式：JSON identity（带 device_id/account_uuid 等其他键），
		// 只取 session_id 字段
		{"claude json user_id full identity", `{"metadata":{"user_id":"{\"device_id\":\"d1\",\"account_uuid\":\"a1\",\"session_id\":\"sess-xyz-123\"}"}}`, "", "claude:sess-xyz-123"},
		// 非 Claude 格式 user_id：不提取，且抑制派生（反垄断契约）
		{"plain metadata user_id suppressed", `{"metadata":{"user_id":"user_123"},"messages":[{"role":"user","content":"hi"}]}`, "", ""},
		{"plain top-level user_id suppressed", `{"user_id":"u1","messages":[{"role":"user","content":"hi"}]}`, "", ""},
		{"top-level session_id", `{"session_id":"s1","messages":[]}`, "", "session:s1"},
		{"metadata.session_id", `{"metadata":{"session_id":"ms1"}}`, "", "session:ms1"},
		{"extra_body.session_id", `{"extra_body":{"session_id":"es1"}}`, "", "session:es1"},
		{"top-level thread_id", `{"thread_id":"t1"}`, "", "thread:t1"},
		{"metadata.thread_id", `{"metadata":{"thread_id":"mt1"}}`, "", "thread:mt1"},
		{"task_id", `{"task_id":"tk1"}`, "", "task:tk1"},
		{"metadata.action_id", `{"metadata":{"action_id":"ac1"}}`, "", "task:ac1"},
		{"conversation object", `{"conversation":{"id":"conv-obj"}}`, "", "conv:conv-obj"},
		{"conversation string", `{"conversation":"conv-str"}`, "", "conv:conv-str"},
		{"thread outranks session", `{"thread_id":"t1","session_id":"s1"}`, "", "thread:t1"},
		// prompt_cache_key：调用方传入的裸值原样返回（高于派生、低于非标准键位）
		{"prompt_cache_key passthrough", `{"messages":[{"role":"user","content":"hi"}]}`, "pck-1", "pck-1"},
		{"body keys outrank prompt_cache_key", `{"session_id":"s1"}`, "pck-1", "session:s1"},
		// 内容派生键：无任何显式标识时的兜底
		{"derived fallback", derivedBody, "", derived},
		// body 为空 / 解析失败：prompt_cache_key 仍可用
		{"empty body pck", "", "pck-9", "pck-9"},
		{"broken json pck", `{not json`, "pck-8", "pck-8"},
		{"empty body no pck", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BodyKey([]byte(c.body), c.pck)
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
			var obj map[string]any
			if json.Unmarshal([]byte(c.body), &obj) == nil {
				if got := BodyKeyObject(obj, c.pck); got != c.want {
					t.Fatalf("parsed-object key=%q want %q", got, c.want)
				}
			}
		})
	}
}

// normalizeSessionID 超长折叠：>256 字节的值折叠为 前缀#sha256，且折叠后稳定。
func TestNormalizeSessionIDBounded(t *testing.T) {
	long := strings.Repeat("a", 4000)
	bounded := normalizeSessionID(long)
	if len(bounded) > 256 {
		t.Fatalf("bounded id too long: %d", len(bounded))
	}
	if !strings.Contains(bounded, "#") {
		t.Fatalf("bounded id should contain '#': %q", bounded)
	}
	if normalizeSessionID(long) != bounded {
		t.Fatal("bounding must be deterministic")
	}
	// UTF-8 边界：截断处不劈开多字节字符。
	multibyte := strings.Repeat("中", 200) // 600 字节
	if !isUTF8Valid(normalizeSessionID(multibyte)) {
		t.Fatal("bounded id must be valid UTF-8")
	}
	// 短值原样保留。
	if got := normalizeSessionID("  abc  "); got != "abc" {
		t.Fatalf("short id should trim only, got %q", got)
	}
}

func isUTF8Valid(s string) bool { return s == string([]rune(s)) }

// 不同命名空间同字面值不互撞：claude:abc 与 session:abc 是两个键。
func TestNamespacePrefixesDisambiguate(t *testing.T) {
	a := HeaderSessionKey(hdr("X-Claude-Code-Session-Id", "abc"))
	b := BodyKey([]byte(`{"session_id":"abc"}`), "")
	if a == b || a == "" || b == "" {
		t.Fatalf("namespaced keys must differ: %q vs %q", a, b)
	}
}

// Claude Code 的 user_id session 段提取不能影响派生抑制闸：平 user_id 仍然
// 不派生内容键（显式格式命中由 BodyKey 键位链第 1 级负责，优先级更高）。
func TestUserIDSuppressionStillHolds(t *testing.T) {
	body := []byte(`{"metadata":{"user_id":"user_123"},"messages":[{"role":"user","content":"hi"}]}`)
	if got := BodyKey(body, ""); got != "" {
		t.Fatalf("plain user_id should suppress derivation, got %q", got)
	}
}
