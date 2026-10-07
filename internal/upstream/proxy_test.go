package upstream

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/proxy"
)

// TestResinAccountHeaderOnCommonHeaders 验证接入 Resin 后，账号相关请求带内部
// 账号标记，且 Transport 把它改写为 Resin 反向代理 URL + X-Resin-Account。
func TestResinAccountHeaderOnCommonHeaders(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResp(200, `{"code":0,"data":{}}`), nil
	})
	rc, err := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "Default"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetProxy(rc)

	req, _ := http.NewRequest(http.MethodGet, "https://chat.example/console/x", nil)
	c.CommonHeaders(req, &auth.Auth{UID: "u1"})
	if _, err := c.HTTP.Do(req); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("base not called")
	}
	want := "http://127.0.0.1:2260/tok/Default/https/chat.example/console/x"
	if got.URL.String() != want {
		t.Errorf("url=%s want %s", got.URL.String(), want)
	}
	if got.Header.Get("X-Resin-Account") != "u1" {
		t.Errorf("X-Resin-Account=%q want u1", got.Header.Get("X-Resin-Account"))
	}
	if got.Header.Get(proxy.InternalAccountHeader) != "" {
		t.Error("internal header must be stripped before egress")
	}
}

// TestResinChatHeadersTagged 验证 chat 出站同样被 Resin 路由。
func TestResinChatHeadersTagged(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	c.ChatHTTP = c.HTTP
	rc, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	c.SetProxy(rc)

	req, _ := http.NewRequest(http.MethodPost, "https://chat.example/v2/chat/completions", nil)
	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u2"}, "", ChatMeta{})
	if _, err := c.ChatHTTP.Do(req); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("X-Resin-Account") != "u2" {
		t.Errorf("X-Resin-Account=%q want u2", got.Header.Get("X-Resin-Account"))
	}
}

// TestNoResinLeavesRequestsUnchanged 验证未接入 Resin 时请求逐字透传（零回归）。
func TestNoResinLeavesRequestsUnchanged(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResp(200, `{"code":0,"data":{}}`), nil
	})
	req, _ := http.NewRequest(http.MethodGet, "https://chat.example/x", nil)
	c.CommonHeaders(req, &auth.Auth{UID: "u1"})
	if _, err := c.HTTP.Do(req); err != nil {
		t.Fatal(err)
	}
	if got.URL.String() != "https://chat.example/x" {
		t.Errorf("url changed without resin: %s", got.URL.String())
	}
	if got.Header.Get(proxy.InternalAccountHeader) != "" || got.Header.Get("X-Resin-Account") != "" {
		t.Error("resin headers must not appear without resin")
	}
}

// TestResinEmptyUIDNoTag 验证 uid 为空（未登录/匿名）时不打标记、不改写。
func TestResinEmptyUIDNoTag(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResp(200, `{"code":0,"data":{}}`), nil
	})
	rc, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	c.SetProxy(rc)
	req, _ := http.NewRequest(http.MethodGet, "https://chat.example/x", nil)
	c.CommonHeaders(req, &auth.Auth{UID: ""})
	if _, err := c.HTTP.Do(req); err != nil {
		t.Fatal(err)
	}
	if got.URL.String() != "https://chat.example/x" {
		t.Errorf("empty uid must not be rewritten: %s", got.URL.String())
	}
}

// TestProxyAccountToggleDisablesEgress 验证账号级代理开关：显式关闭后该账号请求
// 不打内部标记、不经代理改写（直连），其他默认账号仍走代理。
func TestProxyAccountToggleDisablesEgress(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResp(200, `{"code":0,"data":{}}`), nil
	})
	rc, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	c.SetProxy(rc)

	// 默认（未设置）：走代理（use_proxy 缺省 true）。
	def := &auth.Auth{UID: "u1"}
	if !def.UseProxy() {
		t.Fatal("default UseProxy must be true")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://chat.example/x", nil)
	c.CommonHeaders(req, def)
	if _, err := c.HTTP.Do(req); err != nil {
		t.Fatal(err)
	}
	if got.URL.String() != "http://127.0.0.1:2260/tok/P/https/chat.example/x" {
		t.Errorf("default account should be proxied, got %s", got.URL.String())
	}

	// 显式关闭：直连，不改写，无 X-Resin-Account / 内部头。
	off := &auth.Auth{UID: "u2"}
	off.SetUseProxy(false)
	req2, _ := http.NewRequest(http.MethodGet, "https://chat.example/y", nil)
	c.CommonHeaders(req2, off)
	if _, err := c.HTTP.Do(req2); err != nil {
		t.Fatal(err)
	}
	if got.URL.String() != "https://chat.example/y" {
		t.Errorf("disabled account must pass through, got %s", got.URL.String())
	}
	if got.Header.Get("X-Resin-Account") != "" || got.Header.Get(proxy.InternalAccountHeader) != "" {
		t.Error("disabled account must not carry proxy headers")
	}
}

// TestRefreshTokenHonorsProxyToggle 回归：RefreshToken 用显式字段快照构造请求头，
// 该快照必须携带账号的 use_proxy 标志——否则关闭代理的账号在 token 刷新时仍走代理。
func TestRefreshTokenHonorsProxyToggle(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResp(200, `{"code":0,"data":{"accessToken":"newat","refreshToken":"newrt","expiresIn":3600}}`), nil
	})
	rc, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	c.SetProxy(rc)

	a := &auth.Auth{AccessToken: "at", RefreshToken: "rt", UID: "u1", ExpiresAt: 1}
	a.SetUseProxy(false) // 该账号关闭代理
	if err := c.RefreshToken(a); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got == nil {
		t.Fatal("upstream not called")
	}
	if got.URL.Host != "chat.example" {
		t.Errorf("disabled account refresh was proxied: %s", got.URL.String())
	}
	if got.Header.Get("X-Resin-Account") != "" {
		t.Error("disabled account refresh must not carry proxy header")
	}
}
