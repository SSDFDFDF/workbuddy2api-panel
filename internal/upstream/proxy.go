// proxy.go upstream 与出站代理层（普通正向代理 / Resin）的低侵入接线。
//
// 设计（与 internal/proxy 的分工）：
//   - 路由改写与凭证构造全在 internal/proxy（Transport / proxyFunc）；
//   - 这里只做两件事：
//     1) SetProxy：把 proxy Transport 包到 HTTP/ChatHTTP 共用的底层 Transport 上；
//     2) tagProxy：在构造账号相关请求头时打一个内部账号标记头，告诉 Transport
//     「本请求归属哪个代理账号」。
//
// 账号级开关：tagProxy 在打标记前检查 auth.Auth.UseProxy()——账号显式关闭代理时
// 不打标记，请求按未接入路径直连（全站所有账号出站路径都经此闸，故开关全局生效）。
//
// 未调用 SetProxy（未配置 proxy_url / resin_url）时，tagProxy 是空操作，出站行为与
// 主线逐字一致。
package upstream

import (
	"net/http"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/proxy"
)

// proxyAccount 返回本请求归属的代理账号标识。
//
// 账号标识口径：优先 a.UID（账号 ID，登录后稳定且全项目一致），这是 Resin 要求的
// 「同一个账号的标识一定要稳定」——绝不混用邮箱/Token。UID 为空（未登录/匿名）时
// 不打标记，请求按未接入路径直连（登录阶段的临时身份由 panel 登录流程单独处理）。
func (c *Client) proxyAccount(a *auth.Auth) string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.UID)
}

// SetProxy 接入出站代理：把 pc 的 Transport 包到当前共用的出站 Transport 上。
// pc 为 nil 或未启用时空操作。重复调用幂等（已接入则跳过）。
//
// 调用时机：必须在 https 出站 client 的装配期调用（main.go），且在读取底层
// *http.Transport 调整超时之后（包上 Transport 后类型断言不再命中）。
func (c *Client) SetProxy(pc *proxy.Client) {
	if c == nil || !pc.Enabled() || c.proxy != nil || c.HTTP == nil {
		return
	}
	base := c.HTTP.Transport
	if base == nil {
		// 防御：不直接改写进程级 http.DefaultTransport（正代模式下会污染全局），
		// 克隆一份自有副本。生产路径 c.HTTP.Transport 恒非 nil。
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			base = dt.Clone()
		} else {
			base = http.DefaultTransport
		}
	}
	wrapped := pc.Wrap(base)
	c.HTTP.Transport = wrapped
	if c.ChatHTTP != nil {
		c.ChatHTTP.Transport = wrapped
	}
	c.proxy = pc
}

// Proxy 返回已接入的出站代理客户端（未接入为 nil）。
func (c *Client) Proxy() *proxy.Client {
	if c == nil {
		return nil
	}
	return c.proxy
}

// tagProxy 在请求上打内部代理账号标记（未接入/账号为空/账号关闭代理时不做任何事）。
//
// 账号级代理开关（auth.Auth.UseProxy，默认 true）：关闭时直接不打标记 → 该账号的
// 所有出站请求直连（chat / 签到 / 活跃上报 / 保活 / 余额刷新 / 领奖等全站路径）。
func (c *Client) tagProxy(req *http.Request, a *auth.Auth) {
	if c == nil || c.proxy == nil || req == nil {
		return
	}
	if a != nil && !a.UseProxy() {
		return
	}
	proxy.SetAccountHeader(req, c.proxyAccount(a))
}
