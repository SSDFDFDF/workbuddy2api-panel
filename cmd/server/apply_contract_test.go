// apply_contract_test.go 热应用契约的守护测试：internal/config 的字段目录
// 说"即时生效"的字段，saveConfig 第 4 步必须真的应用它。
//
// 为什么需要：目录（catalog.go）与热应用代码（saveConfig）是两个地方。
// 目录说 Hot 而没人应用 → 面板显示"已保存并立即生效"，实际值没进运行期对象，
// 这正是 session_sticky.enabled 曾经那类 bug 的另一种形态（静默不生效）。
// 反过来，这里声明了但目录标 Restart → 声明是噪音，也应清掉。
package main

import (
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/config"
)

// hotAppliedPaths 声明 saveConfig 实际热应用的配置路径（末尾 ".*" = 整组热应用）。
//
// 每一项都必须对应 saveConfig 里真实存在的应用动作（setter / 原子快照 Store），
// 不是"打算做"。P4 逐组件热化时，这里加一行、catalog.go 同步把 Mode 改成 Hot，
// 两处一起改才算完成——测试会拦住只改一处的半成品。
var hotAppliedPaths = []string{
	// config/runtime 快照（面板与服务共享的鉴权/冷却/来源记录）
	"api_key",
	"cooldown.soft_rate",
	"cooldown.soft_rate_max",
	"logging.request_client_info",
	// 出站指纹改写层（不可变层整体替换）
	"fingerprint_rewrite",
	"fingerprint_rules",
	// 媒体策略（进程级原子快照）
	"media.tool_images",
	"media.image_transcode",
	"media.image_max_dimension",
	// 账号池参数（pool 提供整组 setter）
	"pool.*",
	// 排程参数（scheduler.Reconfigure / SetBalanceInterval / …）
	"schedule.*",
}

// TestApplyClaimsEveryHotField 目录里 Hot 且非 Passive 的字段必须被契约声明覆盖。
func TestApplyClaimsEveryHotField(t *testing.T) {
	cat := config.Entries()
	for _, leaf := range config.ConfigLeafPaths() {
		f, ok := cat.Lookup(leaf)
		if !ok {
			continue // 覆盖性由 internal/config 的 TestCatalogCoversEveryConfigLeaf 负责
		}
		if f.Mode != config.Hot || f.Passive {
			continue
		}
		if !claimedByAny(hotAppliedPaths, leaf) {
			t.Errorf("目录说 %s（%s）即时生效，但 saveConfig 没有对应的热应用动作：\n"+
				"  要么在 saveConfig 里接上 setter，要么把 catalog.go 标成 Restart（并写明 Why）",
				leaf, f.Path)
		}
	}
}

// TestApplyContractHasNoGhosts 契约里声明的每一项都必须真的对应一个 Hot 字段：
// 幽灵声明会给人"这个已经热化了"的错觉，而实际上目录说它要重启。
func TestApplyContractHasNoGhosts(t *testing.T) {
	cat := config.Entries()
	for _, claim := range hotAppliedPaths {
		hit := false
		for _, leaf := range config.ConfigLeafPaths() {
			if !config.MatchPath(claim, leaf) {
				continue
			}
			f, ok := cat.Lookup(leaf)
			if ok && f.Mode == config.Hot && !f.Passive {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("hotAppliedPaths 里的 %q 不对应任何 Hot 字段（字段已删/改名，或目录已改回 Restart）", claim)
		}
	}
}

// TestRestartFieldsOnlyFromDiff 保存提示只报"真的改了"的字段：
// 这是"只改一个热字段也回 20 项需重启"那个 bug 的回归测试。
func TestRestartFieldsOnlyFromDiff(t *testing.T) {
	old, err := config.ParseConfig([]byte(`{"listen":":1","pool":{"max_in_flight":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	// 只改一个热字段。
	next, err := config.ParseConfig([]byte(`{"listen":":1","pool":{"max_in_flight":9}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := restartRequiredFields(old, next); len(got) != 0 {
		t.Errorf("只改热字段不该报需重启，实际 %v", got)
	}

	// 改一个真需重启的字段：只报它，不报邻居。
	next2, err := config.ParseConfig([]byte(`{"listen":":1","auth_dir":"./other","pool":{"max_in_flight":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := restartRequiredFields(old, next2)
	if len(got) != 1 || got[0] != "auth_dir" {
		t.Errorf("应只报 auth_dir，实际 %v", got)
	}

	// 旧配置不可解析（nil）→ 保守回退为全量 Restart 清单。
	all := restartRequiredFields(nil, next2)
	if len(all) < 10 {
		t.Errorf("旧配置不可解析时应回退为全量清单，实际只报 %v", all)
	}
}

// TestRestartFieldsCoverSessionStickyEnabled 回归 session_sticky.enabled：
// 面板上取消勾选它曾经显示"已保存"却零效果且无提示。
func TestRestartFieldsCoverSessionStickyEnabled(t *testing.T) {
	old, err := config.ParseConfig([]byte(`{"session_sticky":{"enabled":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	next, err := config.ParseConfig([]byte(`{"session_sticky":{"enabled":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := restartRequiredFields(old, next)
	found := false
	for _, f := range got {
		if f == "session_sticky.enabled" {
			found = true
		}
	}
	if !found {
		t.Errorf("session_sticky.enabled 改动必须提示需重启（装配期构造会话路由器），实际 %v", got)
	}
}

// claimedByAny 报告 leaf 是否被声明列表中的某一项覆盖。
func claimedByAny(claims []string, leaf string) bool {
	for _, c := range claims {
		if config.MatchPath(c, leaf) {
			return true
		}
	}
	return false
}
