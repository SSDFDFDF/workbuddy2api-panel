// fingerprint.go 指纹救场：派生键粘性断链时的窗口匹配找回。
//
// 要解决的问题：ExtractKey/BodyKey 的派生键（deriveKey）正确性依赖「同一会话
// 每轮请求中 system 与首条 user 恒定」。system 带时间戳/动态注入的客户端每轮
// 派生键都变 → 粘性完全失效（比不粘更糟：每轮被当新会话重新选号）；客户端
// fork/编辑历史后继续同理断链。窗口匹配对这类历史突变更鲁棒——只要把 system
// 排除出匹配材料，user/assistant 历史每轮天然重叠。
//
// 误配代价与容错边界：匹配错只是两个会话暂钉同一账号（负载倾斜 + TTL 内自愈，
// 非正确性问题），因此阈值可以放宽。
//
// 单次搭便车：救场命中后**不借用候选键**，由 handler 把候选的账号 Bind 到本
// 请求自己的派生键上。误配影响从「持久吸合」降级为「只影响当前一跳」——下一
// 轮自己的键已有绑定，救场不再参与。
//
// 匹配材料契约（对齐 userContentSignature 的稳定性口径）：
//   - 只取 user/assistant/tool 消息（system/developer 排除——它们正是漂移源）；
//   - 非文本 part 只入 "[type]" 占位不入内容摘要——同一逻辑图片的 URL 逐轮
//     变化不得破坏指纹稳定性（fork 契约：image url changes must not break
//     derived key stability，同一契约延伸到指纹层）；
//   - 指纹只作网关侧救场材料，绝不进入上游 X-Conversation-ID / 缓存键材料
//     （语义隔离契约与 clientkeys.go 一致）。
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// DerivedKeyPrefix 派生键前缀（"d-"）的只读出口：handler 用它判定 convKey 是否
// 来自内容派生——救场与指纹记录只作用于派生键会话（显式键不会漂移，无需救场）。
const DerivedKeyPrefix = derivedKeyPrefix

// 救场参数。
const (
	// fpMaxTrailing 每会话缓存末尾多少条消息的指纹（时间序）。
	fpMaxTrailing = 10
	// fpMinHits 窗口命中阈值：候选与当前请求的重叠指纹数下限。
	fpMinHits = 2
	// fpMaxCandidates 单次救场最多核验的候选数（按最近匹配优先截断）。
	fpMaxCandidates = 4
	// fpMaxConvs 反向索引容纳的最大候选会话数（超限逐出最旧）。
	fpMaxConvs = 512
	// fpIndexTTL 候选指纹的保鲜期（与粘性 TTL 同量级略长：救场发生在绑定
	// 已断链之后，材料要比绑定活得久才有意义）。
	fpIndexTTL = 45 * time.Minute
	// fpBodyMaxBytes 救场解析 body 的长度上限：派生键路径本就可能来自大 body
	//（全量历史），但超过 1MB 的请求逐条哈希成本失控，直接放弃救场。
	fpBodyMaxBytes = 1 << 20
)

// fpHash 短指纹：sha256 前 8 字节 16 hex（指纹长度无需抗碰撞到 64 hex——
// 候选集已按 principal+model 收窄，16 hex 足够区分）。
func fpHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:8])
}

// msgFingerprint 单条消息指纹：role + 内容签名的短哈希。内容签名复用
// userContentSignature（图片 URL 漂移免疫契约）；签名为空（纯 null/空串/
// 解析失败）返回 ""——空指纹会跨会话互撞，不入材料。
func msgFingerprint(role string, content any) string {
	sig := userContentSignature(content)
	if sig == "" {
		return ""
	}
	return fpHash(role + "\x00" + sig)
}

// historyFingerprints 提取请求消息列表末尾 N 条（user/assistant/tool）的指纹，
// 时间序返回。system/developer 排除——它们正是漂移源；角色缺失/未知同样跳过
//（不做贡献）。无可用消息返回 nil。
func historyFingerprints(obj map[string]any) []string {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return nil
	}
	fps := make([]string, 0, fpMaxTrailing)
	for i := len(msgs) - 1; i >= 0 && len(fps) < fpMaxTrailing; i-- {
		msg, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		var role string
		switch strOrEmpty(msg["role"]) {
		case "user", "assistant", "tool":
			role = strOrEmpty(msg["role"])
		default:
			continue // system/developer/未知角色不入匹配材料
		}
		if fp := msgFingerprint(role, msg["content"]); fp != "" {
			fps = append(fps, fp)
		}
	}
	// 倒序收集 → 反转成时间序（旧 → 新）。
	for i, j := 0, len(fps)-1; i < j; i, j = i+1, j-1 {
		fps[i], fps[j] = fps[j], fps[i]
	}
	if len(fps) == 0 {
		return nil
	}
	return fps
}

