// normalize.go 配置归一化与校验：所有「认识的键、值非法」都在这里 fail fast。
//
// **只处理当前版本**（见 migrate.go 的纪律）：
//   - 旧键、旧取值一律不在这里兼容——它们由 migrate.go 一次性迁移掉；
//   - 这里只做三件事：缺省值补齐、非法值钳制/报错、派生字段（*Dur / *Client /
//     PromptRules）计算；
//   - 任何"为了兼容旧配置"的分支都不该出现在本文件，否则兼容债会永久驻留。
//
// 未知键不进这里（只由 keyshape.go 告警）。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/media"
	"github.com/linguo2625469/workbuddy2api-panel/internal/proxy"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scrub"
)

func (c *Config) normalize() error {
	var err error
	if c.Panel.PackageDetailLimit <= 0 {
		c.Panel.PackageDetailLimit = 5
	}
	if c.Logging.RequestRetentionDays <= 0 {
		c.Logging.RequestRetentionDays = 7
	}
	if c.Logging.RequestArchiveMaxMB <= 0 {
		c.Logging.RequestArchiveMaxMB = 100
	}
	// 入站读取上限（issue #100）：空值回落默认 300s；"0" 合法（不限制）；
	// 负值无语义，fail fast（静默钳 0 会把保护悄悄关掉）。
	if c.Server.ReadTimeout == "" {
		c.Server.ReadTimeout = "300s"
	}
	if c.ServerReadTimeoutDur, err = time.ParseDuration(c.Server.ReadTimeout); err != nil {
		return fmt.Errorf("server.read_timeout: %w", err)
	}
	if c.ServerReadTimeoutDur < 0 {
		return fmt.Errorf("server.read_timeout: 负时长 %q 无意义", c.Server.ReadTimeout)
	}
	// 入站准入（server.max_inflight_requests / max_inflight_bytes_mb / ingress_wait）：
	// 0 是合法值（= 该项不限制）；负值无语义，fail fast——静默钳 0 会把保护悄悄
	// 关掉，与 read_timeout 的处理风格一致。
	if c.Server.MaxInflightRequests < 0 {
		return fmt.Errorf("server.max_inflight_requests: 负值 %d 无意义（0 = 不限制）", c.Server.MaxInflightRequests)
	}
	if c.Server.MaxInflightBytesMB < 0 {
		return fmt.Errorf("server.max_inflight_bytes_mb: 负值 %d 无意义（0 = 不限制）", c.Server.MaxInflightBytesMB)
	}
	if c.Server.IngressWait == "" {
		c.Server.IngressWait = "5s"
	}
	if c.IngressWaitDur, err = time.ParseDuration(c.Server.IngressWait); err != nil {
		return fmt.Errorf("server.ingress_wait: %w", err)
	}
	if c.IngressWaitDur < 0 {
		return fmt.Errorf("server.ingress_wait: 负时长 %q 无意义（\"0\" = 满载立即拒绝）", c.Server.IngressWait)
	}
	if c.SoftRateDur, err = time.ParseDuration(c.Cooldown.SoftRate); err != nil {
		return fmt.Errorf("cooldown.soft_rate: %w", err)
	}
	// 空值回落默认 2h（Default() 已置值；此兜底覆盖显式 "" 与 Default() 被绕过的场景）。
	if c.Cooldown.SoftRateMax == "" {
		c.Cooldown.SoftRateMax = "2h"
	}
	if c.SoftRateMaxDur, err = time.ParseDuration(c.Cooldown.SoftRateMax); err != nil {
		return fmt.Errorf("cooldown.soft_rate_max: %w", err)
	}
	if c.BreakerCooldownDur, err = time.ParseDuration(c.Pool.BreakerCooldown); err != nil {
		return fmt.Errorf("pool.breaker_cooldown: %w", err)
	}
	if c.BreakerCooldownMaxD, err = time.ParseDuration(c.Pool.BreakerCooldownMax); err != nil {
		return fmt.Errorf("pool.breaker_cooldown_max: %w", err)
	}
	if c.DegradeCooldownDur, err = time.ParseDuration(c.Pool.DegradeCooldown); err != nil {
		return fmt.Errorf("pool.degrade_cooldown: %w", err)
	}
	if c.DegradeCooldownMaxD, err = time.ParseDuration(c.Pool.DegradeCooldownMax); err != nil {
		return fmt.Errorf("pool.degrade_cooldown_max: %w", err)
	}
	if c.SessionTTL, err = time.ParseDuration(c.SessionSticky.TTL); err != nil {
		return fmt.Errorf("session_sticky.ttl: %w", err)
	}
	if c.SessionGCInterval, err = time.ParseDuration(c.SessionSticky.GCInterval); err != nil {
		return fmt.Errorf("session_sticky.gc_interval: %w", err)
	}
	// 快过期窗口：空 = 禁用（ExpiringSoonDur 0）；非空必须可解析（拼写错误 fail fast）。
	if c.Pool.ExpiringSoon != "" {
		if c.ExpiringSoonDur, err = time.ParseDuration(c.Pool.ExpiringSoon); err != nil {
			return fmt.Errorf("pool.expiring_soon: %w", err)
		}
	}
	if c.ExpiringSoonDur < 0 {
		c.ExpiringSoonDur = 0
		c.Pool.ExpiringSoon = "0"
	}
	// costTier 探索窗口（issue #136）：空值回落默认 30m（Default 已置；此兜底覆盖
	// 显式 ""）；"0" 是合法值（关停，完全回到现状行为），不回落；负值钳 0 同关停
	//（"−5m" 无合理语义）。
	if c.Pool.CostExploreInterval == "" {
		c.Pool.CostExploreInterval = "30m"
	}
	if c.CostExploreIntervalDur, err = time.ParseDuration(c.Pool.CostExploreInterval); err != nil {
		return fmt.Errorf("pool.cost_explore_interval: %w", err)
	}
	if c.CostExploreIntervalDur < 0 {
		c.CostExploreIntervalDur = 0
	}
	// 积分保底：负值钳 0（= 关闭）。0 是合法默认（关闭），无需空值回落。
	if c.Pool.CreditFloor < 0 {
		c.Pool.CreditFloor = 0
	}
	if c.Pool.BreakerThreshold <= 0 {
		c.Pool.BreakerThreshold = 3
	}
	// 连败降权参数缺省归一（非法/未设置回落默认，与 breaker_threshold 同风格）。
	if c.Pool.DegradeThreshold <= 0 {
		c.Pool.DegradeThreshold = 5
	}
	if c.Pool.DegradeCooldown == "" {
		c.Pool.DegradeCooldown = "10m"
	}
	if c.Pool.DegradeCooldownMax == "" {
		c.Pool.DegradeCooldownMax = "2h"
	}
	// global 在途分档：0/负数视为未设置回落默认 2（WAF 403 修复 P1-1）。
	if c.Pool.MaxInFlightGlobal <= 0 {
		c.Pool.MaxInFlightGlobal = 2
	}
	if c.Pool.IdleWeightPerHour <= 0 {
		c.Pool.IdleWeightPerHour = 0.5
	}
	if c.Pool.IdleWeightMax <= 0 {
		c.Pool.IdleWeightMax = 5.0
	}
	if c.Upstream.TimeoutSeconds <= 0 {
		c.Upstream.TimeoutSeconds = 120
	}
	// header 缺省回落 timeout（保"首字节前换号"既有语义）；idle 缺省走内置大值。
	// 任务书约定：0 一律视为"未设置"走默认，真正的"禁用"留待后续（避免歧义）。
	if c.Upstream.HeaderTimeoutSeconds <= 0 {
		c.Upstream.HeaderTimeoutSeconds = c.Upstream.TimeoutSeconds
	}
	if c.Upstream.IdleTimeoutSeconds <= 0 {
		c.Upstream.IdleTimeoutSeconds = 300
	}
	if !strings.HasPrefix(c.Listen, ":") && !strings.Contains(c.Listen, ":") {
		c.Listen = ":" + c.Listen
	}
	// 空数组与 null 反序列化后覆盖掉 Default() 的排程值（键缺席才保留），在此补齐。
	// 空 = 未配置 → 回落默认；「禁用」一律走 *_enabled=false，两者互不混淆。
	if len(c.Schedule.CheckinHours) == 0 {
		c.Schedule.CheckinHours = []int{9, 21}
	}
	if len(c.Schedule.TravelHours) == 0 {
		c.Schedule.TravelHours = []int{9, 21}
	}
	if len(c.Schedule.ActivityHours) == 0 {
		c.Schedule.ActivityHours = []int{10}
	}
	if len(c.Schedule.KeepaliveHours) == 0 {
		c.Schedule.KeepaliveHours = []int{22}
	}
	if len(c.Schedule.BlackcatHours) == 0 {
		c.Schedule.BlackcatHours = []int{23}
	}
	if len(c.Schedule.GrowthHours) == 0 {
		c.Schedule.GrowthHours = []int{1}
	}
	// 余额后台刷新：启用时 minutes<=0 回落默认 5；关闭时 interval 保持 0（不启动）。
	if c.Schedule.BalanceRefreshEnabled {
		if c.Schedule.BalanceRefreshMinutes <= 0 {
			c.Schedule.BalanceRefreshMinutes = 5
		}
		c.BalanceRefreshInterval = time.Duration(c.Schedule.BalanceRefreshMinutes) * time.Minute
	}
	if err := c.validateScheduleHours(); err != nil {
		return err
	}
	// 裸名默认域：空/非法一律回落 cn（向后兼容，不 fail fast——旧 config 无此键）。
	c.ModelDefaultRealm = normalizeModelDefaultRealm(c.ModelDefaultRealm)
	// 出站代理：proxy_url / resin_url 为空 = 未接入；非空则解析并校验
	// （非法值 fail fast，避免面板保存后才在重启时暴露）。二者互斥。
	if c.ResinMode == "" {
		c.ResinMode = "reverse"
	}
	if c.ResinAuthVersion == "" {
		c.ResinAuthVersion = "V1"
	}
	pc, err := proxy.New(proxy.Config{
		ProxyURL:    c.ProxyURL,
		URL:         c.ResinURL,
		Platform:    c.ResinPlatformName,
		Mode:        c.ResinMode,
		AuthVersion: c.ResinAuthVersion,
	})
	if err != nil {
		return err
	}
	c.ProxyClient = pc
	if err := c.normalizeFingerprintRules(); err != nil {
		return err
	}
	// 工具结果图片策略：空 = auto（按入口默认）。这里只校验；生效由 main 的热应用
	// 路径统一 SetToolPolicy（校验失败时不能已经改掉进程级状态）。
	c.Media.ToolImages = strings.ToLower(strings.TrimSpace(c.Media.ToolImages))
	if err := media.ValidateToolPolicy(media.ToolPolicy(c.Media.ToolImages)); err != nil {
		return err
	}
	// 图片像素处理策略（转码/压缩）：同样只校验，生效在 main 的热应用路径。
	if err := media.ValidateImagePolicy(media.ImagePolicy{
		Transcode:    c.Media.ImageTranscode,
		MaxDimension: c.Media.ImageMaxDimension,
	}); err != nil {
		return err
	}
	return c.normalizePrompt()
}

