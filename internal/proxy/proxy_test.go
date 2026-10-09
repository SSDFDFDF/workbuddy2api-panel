package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty url disabled", Config{}, false},
		{"ok reverse", Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default"}, false},
		{"ok forward legacy", Config{URL: "https://resin.example/tok", Platform: "P1", Mode: "forward", AuthVersion: "legacy_v0"}, false},
		{"bad scheme", Config{URL: "socks5://127.0.0.1:1/tok", Platform: "P"}, true},
		{"no host", Config{URL: "http:///tok", Platform: "P"}, true},
		{"no token", Config{URL: "http://127.0.0.1:2260", Platform: "P"}, true},
		{"multi token", Config{URL: "http://127.0.0.1:2260/a/b", Platform: "P"}, true},
		{"empty platform", Config{URL: "http://127.0.0.1:2260/tok"}, true},
		{"platform dot", Config{URL: "http://127.0.0.1:2260/tok", Platform: "a.b"}, true},
		{"platform colon", Config{URL: "http://127.0.0.1:2260/tok", Platform: "a:b"}, true},
		{"bad mode", Config{URL: "http://127.0.0.1:2260/tok", Platform: "P", Mode: "tunnel"}, true},
		{"bad auth", Config{URL: "http://127.0.0.1:2260/tok", Platform: "P", AuthVersion: "V2"}, true},
		{"userinfo", Config{URL: "http://u:p@127.0.0.1:2260/tok", Platform: "P"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got client=%+v", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.cfg.URL == "" {
				if c != nil {
					t.Fatalf("empty url should yield nil client, got %+v", c)
				}
				return
			}
			if c == nil || !c.Enabled() {
				t.Fatal("client should be enabled")
			}
		})
	}
}

func TestReverseURL(t *testing.T) {
	c, err := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.example.com/healthz", "http://127.0.0.1:2260/my-token/Default/https/api.example.com/healthz"},
		{"https://api.example.com/healthz?a=1&b=2", "http://127.0.0.1:2260/my-token/Default/https/api.example.com/healthz?a=1&b=2"},
		{"http://10.0.0.1:8080/v2/chat", "http://127.0.0.1:2260/my-token/Default/http/10.0.0.1:8080/v2/chat"},
		{"wss://ws.example.com/chat", "http://127.0.0.1:2260/my-token/Default/https/ws.example.com/chat"},
		{"ws://ws.example.com/chat", "http://127.0.0.1:2260/my-token/Default/http/ws.example.com/chat"},
		{"https://a.example.com", "http://127.0.0.1:2260/my-token/Default/https/a.example.com/"},
		// 已转义的路径不被二次转义。
		{"https://a.example.com/a%20b/c?q=x%26y", "http://127.0.0.1:2260/my-token/Default/https/a.example.com/a%20b/c?q=x%26y"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.reverseURL(u)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got.String() != tc.want {
			t.Errorf("%s -> %s, want %s", tc.in, got.String(), tc.want)
		}
	}
}

func TestTokenAndPlatformEscaping(t *testing.T) {
	// Token 含特殊字符：基址重建时按路径规则转义，不破坏 URL。
	c, err := New(Config{URL: "http://127.0.0.1:2260/my%20token", Platform: "P"})
	if err != nil {
		t.Fatal(err)
	}
	if c.base != "http://127.0.0.1:2260/my%20token" {
		t.Errorf("base = %q", c.base)
	}
	if c.token != "my token" {
		t.Errorf("token = %q", c.token)
	}

	// Platform 含空格（不在禁止字符集内）：反代路径需转义为单段。
	c2, _ := New(Config{URL: "http://h/tok", Platform: "a b"})
	u, _ := url.Parse("https://api.example.com/x")
	rw, err := c2.reverseURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if rw.String() != "http://h/tok/a%20b/https/api.example.com/x" {
		t.Errorf("reverse url = %s", rw.String())
	}
}

