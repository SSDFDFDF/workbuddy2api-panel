// proxy.go upstream 与出站代理层（普通正向代理 / Resin）的低侵入接线。
//
// 设计（与 internal/proxy 的分工）：
//   - 路由改写与凭证构造全在 internal/proxy（Dynamic / proxyFunc）；
//   - 这里只做两件事：
//     1) SetProxy：把当前代理实例交给常驻的 proxy.Dynamic 转发层（无代理 = nil 直连）；
//     2) tagProxy：在构造账号相关请求头时打一个内部账号标记头，告诉 Transport
//     「本请求归属哪个代理账号」。
//
// 热改：proxy_url / resin_* 改配置时只替换 Dynamic 内的指针（见 main 的保存路径），
// 底层 Transport 与连接池不变，因此请求并发期间没有 Transport 字段竞争。
//
// 账号级开关：tagProxy 在打标记前检查 auth.Auth.UseProxy()——账号显式关闭代理时
// 不打标记，请求按未接入路径直连（全站所有账号出站路径都经此闸，故开关全局生效）。
//
// 未配置代理（Dynamic 的当前实例为 nil）时 tagProxy 是空操作，出站行为与主线逐字一致。
package upstream

import (
	"net/http"
	"strings"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/proxy"
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

// SetProxy 替换当前出站代理实例（nil = 取消代理、恢复直连）。可反复调用：
// 装配期设置初值，配置保存时热替换。
//
// 生产路径（upstream.New）从构造起就持有常驻 Dynamic 转发层，此处只换指针。
// 手工构造的测试 Client（无 proxyDyn）在首次 SetProxy 时就地包一层：
// 单线程装配场景安全；生产（New）不会走到这个分支。
func (c *Client) SetProxy(pc *proxy.Client) {
	if c == nil {
		return
	}
	dyn := c.proxyDyn.Load()
	if dyn == nil {
		c.proxyMu.Lock()
		if dyn = c.proxyDyn.Load(); dyn == nil {
			if c.HTTP == nil {
				c.proxyMu.Unlock()
				return
			}
			base := c.HTTP.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			dyn = proxy.NewDynamic(base)
			c.proxyDyn.Store(dyn)
			c.HTTP.Transport = dyn
			if c.ChatHTTP != nil {
				c.ChatHTTP.Transport = dyn
			}
		}
		c.proxyMu.Unlock()
	}
	dyn.Set(pc)
}

// Proxy 返回当前生效的出站代理客户端（未接入为 nil）。
func (c *Client) Proxy() *proxy.Client {
	if c == nil {
		return nil
	}
	if dyn := c.proxyDyn.Load(); dyn != nil {
		return dyn.Current()
	}
	return nil
}

// tagProxy 在请求上打内部代理账号标记（未接入/账号为空/账号关闭代理时不做任何事）。
//
// 账号级代理开关（auth.Auth.UseProxy，默认 true）：关闭时直接不打标记 → 该账号的
// 所有出站请求直连（chat / 签到 / 活跃上报 / 保活 / 余额刷新 / 领奖等全站路径）。
func (c *Client) tagProxy(req *http.Request, a *auth.Auth) {
	if c == nil || req == nil || c.Proxy() == nil {
		return
	}
	if a != nil && !a.UseProxy() {
		return
	}
	proxy.SetAccountHeader(req, c.proxyAccount(a))
}