// fpCandidate 反向索引里的候选会话：一个曾经的派生键会话的指纹窗口快照。
type fpCandidate struct {
	key    string              // 该会话的完整粘性键（principal:realm:model:convKey）
	scope  string              // 所属命名空间（principalID + "\x00" + bareModel）
	fps    map[string]struct{} // 该会话末尾窗口的指纹集
	expire time.Time
}

// FingerprintIndex 消息指纹反向索引：msgFingerprint → 含该消息的候选会话。
// 只存派生键会话的成功窗口（RecordFingerprint 在上游成功后调用），救场时按
// principal+model 收窄候选再窗口比对。纯内存、进程内：丢了只是救场失效
//（降级为普通轮换），无需持久化。
type FingerprintIndex struct {
	enabled atomic.Bool
	mu      sync.Mutex
	router  *Router // 只用其 PeekForModel（只读借用校验）；锁序：先 f.mu 后 r.mu（rescue 已先解锁）
	byMsg   map[string][]*fpCandidate
	byKey   map[string]*fpCandidate
}

func newFingerprintIndex(r *Router) *FingerprintIndex {
	return &FingerprintIndex{
		router: r,
		byMsg:  map[string][]*fpCandidate{},
		byKey:  map[string]*fpCandidate{},
	}
}

// setEnabled 开关救场。关停时清空索引（已有候选全部作废，重新开启从零积累）。
func (f *FingerprintIndex) setEnabled(on bool) {
	if on {
		f.enabled.Store(true)
		return
	}
	f.enabled.Store(false)
	f.mu.Lock()
	f.byMsg = map[string][]*fpCandidate{}
	f.byKey = map[string]*fpCandidate{}
	f.mu.Unlock()
}

// record 记录一个派生键会话成功后的消息窗口。scope = principalID + "\x00" +
// bareModel（handler 拼装）；sessKey 是完整粘性键。窗口不足 1 条（无可指纹
// 消息）不记录。
//
// 首轮（单条 user）也记录：年轻会话单命中规则（见 rescue）依赖 turn1 的窗口
// 快照才能在 turn2 命中——这是 system 漂移场景唯一的早期救场机会。
func (f *FingerprintIndex) record(sessKey, scope string, obj map[string]any) {
	if f == nil || obj == nil || !f.enabled.Load() {
		return
	}
	fps := historyFingerprints(obj)
	if len(fps) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	exp := now.Add(fpIndexTTL)
	if c, ok := f.byKey[sessKey]; ok {
		old := c.fps
		c.fps = fpSet(fps)
		c.expire = exp
		// 新增指纹入索引（去重移尾）；已滑出窗口的指纹摘除本候选——
		// byMsg 列表不随对话轮数膨胀。
		for fp := range c.fps {
			f.appendCandidate(scope + "\x00" + fp, c)
		}
		for fp := range old {
			if _, ok := c.fps[fp]; !ok {
				f.removeCandidate(scope+"\x00"+fp, c)
			}
		}
		return
	}
	// 容量驱逐：顺带清过期；仍超限则逐出最旧（expire 最小）。
	if len(f.byKey) >= fpMaxConvs {
		var oldest *fpCandidate
		for key, c := range f.byKey {
			if now.After(c.expire) {
				f.removeLocked(key, c)
				continue
			}
			if oldest == nil || c.expire.Before(oldest.expire) {
				oldest = c
			}
		}
		if len(f.byKey) >= fpMaxConvs && oldest != nil {
			f.removeLocked(oldest.key, oldest)
		}
	}
	c := &fpCandidate{key: sessKey, scope: scope, fps: fpSet(fps), expire: exp}
	f.byKey[sessKey] = c
	for fp := range c.fps {
		k := scope + "\x00" + fp
		f.byMsg[k] = append(f.byMsg[k], c)
	}
}