// captureRT 记录最后一次出站请求，并返回一个空 200。
type captureRT struct{ req *http.Request }

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	c.req = req
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestReverseTransportRewrites(t *testing.T) {
	c, err := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default"})
	if err != nil {
		t.Fatal(err)
	}
	cap := &captureRT{}
	tr := c.Wrap(cap)
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/v2/chat?q=1", nil)
	req.Host = "api.example.com" // 模拟被显式设置的 Host，应被清空
	SetAccountHeader(req, "Tom")
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if cap.req == nil {
		t.Fatal("base not called")
	}
	if got := cap.req.URL.String(); got != "http://127.0.0.1:2260/my-token/Default/https/api.example.com/v2/chat?q=1" {
		t.Errorf("rewritten url = %s", got)
	}
	if cap.req.Host != "" {
		t.Errorf("Host should be cleared, got %q", cap.req.Host)
	}
	if got := cap.req.Header.Get("X-Resin-Account"); got != "Tom" {
		t.Errorf("X-Resin-Account = %q want Tom", got)
	}
	if got := cap.req.Header.Get(InternalAccountHeader); got != "" {
		t.Errorf("internal header leaked: %q", got)
	}
}

func TestTransportPassthroughWithoutAccount(t *testing.T) {
	c, _ := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default"})
	cap := &captureRT{}
	tr := c.Wrap(cap)
	req, _ := http.NewRequest(http.MethodGet, "https://third.party/api", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if got := cap.req.URL.String(); got != "https://third.party/api" {
		t.Errorf("untagged request must pass through, got %s", got)
	}
	if cap.req.Header.Get("X-Resin-Account") != "" {
		t.Error("untagged request must not carry X-Resin-Account")
	}
}

func TestForwardProxyUser(t *testing.T) {
	v1, _ := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default", Mode: "forward"})
	u, p := v1.forwardProxyUser("team:user.name")
	if u != "Default.team:user.name" || p != "my-token" {
		t.Errorf("v1 user/pass = %q/%q", u, p)
	}
	want := base64.StdEncoding.EncodeToString([]byte("Default.team:user.name:my-token"))
	if got := base64.StdEncoding.EncodeToString([]byte(u + ":" + p)); got != want {
		t.Errorf("v1 full credential mismatch: %s", got)
	}

	v0, _ := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default", Mode: "forward", AuthVersion: "LEGACY_V0"})
	u0, p0 := v0.forwardProxyUser("Tom")
	if u0 != "my-token" || p0 != "Default:Tom" {
		t.Errorf("legacy user/pass = %q/%q", u0, p0)
	}
}

func TestForwardTransportSetsProxyAuthorization(t *testing.T) {
	// 代理服务器直接回 200，并记录它看到的 Proxy-Authorization 与目标 URL。
	gotAuth := make(chan string, 1)
	gotPath := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Proxy-Authorization")
		gotPath <- r.URL.String()
		w.WriteHeader(200)
	}))
	defer proxy.Close()

	c, err := New(Config{URL: proxy.URL + "/my-token", Platform: "Default", Mode: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Transport{}
	tr := c.Wrap(base)
	req, _ := http.NewRequest(http.MethodGet, "http://target.example/path", nil)
	SetAccountHeader(req, "Tom")
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("Default.Tom:my-token"))
	if auth := <-gotAuth; auth != want {
		t.Errorf("Proxy-Authorization = %q want %q", auth, want)
	}
	if p := <-gotPath; !strings.HasPrefix(p, "http://target.example/path") {
		t.Errorf("proxy saw target %q", p)
	}
}

func TestReverseTransportCloseIdle(t *testing.T) {
	base := &http.Transport{}
	c, _ := New(Config{URL: "http://127.0.0.1:2260/my-token", Platform: "Default"})
	tr := c.Wrap(base).(*Transport)
	tr.CloseIdleConnections() // 不应 panic
}

