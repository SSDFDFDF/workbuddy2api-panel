// migrate.go 配置版本与一次性迁移。
//
// ── 为什么不靠"长期归一化" ─────────────────────────────────────────────
//
// 归一化（normalize）如果顺手兼容旧键/旧取值，每做一次 schema 变更就永久多一段
// 兼容分支：分支之间互相影响（哪个先判断、旧值和新值同名怎么办），且没有任何
// 机制能知道"还有没有人真的在用旧形态"，于是只能一直留着。债越滚越大，
// 最终没人敢删。
//
// 本文件给出相反的机制：**版本号 + 一次性迁移**。
//
//	config_version = 当前版本（写入文件的唯一形态）
//	  ↓ Load（读文件）
//	检测版本 → 逐级迁移到当前版本 → **立即回写**（快照旧文件）→ 解析 + 归一化
//
// 迁移执行过一次之后，磁盘上的文件就是当前版本，旧形态只存在于这次迁移，
// 不会长期驻留代码。这也是"config_version"这个字段存在的意义。
//
// ── 纪律（新增/修改配置项时必须遵守）─────────────────────────────────
//
//  1. normalize 只处理**当前版本**的缺省值、非法值钳制与派生字段；不得出现
//     "旧键兼容""旧取值照收"这类分支。
//  2. 任何 schema 变更（键改名、键删除、取值改名、语义拆分）都必须：
//     bump CurrentVersion + 在 migrations 里加一步 From→From+1 的迁移。
//  3. 迁移只做**机械改写**（键改名/取值映射/删键），不做校验：迁移的结果必须
//     能被当前 normalize 接受，否则是迁移写错了（会在这里 fail，而不是在用户那里）。
//  4. 无法忠实映射的旧键**不要猜**：删掉 + 在 note 里写清替代键，让用户显式决定。
//
// 守护测试：migrate_test.go 的 TestMigrationChainContiguous（链必须连续到
// CurrentVersion）、TestVersionGate（旧版本号不得直接解析）、
// TestMigrationIdempotent（迁移可重复执行）。
package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CurrentVersion 当前配置结构版本（写入 config_version 的值）。
const CurrentVersion = 3

// VersionUnversioned 没有 config_version 键的文件的等效版本。
//
// 版本号是 v2 引入的（FORK_CHANGES.md §4），所以"无版本"= 引入版本号之前的文件。
// 按 2 处理与 v2 时代的既有口径完全一致（原实现："留空按 2 处理"），
// 不改变任何现有行为；这些文件里仍可能带着 v1 时代的键，由迁移一次性清理。
const VersionUnversioned = 2

// MinimumMigratableVersion 能直接迁移的最低版本。更旧的版本 fail fast 并给出
// 人工指引——猜一个没有文档的旧形态去改写，比让用户手工升级危险得多。
const MinimumMigratableVersion = 2

// Migration 一次版本跃迁。From 的配置被改写成 To 的形态（就地改 raw）。
type Migration struct {
	From, To int
	// Summary 这步迁移在做什么（进启动日志与面板"已自动迁移"提示）。
	Summary string
	// Apply 机械改写 raw，返回人类可读的改动说明（键名/取值映射，逐条）。
	Apply func(raw map[string]any) []string
}

// migrations 迁移链。必须连续（From==上一项的 To）且终点为 CurrentVersion，
// 由 TestMigrationChainContiguous 守护。
var migrations = []Migration{{
	From: 2, To: 3,
	Summary: "清理历史遗留键（v2 时代只忽略不迁移）+ prompt.mode 旧取值改名",
	Apply:   migrateV2ToV3,
}}

// MigrateReport 一次迁移的结果。
type MigrateReport struct {
	From, To int
	// Steps 走过的迁移步骤摘要（按顺序）。
	Steps []string
	// Notes 具体改动（"丢弃退役键 cooldown.hard_credit" 这类逐条说明）。
	Notes []string
}

// Migrated 报告本次是否真的发生了版本跃迁。
func (r MigrateReport) Migrated() bool { return r.From != r.To }

