package upstream

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/proxy"
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

// TestSetProxyHotSwap 出站代理热替换：SetProxy 换实例后请求立即按新实例路由；
// SetProxy(nil) 恢复直连（下一次请求不再改写 URL）。
func TestSetProxyHotSwap(t *testing.T) {
	var got *http.Request
	c := testClient(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	call := func() string {
		t.Helper()
		got = nil
		req, _ := http.NewRequest(http.MethodPost, "https://chat.example/v2/chat/completions", nil)
		c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u9"}, "", ChatMeta{})
		if _, err := c.HTTP.Do(req); err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("base not called")
		}
		return got.URL.String()
	}

	a, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/a", Platform: "A"})
	c.SetProxy(a)
	if u := call(); u != "http://127.0.0.1:2260/a/A/https/chat.example/v2/chat/completions" {
		t.Fatalf("代理 A 路由=%s", u)
	}

	b, _ := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/b", Platform: "B"})
	c.SetProxy(b)
	if u := call(); u != "http://127.0.0.1:2260/b/B/https/chat.example/v2/chat/completions" {
		t.Fatalf("代理 B 路由=%s", u)
	}
	if c.Proxy() != b {
		t.Fatal("Proxy() 应返回最新实例")
	}

	c.SetProxy(nil)
	if c.Proxy() != nil {
		t.Fatal("SetProxy(nil) 后 Proxy() 应为 nil")
	}
	if u := call(); u != "https://chat.example/v2/chat/completions" {
		t.Fatalf("取消代理后应直连，得到 %s", u)
	}
	// tagProxy 在未接入时必须不打内部头（零回归）。
	req, _ := http.NewRequest(http.MethodPost, "https://chat.example/v2/chat/completions", nil)
	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u9"}, "", ChatMeta{})
	if req.Header.Get(proxy.InternalAccountHeader) != "" {
		t.Fatal("未接入代理时不应打内部标记头")
	}
}

// TestSetHeaderTimeoutHotSwap 首字节超时热改：重建 Transport/客户端并原子换指针；
// 同值重复调用不重建；代理实例在重建前后都必须保持有效。
func TestSetHeaderTimeoutHotSwap(t *testing.T) {
	c := New()
	oldBase := c.baseTransport
	if c.shortClientBase() != c.HTTP || c.chatClientBase() != c.ChatHTTP {
		t.Fatal("未热改前访问器应返回装配期客户端")
	}

	c.SetHeaderTimeout(17 * time.Second)
	if c.baseTransport == oldBase {
		t.Fatal("值变化应重建底层 Transport（连接池重置）")
	}
	if got := c.baseTransport.ResponseHeaderTimeout; got != 17*time.Second {
		t.Fatalf("新 Transport ResponseHeaderTimeout=%v", got)
	}
	if c.HeaderTimeout != 17*time.Second {
		t.Fatalf("HeaderTimeout=%v", c.HeaderTimeout)
	}
	short, chat := c.shortClientBase(), c.chatClientBase()
	if short == c.HTTP || chat == c.ChatHTTP {
		t.Fatal("访问器应切到热改后的新客户端")
	}
	if short.Transport != chat.Transport {
		t.Fatal("HTTP 与 ChatHTTP 必须共享同一个转发层/连接池")
	}

	// 同值重复调用（配置保存每次都会调）：不重建连接池。
	base2 := c.baseTransport
	c.SetHeaderTimeout(17 * time.Second)
	if c.baseTransport != base2 {
		t.Fatal("同值重复调用不应重建连接池")
	}

	// 代理在 header 重建后保持有效（继承当前实例）。
	pc, err := proxy.New(proxy.Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetProxy(pc)
	c.SetHeaderTimeout(18 * time.Second)
	if c.Proxy() != pc {
		t.Fatal("header 重建后应继承当前代理实例")
	}

	// 反向顺序：先换 header 再 SetProxy，代理必须落在**新**转发层上。
	c2 := New()
	c2.SetHeaderTimeout(19 * time.Second)
	c2.SetProxy(pc)
	if c2.Proxy() != pc {
		t.Fatal("header 重建后 SetProxy 应作用于最新转发层")
	}
}
