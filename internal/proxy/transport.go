package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// accountCtxKey 正向代理路径下，把账号标识从 Transport 传递到 Proxy 函数的 context 键。
type accountCtxKey struct{}

func withAccount(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, accountCtxKey{}, account)
}

// AccountFromContext 取回本请求的账号标识（无则空串）。仅供正向代理 Proxy 函数使用。
func AccountFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(accountCtxKey{}).(string)
	return v
}

// Transport 包裹底层 RoundTripper，在出站前按内部账号标记执行代理路由：
//
//   - reverse：把目标 URL 改写为 Resin 反向代理路径，并设置 X-Resin-Account；
//   - forward：把账号写进 context，交给底层 *http.Transport 的 Proxy 函数生成
//     Proxy-Authorization；
//   - plain：同 forward，但 Proxy 函数返回静态普通代理地址（凭证来自 URL userinfo）。
//
// 无内部标记头时原样透传（未接代理的请求零改动）。CloseIdleConnections 透传到底层，
// 保持既有「失败清连接池」行为不变。
type Transport struct {
	base   http.RoundTripper
	client *Client
}

// Wrap 用代理路由包裹底层 RoundTripper。client 为 nil 或未启用时原样返回 base。
// forward / plain 模式下会顺带在 *http.Transport 上安装 Proxy 函数。
func (c *Client) Wrap(base http.RoundTripper) http.RoundTripper {
	if c == nil || base == nil {
		return base
	}
	if c.mode == ModeForward || c.mode == ModePlain {
		if bt, ok := base.(*http.Transport); ok {
			bt.Proxy = c.proxyFunc
		}
	}
	return &Transport{base: base, client: c}
}

// proxyFunc 正向代理 Proxy 函数（挂在 *http.Transport.Proxy）。
// 无账号 context（未标记的请求）返回 (nil, nil) 直连；有账号则返回携带凭证的代理 URL，
// 由 net/http 自动生成 Proxy-Authorization: Basic base64(username:password)。
func (c *Client) proxyFunc(req *http.Request) (*url.URL, error) {
	if req == nil {
		return nil, nil
	}
	account := strings.TrimSpace(AccountFromContext(req.Context()))
	if account == "" {
		return nil, nil
	}
	// 普通正向代理：静态地址，凭证来自 URL userinfo（可能为空 = 匿名代理）。
	if c.mode == ModePlain {
		if c.plain == nil {
			return nil, nil
		}
		u := *c.plain // 复制，避免上游库改写共享实例
		return &u, nil
	}
	// Resin 正向代理：凭证由 Platform.Account + token 派生。
	u, err := url.Parse(c.base)
	if err != nil {
		return nil, err
	}
	username, password := c.forwardProxyUser(account)
	u.User = url.UserPassword(username, password)
	return u, nil
}

// RoundTrip 实现 http.RoundTripper。
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return nil, errors.New("proxy: nil transport")
	}
	return roundTripAs(t.client, t.base, req)
}

// roundTripAs 用给定代理实例转发一次请求（Transport 与 Dynamic 共用）。
//
// 安全细节：内部账号标记头（X-Wb2a-Proxy-Account）**任何情况下都不得出站**。
// 代理实例为 nil（未接入/已热改取消）时，若请求还带着旧标记（例如与
// SetProxy(nil) 擦肩的在途请求），这里也要把它剥掉再直连。
func roundTripAs(c *Client, base http.RoundTripper, req *http.Request) (*http.Response, error) {
	if base == nil {
		return nil, errors.New("proxy: nil transport")
	}
	if req == nil {
		return base.RoundTrip(req)
	}
	if c == nil {
		if req.Header.Get(InternalAccountHeader) == "" {
			return base.RoundTrip(req)
		}
		clone := req.Clone(req.Context())
		clone.Header.Del(InternalAccountHeader)
		return base.RoundTrip(clone)
	}
	account := strings.TrimSpace(req.Header.Get(InternalAccountHeader))
	if account == "" {
		// 未标记请求（如公开第三方 API）：直连，零改动。
		return base.RoundTrip(req)
	}
	// 删除内部标记头，避免泄漏给代理或目标服务。
	clone := req.Clone(withAccount(req.Context(), account))
	clone.Header.Del(InternalAccountHeader)

	if c.mode == ModeReverse {
		u, err := c.reverseURL(req.URL)
		if err != nil {
			return nil, err
		}
		clone.URL = u
		// 清空 Host，让 transport 用改写后的 URL.Host（代理基址）作为 Host 头；
		// 目标 host 已在路径段中。
		clone.Host = ""
		clone.Header.Set("X-Resin-Account", account)
	}
	return base.RoundTrip(clone)
}

// Dynamic 可热替换的出站代理转发层。
//
// 背景：proxy_url / resin_* 原先在装配期把代理 Transport 包到底层 Transport 上，
// 改配置必须重启。这里包一层**永不更换**的转发层，内部用原子指针指向当前代理
// 实例：配置保存时只换指针，请求热路径 Load 一次拿一致视图。
//
// 两种模式的接线：
//   - 反向代理（Resin reverse）：在 RoundTrip 内按当前实例改写 URL；
//   - 正向/普通代理：构造时在底层 *http.Transport 上安装一个稳定的 Proxy 分发
//     函数，每次选路时读当前实例（nil = 直连）。底层 Transport 与连接池因此
//     始终不变，只换「选路策略」。
//
// 零值不可用；用 NewDynamic 构造。
type Dynamic struct {
	base http.RoundTripper
	cur  atomic.Pointer[Client]
}

// NewDynamic 用底层 RoundTripper 构造动态代理层（base 为 nil 时用默认 Transport）。
// 若 base 是 *http.Transport，会原位安装选路分发函数。
func NewDynamic(base http.RoundTripper) *Dynamic {
	if base == nil {
		base = http.DefaultTransport
	}
	d := &Dynamic{base: base}
	if bt, ok := base.(*http.Transport); ok {
		bt.Proxy = d.proxyFor
	}
	return d
}

// Set 替换当前代理实例（nil = 直连），并关闭底层空闲连接：旧代理设置下建立的
// 连接不应复用到新路径上（在途请求仍用各自已开始的连接，不受影响）。
func (d *Dynamic) Set(c *Client) {
	if d == nil {
		return
	}
	d.cur.Store(c)
	d.CloseIdleConnections()
}

// Current 返回当前代理实例（nil = 直连）。
func (d *Dynamic) Current() *Client {
	if d == nil {
		return nil
	}
	return d.cur.Load()
}

// proxyFor 安装在底层 *http.Transport 上的选路函数：按当前实例决定代理地址。
func (d *Dynamic) proxyFor(req *http.Request) (*url.URL, error) {
	if c := d.Current(); c != nil {
		return c.proxyFunc(req)
	}
	return nil, nil
}

// RoundTrip 实现 http.RoundTripper。
func (d *Dynamic) RoundTrip(req *http.Request) (*http.Response, error) {
	if d == nil || d.base == nil {
		return nil, errors.New("proxy: nil transport")
	}
	return roundTripAs(d.cur.Load(), d.base, req)
}

// CloseIdleConnections 透传到底层 Transport（若实现该接口）。
func (d *Dynamic) CloseIdleConnections() {
	if d == nil || d.base == nil {
		return
	}
	if ci, ok := d.base.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// CloseIdleConnections 透传到底层 Transport（若实现该接口）。
func (t *Transport) CloseIdleConnections() {
	if t == nil || t.base == nil {
		return
	}
	if ci, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}