// validateMigrations 校验迁移链自洽（程序内部错误，启动即暴露）。
func validateMigrations() error {
	if len(migrations) == 0 {
		if CurrentVersion != MinimumMigratableVersion {
			return fmt.Errorf("迁移链为空但 CurrentVersion=%d != MinimumMigratableVersion=%d", CurrentVersion, MinimumMigratableVersion)
		}
		return nil
	}
	if got := migrations[0].From; got != MinimumMigratableVersion {
		return fmt.Errorf("迁移链起点是 %d，但 MinimumMigratableVersion=%d", got, MinimumMigratableVersion)
	}
	for i, m := range migrations {
		if m.To != m.From+1 {
			return fmt.Errorf("迁移 %d→%d 不是单步跃迁（To 必须等于 From+1）", m.From, m.To)
		}
		if i > 0 && migrations[i-1].To != m.From {
			return fmt.Errorf("迁移链不连续：%d→%d 之后是 %d→%d", migrations[i-1].From, migrations[i-1].To, m.From, m.To)
		}
		if m.Apply == nil || m.Summary == "" {
			return fmt.Errorf("迁移 %d→%d 缺少 Apply 或 Summary", m.From, m.To)
		}
	}
	if last := migrations[len(migrations)-1].To; last != CurrentVersion {
		return fmt.Errorf("迁移链终点是 %d，但 CurrentVersion=%d", last, CurrentVersion)
	}
	return nil
}

// Migrate 把 raw 迁到 CurrentVersion（就地改写），并汇报做了什么。
//
// 版本判定：
//   - 缺 config_version → VersionUnversioned（历史文件）；
//   - 高于 CurrentVersion → 报错（文件由更新的程序写入，本程序不认识它的键，
//     强行按当前版本解析会静默丢配置）；
//   - 低于 MinimumMigratableVersion → 报错并给出人工路径。
func Migrate(raw map[string]any) (MigrateReport, error) {
	if err := validateMigrations(); err != nil {
		return MigrateReport{}, fmt.Errorf("internal: 迁移链不合法: %w", err)
	}
	v, err := versionOf(raw)
	if err != nil {
		return MigrateReport{}, err
	}
	rep := MigrateReport{From: v, To: v}
	switch {
	case v > CurrentVersion:
		return rep, fmt.Errorf("config_version: %d 由更新版本的程序写入（本程序支持到 %d）：请用新版程序启动，或恢复旧版配置；"+
			"按当前版本强解析会静默丢掉不认识的键", v, CurrentVersion)
	case v < MinimumMigratableVersion:
		return rep, fmt.Errorf("config_version: %d 太旧，没有到 v%d 的迁移路径（支持 %d 起）——"+
			"请参照 FORK_CHANGES.md 的旧键对照表手工升级，或用 config.example.json 重建", v, MinimumMigratableVersion, MinimumMigratableVersion)
	}
	for v < CurrentVersion {
		m, ok := migrationFrom(v)
		if !ok {
			// 链的连续性已由 validateMigrations 保证，这里只是防御。
			return rep, fmt.Errorf("internal: 缺少 %d→%d 的迁移", v, v+1)
		}
		rep.Notes = append(rep.Notes, m.Apply(raw)...)
		rep.Steps = append(rep.Steps, m.Summary)
		v = m.To
	}
	rep.To = v
	// 迁移完成后版本号必须是当前版本（失败的话下次启动会重复迁移一遍，
	// 虽然幂等，但会让"已迁移"的日志反复出现，掩盖真正的问题）。
	raw["config_version"] = CurrentVersion
	return rep, nil
}

// migrationFrom 找到以 v 为起点的迁移步骤。
func migrationFrom(v int) (Migration, bool) {
	for _, m := range migrations {
		if m.From == v {
			return m, true
		}
	}
	return Migration{}, false
}

