// schema.go 配置结构定义与默认值（原 cmd/server/config.go 前半）。
//
// 本包（internal/config）是配置域的唯一真相：类型、默认值、加载、校验、
// 字段目录（catalog.go）、差异计算（diff.go）都在这里；具体组件的热应用动作
// 由 cmd/server/apply.go 通过 Applier 接口注入，本包不依赖 pool/scheduler/
// server/reqlog/session 等运行期组件。
package config

import (
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/prompt"
	"github.com/linguo2625469/workbuddy2api-panel/internal/proxy"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scrub"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// 入站准入的缺省值（server.* 可覆盖）。与 internal/server 的默认值保持一致；
// 这里单独定义一份，避免 cmd/server 为了两个数字反向依赖 server 包的内部常量。
const (
	defaultMaxInflightRequests = 64
	defaultMaxInflightBytesMB  = 256
)

// Config 顶层配置。
type Config struct {
	// ConfigVersion 配置结构版本（见 migrate.go：一次性版本迁移，不是长期归一化）。
	// 由 Migrate 写入/校验，用户无需手工维护；缺省视为 VersionUnversioned。
	ConfigVersion int    `json:"config_version"`
	Listen        string `json:"listen"`     // ":7863"
	APIKey        string `json:"api_key"`    // 空 = 不鉴权
	AuthDir       string `json:"auth_dir"`   // ./auths
	StateFile     string `json:"state_file"` // ./data/state.json

	// FingerprintRewrite 出站请求体指纹改写（默认 false = 严格逐字透传）。
	//
	// 开启后仅改写上游逐字黑名单里的 7 类已知串（internal/scrub）：
	// Claude Code / Codex 身份句、billing-header 键值、尾随 cc_ 键值、
	// 反馈整句、裸 11128。这些串出现在 user/assistant/tool 消息里时，
	// prompt.mode（只作用于 system/developer）清不掉，只能逐字改写。
	//
	// 代价：**会改用户可见内容**（如对话里的 11128 变成 11-128），
	// 因此默认关闭；面板可热改，无需重启。
	FingerprintRewrite bool `json:"fingerprint_rewrite"`

	// FingerprintRules 自定义改写规则（在内置 7 类指纹之上**叠加**）。
	//
	// 用于把“自己遇到的指纹词/句”加进来，例如某个客户端的特殊标记、
	// 自己的项目名不能出现在请求里、某个内部术语要替换后再出站。
	// 每条规则的 Match/Replace/Mode/Action 见 internal/scrub.Rule，
	// 支持 literal（大小写敏感）/ fold（忽略 ASCII 大小写）两种匹配，
	// replace（换成给定文本）/ remove（整段删除）两种动作。
	//
	// 上限与合法性在 normalize 阶段校验（超量/非法 → 启动或保存报错）。
	// 改动**热生效**（与 FingerprintRewrite 同步），无需重启。
	FingerprintRules []scrub.Rule `json:"fingerprint_rules"`

	Panel struct {
		// PackageDetailLimit 积分构成页单账号默认展示的最近到期包数；<=0 回落 5。
		PackageDetailLimit int `json:"package_detail_limit"`
	} `json:"panel"`

	Media struct {
		// ToolImages 工具结果图片处理策略（internal/media）：
		//   "" / "auto"    —— 按入口默认：原生 Chat 透传（不改既有转发行为），
		//                     Responses / Messages 抬升为工具批次后的 user 图片消息；
		//   "passthrough"  —— 全部入口原样转发 tool 消息里的图片 part；
		//   "hoist"        —— 全部入口抽出图片，改为工具批次之后的 user 图片消息；
		//   "reject"       —— 全部入口明确 400（不转发工具结果图片）。
		//
		// 抬升是官方 custom-model-tool-media-hoist 插件同构的显式兼容变换（改变图片的
		// 角色与上下文位置，不是无损编码）；透传则把未验证的 tool 多模态形态交给上游
		// 判定。改动**热生效**（进程级策略，无需重启）。
		ToolImages string `json:"tool_images"`

		// ImageTranscode 把上游不支持的图片格式（gif/bmp/tiff）转码为 PNG（无损；
		// 若同时开启压缩档，转码结果可能进一步被重编码为更小的 JPEG）。
		//
		// GIF 只保留首帧（动画信息丢失，官方客户端转 PNG 同样如此）；Go 无 webp
		// 编码器，webp 仅在开启压缩档时被重编码为 JPEG。默认 false。
		// 改动**热生效**。
		ImageTranscode bool `json:"image_transcode"`

		// ImageMaxDimension 图片压缩档位：0 = 关闭（默认），1080 / 2000 为内置两档
		// （对齐官方 CODEBUDDY_CODE_IMAGE_COMPRESSION_MAX_DIMENSION：1080 档 base64
		// ≤512000 + JPEG 质量 [85,70,50,30]；2000 档 base64 ≤5 MiB + 质量
		// [80,60,40,20]）。开启后超过档位边长或体积的图片会被等比缩放 + JPEG 重编码
		// （有损）——这是显式声明的兼容变换，不是无损编码。改动**热生效**。
		ImageMaxDimension int `json:"image_max_dimension"`
	} `json:"media"`

	Logging struct {
		// RequestArchiveEnabled 请求元数据 JSONL 归档开关，缺省 true。
		RequestArchiveEnabled bool `json:"request_archive_enabled"`
		// RequestRetentionDays 归档保留天数，缺省 7；<=0 回落默认。
		RequestRetentionDays int `json:"request_retention_days"`
		// RequestArchiveMaxMB 归档总上限（MiB），缺省 100；<=0 回落默认。
		RequestArchiveMaxMB int `json:"request_archive_max_mb"`
		// RequestClientInfo 是否在请求日志（归档事件 + stdout 流水行 + 面板运行
		// 日志）里记录调用来源：客户端 IP 与 User-Agent。缺省 true。
		//
		// 为什么做成开关而不是恒开：来源信息是排查"谁在打网关"的第一手线索，
		// 但它比 token 计数敏感（IP 属个人信息），共享部署/多租户场景可能需要
		// 关掉。关闭后 Event.ClientIP/UserAgent 保持为空，归档里不出现该字段。
		// 热生效（经 config/runtime 快照），无需重启。
		RequestClientInfo bool `json:"request_client_info"`
	} `json:"logging"`

	Server struct {
		// MaxInflightRequests 服务级入站准入：并发「读取 + 整包解析 + 图片校验」的
		// 请求数上限。0 = 不限制该项。缺省 64。
		//
		// 为什么需要：账号租约（单账号在途上限）在选号之后才申请，而最贵的
		// io.ReadAll（最多 32 MiB）+ JSON 解析 + 图片 base64 校验发生在它之前——
		// 没有这道闸门时该阶段的内存放大与并发数成正比，与账号数无关。
		MaxInflightRequests int `json:"max_inflight_requests"`
		// MaxInflightBytesMB 同上，按 Content-Length 计的并发字节预算（MiB）。
		// 0 = 不限制该项。缺省 256。无 Content-Length（chunked）按 2 MiB 计入。
		MaxInflightBytesMB int `json:"max_inflight_bytes_mb"`
		// IngressWait 准入满载时的等待上限（如 "5s"；"0" = 立即拒绝）。
		// 等待期间不持有内存，只占连接；超时后回 503 server_busy。
		IngressWait string `json:"ingress_wait"`
		// ReadTimeout 入站请求读取（含 body 上传）总时长上限（issue #100）。
		// http.Server 的 ReadTimeout 覆盖整个请求读取：大上下文/文件块请求经
		// 反代链转发时上传可超过旧固定值 60s，被掐后客户端拿到
		// 400 "read body: ... i/o timeout"。缺省 "300s"；"0" = 不限制
		//（慢速 body 可无限占用连接，自担风险）；改动需重启进程。
		ReadTimeout string `json:"read_timeout"` // "300s"；"0" = 不限制
	} `json:"server"`

	Cooldown struct {
		// hard_credit / err_threshold / err_cooldown 三个历史键已退役：
		// 硬冷却固定为次日 04:00（CooldownUntilTomorrow4AM），连续错误语义并入熔断器。
		// 旧 config 中的这些键因 JSON 未知字段而自然忽略，不报错。
		SoftRate string `json:"soft_rate"` // "600s"，软限流冷却基数
		// SoftRateMax 软冷却指数退避的封顶，默认 "2h"。
		// 空值回落默认，非法值报错（处理风格同 soft_rate）。
		SoftRateMax string `json:"soft_rate_max"` // "2h"
	} `json:"cooldown"`

	Schedule struct {
		CheckinHours   []int `json:"checkin_hours"`   // [9,21]
		TravelHours    []int `json:"travel_hours"`    // [9,21]
		ActivityHours  []int `json:"activity_hours"`  // [10]
		KeepaliveHours []int `json:"keepalive_hours"` // [22]
		BlackcatHours  []int `json:"blackcat_hours"`  // [23] 夜猫子窗口（23:00–08:00 计数）
		GrowthHours    []int `json:"growth_hours"`    // [1] 成长任务队列（Sequential 族每日零点解锁，01:00 自动扫描执行）
		// CheckinEnabled/TravelEnabled/ActivityEnabled/KeepaliveEnabled/BlackcatEnabled 显式禁用开关（缺省 true）。
		//
		// 为什么用独立 bool 而不是空数组/哨兵值表意"禁用"：
		//   - 空数组与 null 在老语义里已被"未配置 → 回落默认"占用，改判会静默翻转
		//     所有老 config 的行为（用户只想删掉一行，结果关掉了签到）；bool 缺省 true
		//     则对老配置零影响，向后完全兼容。
		//   - 开关与取值解耦：禁用时仍保留用户显式配的小时，重新启用无需补配。
		//   - 无需猜测哨兵（[-1] 之类），非法小时一律报错并提示改用本开关。
		// 旧 config 里的该键因 JSON 未知字段而自然忽略，不报错。
		CheckinEnabled   bool `json:"checkin_enabled"`   // 缺省 true；false = 关签到
		TravelEnabled    bool `json:"travel_enabled"`    // 缺省 true；false = 完全停猫猫旅行
		ActivityEnabled  bool `json:"activity_enabled"`  // 缺省 true；false = 停活跃上报
		KeepaliveEnabled bool `json:"keepalive_enabled"` // 缺省 true；false = 关 token 保活
		BlackcatEnabled  bool `json:"blackcat_enabled"`  // 缺省 true；false = 关夜猫子
		GrowthEnabled    bool `json:"growth_enabled"`    // 缺省 true；false = 关成长任务自动排程

		// IncludeDisabledInTasks 让「保号类」定时任务（签到 / 活跃上报 / token 保活 /
		// 余额刷新）对**已禁用（disabled）**的账号也执行。
		//
		// 为什么需要它：面板「禁用」的语义是「不再参与选号」（见面板确认文案），但这四类
		// 任务此前一律 `if st.Disabled { continue }`，等于把「停用流量」放大成「停止一切
		// 上游保号行为」——被禁用的号拿不到签到积分、不续 token、余额也不再刷新；而
		// ReenableIfCredits 明确不复活 disabled 账号（见 pool.state.go），于是签到这条唯一
		// 的自动回血路径也断了，账号只能靠人工「解冻」回来。
		//
		// 对「一次只放开一个号、用禁用做流量开关」的轮换用法（同 IP 多号防风控），闲置
		// 待命的号恰恰是最需要签到的那批——本开关即为该用法提供出口。
		//
		// 缺省 false = 保持既有行为，对老配置零影响。打开后禁用号仍会签到 / 保活，但
		// **依旧不参与选号**：pool 选号侧的 disabled 过滤不受本开关影响。
		IncludeDisabledInTasks bool `json:"include_disabled_in_tasks"`

		// 余额后台周期刷新：两次签到时点之间 credits 也能保持新鲜（面板/状态观测用）。
		// 解冻语义同签到（余额 > 0 的冷却账号自动解冻），但不做签到不刷 token。
		BalanceRefreshEnabled bool `json:"balance_refresh_enabled"` // 缺省 true；false = 关闭
		BalanceRefreshMinutes int  `json:"balance_refresh_minutes"` // 缺省 5；<=0 回落 5
	} `json:"schedule"`

	Global struct {
		// Enabled global realm 路由开关。缺省 true：Realm() 正常把 realm=global/
		// domain=workbuddy.ai 的账号判为 global 并路由 global base/路径。
		// 显式 "enabled": false 关闭（逃生门，纯 CN 锁定：即便 auth 写了 realm=global
		// 也不路由，auth.Realm() 双保险的第一道闸）。纯 CN 部署行为不变：CN 账号
		// 恒判 cn，global base 只在 realm=global 的账号上被使用。
		Enabled bool `json:"enabled"`
		// ChatBase / BillingBase 国际版上游 base 覆盖；空 = 回落内置默认
		// https://www.workbuddy.ai（internal/upstream.defaultGlobalBase）。
		ChatBase    string `json:"chat_base"`
		BillingBase string `json:"billing_base"`
	} `json:"global"`

	// ModelDefaultRealm 裸模型名的默认域策略（无 cn:/global: 前缀时）：
	//   "cn"（默认，零回归）= 裸名归 CN；
	//   "global" = 裸名归国际版（纯 global 池免写前缀）；
	//   "auto"（或 "auto:cn,global"）= 自动判定（CN 优先，无号/失败时轮退至 global）；
	//   "auto:global,cn" = 自动判定（Global 优先，无号/失败时轮退至 cn）。
	// 显式 cn:/global: 前缀恒优先，不受本项影响。
	ModelDefaultRealm string `json:"model_default_realm"`

	Upstream struct {
		// TimeoutSeconds 短 RPC（refresh/checkin/balance/FetchModels）总时长上限，默认 120。
		TimeoutSeconds int `json:"timeout_seconds"`
		// HeaderTimeoutSeconds 聊天 SSE 首字节前（响应头）上限；<=0 回落 TimeoutSeconds。
		HeaderTimeoutSeconds int `json:"header_timeout_seconds"`
		// IdleTimeoutSeconds 聊天 SSE 流中空闲上限（活跃吐数据续命不掐）；<=0 回落默认 300。
		IdleTimeoutSeconds int                                 `json:"idle_timeout_seconds"`
		Profiles           map[string]upstream.IdentityProfile `json:"profiles"`
		ChatBaseCN         string                              `json:"chat_base_cn"`
		// SegmentTypes 与默认值：0 表示未设置。
		FirstModelEventSeconds int `json:"first_model_event_seconds"`
		FirstGenerationSeconds int `json:"first_generation_seconds"`
		TailSeconds            int `json:"tail_seconds"`
		// ClientName 用量归属头取值（X-Product / X-IDE-Name / X-IDE-Type / X-IDE-Version）。
		// 空 = 旧行为 X-Product="SaaS" 不设 X-IDE-*；配 "WorkBuddy" 则四头跟随。
		ClientName string `json:"client_name"`
		// DeviceToken 设备风控 Token（X-Device-Token 头）全局兜底；空 = 不注入。
		// 每号 auth 文件的 device_token 键优先于本项。
		DeviceToken string `json:"device_token"`
		// DeviceTokenFile device token 文件路径兜底（宿主落盘的桌面端 token，5 分钟读取缓存）。
		DeviceTokenFile string `json:"device_token_file"`
		// PassthroughIP 是否透传客户端 IP 给上游（默认 false，反代安全边界）。
		PassthroughIP bool `json:"passthrough_ip"`
	} `json:"upstream"`

	Prompt struct {
		// Mode 组合位置（默认 none = 不改写，零回归）：
		//
		//	none    透传客户端原始 system/developer（不改一个字节）
		//	replace 删除全部 system/developer，只留网关提示词（指纹面最小）
		//	append  客户端开头 system/developer 块之后插网关提示词（客户端在前）
		//	after   网关提示词置首，客户端开头块紧随其后（后组合，客户端内容不丢）
		//
		// 历史别名照收（custom→replace、passthrough→none）。
		Mode string `json:"mode"`
		// Preset 内置预设：default（defaultprompt.md，约 2 KB，分域共用）/
		// minimal（按 realm 取 minimal_cn / minimal_global，约 10 行，量级对齐
		// 官方 Quick 模式模板）。空 = default。
		Preset string `json:"preset"`
		// File 提示词文件路径；非空且不可读 → 启动/保存报错（fail fast）。
		// 优先级低于 Text、高于 Preset。
		File string `json:"file"`
		// Text 内联提示词正文（面板可直编）。非空优先于 File/Preset——
		// “直接把内容写进配置”的入口，无需额外落盘文件。
		Text string `json:"text"`
		// Profiles 按账号域覆盖上列四项（键：cn / global）。未设置的字段
		// 逐项回落到顶层（含 mode 本身）；顶层未设 → 内置缺省。
		// 用于“同一网关同时对 CN 与 Global 账号使用不同提示词”。
		Profiles map[string]PromptProfile `json:"profiles"`
	} `json:"prompt"`

	// PromptText 默认域解析后的系统提示词文本（历史字段，保留兼容）。
	// 等价于 PromptRules[""] 的 Text；新代码请读 PromptRules。
	PromptText string `json:"-"`

	// PromptRules 各域生效的提示词规则（键："" 默认、"cn"、"global"）。
	// 装配期构建，不可热改（改 prompt 需重启）；handler 按请求 realm 选规则。
	PromptRules map[string]prompt.Rule `json:"-"`

	// Warnings 载入期的配置告警（未知键、被忽略的已知非法项）。运行期元数据：
	// 不从文件读入（runtimeMetaKeys）、也不落盘，供启动日志与面板 `_warnings` 展示。
	Warnings []string `json:"_warnings,omitempty"`

	// Migrations 本次加载实际执行过的版本迁移（步骤摘要 + 逐条改动说明）。
	// 只出现一次：迁移立即回写磁盘，旧形态不会留在文件里反复迁移。
	// 运行期元数据（同 Warnings：不读入、不落盘）。
	Migrations []string `json:"_migrations,omitempty"`

	Upstash struct {
		URL   string `json:"url"`   // 空 = 纯内存模式；支持完整 rediss:// URL 或 https://xxx.upstash.io host
		Token string `json:"token"` // url 非完整连接串时用于组装 rediss://default:<token>@<host>:6379
	} `json:"upstash"`

	Pool struct {
		MaxInFlight        int    `json:"max_in_flight"`        // 单账号最大在途请求数，0 = 不限
		MaxInFlightGlobal  int    `json:"max_in_flight_global"` // global 域单账号在途上限（WAF 风控紧域压低并发），0 = 回落默认 2
		BreakerThreshold   int    `json:"breaker_threshold"`    // 连续失败次数触发熔断，默认 3
		BreakerCooldown    string `json:"breaker_cooldown"`     // 基础熔断时长，默认 "30m"
		BreakerCooldownMax string `json:"breaker_cooldown_max"` // 指数退避封顶，默认 "6h"
		// 连败降权（issue #114）：ErrClient/传输层这类「不罚号」失败连续计数，达阈
		// 临时出池。与冷却/熔断并存取更长者不叠加。默认 5 次 / 10m。
		DegradeThreshold   int     `json:"degrade_threshold"`    // 连败次数触发降权，默认 5
		DegradeCooldown    string  `json:"degrade_cooldown"`     // 降权时长（固定，非指数退避），默认 "10m"
		DegradeCooldownMax string  `json:"degrade_cooldown_max"` // 降权时长的上限钳制，默认 "2h"（仅当 cooldown 超该值才钳制）
		IdleWeightPerHour  float64 `json:"idle_weight_per_hour"` // 闲置补偿：每小时未用 +0.5 权重
		IdleWeightMax      float64 `json:"idle_weight_max"`      // 闲置补偿封顶，默认 5.0
		// PreferExpiring 快过期积分加权开关，默认 true。开启且 expiring_soon 窗口内
		// 存在有效批次时，该账号选号权重 ×3（虚拟实例，见 pool 路由加权）；
		// 不按到期时间排序、与批次金额无关（issue #101 对齐实现口径）。
		// 关闭后完全不使用到期信息选号。
		PreferExpiring bool `json:"prefer_expiring"`
		// ExpiringSoon 快过期积分窗口（如 "168h"=7天）：签到/余额刷新时，到期时间在
		// 此窗口内的批次令账号命中上述 ×3 加权；窗口开大 → 命中账号变多、
		// 偏好被稀释。空/0 = 禁用该加权门槛。
		ExpiringSoon string `json:"expiring_soon"`
		// CostExploreInterval costTier 条件探索窗口（issue #136 方案 a′）：tier 0
		// 垄断层存在且 tier 1 有成员时，距上次探索 ≥ 窗口则本次 pick 生效层切
		// tier 1-only（探索=搭车改道，零新增上游请求；成功即毕业，失败走既有
		// 错误策略）。默认 "30m"（≤48 次/天/模型）；"0" 关停（完全回到现状行为）；
		// 空值回落默认。
		CostExploreInterval string `json:"cost_explore_interval"`
		// CreditFloor 积分保底：账号余额低于该值时，对实测收费模型（tier 2）不再
		// 参与选号——防止收费请求把余额打穿、连免费模型都 402 冷却到次日签到。
		// tier 0（免费）/ tier 1（无观测）不受限；签到回血越过 floor 自动恢复。
		// 默认 0 = 关闭；负值钳 0。
		CreditFloor int64 `json:"credit_floor"`
	} `json:"pool"`

	SessionSticky struct {
		Enabled    bool   `json:"enabled"`     // 默认 true
		TTL        string `json:"ttl"`         // 会话绑定 TTL，默认 "30m"
		GCInterval string `json:"gc_interval"` // 会话 GC 周期，默认 "5m"
	} `json:"session_sticky"`

	// 出站代理接入（低侵入，见 internal/proxy 与 FORK_CHANGES.md）。
	// 普通正向代理（proxy_url）与 Resin（resin_url）互斥，空 = 未接入，
	// 出站行为与主线逐字一致。账号可用 use_proxy 单独关闭代理（默认开）。
	//
	// ProxyURL 普通正向代理地址：http/https/socks5/socks5h，可含 userinfo 凭证，
	// 如 http://user:pass@127.0.0.1:8080 或 socks5://127.0.0.1:1080。
	ProxyURL string `json:"proxy_url"`
	// Resin 外部粘性代理池：
	ResinURL          string `json:"resin_url"`           // 含基址与 Token，如 http://127.0.0.1:2260/my-token
	ResinPlatformName string `json:"resin_platform_name"` // Platform 字段，必须与部署一致
	ResinMode         string `json:"resin_mode"`          // reverse（默认，推荐）/ forward
	ResinAuthVersion  string `json:"resin_auth_version"`  // V1（默认）/ LEGACY_V0，仅正向代理使用

	// 解析后
	SoftRateDur            time.Duration `json:"-"`
	SoftRateMaxDur         time.Duration `json:"-"`
	BreakerCooldownDur     time.Duration `json:"-"`
	BreakerCooldownMaxD    time.Duration `json:"-"`
	DegradeCooldownDur     time.Duration `json:"-"`
	DegradeCooldownMaxD    time.Duration `json:"-"`
	SessionTTL             time.Duration `json:"-"`
	SessionGCInterval      time.Duration `json:"-"`
	BalanceRefreshInterval time.Duration `json:"-"` // 0 = 不启动（enabled=false）
	ExpiringSoonDur        time.Duration `json:"-"`
	// CostExploreIntervalDur 解析后的 costTier 探索窗口（issue #136）；0 = 关停。
	CostExploreIntervalDur time.Duration `json:"-"`
	// ServerReadTimeoutDur 解析后的入站请求读取上限（issue #100）；0 = 不限制。
	ServerReadTimeoutDur time.Duration `json:"-"`
	// IngressWaitDur 解析后的入站准入等待上限（server.ingress_wait）；0 = 满载立即拒绝。
	IngressWaitDur time.Duration `json:"-"`
	// ProxyClient 解析校验后的代理接入实例（普通代理或 Resin）；nil = 未接入
	// （JSON 不序列化）。
	ProxyClient *proxy.Client `json:"-"`
}

// Default 默认配置。
func Default() *Config {
	c := &Config{
		Listen:    ":7863",
		APIKey:    "",
		AuthDir:   "./auths",
		StateFile: "./data/state.json",
	}
	c.Cooldown.SoftRate = "600s"
	c.Cooldown.SoftRateMax = "2h"
	c.Server.ReadTimeout = "300s"
	c.Server.MaxInflightRequests = defaultMaxInflightRequests
	c.Server.MaxInflightBytesMB = defaultMaxInflightBytesMB
	c.Server.IngressWait = "5s"
	c.Panel.PackageDetailLimit = 5
	// 工具结果图片策略默认 auto（原生 Chat 透传 / 桥接抬升）。显式写成 auto 而不是留
	// 空串，是为了面板下拉有匹配项：空串在 <select> 里匹配不上任何 option，回显会是
	// 空白（配置与表单显示漂移）。空串仍是合法别名（兼容手写配置）。
	c.Media.ToolImages = "auto"
	c.Logging.RequestArchiveEnabled = true
	c.Logging.RequestRetentionDays = 7
	c.Logging.RequestArchiveMaxMB = 100
	// 缺省 true 靠显式赋值实现（同 Schedule 开关）：JSON 里键缺席时字段保留此值，
	// 只有显式 false 才关闭来源记录。
	c.Logging.RequestClientInfo = true
	c.Schedule.CheckinHours = []int{9, 21}
	c.Schedule.TravelHours = []int{9, 21}
	c.Schedule.ActivityHours = []int{10}
	c.Schedule.KeepaliveHours = []int{22}
	c.Schedule.BlackcatHours = []int{23}
	c.Schedule.GrowthHours = []int{1}
	// 开关「缺省 true」靠这几行实现：Load 先取 Default() 再 json.Unmarshal 覆盖，
	// 键缺席（或为 null）时字段原样保留 true，只有显式 false 才关。
	c.Schedule.CheckinEnabled = true
	c.Schedule.GrowthEnabled = true
	c.Schedule.TravelEnabled = true
	c.Schedule.ActivityEnabled = true
	c.Schedule.KeepaliveEnabled = true
	c.Schedule.BlackcatEnabled = true
	c.Schedule.BalanceRefreshEnabled = true
	c.Schedule.BalanceRefreshMinutes = 5
	c.Upstream.TimeoutSeconds = 120
	// HeaderTimeoutSeconds/IdleTimeoutSeconds 默认 0（未设置态），回落见 normalize()。
	c.Upstream.HeaderTimeoutSeconds = 0
	c.Upstream.IdleTimeoutSeconds = 0
	// Global.Enabled 缺省 true（纯 CN 行为不变：CN 账号恒判 cn，global base 不被使用）；
	// ChatBase/BillingBase 缺省空（回落内置默认）。
	c.Global.Enabled = true
	c.ConfigVersion = CurrentVersion
	c.Upstream.FirstModelEventSeconds = 120
	c.Upstream.FirstGenerationSeconds = 300
	c.Upstream.TailSeconds = 10
	c.Upstream.Profiles = map[string]upstream.IdentityProfile{"cn": upstream.DefaultIdentity("cn"), "global": upstream.DefaultIdentity("global")}
	c.Upstream.ChatBaseCN = "https://www.workbuddy.cn"
	c.Prompt.Mode = "none"
	c.Pool.MaxInFlight = 3
	// MaxInFlightGlobal 缺省 2：global 域 WAF 风控更紧，压低单号并发（WAF 403 修复
	// P1-1）；0/负数 normalize 回落默认（与 max_in_flight 的 0=不限语义不同，分档键
	// 的 0 没有合理语义，回退分档默认最稳）。
	c.Pool.MaxInFlightGlobal = 2
	c.Pool.BreakerThreshold = 3
	c.Pool.BreakerCooldown = "30m"
	c.Pool.BreakerCooldownMax = "6h"
	c.Pool.DegradeThreshold = 5
	c.Pool.DegradeCooldown = "10m"
	c.Pool.DegradeCooldownMax = "2h"
	c.Pool.IdleWeightPerHour = 0.5
	c.Pool.IdleWeightMax = 5.0
	c.Pool.PreferExpiring = true
	c.Pool.ExpiringSoon = "168h" // 快过期窗口默认 7 天：官方活动奖励积分多在两周内过期
	// costTier 探索默认 30m（issue #136：垄断破除 + 搭车改道零新增请求）；"0" 关停。
	c.Pool.CostExploreInterval = "30m"
	c.SessionSticky.Enabled = true
	c.SessionSticky.TTL = "30m"
	c.SessionSticky.GCInterval = "5m"
	// Resin 缺省未接入：URL/Platform 空，模式与认证版本给推荐值便于展示/落盘。
	c.ResinMode = "reverse"
	c.ResinAuthVersion = "V1"
	// 裸名默认域：缺省 cn（历史行为零回归）。
	c.ModelDefaultRealm = "cn"
	return c
}
