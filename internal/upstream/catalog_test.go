// catalog_test.go 模型目录快照（显式刷新制）的单元回归。
//
// 契约（与 server/modelcheck.go 的门禁共用）：
//   - 快照为空 → LookupCatalog 报「目录未就绪」（调用方 fail-open）；
//   - 快照非空 → 命中/未命中二态，且大小写变体返回目录里的规范 id；
//   - 窄表形态（只有模型名、没有富字段）也要进快照（裸 ID 条目，不编造字段）；
//   - 刷新失败保留旧快照并记 LastErr。
package upstream

import (
	"testing"
)

func TestCatalogLookupThreeStates(t *testing.T) {
	c := New()
	c.SetCatalogForTest("cn", []ModelInfo{{ID: "glm-5.2"}, {ID: "kimi-k3", SupportsImages: true}})

	if _, _, ready := c.LookupCatalog("cn", "glm-5.2"); !ready {
		t.Fatal("catalog should be ready")
	}
	if id, ok, ready := c.LookupCatalog("cn", "kimi-k3"); !ok || !ready || id != "kimi-k3" {
		t.Errorf("exact hit: id=%q ok=%v ready=%v", id, ok, ready)
	}
	// 大小写变体 → 命中并返回目录规范值（客户端写错大小写不再撞 11102）。
	if id, ok, _ := c.LookupCatalog("cn", "GLM-5.2"); !ok || id != "glm-5.2" {
		t.Errorf("case-insensitive hit: id=%q ok=%v", id, ok)
	}
	// 目录非空但无此模型 → 未命中且 ready（调用方可拒绝）。
	if _, ok, ready := c.LookupCatalog("cn", "no-such-model"); ok || !ready {
		t.Errorf("unknown model: ok=%v ready=%v (want false/true)", ok, ready)
	}
	// 另一域未刷新 → 未就绪（fail-open）。
	if _, _, ready := c.LookupCatalog("global", "glm-5.2"); ready {
		t.Error("unrefreshed realm must report not-ready so the gate fails open")
	}
	// 富字段随快照透出（supports_images 等由 modelList 读取）。
	for _, mi := range c.Catalog("cn").Infos {
		if mi.ID == "kimi-k3" && !mi.SupportsImages {
			t.Error("rich fields must survive the snapshot round-trip")
		}
	}
}

func TestCatalogInfosFillsBareIDsForNarrowTable(t *testing.T) {
	// 窄表形态：names 有 3 个模型，富字段只有 1 个 → 其余按裸 ID 入表
	// （不编造字段，与 /v1/models 的裸条目输出同口径）。
	infos := catalogInfos(
		[]string{"a-model", "b-model", "c-model"},
		[]ModelInfo{{ID: "b-model", ContextWindow: 200000}},
	)
	if len(infos) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(infos), infos)
	}
	if infos[0].ID != "a-model" || infos[1].ID != "b-model" || infos[2].ID != "c-model" {
		t.Fatalf("order must follow names: %+v", infos)
	}
	if infos[0].ContextWindow != 0 {
		t.Errorf("bare id entry must not fabricate fields: %+v", infos[0])
	}
	if infos[1].ContextWindow != 200000 {
		t.Errorf("rich entry must keep its fields: %+v", infos[1])
	}
	// 防御：rich 里有、names 里没有的 id 也不丢；重复 id 只保留一次。
	infos = catalogInfos([]string{"a", "a"}, []ModelInfo{{ID: "a"}, {ID: "z"}})
	if len(infos) != 2 || infos[0].ID != "a" || infos[1].ID != "z" {
		t.Fatalf("dedupe/append broken: %+v", infos)
	}
}