// versionOf 读取 raw 里的 config_version（缺省 = VersionUnversioned）。
// 数字以外的取值报错：版本号解析不出来就没法判断该不该迁移，宁可 fail fast。
func versionOf(raw map[string]any) (int, error) {
	v, ok := raw["config_version"]
	if !ok || v == nil {
		return VersionUnversioned, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("config_version: %v 不是整数版本号", n)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("config_version: %q 不是整数版本号", n.String())
		}
		return int(i), nil
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("config_version: %q 不是整数版本号", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("config_version: 不认识的取值类型 %T（应为整数）", v)
	}
}

// ── v2 → v3 ─────────────────────────────────────────────────────────────
//
// 这一步把历史遗留一次性清干净，之后 normalize 只认当前形态。
//
// 遗留清单（按来源分两类，都有据可查）：
//
//	A) v1 → v2 时**改了名但没迁移**的键（FORK_CHANGES.md §4 的对照表）：
//	     features.sanitize_blacklist_fingerprints → fingerprint_rewrite (+ fingerprint_rules)
//	     upstream.client_version / cli_version    → upstream.profiles.<realm>.*
//	     upstream.user_agent                      → upstream.profiles.<realm>.user_agents.<用途>
//	     prompt.mode: custom / passthrough        → replace / none
//	   当时的实现是"旧键不识别、不迁移：启动告警、面板保存时丢弃"，于是一个
//	   v2 文件里可能长期带着这些键：每次启动都告警，值却早已不生效。
//
//	B) 更早已经退役、只剩"忽略"的键：cooldown.hard_credit / err_threshold /
//	   err_cooldown、schedule.travel_interval_minutes。
//
// 迁移原则：**能忠实映射的才映射，映射不了的不猜**。所以
// upstream.client_version/cli_version（v1 是全局单值、v2 是同名的按域字段）
// 会搬到 cn/global 两个域；而 upstream.user_agent（v1 是"全路径完全覆盖"，
// 连未知用途都覆盖）在 v2 里只有"按域 × 按用途"的表，机械映射必然猜错用途，
// 因此删掉并在 note 里写明目标键，交给用户显式决定。
func migrateV2ToV3(raw map[string]any) []string {
	var notes []string

	// ── A1) features 段：拆到 fingerprint_rewrite ──
	if feats, ok := raw["features"].(map[string]any); ok {
		if b, isBool := feats["sanitize_blacklist_fingerprints"].(bool); isBool {
			if b {
				// 只在用户**显式**打开时映射：v1 的缺省是 true，而 v2 有意把
				// 缺省改成 false（它会改用户可见内容）。把缺省一起搬过去，
				// 等于替用户打开一个他没写过的开关。
				if _, exists := raw["fingerprint_rewrite"]; !exists {
					raw["fingerprint_rewrite"] = true
					notes = append(notes, "features.sanitize_blacklist_fingerprints=true → fingerprint_rewrite=true")
				} else {
					notes = append(notes, "丢弃 features.sanitize_blacklist_fingerprints（已显式配置 fingerprint_rewrite，以它为准）")
				}
			} else {
				notes = append(notes, "丢弃 features.sanitize_blacklist_fingerprints=false（v2 缺省即「严格逐字透传」）")
			}
		}
		for _, k := range sortedKeys(feats) {
			if k != "sanitize_blacklist_fingerprints" {
				notes = append(notes, "丢弃退役键 features."+k)
			}
		}
		delete(raw, "features")
	}

	// ── A2) upstream 顶层身份键 → 按域 profile ──
	if up, ok := raw["upstream"].(map[string]any); ok {
		// client_version / cli_version：v1 是全局单值，对两个域都生效，逐域搬。
		// 目标域已有显式值时不覆盖（显式配置优先，迁移只补空缺）。
		for _, key := range []string{"client_version", "cli_version"} {
			v, present := up[key]
			if !present {
				continue
			}
			moved := false
			for _, realm := range []string{"cn", "global"} {
				prof := childMap(up, "profiles", realm)
				if _, exists := prof[key]; exists {
					continue
				}
				prof[key] = v
				moved = true
				notes = append(notes, fmt.Sprintf("upstream.%s → upstream.profiles.%s.%s", key, realm, key))
			}
			if !moved {
				notes = append(notes, "丢弃 upstream."+key+"（各域 profile 已有显式值）")
			}
			delete(up, key)
		}
		if _, present := up["user_agent"]; present {
			delete(up, "user_agent")
			notes = append(notes, "丢弃 upstream.user_agent —— v2 无「全局 UA 覆盖」这一形态；"+
				"如需自定义请写 upstream.profiles.<cn|global>.user_agents.<用途>（用途见 README 的 profiles 表）")
		}
	}

	// ── A3) prompt.mode 旧取值 → 当前取值 ──
	// custom（网关提示词替换客户端 system）→ replace
	// passthrough（逐字透传客户端 system）→ none
	// append 两版同名同义，不动；其余取值不认识的交给 normalize 报错。
	if p, ok := raw["prompt"].(map[string]any); ok {
		notes = append(notes, migratePromptMode(p, "prompt.mode")...)
		if profs, ok := p["profiles"].(map[string]any); ok {
			for _, realm := range sortedKeys(profs) {
				pr, ok := profs[realm].(map[string]any)
				if !ok {
					continue
				}
				notes = append(notes, migratePromptMode(pr, "prompt.profiles."+realm+".mode")...)
			}
		}
	}

	// ── B) 早已退役、只剩"忽略"的键 ──
	for _, path := range []string{
		"cooldown.hard_credit",
		"cooldown.err_threshold",
		"cooldown.err_cooldown",
		"schedule.travel_interval_minutes",
	} {
		if delPath(raw, path) {
			notes = append(notes, "丢弃退役键 "+path)
		}
	}

	return notes
}

