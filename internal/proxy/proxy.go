// Package proxy 统一出站代理层：支持「普通正向代理」与外部粘性代理池 Resin。
//
// 设计目标（低侵入 / 便于与主线同步）：
//   - 本包自成一体，不反向依赖 internal/upstream；所有代理语义（普通代理转发、
//     Resin 反向代理路径拼接、正向代理 Proxy-Authorization、账号标识注入、
//     inherit-lease）都收敛在这里。
//   - 上游代码只在「构造请求头」处调用 upstream.Client.tagProxy / proxy.SetAccountHeader
//     打一个内部标记头；真正的路由改写发生在 HTTP Transport 层，业务请求构造零改动。
//   - 未配置 proxy_url / resin_url 时本包不参与任何路径，行为与主线逐字一致。
//
// 支持两种代理形态，由 Config 决定：
//
//   - 普通正向代理（proxy_url）：http/https/socks5(s) 代理地址，凭证可写在 URL 的
//     userinfo 里（net/http 自动生成 Proxy-Authorization）。所有带账号标记的请求
//     经该代理转发；未打标记的请求（如公开第三方 API）直连。
//
//   - Resin 外部粘性代理池（resin_url）：两种接入方式，由 Config.Mode 选择：
//
//   - reverse（默认，推荐）：客户端把目标编码进路径，
//     <resin_url>/<Platform>/<protocol>/<host>/<path>?<query>，
//     并用请求头 X-Resin-Account 携带账号标识。
//
//   - forward：标准 HTTP 正向代理，Proxy-Authorization: Basic base64(凭证)，
//     V1 凭证为 Platform.Account:token（LEGACY_V0 为 token:Platform:Account）。
//
// WebSocket（ws/wss）反向代理约定：客户端到 Resin 这一段只能是 ws；路径中的
// protocol 段必须填 http 或 https（分别对应目标 ws / wss），不能填 ws/wss。
// reverseURL 按此约定把 ws→http、wss→https 映射。
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 认证版本。集成时按部署实际值显式配置，绝不自动切换。
const (
	AuthV1       = "V1"        // 当前推荐：Platform.Account:RESIN_PROXY_TOKEN
	AuthLegacyV0 = "LEGACY_V0" // 旧兼容：RESIN_PROXY_TOKEN:Platform:Account
)

// 代理形态。
const (
	ModePlain   = "plain"   // 普通正向代理（proxy_url）
	ModeReverse = "reverse" // Resin 反向代理（默认，推荐）
	ModeForward = "forward" // Resin 正向代理
)

// InternalAccountHeader 业务侧写入的「本请求归属哪个代理账号」内部标记头。
// Transport 读到它后：反代模式改写 URL 并换成 X-Resin-Account；正代/普通代理
// 模式把它转成 context 供 Proxy 函数生成 Proxy-Authorization。该头在出站前必被删除，
// 不会泄漏给代理或目标服务。
//
// 选带前缀的私有名（X-Wb2a-）以杜绝与真实业务头冲突；Go 会把头名规范化为
// X-Wb2a-Proxy-Account。
const InternalAccountHeader = "X-Wb2a-Proxy-Account"

// Config 一份代理接入配置。ProxyURL 与 URL 互斥（同时非空报错）。
type Config struct {
	// ProxyURL 普通正向代理地址（http/https/socks5/socks5h），可含 userinfo 凭证，
	// 例如 http://user:pass@127.0.0.1:8080 或 socks5://127.0.0.1:1080。
	// 空 = 不启用普通代理。
	ProxyURL string
	// URL resin_url 原值，含 Resin 基址与 Token，例如
	// http://127.0.0.1:2260/my-token。空 = 未启用 Resin。
	URL string
	// Platform resin_platform_name。必须与部署一致；不能含 '.'、':' 或 '/'。
	Platform string
	// Mode reverse（默认）/ forward。仅 Resin 使用；普通代理恒为 plain。
	Mode string
	// AuthVersion V1（默认）/ LEGACY_V0。仅 Resin 正向代理使用；反向代理不使用。
	AuthVersion string
}

// Client 一个已解析的代理接入实例。
type Client struct {
	// 普通正向代理
	plain *url.URL // 静态代理地址（含可选 userinfo）

	// Resin
	base        string // <scheme>://<host>/<token>（无尾斜杠），inherit-lease 用
	token       string // Token 路径段
	platform    string
	authVersion string

	mode       string       // plain / reverse / forward
	httpClient *http.Client // inherit-lease 控制面专用（不走代理，直连 Resin）
}

