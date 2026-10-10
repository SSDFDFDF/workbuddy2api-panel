// modelcheck.go 出站前的模型目录门禁（model-catalog gate，显式刷新制）。
//
// 背景：客户端可以请求任意模型名，此前网关不做任何目录校验——裸名解析后直接选号、
// 打上游，由上游用 11102「该后端无此模型」兜底。代价有两层：
//
//  1. 一个写错的模型名（或客户端自造的别名）会逐个账号去上游撞 11102：每次
//     applyErrorPolicy(ErrModelBlocked) 都把 (账号, 模型) 写进 6h 起的模型级负缓存，
//     于是「模型锁池」里凭空长出一个要等半天的条目，每个号被白打一次上游，
//     还把当次请求拖到 MaxRotate 轮；
//  2. 这些账号本可以用在真实请求上。
//
// 本文件把「模型是否存在」的裁决前移到网关：**只查目录缓存快照**
// （upstream.Client.Catalog，见 internal/upstream/catalog.go），快照可用时，
// 不存在的模型在选号之前直接 404 model_not_found；快照为空（未刷新/从未成功
// 刷新过）时 fail-open，维持既有行为——宁可放行也不能把「没刷新过」误判成
// 「模型不存在」，更不为一次客户端请求去探测上游。
//
// 目录写入点只有两个：启动预热与面板手动刷新（见 FORK_CHANGES §2）。
//
// **目录的权威边界**：快照由「该域第一个可用账号」单账号探测而来，因此它回答的是
// 「探测账号能看到什么」，不是「每个账号都能看到什么」——上游 11102 本就带账号维度
// （同一模型可能 A 号无、B 号有）。门禁据此拒绝时宁可偏严：确有一个号能服务而它不在
// 目录里的情形，客户端会被 404 挡下（旧行为是逐个号试过去、偶尔成功）。这是本设计
// 明确的取舍：把「错写的模型名 / 已下线模型」的代价从「全池各写一条 6h 负缓存」降到
// 「一次 404」，代价是丧失跨账号的目录差集宽容度。
package server

import "strings"

// ModelCatalogState 目录查询的三态结果。调用方按状态区分「模型不存在」与
// 「目录不可用」，只有前者才允许拒绝请求。
type ModelCatalogState int

const (
	// ModelCatalogKnown 目录可用且命中该模型；lookup 返回目录里的规范 id。
	ModelCatalogKnown ModelCatalogState = iota
	// ModelCatalogUnknown 目录可用但没有这个模型——上游必然 11102，可直接拒绝。
	ModelCatalogUnknown
	// ModelCatalogNoCatalog 目录不可用（快照为空/上游未接线）——无法判定，
	// 必须 fail-open（放行），由上游 11102 兜底。
	ModelCatalogNoCatalog
)

// lookupModelCatalog 查指定域的目录快照并匹配裸模型名（纯读，零上游调用）。
//
// 返回 (canonical, state)：
//   - Known     → canonical 为目录中的规范 id（大小写写错也在这里纠正）；
//   - Unknown   → 目录可用但没有这个模型；
//   - NoCatalog → 目录为空/上游未接线，调用方必须放行。
func (h *Handler) lookupModelCatalog(realm, bare string) (string, ModelCatalogState) {
	if h == nil || bare == "" || h.cfg.Upstream == nil {
		return bare, ModelCatalogNoCatalog
	}
	id, ok, ready := h.cfg.Upstream.LookupCatalog(realm, bare)
	if !ready {
		return bare, ModelCatalogNoCatalog
	}
	if ok {
		return id, ModelCatalogKnown
	}
	return bare, ModelCatalogUnknown
}

// checkModel 用目录快照校验候选域列表，返回（重排后的候选域, 规范化模型名, 是否拒绝）。
//
// 三种结果：
//   - 命中（任一候选域目录里有该模型）：候选域重排为「命中域优先、其余按原优先级
//     跟随」。global-only 的模型不再先拿 CN 号去撞一次 11102（那会给每个 CN 号写
//     6h 模型负缓存），反之亦然。
//   - 拒绝：所有候选域的目录都可用、且都没有这个模型 → 真不存在，直接 404，
//     一个账号都不碰。
//   - 放行（fail-open）：任一候选域目录为空（未刷新过）→ 无法判定，交回上游兜底。
func (h *Handler) checkModel(candidateRealms []string, bare string) ([]string, string, bool) {
	if len(candidateRealms) == 0 || bare == "" || h == nil {
		return candidateRealms, bare, false
	}
	known := make([]string, 0, len(candidateRealms))
	canonical := ""
	unknown, noCatalog := 0, 0
	for _, realm := range candidateRealms {
		id, state := h.lookupModelCatalog(realm, bare)
		switch state {
		case ModelCatalogKnown:
			known = append(known, realm)
			if canonical == "" {
				// 首个命中域（选号优先级最高）的目录拼写即规范值：出站模型名
				// 必须与该域目录逐字一致，不能拿后面域的拼写去写第一优先域。
				canonical = id
			}
		case ModelCatalogUnknown:
			unknown++
		default:
			noCatalog++
		}
	}
	if len(known) > 0 {
		ordered := make([]string, 0, len(candidateRealms))
		ordered = append(ordered, known...)
		for _, realm := range candidateRealms {
			if !containsRealm(known, realm) {
				ordered = append(ordered, realm)
			}
		}
		return ordered, canonical, false
	}
	if unknown > 0 && noCatalog == 0 {
		return candidateRealms, bare, true
	}
	return candidateRealms, bare, false
}

// modelNotFoundCode 404 model_not_found 的 error.code（客户端据此判定不可重试）。
const modelNotFoundCode = "model_not_found"

// modelNotFoundHint 404 model_not_found 的 gateway_hint：把「为什么拒绝」和
// 「去哪拿可用名单」讲清楚，客户端不必猜，也不会当成可重试错误。
func modelNotFoundHint(bare string, realms []string) string {
	return "model " + bare + " is not in the upstream model catalog for realm(s): " +
		strings.Join(realms, ", ") + "; call GET /v1/models for available ids"
}
