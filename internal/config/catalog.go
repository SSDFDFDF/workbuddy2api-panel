// catalog.go 字段目录：每个配置字段「改了要不要重启」的唯一真相。
//
// 为什么需要它：在引入本文件之前，"哪些字段热生效 / 哪些需重启"分别写在
// 三处——saveConfig 的热应用代码、restartRequiredFields 的手写清单、
// 面板 index.html 手写的「需重启」徽标。三者已经漂移（例如
// session_sticky.enabled 既没进清单也没徽标：面板上改它显示"已保存"，
// 实际零效果且无任何提示）。
//
// 现在只有这里一份：
//   - 保存响应：DiffConfig 算出的实际改动 ∩ Restart 字段 → 面板提示；
//   - HTTP：GET /panel/api/config/catalog → 面板按字段自动渲染徽标与原因；
//   - 守护测试：TestCatalogCoversEveryConfigLeaf 保证 Config 的每个叶子都有登记，
//     TestCatalogModesConsistent 保证通配项之间不打架。
//
// 新增配置字段时**必须**在这里登记：TestCatalogCoversEveryConfigLeaf 会拦住漏登记。
package config

import (
	"encoding/json"
	"sort"
	"strings"
)

// Mode 字段的生效方式。
type Mode uint8

const (
	// Hot 保存即生效：saveConfig 里有对应的热应用动作，无需重启。
	Hot Mode = iota
	// Restart 必须重启进程：字段被装配期对象捕获（监听地址、出站 client、
	// 文件句柄、目录扫描、连接池……），进程内没有安全的替换点。
	Restart
)

// String 供日志与 API 使用（JSON 里也走它，见 MarshalJSON）。
func (m Mode) String() string {
	if m == Restart {
		return "restart"
	}
	return "hot"
}

// MarshalJSON 让 API 返回 "hot"/"restart" 而不是数字：面板与第三方脚本
// 读的是语义，不是枚举序号（序号会随新增枚举漂移）。
func (m Mode) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

// Field 单个配置字段的元数据。
type Field struct {
	// Path 点分路径。末尾的 ".*" 表示"该子树下的全部叶子"（见 MatchPath）。
	Path string `json:"path"`
	// Mode 热生效 / 需重启。
	Mode Mode `json:"mode"`
	// Passive 为真表示 Hot 但无需任何 apply 动作：值在读侧按需读取
	// （例：panel.package_detail_limit 由面板每次 GET 配置时现读）。
	// 它的意义是让 TestApplyClaimsEveryHotField 不要求一个不存在的 applier。
	Passive bool `json:"passive,omitempty"`
	// Why Mode==Restart 的原因：面板把它作为徽标的 title 展示，保存响应里
	// 也用它解释"为什么这项要重启"。Hot 项留空。
	Why string `json:"why,omitempty"`
	// Group 面板分组（base/upstream/realm/prompt/pool/schedule/session/
	// ingress/traffic/proxy/storage），仅用于展示与文档归类。
	Group string `json:"group,omitempty"`
}

// Catalog 字段目录（不可变快照，只读）。
type Catalog []Field

// Lookup 返回覆盖 path 的目录项。path 为具体叶子路径（DiffConfig / LeafPaths
// 的产物），模式里的 "*" 只出现在目录项一侧。
func (c Catalog) Lookup(path string) (Field, bool) {
	for _, f := range c {
		if MatchPath(f.Path, path) {
			return f, true
		}
	}
	return Field{}, false
}

// LookupAll 返回全部覆盖 path 的目录项（守护测试用：检查是否存在语义冲突）。
func (c Catalog) LookupAll(path string) []Field {
	var out []Field
	for _, f := range c {
		if MatchPath(f.Path, path) {
			out = append(out, f)
		}
	}
	return out
}

// MatchPath 报告目录模式 pattern 是否覆盖具体路径 path。
//
// 唯一的通配语义：末尾 ".*" = 该子树下的**全部叶子（递归）**，不含节点自身。
//
//	MatchPath("pool.*", "pool.max_in_flight")           → true
//	MatchPath("pool.*", "pool.breaker.cooldown")        → true（递归）
//	MatchPath("upstream.profiles.*", "upstream.profiles.cn.client_version") → true
//	MatchPath("upstream.profiles.*", "upstream.profiles")                   → false（节点自身不是叶子）
//	MatchPath("listen", "listen")                       → true
//	MatchPath("listen", "listen_extra")                 → false（不做前缀匹配）
func MatchPath(pattern, path string) bool {
	if prefix, ok := strings.CutSuffix(pattern, ".*"); ok {
		return strings.HasPrefix(path, prefix+".")
	}
	return pattern == path
}