// rescue 指纹救场：解析 body、提取查询窗口、在 scope 命名空间内找窗口重叠
// 达标的候选，经 PeekForModel 只读校验其绑定仍有效后返回该账号 uid。
// 只读（绝不走慢路径分配——那会给候选键写一条无主绑定）；命中后由调用方
// 把 uid Bind 到请求自己的键上（单次搭便车）。未命中返回 ok=false。
func (f *FingerprintIndex) rescue(body []byte, scope, model string) (string, bool) {
	if f == nil || !f.enabled.Load() || len(body) == 0 || len(body) > fpBodyMaxBytes {
		return "", false
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", false
	}
	fps := historyFingerprints(obj)
	if len(fps) == 0 {
		return "", false
	}
	f.mu.Lock()
	now := time.Now()
	// 候选收集：从最新指纹往回扫（遍历顺序 ≈ 最近匹配优先），去重 + 截断。
	// 过期/已驱逐的候选顺手跳过（byKey 校验兼防悬挂指针）。
	seen := make(map[*fpCandidate]bool, 8)
	cands := make([]*fpCandidate, 0, fpMaxCandidates)
	for i := len(fps) - 1; i >= 0 && len(cands) < fpMaxCandidates; i-- {
		for _, c := range f.byMsg[scope+"\x00"+fps[i]] {
			if seen[c] {
				continue
			}
			seen[c] = true
			if now.After(c.expire) || f.byKey[c.key] != c {
				continue
			}
			cands = append(cands, c)
			if len(cands) >= fpMaxCandidates {
				break
			}
		}
	}
	if len(cands) == 0 {
		f.mu.Unlock()
		return "", false
	}
	// 命中数比对：候选窗口集 ∩ 查询窗口。阈值 ≥ fpMinHits；年轻会话（查询
	// 窗口 ≤3 条）≥1 即可——多轮早期只有首条 user 与 turn1 快照重叠，且
	// scope 已按 principal+model 收窄，单命中误配的代价是单跳搭错车，可接受。
	qset := make(map[string]struct{}, len(fps))
	for _, fp := range fps {
		qset[fp] = struct{}{}
	}
	var best *fpCandidate
	bestHits := 0
	for _, c := range cands {
		hits := 0
		for fp := range c.fps {
			if _, ok := qset[fp]; ok {
				hits++
			}
		}
		if hits > bestHits {
			bestHits = hits
			best = c
		}
	}
	minHits := fpMinHits
	if len(fps) <= 3 {
		minHits = 1
	}
	if best == nil || bestHits < minHits {
		f.mu.Unlock()
		return "", false
	}
	key := best.key
	f.mu.Unlock() // PeekForModel 取 r.mu：先放 f.mu 再调，保持锁序 f.mu → r.mu 不嵌套
	uid, ok := f.router.PeekForModel(key, model)
	if !ok {
		return "", false
	}
	return uid, true
}

// appendCandidate 把候选挂到消息键的列表末尾（已存在则移尾：最近使用优先）。
// 调用方持 f.mu。
func (f *FingerprintIndex) appendCandidate(k string, c *fpCandidate) {
	list := f.byMsg[k]
	for i, e := range list {
		if e == c {
			f.byMsg[k] = append(append(list[:i:i], list[i+1:]...), c)
			return
		}
	}
	f.byMsg[k] = append(list, c)
}

// removeCandidate 从消息键列表摘除候选。调用方持 f.mu。
func (f *FingerprintIndex) removeCandidate(k string, c *fpCandidate) {
	list := f.byMsg[k]
	for i, e := range list {
		if e == c {
			f.byMsg[k] = append(list[:i:i], list[i+1:]...)
			return
		}
	}
}

// removeLocked 从 byKey 删除候选并清理其全部消息键悬挂引用。调用方持 f.mu。
func (f *FingerprintIndex) removeLocked(key string, c *fpCandidate) {
	if f.byKey[key] != c {
		return
	}
	delete(f.byKey, key)
	for fp := range c.fps {
		f.removeCandidate(c.scope+"\x00"+fp, c)
	}
}

func fpSet(fps []string) map[string]struct{} {
	set := make(map[string]struct{}, len(fps))
	for _, fp := range fps {
		set[fp] = struct{}{}
	}
	return set
}

// ─────────────────────────────────────────────────────────────────────────────
// Router 侧出口（fingerprint_enabled 配置的热改与调用面）
// ─────────────────────────────────────────────────────────────────────────────

// setFingerprint 应用指纹救场开关（reconfigure 调用；开关底层使用 atomic）。
func (r *Router) setFingerprint(on bool) {
	if r == nil || r.fp == nil {
		return
	}
	r.fp.setEnabled(on)
}

// FingerprintEnabled 报告指纹救场是否启用（handler 救场前的前置检查）。
func (r *Router) FingerprintEnabled() bool {
	if r == nil || !r.enabled.Load() || r.fp == nil {
		return false
	}
	return r.fp.enabled.Load()
}

// RecordFingerprint 记录派生键会话成功后的消息窗口（handler 成功路径调用）。
// scope 维度 = principalID + bareModel（principalID 为空串表示网关未配 key——
// 所有调用方共享一个命名空间，与粘性键的 principal 维度缺省行为一致）。
func (r *Router) RecordFingerprint(sessKey, principalID, model string, obj map[string]any) {
	if r == nil || !r.enabled.Load() || r.fp == nil {
		return
	}
	r.fp.record(sessKey, principalID+"\x00"+model, obj)
}

// RescueDerivedKey 指纹救场：派生键粘性未命中时，用消息窗口匹配找回该会话
// 之前使用的账号。只读（PeekForModel，不产生新绑定）；命中后由调用方 Bind
// 自己的键。body 是入站原始字节（与 BodyKey 同源）。未命中/禁用返回 ok=false。
// 两个模型维度分工：scopeModel 进候选命名空间（bareModel，与粘性键同口径），
// upstreamModel 用于账号可用性校验（peek.Model，带 realm 解析，与
// ResolveForModel 同口径）。
func (r *Router) RescueDerivedKey(body []byte, principalID, scopeModel, upstreamModel string) (string, bool) {
	if r == nil || !r.enabled.Load() || r.fp == nil {
		return "", false
	}
	return r.fp.rescue(body, principalID+"\x00"+scopeModel, upstreamModel)
}