// legacyPromptModes 历史 mode 取值 → 当前取值的映射（有据可查的一对一改名）。
var legacyPromptModes = map[string]string{
	"custom":      "replace",
	"passthrough": "none",
}

// migratePromptMode 就地把旧 mode 改名；返回改动说明（无改动返回 nil）。
func migratePromptMode(m map[string]any, path string) []string {
	v, ok := m["mode"].(string)
	if !ok {
		return nil
	}
	cur, isLegacy := legacyPromptModes[normalizeToken(v)]
	if !isLegacy {
		return nil
	}
	m["mode"] = cur
	return []string{fmt.Sprintf("%s: %q → %q", path, v, cur)}
}

// ── map 小工具 ──────────────────────────────────────────────────────────

// childMap 取 raw[key1][key2]（不存在则创建并挂回 raw[key1]）。
// 用于"往没有 profiles 段的配置里补一个域"这种补空缺场景。
func childMap(raw map[string]any, key1, key2 string) map[string]any {
	parent, ok := raw[key1].(map[string]any)
	if !ok {
		parent = map[string]any{}
		raw[key1] = parent
	}
	child, ok := parent[key2].(map[string]any)
	if !ok {
		child = map[string]any{}
		parent[key2] = child
	}
	return child
}

// delPath 删除点分路径指向的键；返回是否真的删掉了。
func delPath(raw map[string]any, path string) bool {
	parent, key, ok := walkTo(raw, path)
	if !ok {
		return false
	}
	if _, exists := parent[key]; !exists {
		return false
	}
	delete(parent, key)
	return true
}

// walkTo 走到 path 的父 map，返回父 map、最后一段键名。
func walkTo(raw map[string]any, path string) (map[string]any, string, bool) {
	segs := splitPath(path)
	if len(segs) == 0 {
		return nil, "", false
	}
	cur := raw
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil, "", false
		}
		cur = next
	}
	return cur, segs[len(segs)-1], true
}

// normalizeToken 迁移期的取值归一：去空白 + 小写（迁移是容错路径，
// 不做大小写敏感判定，否则 "Custom" 这种手写值会被漏掉）。
func normalizeToken(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// splitPath 把点分路径拆成段（空路径返回 nil）。
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}

// sortedKeys 返回 map 的键排序结果（确定性的 note 顺序，便于测试与阅读）。
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
