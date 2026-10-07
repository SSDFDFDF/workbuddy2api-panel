package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
)

func TestRealmResolverExplicitPrefixWins(t *testing.T) {
	// 显式前缀恒优先，不受 Default 策略影响。
	for _, def := range []string{RealmDefaultCN, RealmDefaultGlobal, RealmDefaultAuto} {
		r := NewRealmResolver(def, func(string) bool { return true })
		if realm, bare := r.Resolve("cn:glm-5.2"); realm != "cn" || bare != "glm-5.2" {
			t.Errorf("def=%s cn: -> %s/%s", def, realm, bare)
		}
		if realm, bare := r.Resolve("global:gpt-5.4"); realm != "global" || bare != "gpt-5.4" {
			t.Errorf("def=%s global: -> %s/%s", def, realm, bare)
		}
	}
}

func TestRealmResolverBareDefaults(t *testing.T) {
	cases := []struct {
		def   string
		ready func(string) bool
		want  string
	}{
		{RealmDefaultCN, nil, "cn"},
		{"", nil, "cn"},             // 空/非法 → cn
		{"bogus", nil, "cn"},        // 非法 → cn
		{"CN", nil, "cn"},           // 大小写归一
		{" Global ", nil, "global"}, // 空白+大小写归一
		{RealmDefaultGlobal, nil, "global"},
		// auto：只有一个域有可用号 → 归该域。
		{RealmDefaultAuto, func(realm string) bool { return realm == "global" }, "global"},
		{RealmDefaultAuto, func(realm string) bool { return realm == "cn" }, "cn"},
		// auto：两域都有 / 都没有 → cn（稳定不摇摆）。
		{RealmDefaultAuto, func(string) bool { return true }, "cn"},
		{RealmDefaultAuto, func(string) bool { return false }, "cn"},
		// auto：RealmReady 为 nil → cn（安全默认）。
		{RealmDefaultAuto, nil, "cn"},
	}
	for _, tc := range cases {
		r := NewRealmResolver(tc.def, tc.ready)
		realm, bare := r.Resolve("deepseek-v4.1-flash")
		if realm != tc.want {
			t.Errorf("def=%q realm=%q want %q", tc.def, realm, tc.want)
		}
		if bare != "deepseek-v4.1-flash" {
			t.Errorf("def=%q bare=%q", tc.def, bare)
		}
	}
}

func TestRealmResolverNonRealmPrefixIsBare(t *testing.T) {
	// 非枚举前缀（含冒号但不是 cn/global）整串视为裸名。
	r := NewRealmResolver(RealmDefaultGlobal, nil)
	if realm, bare := r.Resolve("foo:bar"); realm != "global" || bare != "foo:bar" {
		t.Errorf("foo:bar -> %s/%s", realm, bare)
	}
	// 无冒号裸名。
	if realm, bare := r.Resolve("gpt-5"); realm != "global" || bare != "gpt-5" {
		t.Errorf("gpt-5 -> %s/%s", realm, bare)
	}
}

func TestRealmResolverNilSafe(t *testing.T) {
	var r *RealmResolver
	if realm, bare := r.Resolve("global:x"); realm != "global" || bare != "x" {
		t.Errorf("nil resolver global: -> %s/%s", realm, bare)
	}
	if realm, bare := r.Resolve("x"); realm != "cn" || bare != "x" {
		t.Errorf("nil resolver bare -> %s/%s", realm, bare)
	}
}

func TestResolveModelLegacyDefaultsCN(t *testing.T) {
	// 旧函数面保持默认 cn（零回归）。
	if realm, bare := ResolveModel("glm-5.2"); realm != "cn" || bare != "glm-5.2" {
		t.Errorf("ResolveModel bare -> %s/%s", realm, bare)
	}
	if realm, bare := resolveModel("global:gpt"); realm != "global" || bare != "gpt" {
		t.Errorf("resolveModel -> %s/%s", realm, bare)
	}
}

// TestChatBareModelRealmPolicy 端到端验证 model_default_realm 影响选号域：
// 池内只有 global 账号时，裸名默认 cn → 503；默认 global / auto → 命中该号。
func TestChatBareModelRealmPolicy(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })

	newGlobalOnlyPool := func() *pool.Pool {
		return testPoolWith(&auth.Auth{
			UID: "g1", AccessToken: "at-g", ExpiresAt: 9999999999,
			Domain: "www.workbuddy.ai", // → Realm()=="global"
		})
	}
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, sseOK, true })
	body := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`

	run := func(cfg Config) int {
		rec := httptest.NewRecorder()
		NewHandler(cfg).ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		return rec.Code
	}

	// 默认（无 resolver）→ cn：池内无 CN 号 → 503。
	if code := run(Config{Pool: newGlobalOnlyPool(), Upstream: up}); code != http.StatusServiceUnavailable {
		t.Errorf("bare model default cn code=%d want 503", code)
	}
	// Default=global → 命中 global 号。
	if code := run(Config{Pool: newGlobalOnlyPool(), Upstream: up,
		RealmResolver: NewRealmResolver(RealmDefaultGlobal, nil)}); code != http.StatusOK {
		t.Errorf("bare model default global code=%d want 200", code)
	}
	// auto + 池内仅 global 可用 → global。
	auto := func(p *pool.Pool) *RealmResolver {
		return NewRealmResolver(RealmDefaultAuto, func(realm string) bool {
			return len(p.AvailableUIDsForRealm(realm)) > 0
		})
	}
	p := newGlobalOnlyPool()
	if code := run(Config{Pool: p, Upstream: up, RealmResolver: auto(p)}); code != http.StatusOK {
		t.Errorf("bare model auto(global-only pool) code=%d want 200", code)
	}
	// 显式 global: 前缀在默认 cn 策略下也命中。
	prefixed := `{"model":"global:deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	NewHandler(Config{Pool: newGlobalOnlyPool(), Upstream: up}).ServeHTTP(rec,
		httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(prefixed)))
	if rec.Code != http.StatusOK {
		t.Errorf("explicit global: prefix code=%d want 200", rec.Code)
	}
}
