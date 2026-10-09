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