// normalizePrompt / buildPromptRule / normalizePromptMode 等见 prompt_config.go：
// 校验 prompt.mode（none/replace/append/after/inject，历史别名迁移并告警）、按域覆盖
// 构建 c.PromptRules（键 "" / cn / global），并在需要正文时解析 preset/file/text
// （素材优先级 text > file > preset，file 不可读 → fail fast）。

// validateScheduleHours 校验排程小时落在 0-23。
//
// 为什么不用 `[-1]` 之类的哨兵值表意"禁用"：非法小时被静默吞掉时，用户以为关掉了签到，
// 实际可能被当成另一个整点照常执行；这里直接快速失败，并在错误信息里指向正确的开关
// （checkin_enabled / keepalive_enabled），避免用户靠猜哨兵值来配。
func (c *Config) validateScheduleHours() error {
	if err := checkHourRange("schedule.checkin_hours", "checkin_enabled", c.Schedule.CheckinHours); err != nil {
		return err
	}
	if err := checkHourRange("schedule.travel_hours", "travel_enabled", c.Schedule.TravelHours); err != nil {
		return err
	}
	if err := checkHourRange("schedule.activity_hours", "activity_enabled", c.Schedule.ActivityHours); err != nil {
		return err
	}
	if err := checkHourRange("schedule.keepalive_hours", "keepalive_enabled", c.Schedule.KeepaliveHours); err != nil {
		return err
	}
	if err := checkHourRange("schedule.blackcat_hours", "blackcat_enabled", c.Schedule.BlackcatHours); err != nil {
		return err
	}
	return checkHourRange("schedule.growth_hours", "growth_enabled", c.Schedule.GrowthHours)
}

