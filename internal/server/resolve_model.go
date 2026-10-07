package server

import "strings"

// 裸模型名的默认域策略（config model_default_realm）。
const (
	// RealmDefaultCN 裸名归 CN 域（历史默认，零回归）。
	RealmDefaultCN = "cn"
	// RealmDefaultGlobal 裸名归 global 域（纯国际版部署用，免写前缀）。
	RealmDefaultGlobal = "global"
	// RealmDefaultAuto 按池内可用账号域自动判定（只在一个域有可用号时归该域，
	// 两域都有或都没有时回落 cn）。
	RealmDefaultAuto = "auto"
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
	// Default 裸名默认域：cn / global / auto（空/非法视为 cn）。
	Default string
	// RealmReady 报告某域当前是否有可用账号（auto 判定用）。nil = 两域都视为可用
	// （auto 退化为 cn，安全默认）。
	RealmReady func(realm string) bool
}

// NewRealmResolver 构造 resolver。defaultRealm 归一化：非 global/auto 一律 cn。
func NewRealmResolver(defaultRealm string, realmReady func(realm string) bool) *RealmResolver {
	d := strings.ToLower(strings.TrimSpace(defaultRealm))
	if d != RealmDefaultGlobal && d != RealmDefaultAuto {
		d = RealmDefaultCN
	}
	return &RealmResolver{Default: d, RealmReady: realmReady}
}

// Resolve 解析模型名 → (realm, bare)。r 为 nil 时等价于默认 cn（零回归兜底）。
func (r *RealmResolver) Resolve(model string) (realm, bare string) {
	if idx := strings.IndexByte(model, ':'); idx >= 0 {
		prefix := model[:idx]
		if prefix == "cn" || prefix == "global" {
			return prefix, model[idx+1:]
		}
	}
	if r == nil {
		return RealmDefaultCN, model
	}
	switch r.Default {
	case RealmDefaultGlobal:
		return RealmDefaultGlobal, model
	case RealmDefaultAuto:
		return r.autoRealm(), model
	default:
		return RealmDefaultCN, model
	}
}

// autoRealm 自动判定裸名归属：只在一个域有可用账号时归该域，其余（两域都有/都没有）
// 回落 cn——绝不能因判定摇摆让同一裸名在两次请求间换域。
func (r *RealmResolver) autoRealm() string {
	if r.RealmReady == nil {
		return RealmDefaultCN
	}
	cnReady := r.RealmReady(RealmDefaultCN)
	globalReady := r.RealmReady(RealmDefaultGlobal)
	if globalReady && !cnReady {
		return RealmDefaultGlobal
	}
	return RealmDefaultCN
}

// resolveModel 包内便捷包装（handler 用；等价于一个默认 cn 的 resolver）。
// 保留函数形态以减少调用点改动量；需要策略时用 RealmResolver。
func resolveModel(model string) (realm, bare string) {
	return (*RealmResolver)(nil).Resolve(model)
}

// ResolveModel 是 resolveModel 的导出面（默认 cn 策略）。需要 model_default_realm
// 策略的调用方请用 RealmResolver.Resolve。
func ResolveModel(model string) (realm, bare string) { return resolveModel(model) }