func TestInheritLease(t *testing.T) {
	type body struct {
		Parent string `json:"parent_account"`
		New    string `json:"new_account"`
	}
	gotPath := make(chan string, 1)
	gotBody := make(chan body, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		var b body
		_ = json.Unmarshal(raw, &b)
		gotBody <- b
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL + "/my-token", Platform: "Default"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.InheritLease(context.Background(), "login-abc", "uid-123"); err != nil {
		t.Fatal(err)
	}
	if p := <-gotPath; p != "/my-token/api/v1/Default/actions/inherit-lease" {
		t.Errorf("path = %q", p)
	}
	if b := <-gotBody; b.Parent != "login-abc" || b.New != "uid-123" {
		t.Errorf("body = %+v", b)
	}
}

func TestInheritLeaseDisabledNoop(t *testing.T) {
	c, _ := New(Config{})
	if err := c.InheritLease(context.Background(), "a", "b"); err != nil {
		t.Fatalf("nil client should no-op: %v", err)
	}
}

func TestSetAccountHeaderMethodGuard(t *testing.T) {
	// nil 客户端：不写头。
	var nilClient *Client
	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	nilClient.SetAccountHeader(req, "u1")
	if req.Header.Get(InternalAccountHeader) != "" {
		t.Error("nil client must not set internal header")
	}
	// 已启用客户端：写头。
	c, _ := New(Config{URL: "http://127.0.0.1:2260/tok", Platform: "P"})
	c.SetAccountHeader(req, "u1")
	if req.Header.Get(InternalAccountHeader) != "u1" {
		t.Error("enabled client must set internal header")
	}
}

func TestPlainProxyValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"ok http", Config{ProxyURL: "http://127.0.0.1:8080"}, false},
		{"ok https userinfo", Config{ProxyURL: "https://u:p@proxy.example:8443"}, false},
		{"ok socks5", Config{ProxyURL: "socks5://127.0.0.1:1080"}, false},
		{"ok socks5h", Config{ProxyURL: "socks5h://127.0.0.1:1080"}, false},
		{"bad scheme", Config{ProxyURL: "ftp://127.0.0.1:21"}, true},
		{"no host", Config{ProxyURL: "http://"}, true},
		{"mutually exclusive", Config{ProxyURL: "http://127.0.0.1:8080", URL: "http://127.0.0.1:2260/tok", Platform: "P"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c == nil || !c.Enabled() || c.Mode() != ModePlain || !c.IsPlain() {
				t.Fatalf("plain client wrong: %+v", c)
			}
			if c.Platform() != "" || c.AuthVersion() != "" {
				t.Errorf("plain proxy must not report platform/auth: %q/%q", c.Platform(), c.AuthVersion())
			}
		})
	}
}

