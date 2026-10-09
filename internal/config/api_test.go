// api_test.go 配置域 HTTP 接口的形状守护：路由 pattern、响应信封、
// 能力缺失（nil 闭包）时的 501、以及保存失败不返回 200。
//
// 这些端点是面板「配置」页的全部数据通路，也是配置域从 internal/panel 迁出后
// 唯一对外契约（panel 只负责挂载与鉴权），因此单列测试固定住。
package config

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testAPI 构建一个所有依赖都返回固定值的 API。
func testAPI() *API {
	return NewAPI(APIConfig{
		ConfigPath: "/tmp/config.json",
		LoadConfig: func() (any, error) { return map[string]any{"listen": ":7863"}, nil },
		SaveConfig: func(raw []byte) ([]string, error) {
			if strings.Contains(string(raw), "boom") {
				return nil, errors.New("invalid config: boom")
			}
			return []string{"listen", "upstash"}, nil
		},
		PreviewPrompt: func(raw []byte) (any, error) {
			return map[string]any{"note": "preview", "raw": string(raw)}, nil
		},
		VersionInfo: func() any { return map[string]any{"latest": "1.2.3"} },
	})
}

// do 发一个请求并返回记录器。
func do(t *testing.T, a *API, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, r)
	return rec
}

// TestAPIPathsAllServed Paths() 列出的每个 pattern 都必须真的被本 handler 服务：
// 面板按 Paths() 注册路由，一旦 pattern 写错（或漏注册）就会在运行期 404，
// 而这种错不会在编译期暴露。
func TestAPIPathsAllServed(t *testing.T) {
	a := testAPI()
	paths := a.Paths()
	if len(paths) == 0 {
		t.Fatal("Paths() 为空：面板没有任何配置接口可挂载")
	}
	for _, pat := range paths {
		method, path, ok := strings.Cut(pat, " ")
		if !ok {
			t.Fatalf("pattern %q 不是 \"METHOD /path\" 形式", pat)
		}
		rec := do(t, a, method, path, "{}")
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s: handler 未注册该 pattern（面板注册后运行期会 404）", pat)
		}
	}
}

// TestAPIGetShape GET /panel/api/config 回显路径、配置对象与版本信息。
func TestAPIGetShape(t *testing.T) {
	rec := do(t, testAPI(), http.MethodGet, "/panel/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"ok":true`, `"/tmp/config.json"`, `":7863"`, `"1.2.3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("响应缺 %s：%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q want application/json", ct)
	}
}

// TestAPIGetUnavailable LoadConfig 缺失时返回 501 而不是 panic/500。
func TestAPIGetUnavailable(t *testing.T) {
	a := NewAPI(APIConfig{ConfigPath: "/tmp/config.json"})
	rec := do(t, a, http.MethodGet, "/panel/api/config", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d want 501", rec.Code)
	}
}

// TestAPIGetVersionInfoNil VersionInfo 未注入时不 panic，且字段存在（前端按 schema 取用）。
func TestAPIGetVersionInfoNil(t *testing.T) {
	a := NewAPI(APIConfig{LoadConfig: func() (any, error) { return map[string]any{}, nil }})
	rec := do(t, a, http.MethodGet, "/panel/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"version_info":null`) {
		t.Errorf("version_info 字段缺失或形状变化：%s", rec.Body.String())
	}
}

// TestAPISaveShape 保存成功回 restart_required；闭包返回 nil 时归一为 []（前端按数组取 length）。
func TestAPISaveShape(t *testing.T) {
	rec := do(t, testAPI(), http.MethodPost, "/panel/api/config", `{"listen":":1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"restart_required":["listen","upstash"]`) {
		t.Errorf("restart_required 形状变化：%s", body)
	}

	// nil → []（不能是 null：前端 r.restart_required.length 会崩）。
	a := NewAPI(APIConfig{SaveConfig: func([]byte) ([]string, error) { return nil, nil }})
	rec = do(t, a, http.MethodPost, "/panel/api/config", `{}`)
	if !strings.Contains(rec.Body.String(), `"restart_required":[]`) {
		t.Errorf("nil restart_required 必须归一为 []：%s", rec.Body.String())
	}
}

// TestAPISaveError 校验失败 → 400 且错误文案透出（前端 toast 直接展示）。
func TestAPISaveError(t *testing.T) {
	rec := do(t, testAPI(), http.MethodPost, "/panel/api/config", `{"boom":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid config: boom") {
		t.Errorf("错误文案未透出：%s", rec.Body.String())
	}
}

// TestAPIPreviewShape 预览接口回传闭包结果（面板据此渲染各域正文）。
func TestAPIPreviewShape(t *testing.T) {
	rec := do(t, testAPI(), http.MethodPost, "/panel/api/prompt/preview", `{"prompt":{"mode":"none"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"note":"preview"`) {
		t.Errorf("预览结果未透出：%s", rec.Body.String())
	}
}

// TestAPIUnavailableWhenNil 三个接口在依赖缺失时都必须回 501（而非 panic）。
func TestAPIUnavailableWhenNil(t *testing.T) {
	a := NewAPI(APIConfig{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/panel/api/config"},
		{http.MethodPost, "/panel/api/config"},
		{http.MethodPost, "/panel/api/prompt/preview"},
	} {
		rec := do(t, a, tc.method, tc.path, "{}")
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status=%d want 501", tc.method, tc.path, rec.Code)
		}
	}
}
