package panel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/pool"
	"workbuddy_manager/internal/proxy"
)

// TestAccountProxyEndpoint 验证账号级代理开关接口：切换 → 落盘 → 状态透出 → 校验。
func TestAccountProxyEndpoint(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "workbuddy-u1.json")
	a := &auth.Auth{AccessToken: "at", UID: "u1", FilePath: fp}
	if err := a.SaveAtomic(); err != nil {
		t.Fatal(err)
	}
	pl := pool.New("")
	pl.Add(a)
	p := New(Config{Version: "test", APIKey: "k", Pool: pl})

	do := func(uid, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/panel/api/accounts/"+uid+"/proxy", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer k")
		p.ServeHTTP(rec, req)
		return rec
	}

	// 关闭。
	if rec := do("u1", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable code=%d body=%s", rec.Code, rec.Body.String())
	}
	if a.UseProxy() {
		t.Error("account proxy should be disabled")
	}
	raw, _ := os.ReadFile(fp)
	if !strings.Contains(string(raw), `"use_proxy": false`) {
		t.Errorf("use_proxy not persisted: %s", raw)
	}
	if st, _ := pl.Status("u1"); st.ProxyEnabled {
		t.Error("status proxy_enabled should be false")
	}

	// 再开启。
	if rec := do("u1", `{"enabled":true}`); rec.Code != http.StatusOK || !a.UseProxy() {
		t.Fatalf("re-enable failed: code=%d use=%v", rec.Code, a.UseProxy())
	}
	if st, _ := pl.Status("u1"); !st.ProxyEnabled {
		t.Error("status proxy_enabled should be true")
	}

	// 缺 enabled → 400。
	if rec := do("u1", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing enabled should be 400, got %d", rec.Code)
	}
	// 未知账号 → 404。
	if rec := do("nope", `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown uid should be 404, got %d", rec.Code)
	}
}

// TestPanelAuthDirAndProxyHotSwap 面板的凭证目录与出站代理热替换：
// 保存配置后登录/导入写新目录、登录链路走新代理，且旧值不残留。
func TestPanelAuthDirAndProxyHotSwap(t *testing.T) {
	p := New(Config{AuthDir: "/old/dir"})
	if got := p.authDirPath(); got != "/old/dir" {
		t.Fatalf("初始 auth dir=%q", got)
	}
	p.SetAuthDir("/new/dir")
	if got := p.authDirPath(); got != "/new/dir" {
		t.Fatalf("热改后 auth dir=%q", got)
	}
	if p.proxyClient() != nil {
		t.Fatal("初始不应有代理")
	}
	pc, err := proxy.New(proxy.Config{ProxyURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	p.SetProxy(pc)
	if p.proxyClient() != pc {
		t.Fatal("热改后应返回新代理实例")
	}
	p.SetProxy(nil)
	if p.proxyClient() != nil {
		t.Fatal("SetProxy(nil) 后应无代理（不得回落到装配期 cfg.Proxy）")
	}

	// 回归：装配期配置了代理时，SetProxy(nil) 必须真的取消（不能回落旧代理）。
	pc2, err := proxy.New(proxy.Config{ProxyURL: "http://127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	p2 := New(Config{Proxy: pc2})
	if p2.proxyClient() != pc2 {
		t.Fatal("装配期代理应生效")
	}
	p2.SetProxy(nil)
	if p2.proxyClient() != nil {
		t.Fatal("SetProxy(nil) 后旧代理复活（回落到了 cfg.Proxy）")
	}
}