// TestPlainProxyRoutesTaggedRequests 验证普通正向代理：打标记的请求经代理转发
// （HTTP 绝对形式 URL 到达代理），未打标记的请求直连（Proxy 返回 nil）。
func TestPlainProxyRoutesTaggedRequests(t *testing.T) {
	got := make(chan *http.Request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{ProxyURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Transport{}
	tr := c.Wrap(base)

	// 未打标记：Proxy 函数返回 nil（直连）。
	plain, _ := http.NewRequest(http.MethodGet, "http://target.invalid/x", nil)
	if p, _ := base.Proxy(plain); p != nil {
		t.Errorf("untagged request must not use proxy, got %v", p)
	}

	// 打标记：经代理转发，目标 URL 以绝对形式到达代理。
	req, _ := http.NewRequest(http.MethodGet, "http://target.invalid/x", nil)
	SetAccountHeader(req, "u1")
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	r := <-got
	if r.URL.String() != "http://target.invalid/x" {
		t.Errorf("proxy saw %q want http://target.invalid/x", r.URL.String())
	}
	if r.Header.Get(InternalAccountHeader) != "" {
		t.Error("internal header must be stripped before egress")
	}
}

// TestPlainProxyUserinfoForwarded 验证普通代理的 userinfo 凭证经 net/http 自动
// 转成 Proxy-Authorization。
func TestPlainProxyUserinfoForwarded(t *testing.T) {
	gotAuth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c, err := New(Config{ProxyURL: "http://alice:s3cret@" + host})
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Transport{}
	tr := c.Wrap(base)
	req, _ := http.NewRequest(http.MethodGet, "http://target.invalid/x", nil)
	SetAccountHeader(req, "u1")
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if a := <-gotAuth; a != want {
		t.Errorf("Proxy-Authorization = %q want %q", a, want)
	}
}

func TestPlainProxyInheritLeaseNoop(t *testing.T) {
	c, _ := New(Config{ProxyURL: "http://127.0.0.1:8080"})
	if err := c.InheritLease(context.Background(), "a", "b"); err != nil {
		t.Fatalf("plain proxy inherit-lease must no-op: %v", err)
	}
}

// TestDynamicHotSwap 动态转发层：换实例后下一次请求按新代理路由；Set(nil) 恢复直连。
func TestDynamicHotSwap(t *testing.T) {
	cap := &captureRT{}
	d := NewDynamic(cap)

	route := func(account string) string {
		t.Helper()
		cap.req = nil
		req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/x", nil)
		if account != "" {
			SetAccountHeader(req, account)
		}
		if _, err := d.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
		if cap.req == nil {
			t.Fatal("base not called")
		}
		return cap.req.URL.String()
	}

	// 未接入：直连（未标记请求不改写）。
	if got := route(""); got != "https://api.example.com/x" {
		t.Fatalf("直连 URL=%s", got)
	}

	a, err := New(Config{URL: "http://127.0.0.1:2260/tokA", Platform: "PA"})
	if err != nil {
		t.Fatal(err)
	}
	d.Set(a)
	if got := route("u1"); got != "http://127.0.0.1:2260/tokA/PA/https/api.example.com/x" {
		t.Fatalf("代理 A URL=%s", got)
	}

	b, err := New(Config{URL: "http://127.0.0.1:2260/tokB", Platform: "PB"})
	if err != nil {
		t.Fatal(err)
	}
	d.Set(b)
	if got := route("u2"); got != "http://127.0.0.1:2260/tokB/PB/https/api.example.com/x" {
		t.Fatalf("代理 B URL=%s", got)
	}
	if d.Current() != b {
		t.Fatal("Current 应返回最新实例")
	}

	d.Set(nil)
	if d.Current() != nil {
		t.Fatal("Set(nil) 后 Current 应为 nil")
	}
	if got := route("u3"); got != "https://api.example.com/x" {
		t.Fatalf("取消代理后应直连，URL=%s", got)
	}
}

// TestDynamicForwardDispatch 正向/普通代理模式：底层 *http.Transport 上安装的
// 选路分发函数必须读当前实例（nil = 直连，换实例即换代理地址）。
func TestDynamicForwardDispatch(t *testing.T) {
	base := &http.Transport{}
	d := NewDynamic(base)
	if base.Proxy == nil {
		t.Fatal("NewDynamic 应在 *http.Transport 上安装 Proxy 分发函数")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/x", nil)
	req = req.WithContext(withAccount(req.Context(), "u1"))

	if u, err := base.Proxy(req); err != nil || u != nil {
		t.Fatalf("未接入应直连：u=%v err=%v", u, err)
	}
	pc, err := New(Config{ProxyURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	d.Set(pc)
	u, err := base.Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("普通代理选路失败：u=%v err=%v", u, err)
	}
	d.Set(nil)
	if u, err := base.Proxy(req); err != nil || u != nil {
		t.Fatalf("取消代理后应直连：u=%v err=%v", u, err)
	}
}

// TestDynamicStripsAccountHeaderWhenDirect 热改取消代理后：即便请求还带着旧标记
// （与 SetProxy(nil) 擦肩的在途请求），内部头也绝不能泄漏到直连上游。
func TestDynamicStripsAccountHeaderWhenDirect(t *testing.T) {
	cap := &captureRT{}
	d := NewDynamic(cap)
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/x", nil)
	SetAccountHeader(req, "u1") // 模拟已标记的在途请求
	if _, err := d.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if cap.req == nil {
		t.Fatal("base not called")
	}
	if got := cap.req.Header.Get(InternalAccountHeader); got != "" {
		t.Fatalf("内部标记头泄漏到直连上游：%q", got)
	}
}
