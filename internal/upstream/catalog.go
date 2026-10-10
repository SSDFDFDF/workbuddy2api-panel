// catalog.go 模型目录缓存（显式刷新制）。
//
// 设计定位（**唯一事实来源**）：进程内每个域（cn / global）一份模型目录快照，
// 只在两处写入：
//
//  1. 启动预热（cmd/server.warmModelCatalog → Client.RefreshCatalog）；
//  2. 手动刷新（面板「模型能力」页「重新获取」→ Panel.models → RefreshCatalog）。
//
// 其余路径一律**只读**：
//   - 网关出站门禁（server.checkModel）：客户端请求的模型不在快照里 → 直接 404，
//     绝不为一次客户端请求去探测上游；
//   - 公开 /v1/models 与面板列表：读同一份快照。
//
// 为什么不做 TTL 懒刷新（推翻此前 handler.dynamicModelsCache 的 10min TTL 设计）：
// 目录内容变化极慢（上游产品改版才有增删），而懒刷新意味着**一次客户端请求可能
// 触发 2~4 次上游探测**（CN：/v3/config + 企业端点；global：三 UA + 企业端点家族），
// 且探测路径与 chat 抢同一账号的在途名额。改成显式刷新后：
//   - 客户端请求的路径上零上游探测，行为完全可预测；
//   - 目录为空 = 「未知」→ 门禁 fail-open（放行，由上游 11102 兜底），
//     绝不会把「没刷新过」误判成「模型不存在」而拒掉正常请求。
//
// 快照带 FetchedAt / LastErr：面板可展示「目录更新于 / 上次刷新失败原因」，
// 刷新失败**保留**旧快照（旧目录总比没有强，且不会被一次网络抖动清空）。
package upstream

import (
	"errors"
	"strings"
	"sync"
	"time"

	"workbuddy_manager/internal/auth"
)

// CatalogEntry 单个域的模型目录快照（只读副本）。
type CatalogEntry struct {
	// Infos 目录条目（上游下发顺序）；空 = 尚无可用目录（门禁 fail-open）。
	Infos []ModelInfo
	// FetchedAt 最近一次成功刷新时刻（零值 = 从未成功刷新过）。
	FetchedAt time.Time
	// LastErr 最近一次刷新失败原因（成功后被清空）；仅用于展示。
	LastErr string
}

// Empty 报告该域目录是否尚未就绪。
func (e CatalogEntry) Empty() bool { return len(e.Infos) == 0 }

// catalogCache 每域一份快照 + 最近刷新元信息。内嵌 mutex，随 Client 实例持有
// （测试新建 Client 即隔离，与 globalModels / efforts 同模式）。
type catalogCache struct {
	sync.Mutex
	entries map[string]CatalogEntry // realm → 快照
}

// Catalog 返回指定域目录快照（只读，深拷贝切片头，不触发任何上游调用）。
func (c *Client) Catalog(realm string) CatalogEntry {
	c.catalog.Lock()
	defer c.catalog.Unlock()
	e := c.catalog.entries[realm]
	if len(e.Infos) == 0 {
		return CatalogEntry{FetchedAt: e.FetchedAt, LastErr: e.LastErr}
	}
	out := make([]ModelInfo, len(e.Infos))
	copy(out, e.Infos)
	return CatalogEntry{Infos: out, FetchedAt: e.FetchedAt, LastErr: e.LastErr}
}

// LookupCatalog 在指定域目录里匹配裸模型名，返回 (规范 id, 是否命中, 目录是否有数据)。
//
// 三态语义：
//   - 命中（含大小写变体，返回目录里的规范 id）；
//   - 目录非空但无此模型 → (bare, false, true)：上游必然 11102，门禁可直接拒绝；
//   - 目录为空 → (bare, false, false)：无法判定，调用方必须 fail-open。
func (c *Client) LookupCatalog(realm, bare string) (string, bool, bool) {
	if bare == "" {
		return bare, false, false
	}
	entry := c.Catalog(realm)
	if entry.Empty() {
		return bare, false, false
	}
	for _, mi := range entry.Infos {
		if mi.ID == bare {
			return mi.ID, true, true
		}
	}
	for _, mi := range entry.Infos {
		if strings.EqualFold(mi.ID, bare) {
			return mi.ID, true, true // 大小写写错：纠正成目录值再出站
		}
	}
	return bare, false, true
}

