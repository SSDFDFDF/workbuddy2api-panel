package server

import (
	"strings"
	"sync/atomic"
)

// 裸模型名的默认域策略（config model_default_realm）。
const (
	// RealmDefaultCN 裸名归 CN 域（历史默认，零回归）。
	RealmDefaultCN = "cn"
	// RealmDefaultGlobal 裸名归 global 域（纯国际版部署用，免写前缀）。
	RealmDefaultGlobal = "global"
	// RealmDefaultAuto 按池内可用账号域自动判定（CN 优先，两域均可用时归 cn，
	// cn 无号/失败时轮退至 global）。
	RealmDefaultAuto = "auto"
	// RealmDefaultAutoGlobalCN 按池内可用账号域自动判定（Global 优先，两域均可用时归 global，
	// global 无号/失败时轮退至 cn）。
	RealmDefaultAutoGlobalCN = "auto:global,cn"
)

// RealmResolver 解析模型名协议（PLAN D6）：
//
//	[realm:]model
//
// 显式前缀恒优先：取第一个 ":"，前段恰为小写 "cn"/"global" 才剥离（大小写敏感）；
// 其余（含裸名、非枚举前缀）按 Default 策略决定 realm。bare 为出站/选号/账本使用的
// 裸模型名。
//
// 该类型同时被 handler（chat 出站路由）与 session 粘性闭包使用——二者必须用**同一个
// resolver**，否则粘性分配的账号域与请求实际路由域可能不一致。
type RealmResolver struct {
	// Default 裸名默认域：cn / global / auto / auto:global,cn（空/非法视为 cn）。
	// **装配期初值**：运行期热改走 SetDefault（内部原子覆盖），热路径读 defaultPolicy。
	Default string
	// RealmReady 报告某域当前是否有可用账号（auto 判定用）。nil = 视为不可用，回落首选域。
	RealmReady func(realm string) bool
	// RealmReadyForModel 报告某域当前对指定模型是否有可用账号（可选，若提供则优先使用模型级判定）。
	RealmReadyForModel func(realm, model string) bool

	// defaultDyn 热改覆盖值（config model_default_realm 保存时写入）；nil = 用 Default。
	defaultDyn atomic.Pointer[string]
}

// SetDefault 热替换裸名默认域策略（配置保存路径调用；值需已归一化）。
// 实例被 handler 与会话粘性闭包共享，因此必须原地更新而不是替换 resolver 实例，
// 否则两处会看到不同的域策略。
func (r *RealmResolver) SetDefault(policy string) {
	if r == nil {
		return
	}
	n := NormalizeRealmPolicy(policy)
	r.defaultDyn.Store(&n)
}

// defaultPolicy 返回当前生效的默认域策略（热改优先，回落装配期 Default）。
func (r *RealmResolver) defaultPolicy() string {
	if r == nil {
		return RealmDefaultCN
	}
	if p := r.defaultDyn.Load(); p != nil {
		return *p
	}
	return r.Default
}

// NormalizeRealmPolicy 归一化策略值：小写、去空格，接受 cn/global/auto/auto:global,cn 等。
func NormalizeRealmPolicy(defaultRealm string) string {
	d := strings.ToLower(strings.TrimSpace(defaultRealm))
	d = strings.ReplaceAll(d, " ", "")
	switch d {
	case RealmDefaultGlobal:
		return RealmDefaultGlobal
	case RealmDefaultAuto, "auto:cn,global", "cn,global":
		return RealmDefaultAuto
	case RealmDefaultAutoGlobalCN, "global,cn":
		return RealmDefaultAutoGlobalCN
	default:
		return RealmDefaultCN
	}
}

// NewRealmResolver 构造 resolver。defaultRealm 归一化：支持 cn、global、auto、auto:global,cn。
func NewRealmResolver(defaultRealm string, realmReady func(realm string) bool) *RealmResolver {
	return &RealmResolver{Default: NormalizeRealmPolicy(defaultRealm), RealmReady: realmReady}
}

func (r *RealmResolver) candidates() []string {
	if r == nil {
		return []string{RealmDefaultCN}
	}
	switch r.defaultPolicy() {
	case RealmDefaultGlobal:
		return []string{RealmDefaultGlobal}
	case RealmDefaultAutoGlobalCN:
		return []string{RealmDefaultGlobal, RealmDefaultCN}
	case RealmDefaultAuto:
		return []string{RealmDefaultCN, RealmDefaultGlobal}
	default:
		return []string{RealmDefaultCN}
	}
}

// CandidateRealms 返回模型名解析后的有序候选域列表与裸模型名。
//
// 显式前缀恒优先：显式带 "cn:" 或 "global:" 时仅返回该单一域（严禁跨域轮退）。
// 裸模型名按 Default 策略返回有序候选域切片（供主备轮退选号使用）。
func (r *RealmResolver) CandidateRealms(model string) (realms []string, bare string) {
	if idx := strings.IndexByte(model, ':'); idx >= 0 {
		prefix := model[:idx]
		if prefix == "cn" || prefix == "global" {
			return []string{prefix}, model[idx+1:]
		}
	}
	if r == nil {
		return []string{RealmDefaultCN}, model
	}
	return r.candidates(), model
}

func (r *RealmResolver) isReady(realm, bare string) bool {
	if r.RealmReadyForModel != nil && bare != "" {
		return r.RealmReadyForModel(realm, bare)
	}
	if r.RealmReady != nil {
		return r.RealmReady(realm)
	}
	return false
}

// Resolve 解析模型名 → (realm, bare)。r 为 nil 时等价于默认 cn（零回归兜底）。
func (r *RealmResolver) Resolve(model string) (realm, bare string) {
	candidates, bareModel := r.CandidateRealms(model)
	if len(candidates) <= 1 {
		if len(candidates) == 1 {
			return candidates[0], bareModel
		}
		return RealmDefaultCN, bareModel
	}
	return r.autoRealm(bareModel), bareModel
}

// autoRealm 自动判定裸名归属：按候选域优先级遍历健康状态，首个就绪者胜出；
// 若均未就绪（或无探测闭包），回落优先级首位候选域。
func (r *RealmResolver) autoRealm(bare string) string {
	candidates := r.candidates()
	if len(candidates) <= 1 {
		if len(candidates) == 1 {
			return candidates[0]
		}
		return RealmDefaultCN
	}
	for _, cand := range candidates {
		if r.isReady(cand, bare) {
			return cand
		}
	}
	return candidates[0]
}

// resolveModel 包内便捷包装（handler 用；等价于一个默认 cn 的 resolver）。
// 保留函数形态以减少调用点改动量；需要策略时用 RealmResolver。
func resolveModel(model string) (realm, bare string) {
	return (*RealmResolver)(nil).Resolve(model)
}

// ResolveModel 是 resolveModel 的导出面（默认 cn 策略）。需要 model_default_realm
// 策略的调用方请用 RealmResolver.Resolve。
func ResolveModel(model string) (realm, bare string) { return resolveModel(model) }
