package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
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
	if t == nil || t.base == nil {
		return nil, errors.New("proxy: nil transport")
	}
	if t.client == nil || req == nil {
		return t.base.RoundTrip(req)
	}
	account := strings.TrimSpace(req.Header.Get(InternalAccountHeader))
	if account == "" {
		// 未标记请求（如公开第三方 API）：直连，零改动。
		return t.base.RoundTrip(req)
	}
	// 删除内部标记头，避免泄漏给代理或目标服务。
	clone := req.Clone(withAccount(req.Context(), account))
	clone.Header.Del(InternalAccountHeader)

	if t.client.mode == ModeReverse {
		u, err := t.client.reverseURL(req.URL)
		if err != nil {
			return nil, err
		}
		clone.URL = u
		// 清空 Host，让 transport 用改写后的 URL.Host（代理基址）作为 Host 头；
		// 目标 host 已在路径段中。
		clone.Host = ""
		clone.Header.Set("X-Resin-Account", account)
	}
	return t.base.RoundTrip(clone)
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