// RefreshCatalog 显式刷新 a 所属域的模型目录快照（启动预热 / 面板手动刷新）。
//
// 探测走既有路径（CN：/v3/config + 企业端点两路并发；global：三 UA + 企业端点
// 家族），并顺带刷新该域积分倍率与 effort 能力桶（副作用与 FetchModels /
// FetchGlobalModelInfos 一致）。返回值：
//   - 成功 → 探测到的目录；
//   - 失败 → nil + error，**旧快照保留**（记 LastErr，供面板展示失败原因）；
//   - 账号域无路由（global 逃生门关闭）→ nil + error。
//
// 注意 global 域内部另有 1h 成功缓存 / 5min 负缓存（global_models.go）：
// 连续手动刷新不重复探上游，直接复用最近一次探测结果（与改动前的面板行为一致）。
func (c *Client) RefreshCatalog(a *auth.Auth) ([]ModelInfo, error) {
	if a == nil {
		return nil, errCatalogNoAccount
	}
	realm := realmKey(a.Realm())
	if realm == "global" && !c.globalOn(a) {
		return nil, errCatalogNoAccount
	}
	var (
		infos []ModelInfo
		err   error
	)
	if realm == "global" {
		// global 探测有两种形态：对象形态（infos 带全字段）与**窄表形态**
		// （只下发模型名，infos 为空）。后者历史上按裸 ID 输出（不编造字段），
		// 故此处以 names 为准补齐条目：有富字段的用富字段，没有的只留 ID。
		// 两次调用共享同一次探测（1h 缓存），不会打两遍上游。
		names := c.FetchGlobalModels(a)
		rich := c.FetchGlobalModelInfos(a)
		if len(names) == 0 {
			err = errCatalogEmpty
		} else {
			infos = catalogInfos(names, rich)
		}
	} else {
		infos, err = c.FetchModels(a)
	}
	if err != nil || len(infos) == 0 {
		if err == nil {
			err = errCatalogEmpty
		}
		c.catalog.Lock()
		c.ensureCatalogLocked()
		e := c.catalog.entries[realm]
		e.LastErr = err.Error()
		c.catalog.entries[realm] = e // 旧 Infos/FetchedAt 原样保留
		c.catalog.Unlock()
		return nil, err
	}
	c.catalog.Lock()
	c.ensureCatalogLocked()
	c.catalog.entries[realm] = CatalogEntry{Infos: infos, FetchedAt: time.Now()}
	c.catalog.Unlock()
	return infos, nil
}

// SetCatalogForTest 直接写入某域快照（测试隔离用，避免用例触发真实探测）。
// 生产路径只有 RefreshCatalog 一个写入点。
func (c *Client) SetCatalogForTest(realm string, infos []ModelInfo) {
	c.catalog.Lock()
	defer c.catalog.Unlock()
	c.ensureCatalogLocked()
	if len(infos) == 0 {
		delete(c.catalog.entries, realm)
		return
	}
	cp := make([]ModelInfo, len(infos))
	copy(cp, infos)
	c.catalog.entries[realm] = CatalogEntry{Infos: cp, FetchedAt: time.Now()}
}

// ensureCatalogLocked 惰性初始化快照表（持锁调用）。
func (c *Client) ensureCatalogLocked() {
	if c.catalog.entries == nil {
		c.catalog.entries = map[string]CatalogEntry{}
	}
}

// errCatalogNoAccount / errCatalogEmpty 刷新失败的两类原因（面板展示与日志用）。
var (
	errCatalogNoAccount = errors.New("no routable account for this realm")
	errCatalogEmpty     = errors.New("upstream returned an empty model catalog")
)

// catalogInfos 把「模型名清单 + 富字段条目」合并成快照条目列表：顺序按 names
// （上游下发顺序），富字段命中则带上，未命中只留 ID（窄表形态——不编造字段，
// 与 modelList 的裸条目输出同口径）。names 与 rich 不同源时以 names 为准，
// rich 中多出的 id 追加在后（防御：不因两份清单错位丢模型）。
func catalogInfos(names []string, rich []ModelInfo) []ModelInfo {
	byID := make(map[string]ModelInfo, len(rich))
	for _, mi := range rich {
		if mi.ID != "" {
			byID[mi.ID] = mi
		}
	}
	seen := make(map[string]bool, len(names)+len(rich))
	out := make([]ModelInfo, 0, len(byID))
	for _, id := range names {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if mi, ok := byID[id]; ok {
			out = append(out, mi)
			continue
		}
		out = append(out, ModelInfo{ID: id}) // 窄表：只有 id，不编造字段
	}
	// 防御：rich 里有、names 里没列出的 id（两份清单错位时仍保留）。
	for _, mi := range rich {
		if mi.ID == "" || seen[mi.ID] {
			continue
		}
		seen[mi.ID] = true
		out = append(out, mi)
	}
	return out
}
