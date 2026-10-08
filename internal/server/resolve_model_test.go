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
	up.GlobalEnabled = true
	body := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`

	run := func(cfg Config) int {
		rec := httptest.NewRecorder()
		NewHandler(cfg).ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		return rec.Code
	}

	// 默认（无 resolver）→ cn：池内无 CN 号 → 503。
	if code := run(Config{GlobalEnabled: true, Pool: newGlobalOnlyPool(), Upstream: up}); code != http.StatusServiceUnavailable {
		t.Errorf("bare model default cn code=%d want 503", code)
	}
	// Default=global → 命中 global 号。
	if code := run(Config{GlobalEnabled: true, Pool: newGlobalOnlyPool(), Upstream: up,
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
	if code := run(Config{GlobalEnabled: true, Pool: p, Upstream: up, RealmResolver: auto(p)}); code != http.StatusOK {
		t.Errorf("bare model auto(global-only pool) code=%d want 200", code)
	}
	// 显式 global: 前缀在默认 cn 策略下也命中。
	prefixed := `{"model":"global:deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	NewHandler(Config{GlobalEnabled: true, Pool: newGlobalOnlyPool(), Upstream: up}).ServeHTTP(rec,
		httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(prefixed)))
	if rec.Code != http.StatusOK {
		t.Errorf("explicit global: prefix code=%d want 200", rec.Code)
	}
}

func TestRealmResolverCandidates(t *testing.T) {
	// 显式前缀恒优先：候选域严格唯一定位，绝不包含另一域
	for _, def := range []string{RealmDefaultCN, RealmDefaultGlobal, RealmDefaultAuto, RealmDefaultAutoGlobalCN} {
		r := NewRealmResolver(def, nil)
		if cands, bare := r.CandidateRealms("cn:glm-5.2"); len(cands) != 1 || cands[0] != "cn" || bare != "glm-5.2" {
			t.Errorf("def=%s cn: candidates=%v bare=%s", def, cands, bare)
		}
		if cands, bare := r.CandidateRealms("global:gpt-5.4"); len(cands) != 1 || cands[0] != "global" || bare != "gpt-5.4" {
			t.Errorf("def=%s global: candidates=%v bare=%s", def, cands, bare)
		}
	}

	// 裸名候选域按策略优先级排序
	rCN := NewRealmResolver(RealmDefaultCN, nil)
	if cands, _ := rCN.CandidateRealms("glm-5.2"); len(cands) != 1 || cands[0] != "cn" {
		t.Errorf("CN candidates=%v", cands)
	}

	rGlobal := NewRealmResolver(RealmDefaultGlobal, nil)
	if cands, _ := rGlobal.CandidateRealms("glm-5.2"); len(cands) != 1 || cands[0] != "global" {
		t.Errorf("Global candidates=%v", cands)
	}

	rAuto := NewRealmResolver(RealmDefaultAuto, nil)
	if cands, _ := rAuto.CandidateRealms("glm-5.2"); len(cands) != 2 || cands[0] != "cn" || cands[1] != "global" {
		t.Errorf("Auto candidates=%v", cands)
	}

	rAutoGlobalCN := NewRealmResolver(RealmDefaultAutoGlobalCN, nil)
	if cands, _ := rAutoGlobalCN.CandidateRealms("glm-5.2"); len(cands) != 2 || cands[0] != "global" || cands[1] != "cn" {
		t.Errorf("AutoGlobalCN candidates=%v", cands)
	}
}

func TestRealmResolverAutoGlobalCNPolicy(t *testing.T) {
	cases := []struct {
		ready func(string) bool
		want  string
	}{
		// 两域均可用 → global 优先
		{func(string) bool { return true }, "global"},
		// 仅 global 可用 → global
		{func(r string) bool { return r == "global" }, "global"},
		// 仅 cn 可用 → cn 兜底轮退
		{func(r string) bool { return r == "cn" }, "cn"},
		// 两域均不可用 → global (首选域)
		{func(string) bool { return false }, "global"},
		// 无探测闭包 → global (首选域)
		{nil, "global"},
	}
	for _, tc := range cases {
		r := NewRealmResolver(RealmDefaultAutoGlobalCN, tc.ready)
		realm, bare := r.Resolve("deepseek-v4.1-flash")
		if realm != tc.want {
			t.Errorf("AutoGlobalCN realm=%q want %q", realm, tc.want)
		}
		if bare != "deepseek-v4.1-flash" {
			t.Errorf("AutoGlobalCN bare=%q", bare)
		}
	}
}