// entries 目录全量数据。
//
// 口径：**如实反映当前实现**，不写"目标态"。P4 逐组件热化时把对应项的 Mode
// 改成 Hot，并同步在 cmd/server 的热应用里接上动作——TestApplyClaimsEveryHotField
// 会在两者不一致时失败。这样"面板说即时生效"永远等于"真的即时生效"。
var entries = Catalog{
	// ── 基础 ────────────────────────────────────────────────────────────
	// config_version：由 migrate.go 在加载/保存时自动归一（一次性版本迁移），
	// 用户无需维护，也没有热应用动作——Passive 即"无需 apply"。
	{Path: "config_version", Mode: Hot, Passive: true, Group: "base"},
	{Path: "listen", Mode: Restart, Group: "base",
		Why: "端口在装配期由 http.Server.Addr 绑定，热换需要起第二个 listener 并排空旧连接"},
	{Path: "api_key", Mode: Hot, Group: "base",
		Why: ""},
	{Path: "auth_dir", Mode: Restart, Group: "base",
		Why: "账号目录只在启动时扫描一次（auth.LoadDir + pool.SyncToDir）；面板内登录的新账号走 Add 热加载，不受此项影响"},
	{Path: "state_file", Mode: Restart, Group: "base",
		Why: "状态文件路径派生出用量/请求归档/模型缓存/cache-secret 等多个数据文件，中途换路径会分裂持久化视图"},
	{Path: "panel.package_detail_limit", Mode: Hot, Passive: true, Group: "base",
		Why: ""},

	// ── 指纹与媒体 ──────────────────────────────────────────────────────
	{Path: "fingerprint_rewrite", Mode: Hot, Group: "traffic", Why: ""},
	{Path: "fingerprint_rules", Mode: Hot, Group: "traffic", Why: ""},
	{Path: "media.*", Mode: Hot, Group: "traffic", Why: ""},

	// ── 日志与归档 ──────────────────────────────────────────────────────
	{Path: "logging.request_client_info", Mode: Hot, Group: "base", Why: ""},
	{Path: "logging.request_archive_enabled", Mode: Restart, Group: "base",
		Why: "归档器（reqlog.Recorder）在装配期构造并持有开关/保留天数/上限，尚无 Reconfigure 入口"},
	{Path: "logging.request_retention_days", Mode: Restart, Group: "base",
		Why: "同上：归档器装配期构造"},
	{Path: "logging.request_archive_max_mb", Mode: Restart, Group: "base",
		Why: "同上：归档器装配期构造"},

	// ── 入站准入与读取 ──────────────────────────────────────────────────
	{Path: "server.max_inflight_requests", Mode: Restart, Group: "ingress",
		Why: "准入 limiter 在装配期构造（含上限与等待时长），尚无 SetIngress 入口"},
	{Path: "server.max_inflight_bytes_mb", Mode: Restart, Group: "ingress",
		Why: "同上：准入 limiter 装配期构造"},
	{Path: "server.ingress_wait", Mode: Restart, Group: "ingress",
		Why: "同上：准入 limiter 装配期构造"},
	{Path: "server.read_timeout", Mode: Restart, Group: "ingress",
		Why: "http.Server.ReadTimeout 在 Serve 后被并发读取，进程内裸赋值有数据竞争"},

	// ── 冷却与池 ────────────────────────────────────────────────────────
	{Path: "cooldown.*", Mode: Hot, Group: "pool", Why: ""},
	{Path: "pool.*", Mode: Hot, Group: "pool", Why: ""},

	// ── 排程 ────────────────────────────────────────────────────────────
	{Path: "schedule.*", Mode: Hot, Group: "schedule", Why: ""},

	// ── 域策略 ──────────────────────────────────────────────────────────
	{Path: "global.enabled", Mode: Restart, Group: "realm",
		Why: "global 域开关在装配期写入上游 client 与 auth 侧闸门，尚无热替换入口"},
	{Path: "global.chat_base", Mode: Restart, Group: "realm",
		Why: "上游 base 被出站 client 在装配期捕获"},
	{Path: "global.billing_base", Mode: Restart, Group: "realm",
		Why: "上游 base 被出站 client 在装配期捕获"},
	{Path: "model_default_realm", Mode: Restart, Group: "realm",
		Why: "RealmResolver 在装配期构造，且被 handler 与会话粘性闭包共享实例"},

	// ── 提示词 ──────────────────────────────────────────────────────────
	{Path: "prompt.*", Mode: Restart, Group: "prompt",
		Why: "分域提示词规则在装配期构建后按值透传给 handler，尚无运行期快照入口"},

	// ── 上游与出站身份 ──────────────────────────────────────────────────
	{Path: "upstream.profiles.*", Mode: Restart, Group: "upstream",
		Why: "出站身份 profile 在装配期写进出站 client（按 realm 选 UA/版本头）"},
	{Path: "upstream.client_name", Mode: Restart, Group: "upstream",
		Why: "用量归属头取值在装配期写进出站 client"},
	{Path: "upstream.device_token", Mode: Restart, Group: "upstream",
		Why: "设备风控 token 在装配期写进出站 client"},
	{Path: "upstream.device_token_file", Mode: Restart, Group: "upstream",
		Why: "设备 token 文件路径在装配期写进出站 client"},
	{Path: "upstream.passthrough_ip", Mode: Restart, Group: "upstream",
		Why: "客户端 IP 透传开关在装配期写进出站 client"},
	{Path: "upstream.chat_base_cn", Mode: Restart, Group: "upstream",
		Why: "上游 base 被出站 client 在装配期捕获"},
	{Path: "upstream.timeout_seconds", Mode: Restart, Group: "upstream",
		Why: "短 RPC 超时写进 http.Client.Timeout，运行期替换会与在途请求竞争"},
	{Path: "upstream.header_timeout_seconds", Mode: Restart, Group: "upstream",
		Why: "首字节超时写进 Transport.ResponseHeaderTimeout，Transport 被连接池共享"},
	{Path: "upstream.idle_timeout_seconds", Mode: Restart, Group: "upstream",
		Why: "SSE 流空闲上限在装配期写入出站 client"},
	{Path: "upstream.first_model_event_seconds", Mode: Restart, Group: "upstream",
		Why: "SSE 阶段上限在装配期写入出站 client"},
	{Path: "upstream.first_generation_seconds", Mode: Restart, Group: "upstream",
		Why: "SSE 阶段上限在装配期写入出站 client"},
	{Path: "upstream.tail_seconds", Mode: Restart, Group: "upstream",
		Why: "SSE 阶段上限在装配期写入出站 client"},

	// ── 会话粘性 ────────────────────────────────────────────────────────
	{Path: "session_sticky.*", Mode: Restart, Group: "session",
		Why: "会话路由器（含启用开关、TTL、GC 周期）在装配期构造；改这里需重启（面板登录的热加载不受影响）"},

	// ── 出站代理 ────────────────────────────────────────────────────────
	{Path: "proxy_url", Mode: Restart, Group: "proxy",
		Why: "出站代理在装配期包一层 RoundTripper，运行期改写会与在途请求竞争"},
	{Path: "resin_url", Mode: Restart, Group: "proxy",
		Why: "同上：代理接入在装配期完成（Resin 还涉及 per-UID lease 身份）"},
	{Path: "resin_platform_name", Mode: Restart, Group: "proxy",
		Why: "同上：代理接入在装配期完成"},
	{Path: "resin_mode", Mode: Restart, Group: "proxy",
		Why: "同上：代理接入在装配期完成"},
	{Path: "resin_auth_version", Mode: Restart, Group: "proxy",
		Why: "同上：代理接入在装配期完成"},

	// ── 持久化镜像 ──────────────────────────────────────────────────────
	{Path: "upstash.url", Mode: Restart, Group: "storage",
		Why: "redisstore 连接在装配期建立，且粘性会话镜像依赖它"},
	{Path: "upstash.token", Mode: Restart, Group: "storage",
		Why: "同上：redisstore 连接在装配期建立"},
}

