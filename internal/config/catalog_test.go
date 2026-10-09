// catalog_test.go 字段目录的守护测试。
//
// 这些测试的职责不是"验证某个字段现在的取值"，而是**保证目录不会漏、不会错、
// 不会自相矛盾**——目录是面板徽标与保存提示的唯一数据源，一旦漏登记，
// 用户看到的就是"保存成功但没生效"（这正是 session_sticky.enabled 曾经的 bug）。
package config

import (
	"strings"
	"testing"
)

// TestMatchPathWildcard 通配语义的唯一定义处（catalog.go 的 MatchPath）：
// 末尾 ".*" = 子树全部叶子（递归），不含节点自身；其余为精确匹配。
func TestMatchPathWildcard(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"upstream.profiles.*", "upstream.profiles.cn.client_version", true},
		{"upstream.profiles.*", "upstream.profiles.cn.user_agents.chat", true}, // 递归
		{"upstream.profiles.*", "upstream.profiles", false},                    // 节点自身不是叶子
		{"upstream.profile*", "upstream.profiles.cn", false},                   // 通配只认整段 ".*"
		{"pool.*", "pool.max_in_flight", true},
		{"pool.*", "pool.breaker.cooldown", true}, // 递归（当前 pool 无嵌套对象，语义仍须一致）
		{"pool.*", "poolish.max_in_flight", false},
		{"listen", "listen", true},
		{"listen", "listen_extra", false}, // 不做前缀匹配
		{"listen", "server.listen", false},
	} {
		if got := MatchPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchPath(%q, %q)=%v want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// TestCatalogCoversEveryConfigLeaf Config 的每个叶子都必须被目录覆盖，且模式一致。
//
// 这是"新增配置字段必须登记生效方式"的强制闸门：漏登记 = 测试失败，
// 而不是等用户发现"面板说保存成功但没生效"。
func TestCatalogCoversEveryConfigLeaf(t *testing.T) {
	cat := Entries()
	seen := map[string]bool{}
	for _, f := range cat {
		if seen[f.Path] {
			t.Errorf("目录项重复：%s", f.Path)
		}
		seen[f.Path] = true
		if f.Path == "" {
			t.Error("目录项路径为空")
		}
		if f.Mode == Restart && f.Why == "" {
			t.Errorf("%s 标为需重启但没写原因（Why 会展示给用户，必须解释为什么）", f.Path)
		}
		if f.Mode == Hot && f.Passive && f.Why != "" {
			t.Errorf("%s 是 Passive 热字段，Why 应留空（无重启语义）", f.Path)
		}
		// Restart 项不允许 Passive：Passive 的定义就是"无需动作即生效"。
		if f.Mode == Restart && f.Passive {
			t.Errorf("%s 同时标了 Restart 与 Passive，语义矛盾", f.Path)
		}
	}

	for _, leaf := range ConfigLeafPaths() {
		if reason, exempt := nonUserLeaves[leaf]; exempt {
			if reason == "" {
				t.Errorf("豁免项 %s 没写理由", leaf)
			}
			continue
		}
		hits := cat.LookupAll(leaf)
		if len(hits) == 0 {
			t.Errorf("字段 %s 未登记生效方式：请在 catalog.go 里声明 Hot 还是 Restart（漏登记会让面板无法提示）", leaf)
		}
	}
}

// TestCatalogEntriesExistInConfig 反向守护：目录里的每一项都必须真的对应
// Config 里的某个叶子。防的是"字段改名/删除后目录留了幽灵项"——
// 幽灵项会让重启清单永远多报一项。
func TestCatalogEntriesExistInConfig(t *testing.T) {
	leaves := ConfigLeafPaths()
	for _, f := range Entries() {
		found := false
		for _, leaf := range leaves {
			if MatchPath(f.Path, leaf) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("目录项 %s 在 Config 里不存在（字段已改名/删除？）：它会让重启清单永远多报一项", f.Path)
		}
	}
}

// TestCatalogModesConsistent 同一叶子不允许被两个模式不同的目录项覆盖。
//
// 防的是"加了 server.* 通配又把 server.read_timeout 单列为 Restart"这类打架：
// diff 得到的路径经 Lookup 只有一个答案，两个来源不一致时行为取决于顺序。
func TestCatalogModesConsistent(t *testing.T) {
	cat := Entries()
	for _, leaf := range ConfigLeafPaths() {
		hits := cat.LookupAll(leaf)
		if len(hits) < 2 {
			continue
		}
		first := hits[0]
		for _, h := range hits[1:] {
			if h.Mode != first.Mode || h.Passive != first.Passive {
				t.Errorf("字段 %s 被多个目录项以不同模式覆盖：%s(%s) vs %s(%s)",
					leaf, first.Path, first.Mode, h.Path, h.Mode)
			}
		}
	}
}

// TestCatalogRestartPathsCoverSessionEnabled 回归：session_sticky 三项
// （尤其 enabled）必须都在需重启集合内。
//
// 历史 bug：enabled 既不在 restartRequiredFields 里，面板也没徽标——
// 用户取消勾选后显示"配置已保存"，实际粘性路由照旧。
func TestCatalogRestartPathsCoverSessionEnabled(t *testing.T) {
	cat := Entries()
	for _, p := range []string{
		"session_sticky.enabled",
		"session_sticky.ttl",
		"session_sticky.gc_interval",
	} {
		if !cat.IsRestartPath(p) {
			t.Errorf("%s 必须是 Restart（装配期构造会话路由器），否则面板改了不会生效也不提示", p)
		}
	}
}

// TestDiffConfigOnlyChangedLeaves 差异必须真的算出来：未改动的字段不能出现。
func TestDiffConfigOnlyChangedLeaves(t *testing.T) {
	base, err := ParseConfig([]byte(`{"listen":":1","pool":{"max_in_flight":3},"schedule":{"checkin_hours":[9,21]}}`))
	if err != nil {
		t.Fatal(err)
	}

	// 只改 pool.max_in_flight：listen / schedule 不能出现。
	next, err := ParseConfig([]byte(`{"listen":":1","pool":{"max_in_flight":7},"schedule":{"checkin_hours":[9,21]}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := DiffConfig(base, next)
	if !d.Has("pool.max_in_flight") {
		t.Errorf("改动的字段没被检出：%v", d.Slice())
	}
	for _, unexpected := range []string{"listen", "schedule.checkin_hours", "api_key"} {
		if d.Has(unexpected) {
			t.Errorf("未改动的 %s 出现在差异里：%v", unexpected, d.Slice())
		}
	}
	if d.Len() != 1 {
		t.Errorf("差异应恰为 1 项，实际 %d：%v", d.Len(), d.Slice())
	}
}

// TestDiffConfigNestedAndMapLeaves 嵌套对象与 map 的键位要展开成具体叶子路径
// （这样保存提示能精确到 upstream.profiles.cn.client_version，而不是笼统的
// "upstream.profiles"）。
func TestDiffConfigNestedAndMapLeaves(t *testing.T) {
	base, err := ParseConfig([]byte(`{"upstream":{"profiles":{"global":{"client_version":"1.0.0"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseConfig([]byte(`{"upstream":{"profiles":{"global":{"client_version":"2.0.0"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := DiffConfig(base, next)
	if !d.Has("upstream.profiles.global.client_version") {
		t.Errorf("map 键位未展开：%v", d.Slice())
	}
	// 未改动的兄弟字段（cli_version 等）不能出现。
	for _, p := range d.Slice() {
		if !strings.HasSuffix(p, "global.client_version") {
			t.Errorf("无关字段进入差异：%s", p)
		}
	}

	// 整个 realm 被删除：该键下的全部叶子都算改动。
	next2, err := ParseConfig([]byte(`{"upstream":{"profiles":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	d2 := DiffConfig(base, next2)
	if !d2.Has("upstream.profiles.global.client_version") {
		t.Errorf("删除 map 键未展开成叶子差异：%v", d2.Slice())
	}
}

// TestDiffConfigNilTreatedAsAllChanged 旧配置不可解析时调用方传 nil：
// 差异必须是"全部叶子"，从而退回保守的全量重启清单（宁可多报不可漏报）。
func TestDiffConfigNilTreatedAsAllChanged(t *testing.T) {
	next, err := ParseConfig([]byte(`{"listen":":1"}`))
	if err != nil {
		t.Fatal(err)
	}
	d := DiffConfig(nil, next)
	if d.Len() < len(ConfigLeafPaths())/2 {
		t.Errorf("nil 旧配置应视为全部变更，实际只检出 %d 项", d.Len())
	}
	if !d.Has("listen") {
		t.Errorf("listen 应为改动：%v", d.Slice())
	}
}

// TestDiffConfigDerivedFieldsIgnored json:"-" 的派生态（*Dur / PromptRules /
// ProxyClient / Warnings）不能进入差异：它们由 normalize 从用户字段推导，
// 派生值不同不代表用户配置变了。
func TestDiffConfigDerivedFieldsIgnored(t *testing.T) {
	next, err := ParseConfig([]byte(`{"pool":{"breaker_cooldown":"30m"}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := DiffConfig(nil, next)
	for _, p := range d.Slice() {
		if strings.HasPrefix(p, "_warnings") {
			t.Errorf("运行时元数据进入差异：%s", p)
		}
		if strings.ContainsAny(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("派生字段（Go 字段名，json:\"-\"）进入差异：%s", p)
		}
	}
}

// TestConfigLeafPathsStable LeafPaths 的形状本身是契约（覆盖测试依赖它）：
// map 用 "*" 展开、派生态被跳过。
func TestConfigLeafPathsStable(t *testing.T) {
	leaves := ConfigLeafPaths()
	set := map[string]bool{}
	for _, l := range leaves {
		set[l] = true
	}
	for _, want := range []string{
		"listen", "api_key", "auth_dir", "state_file",
		"pool.max_in_flight", "cooldown.soft_rate",
		"schedule.checkin_hours", "schedule.balance_refresh_enabled",
		"upstream.timeout_seconds", "upstream.profiles.*.client_version",
		"prompt.mode", "prompt.profiles.*.preset",
		"session_sticky.enabled", "server.read_timeout",
		"logging.request_client_info", "media.tool_images",
		"proxy_url", "resin_mode", "upstash.url", "global.enabled",
		"model_default_realm", "fingerprint_rules", "config_version",
	} {
		if !set[want] {
			t.Errorf("叶子路径 %s 缺失（形状变了会让目录覆盖率检查失真）", want)
		}
	}
	for _, never := range []string{"SoftRateDur", "PromptRules", "ProxyClient", "PromptText"} {
		for _, l := range leaves {
			if l == never {
				t.Errorf("派生态/元数据 %s 不应出现在叶子列表里", never)
			}
		}
	}
}