func checkHourRange(field, switchKey string, hours []int) error {
	for _, h := range hours {
		if h < 0 || h > 23 {
			return fmt.Errorf("%s: %d 不是合法小时（0-23）；如要关闭该任务请设 schedule.%s=false", field, h, switchKey)
		}
	}
	return nil
}

// normalizeModelDefaultRealm 归一化 model_default_realm：小写去空白，接受
// cn/global/auto/auto:global,cn（及别名 cn,global / global,cn 等），
// 其余（含空）一律回落 cn（向后兼容：旧 config 无此键）。
func normalizeModelDefaultRealm(v string) string {
	d := strings.ToLower(strings.TrimSpace(v))
	d = strings.ReplaceAll(d, " ", "")
	switch d {
	case "global":
		return "global"
	case "auto", "auto:cn,global", "cn,global":
		return "auto"
	case "auto:global,cn", "global,cn":
		return "auto:global,cn"
	default:
		return "cn"
	}
}

// normalizeFingerprintRules 校验自定义改写规则并把归一化结果写回配置。
//
// 为什么在 normalize 阶段就试编译（而不是等到装配 client）：
// 非法规则要在**启动/保存时报错**——若推迟到运行期，用户会看到"配置已保存"
// 但改写静默不生效（或整层失效），排查成本高得多。试编译同时完成
// mode/action 的归一化（空 = 缺省），使面板回显的就是生效值。
func (c *Config) normalizeFingerprintRules() error {
	if len(c.FingerprintRules) == 0 {
		c.FingerprintRules = nil
		return nil
	}
	w, err := scrub.Build(c.FingerprintRules)
	if err != nil {
		return fmt.Errorf("fingerprint_rules: %w", err)
	}
	c.FingerprintRules = w.Rules()
	return nil
}