func TestRealmResolverModelAware(t *testing.T) {
	r := NewRealmResolver(RealmDefaultAuto, func(realm string) bool { return true })
	// CN 域整体有号，但对该模型无可用号；Global 域对该模型有可用号
	r.RealmReadyForModel = func(realm, model string) bool {
		if model == "rare-model" {
			return realm == "global"
		}
		return realm == "cn"
	}
	if realm, _ := r.Resolve("rare-model"); realm != "global" {
		t.Errorf("model-aware rare-model realm=%s want global", realm)
	}
	if realm, _ := r.Resolve("common-model"); realm != "cn" {
		t.Errorf("model-aware common-model realm=%s want cn", realm)
	}
}

func TestChatAutoCrossRealmFailover(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })

	// 双域混合池：1 个 CN 账号，1 个 Global 账号
	p := testPoolWith(
		&auth.Auth{
			UID: "cn1", AccessToken: "at-cn", ExpiresAt: 9999999999,
			Domain: "workbuddy.cn", // Realm() == "cn"
		},
		&auth.Auth{
			UID: "g1", AccessToken: "at-g", ExpiresAt: 9999999999,
			Domain: "www.workbuddy.ai", // Realm() == "global"
		},
	)

	// 上游模拟：CN 账号返回 11102（模型在当前域后端不可用，安全重试），Global 账号返回 200 成功
	up := newFakeUpstream(t, func(authHeader string) (int, string, bool) {
		if strings.Contains(authHeader, "at-cn") {
			return 400, `{"code":11102,"msg":"model service info not found"}`, false
		}
		return 200, sseOK, true
	})
	up.GlobalEnabled = true

	auto := NewRealmResolver(RealmDefaultAuto, func(realm string) bool {
		return len(p.AvailableUIDsForRealm(realm)) > 0
	})
	handler := NewHandler(Config{GlobalEnabled: true, Pool: p, Upstream: up, RealmResolver: auto, MaxRotate: 3})

	// 1. 裸模型名 deepseek-v4.1-flash：CN 尝试失败后，在同一个请求内轮退到 Global 账号成功！
	bareBody := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(bareBody)))
	if rec.Code != http.StatusOK {
		t.Errorf("bare model auto failover code=%d want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// 2. 显式 cn: 前缀：CN 尝试失败后，绝不能轮转至 Global，池内无更多 CN 号必返回 503（显式前缀恒优先）
	prefixedBody := `{"model":"cn:deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`
	recPrefixed := httptest.NewRecorder()
	handler.ServeHTTP(recPrefixed, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(prefixedBody)))
	if recPrefixed.Code != http.StatusServiceUnavailable {
		t.Errorf("explicit prefix cn: code=%d want 503 (must not failover to global)", recPrefixed.Code)
	}
}

func TestChatAutoGlobalFirstFailover(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })

	// 双域混合池：1 个 CN 账号，1 个 Global 账号
	p := testPoolWith(
		&auth.Auth{
			UID: "cn1", AccessToken: "at-cn", ExpiresAt: 9999999999,
			Domain: "workbuddy.cn", // Realm() == "cn"
		},
		&auth.Auth{
			UID: "g1", AccessToken: "at-g", ExpiresAt: 9999999999,
			Domain: "www.workbuddy.ai", // Realm() == "global"
		},
	)

	// 上游模拟：Global 账号返回 11102，CN 账号返回 200 成功
	up := newFakeUpstream(t, func(authHeader string) (int, string, bool) {
		if strings.Contains(authHeader, "at-g") {
			return 400, `{"code":11102,"msg":"model service info not found"}`, false
		}
		return 200, sseOK, true
	})
	up.GlobalEnabled = true

	autoGlobalFirst := NewRealmResolver(RealmDefaultAutoGlobalCN, func(realm string) bool {
		return len(p.AvailableUIDsForRealm(realm)) > 0
	})
	handler := NewHandler(Config{GlobalEnabled: true, Pool: p, Upstream: up, RealmResolver: autoGlobalFirst, MaxRotate: 3})

	bareBody := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(bareBody)))
	if rec.Code != http.StatusOK {
		t.Errorf("auto:global,cn failover code=%d want 200", rec.Code)
	}
}