// New 解析并校验配置。ProxyURL 与 URL 均为空返回 (nil, nil)，表示未启用。
func New(cfg Config) (*Client, error) {
	plainRaw := strings.TrimSpace(cfg.ProxyURL)
	resinRaw := strings.TrimSpace(cfg.URL)
	if plainRaw != "" && resinRaw != "" {
		return nil, fmt.Errorf("proxy_url 与 resin_url 互斥，只能配置其一")
	}
	if plainRaw != "" {
		return newPlain(plainRaw)
	}
	if resinRaw == "" {
		return nil, nil
	}
	u, err := url.Parse(resinRaw)
	if err != nil {
		return nil, fmt.Errorf("resin_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("resin_url: scheme 必须是 http 或 https（当前 %q）", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("resin_url: 缺少主机（示例 http://127.0.0.1:2260/my-token）")
	}
	token := strings.Trim(u.Path, "/")
	if token == "" {
		return nil, fmt.Errorf("resin_url: 缺少 Token 路径段（示例 http://127.0.0.1:2260/my-token）")
	}
	if strings.Contains(token, "/") {
		return nil, fmt.Errorf("resin_url: Token 必须是单个路径段，当前 %q", token)
	}
	platform := strings.TrimSpace(cfg.Platform)
	if platform == "" {
		return nil, fmt.Errorf("resin_platform_name: 不能为空")
	}
	if strings.ContainsAny(platform, ".:/") {
		return nil, fmt.Errorf("resin_platform_name: 不能含 '.'、':' 或 '/'（当前 %q）", platform)
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = ModeReverse
	}
	if mode != ModeReverse && mode != ModeForward {
		return nil, fmt.Errorf("resin_mode: 只支持 %q 或 %q（当前 %q）", ModeReverse, ModeForward, cfg.Mode)
	}
	auth := strings.ToUpper(strings.TrimSpace(cfg.AuthVersion))
	if auth == "" {
		auth = AuthV1
	}
	if auth != AuthV1 && auth != AuthLegacyV0 {
		return nil, fmt.Errorf("resin_auth_version: 只支持 %q 或 %q（当前 %q）", AuthV1, AuthLegacyV0, cfg.AuthVersion)
	}
	if u.User != nil {
		return nil, fmt.Errorf("resin_url: 不应在 URL 里内嵌用户名密码（凭证由 Token 与 Platform/Account 组成）")
	}
	// 用 url.URL 重建基址，让 Token 里的特殊字符按路径规则正确转义（token 已
	// 校验为单段，不含 '/'）。
	base := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/" + token}).String()
	return &Client{
		base:        base,
		token:       token,
		platform:    platform,
		mode:        mode,
		authVersion: auth,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// newPlain 解析普通正向代理地址。
func newPlain(raw string) (*Client, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy_url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("proxy_url: scheme 必须是 http/https/socks5/socks5h（当前 %q）", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy_url: 缺少主机（示例 http://user:pass@127.0.0.1:8080）")
	}
	return &Client{
		plain:      u,
		mode:       ModePlain,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Enabled 报告是否启用。
func (c *Client) Enabled() bool { return c != nil }

// Mode 返回生效模式（plain / reverse / forward）。
func (c *Client) Mode() string {
	if c == nil {
		return ""
	}
	return c.mode
}

// Platform 返回配置的平台名（普通代理为空）。
func (c *Client) Platform() string {
	if c == nil {
		return ""
	}
	return c.platform
}

// AuthVersion 返回认证版本（普通代理为空）。
func (c *Client) AuthVersion() string {
	if c == nil {
		return ""
	}
	return c.authVersion
}

// IsPlain 报告是否为普通正向代理。
func (c *Client) IsPlain() bool { return c != nil && c.mode == ModePlain }

// SetAccountHeader 在请求上打内部账号标记（空账号不写）。
func SetAccountHeader(req *http.Request, account string) {
	if req == nil {
		return
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return
	}
	req.Header.Set(InternalAccountHeader, account)
}

// SetAccountHeader 方法版：仅在已启用时为请求打标记（nil 客户端安全）。
// 未启用时是空操作，保证不接入代理的部署不会多出内部头（零回归）。
func (c *Client) SetAccountHeader(req *http.Request, account string) {
	if !c.Enabled() {
		return
	}
	SetAccountHeader(req, account)
}

// reverseURL 把原始目标 URL 编码为 Resin 反向代理 URL：
// <resin_url>/<Platform>/<protocol>/<host>/<path>?<query>。
// protocol 段：https（含 wss）→ https，其余 → http。
func (c *Client) reverseURL(orig *url.URL) (*url.URL, error) {
	proto := "http"
	switch strings.ToLower(orig.Scheme) {
	case "https", "wss":
		proto = "https"
	}
	host := orig.Host
	if host == "" {
		return nil, fmt.Errorf("resin reverse: 目标 URL 缺少 host（%s）", orig.String())
	}
	p := orig.EscapedPath()
	if p == "" {
		p = "/"
	}
	// 逐段拼接后整体 parse：orig.EscapedPath() 与 url.PathEscape(platform)
	// 均已转义，避免二次转义。
	raw := c.base + "/" + url.PathEscape(c.platform) + "/" + proto + "/" + host + p
	if orig.RawQuery != "" {
		raw += "?" + orig.RawQuery
	}
	out, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("resin reverse: 拼接目标 URL 失败: %w", err)
	}
	return out, nil
}

// InheritLease 把临时身份（登录阶段）的 IP 租约平滑继承给登录后得到的稳定身份。
// 仅 Resin 支持：调用 POST <resin_url>/api/v1/<Platform>/actions/inherit-lease。
// 普通代理 / 未启用时为空操作；parent 或 newAccount 为空返回错误。
func (c *Client) InheritLease(ctx context.Context, parent, newAccount string) error {
	if c == nil || c.mode == ModePlain {
		return nil
	}
	if strings.TrimSpace(parent) == "" || strings.TrimSpace(newAccount) == "" {
		return fmt.Errorf("resin inherit-lease: parent_account 与 new_account 均不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := c.base + "/api/v1/" + url.PathEscape(c.platform) + "/actions/inherit-lease"
	body, err := json.Marshal(map[string]string{
		"parent_account": parent,
		"new_account":    newAccount,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("resin inherit-lease http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// forwardProxyUser 构造 Resin 正向代理凭证的 (username, password)。
//   - V1:        username = Platform.Account, password = token
//   - LEGACY_V0: username = token,             password = Platform.Account
//
// 两种版本都对完整凭证做一次 Base64，由 net/http 从 url.Userinfo 自动完成；
// 因此 Account 里的 '.' 与 ':' 不会被二次拆分（不会按所有分隔符切割）。
func (c *Client) forwardProxyUser(account string) (string, string) {
	if c.authVersion == AuthLegacyV0 {
		return c.token, c.platform + ":" + account
	}
	return c.platform + "." + account, c.token
}