// nonUserLeaves 不是"用户可配置项"的叶子，不要求目录登记，附理由。
//
// 与 json:"-" 的派生态不同：这些字段**有 json tag**（面板读取它们展示），
// 但语义上不是可调配置，也不从文件读入（见 runtimeMetaKeys）。
// 守护测试要求每个豁免都写明原因（防止拿豁免绕过漏登记）。
var nonUserLeaves = map[string]string{
	"_warnings":   "载入期告警的运行时载体（面板只读展示），不是可配置项",
	"_migrations": "本次加载实际执行过的版本迁移（面板只读展示），不是可配置项",
}

// Entries 返回目录全量（按 Path 稳定排序的副本，调用方可安全持有）。
func Entries() Catalog {
	out := make(Catalog, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// IsRestartPath 报告具体路径是否落在某个 Restart 目录项内。
func (c Catalog) IsRestartPath(path string) bool {
	f, ok := c.Lookup(path)
	return ok && f.Mode == Restart
}

// ConfigLeafPaths 返回 Config 的全部叶子路径模式（map 的键位用 "*"），
// 供守护测试核对目录覆盖率。排序去重。
func ConfigLeafPaths() []string {
	var out []string
	leafPatterns(cfgType(), "", &out)
	sort.Strings(out)
	return out
}
