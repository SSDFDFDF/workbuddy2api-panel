// migrate_test.go 版本迁移的守护测试。
//
// 三条主线：
//  1. 迁移链自洽（连续、单调、终点 = CurrentVersion）——链断了就是程序 bug；
//  2. 版本闸门生效——旧版本号不能被直接解析（否则兼容分支会重新长回 normalize）；
//  3. 每条历史遗留都被真的迁移/清理，且**回写磁盘**（一次性，不重复迁）。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMigrationChainContiguous 迁移链必须连续、单步、终点等于 CurrentVersion。
//
// 防的是"bump 了版本号却忘了写迁移"和"跳版本"这两类：前者会让旧文件报错或
// 静默按新版本解析（丢键），后者根本没有对应的改写逻辑。
func TestMigrationChainContiguous(t *testing.T) {
	if err := validateMigrations(); err != nil {
		t.Fatalf("迁移链不合法: %v", err)
	}
	if CurrentVersion < MinimumMigratableVersion {
		t.Fatalf("CurrentVersion=%d < MinimumMigratableVersion=%d", CurrentVersion, MinimumMigratableVersion)
	}
	// 链起点必须是我们声明支持的最低版本。
	if len(migrations) > 0 && migrations[0].From != MinimumMigratableVersion {
		t.Errorf("迁移链起点 %d != MinimumMigratableVersion %d", migrations[0].From, MinimumMigratableVersion)
	}
}

// TestVersionGate 版本闸门：旧版本号必须被拒绝（提示走迁移），新版本号必须被拒绝
// （提示用新版程序），缺省版本号按"未声明"接受。
//
// 这是"不再长期归一化"的执行点：只要旧版本号进不来，normalize 里就不可能悄悄
// 长出兼容分支。
func TestVersionGate(t *testing.T) {
	// 旧版本：明确拒绝，且错误信息要指路。
	_, err := ParseConfig([]byte(`{"config_version":2,"listen":":1"}`))
	if err == nil {
		t.Fatal("显式旧版本号（2）必须被拒绝：旧形态不允许走进 normalize")
	}
	if !strings.Contains(err.Error(), "迁移") {
		t.Errorf("错误信息应指向迁移路径，实际: %v", err)
	}
	// 更新版本：拒绝（不认识它的键，强解析会静默丢配置）。
	_, err = ParseConfig([]byte(`{"config_version":99,"listen":":1"}`))
	if err == nil || !strings.Contains(err.Error(), "更新版本") {
		t.Fatalf("更新版本号必须被拒绝并说明原因，实际: %v", err)
	}
	// 当前版本 / 未声明：接受。
	if c, err := ParseConfig([]byte(`{"config_version":3,"listen":":1"}`)); err != nil || c.ConfigVersion != CurrentVersion {
		t.Fatalf("当前版本号应被接受: %v %+v", err, c)
	}
	if c, err := ParseConfig([]byte(`{"listen":":1"}`)); err != nil || c.ConfigVersion != CurrentVersion {
		t.Fatalf("未声明版本号应被接受并归一为当前版本: %v %+v", err, c)
	}
}

// TestMigrateVersionBounds 版本边界：太旧没有迁移路径 → 报错；更新版本 → 报错；
// 非整数版本号 → 报错（版本号解析不出来就无法判断该不该迁移）。
func TestMigrateVersionBounds(t *testing.T) {
	if _, err := Migrate(map[string]any{"config_version": 1}); err == nil || !strings.Contains(err.Error(), "太旧") {
		t.Errorf("v1 应为\"太旧无迁移路径\"，实际: %v", err)
	}
	if _, err := Migrate(map[string]any{"config_version": CurrentVersion + 1}); err == nil || !strings.Contains(err.Error(), "更新版本") {
		t.Errorf("超前版本应报错，实际: %v", err)
	}
	if _, err := Migrate(map[string]any{"config_version": "三"}); err == nil {
		t.Error("非整数版本号必须报错")
	}
	if _, err := Migrate(map[string]any{"config_version": 2.5}); err == nil {
		t.Error("小数版本号必须报错")
	}
	// 缺省 = VersionUnversioned（历史文件），可迁移。
	rep, err := Migrate(map[string]any{"listen": ":1"})
	if err != nil {
		t.Fatalf("未声明版本号应可迁移: %v", err)
	}
	if rep.From != VersionUnversioned || rep.To != CurrentVersion || !rep.Migrated() {
		t.Errorf("rep=%+v want from=%d to=%d", rep, VersionUnversioned, CurrentVersion)
	}
}

// TestMigrateIdempotent 迁移必须幂等：回写失败（只读挂载）时每次启动都会重跑，
// 第二次跑不能再改动任何东西（尤其是不能再产生新的迁移说明）。
func TestMigrateIdempotent(t *testing.T) {
	raw := map[string]any{
		"features": map[string]any{"sanitize_blacklist_fingerprints": true},
		"upstream": map[string]any{
			"client_version": "6.0.0",
			"cli_version":    "3.0.0",
			"user_agent":     "Custom/1.0",
		},
		"prompt": map[string]any{"mode": "custom"},
		"cooldown": map[string]any{
			"hard_credit": "1h",
			"soft_rate":   "30s",
		},
	}
	first, err := Migrate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Notes) == 0 {
		t.Fatal("首次迁移应有改动说明")
	}
	snapshot, _ := json.Marshal(raw)
	second, err := Migrate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if second.Migrated() {
		t.Errorf("第二次迁移不该再发生版本跃迁: %+v", second)
	}
	if len(second.Notes) != 0 {
		t.Errorf("第二次迁移不该再有改动: %v", second.Notes)
	}
	after, _ := json.Marshal(raw)
	if string(snapshot) != string(after) {
		t.Errorf("第二次迁移改动了内容:\n%s\nvs\n%s", snapshot, after)
	}
}

// TestMigrateV2ToV3Features features 段拆分：显式 true 映射成 fingerprint_rewrite；
// 缺省（v1 语义是开）不映射——v2 有意把缺省改成关闭，迁移不得替用户打开开关。
func TestMigrateV2ToV3Features(t *testing.T) {
	raw := map[string]any{"features": map[string]any{"sanitize_blacklist_fingerprints": true}}
	on, err := Migrate(raw) // Migrate 就地改写，断言用同一份
	if err != nil {
		t.Fatal(err)
	}
	if raw["fingerprint_rewrite"] != true {
		t.Errorf("显式 true 应映射为 fingerprint_rewrite=true: %v", raw)
	}
	if _, exists := raw["features"]; exists {
		t.Errorf("features 段应被删除: %v", raw)
	}
	if !hasMigration(on.Notes, "fingerprint_rewrite") {
		t.Errorf("迁移说明应含映射关系: %v", on.Notes)
	}

	// 显式 false：只删段，不写 fingerprint_rewrite（v2 缺省即关闭）。
	raw2 := map[string]any{"features": map[string]any{"sanitize_blacklist_fingerprints": false}}
	if _, err := Migrate(raw2); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw2["fingerprint_rewrite"]; exists {
		t.Errorf("显式 false 不该写 fingerprint_rewrite: %v", raw2)
	}

	// 已有显式 fingerprint_rewrite：以它为准（迁移不覆盖用户在当前版本的显式配置）。
	raw3 := map[string]any{"fingerprint_rewrite": false, "features": map[string]any{"sanitize_blacklist_fingerprints": true}}
	if _, err := Migrate(raw3); err != nil {
		t.Fatal(err)
	}
	if raw3["fingerprint_rewrite"] != false {
		t.Errorf("已显式配置的 fingerprint_rewrite 不该被覆盖: %v", raw3)
	}
}

// TestMigrateV2ToV3UpstreamIdentity 身份键搬迁：client_version/cli_version 逐域搬
// （v1 是全局单值），user_agent 无忠实映射 → 删除但不猜（给出目标键提示）。
func TestMigrateV2ToV3UpstreamIdentity(t *testing.T) {
	raw := map[string]any{
		"upstream": map[string]any{
			"client_version": "6.0.0",
			"cli_version":    "3.0.0",
			"user_agent":     "Custom/1.0",
			"profiles": map[string]any{
				"global": map[string]any{"client_version": "9.9.9"}, // 已显式：不覆盖
			},
		},
	}
	rep, err := Migrate(raw)
	if err != nil {
		t.Fatal(err)
	}
	up := raw["upstream"].(map[string]any)
	profs := up["profiles"].(map[string]any)
	cn := profs["cn"].(map[string]any)
	global := profs["global"].(map[string]any)
	if cn["client_version"] != "6.0.0" || cn["cli_version"] != "3.0.0" {
		t.Errorf("cn 域应继承 v1 全局值: %v", cn)
	}
	if global["client_version"] != "9.9.9" {
		t.Errorf("已显式配置的域不该被覆盖: %v", global)
	}
	if global["cli_version"] != "3.0.0" {
		t.Errorf("global 域缺失的键应补齐: %v", global)
	}
	for _, gone := range []string{"client_version", "cli_version", "user_agent"} {
		if _, exists := up[gone]; exists {
			t.Errorf("upstream.%s 应被搬走/清理: %v", gone, up)
		}
	}
	if !hasMigration(rep.Notes, "user_agent") || !hasMigration(rep.Notes, "user_agents.<用途>") {
		t.Errorf("user_agent 的删除必须给出目标键提示（不猜用途）: %v", rep.Notes)
	}
}

// TestMigrateV2ToV3PromptMode prompt.mode 旧取值改名（顶层 + 分域），其余取值不动。
func TestMigrateV2ToV3PromptMode(t *testing.T) {
	raw := map[string]any{
		"prompt": map[string]any{
			"mode": "custom",
			"profiles": map[string]any{
				"cn":     map[string]any{"mode": "passthrough"},
				"global": map[string]any{"mode": "append"}, // 同名同义：不动
			},
		},
	}
	rep, err := Migrate(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := raw["prompt"].(map[string]any)
	if p["mode"] != "replace" {
		t.Errorf("custom → replace，实际 %v", p["mode"])
	}
	profs := p["profiles"].(map[string]any)
	if profs["cn"].(map[string]any)["mode"] != "none" {
		t.Errorf("passthrough → none，实际 %v", profs["cn"])
	}
	if profs["global"].(map[string]any)["mode"] != "append" {
		t.Errorf("append 不该被改动: %v", profs["global"])
	}
	if !hasMigration(rep.Notes, `"custom" → "replace"`) {
		t.Errorf("迁移说明应写明取值映射: %v", rep.Notes)
	}
	// 迁移后必须能被当前 normalize 接受（值合法）。
	if _, err := ParseConfig(MergedJSON(raw)); err != nil {
		t.Fatalf("迁移结果必须能被当前版本解析: %v", err)
	}
}

// TestMigrateV2ToV3UnknownKeysPreserved 迁移只动它认识的键：用户手写的其它键
// （注释性字段、拼错的键）原样保留——迁移不是"配置文件大扫除"。
func TestMigrateV2ToV3UnknownKeysPreserved(t *testing.T) {
	raw := map[string]any{
		"listen":  ":1",
		"my_note": "keep me",
		"custom":  map[string]any{"nested": "value"},
	}
	if _, err := Migrate(raw); err != nil {
		t.Fatal(err)
	}
	if raw["my_note"] != "keep me" {
		t.Errorf("未知顶层键被迁移删掉了: %v", raw)
	}
	if raw["custom"].(map[string]any)["nested"] != "value" {
		t.Errorf("未知嵌套键被迁移改动了: %v", raw)
	}
	if raw["config_version"] != CurrentVersion {
		t.Errorf("迁移后版本号应为 %d: %v", CurrentVersion, raw["config_version"])
	}
}

// TestLoadMigratesAndWritesBackOnce Load 是"一次性迁移"的执行点：迁移 + 回写 +
// 快照 + 兜底备份，且第二次加载不再迁移。
func TestLoadMigratesAndWritesBackOnce(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "config.json")
	orig := `{"upstream":{"client_version":"6.0.0"},"prompt":{"mode":"custom"},"cooldown":{"hard_credit":"1h","soft_rate":"30s"},"my_note":"keep"}`
	if err := os.WriteFile(fp, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(fp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Upstream.Profiles["cn"].ClientVersion != "6.0.0" {
		t.Errorf("迁移没生效（身份键没搬到 cn 域）: %+v", c.Upstream.Profiles["cn"])
	}
	if c.Prompt.Mode != "replace" {
		t.Errorf("prompt.mode 未迁移: %q", c.Prompt.Mode)
	}
	if len(c.Migrations) == 0 {
		t.Errorf("应报告本次执行的迁移: %v", c.Migrations)
	}

	// 磁盘：当前版本 + 旧键已清 + 未知键保留。
	after, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := ParseConfig(after)
	if err != nil {
		t.Fatalf("回写的文件必须能被当前版本解析: %v\n%s", err, after)
	}
	if disk.ConfigVersion != CurrentVersion {
		t.Errorf("回写文件的 config_version=%d want %d", disk.ConfigVersion, CurrentVersion)
	}
	for _, gone := range []string{"hard_credit", "user_agent"} {
		if strings.Contains(string(after), gone) {
			t.Errorf("旧键 %s 仍在磁盘上:\n%s", gone, after)
		}
	}
	if !strings.Contains(string(after), "keep") {
		t.Errorf("未知键被迁移回写删掉了:\n%s", after)
	}
	if !strings.Contains(string(after), "\n") {
		t.Errorf("回写必须保持缩进可读（不能是一整行）:\n%s", after)
	}

	// 快照：迁移前的原文完整保留（用户可 cp 回退）。
	snap, err := os.ReadFile(VersionSnapshotPath(fp, VersionUnversioned))
	if err != nil {
		t.Fatalf("迁移前快照缺失: %v", err)
	}
	if string(snap) != orig {
		t.Errorf("快照必须是迁移前原文:\n%s", snap)
	}
	// 兜底备份种子。
	if _, err := os.Stat(BackupPath(fp)); err != nil {
		t.Errorf("首次成功加载应种一份 .bak: %v", err)
	}

	// 第二次加载：不再迁移（一次性）。
	c2, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Migrations) != 0 {
		t.Errorf("第二次加载不该再迁移: %v", c2.Migrations)
	}
}

// TestLoadMigrationWriteBackFailureIsNonFatal 只读部署（:ro 挂载）下迁移不能
// 阻断启动：内存里必须是当前版本，且把"回写失败"作为告警透出。
func TestLoadMigrationWriteBackFailureIsNonFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限，本用例不可判定")
	}
	dir := t.TempDir()
	fp := filepath.Join(dir, "config.json")
	if err := os.WriteFile(fp, []byte(`{"prompt":{"mode":"custom"},"listen":":1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // 目录只读：写 tmp 会失败
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	c, err := Load(fp)
	if err != nil {
		t.Fatalf("回写失败不得阻断启动: %v", err)
	}
	if c.Prompt.Mode != "replace" {
		t.Errorf("内存里应已是迁移后的值: %q", c.Prompt.Mode)
	}
	if !hasWarning(c.Warnings, "回写磁盘失败") {
		t.Errorf("回写失败必须作为告警透出: %v", c.Warnings)
	}
}

// TestRuntimeMetaNotReadFromFile _warnings/_migrations 是运行期元数据：
// 文件里的残留不得污染当期告警，也不得被迁移回写持久化。
func TestRuntimeMetaNotReadFromFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "config.json")
	if err := os.WriteFile(fp, []byte(`{"listen":":1","_warnings":["stale"],"_migrations":["stale"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if hasWarning(c.Warnings, "stale") {
		t.Errorf("文件里的 _warnings 不该被读进来: %v", c.Warnings)
	}
	for _, m := range c.Migrations {
		if m == "stale" {
			t.Errorf("文件里的 _migrations 不该被读进来: %v", c.Migrations)
		}
	}
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "stale") {
		t.Errorf("迁移回写应丢弃运行期元数据键:\n%s", raw)
	}
}

// TestMigrationNotesVisibleToPanel 迁移说明要能被面板看到（否则用户不知道
// 配置文件被自动改写过）——字段带 json tag，且写在 _migrations 下。
func TestMigrationNotesVisibleToPanel(t *testing.T) {
	raw := map[string]any{"cooldown": map[string]any{"hard_credit": "1h"}}
	rep, err := Migrate(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Migrations = append(rep.Steps, rep.Notes...)
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["_migrations"]; !ok {
		t.Fatalf("_migrations 必须出现在面板可见的 JSON 里: %s", out)
	}
}

// TestFileTxSerializesSaveAndMigration 事务锁把保存与迁移回写串在一起：
// 两条路径并发跑同一文件时必须串行（否则迁移回写会覆盖掉刚保存的改动）。
// 这里用"临界区内观察到的重叠"来判定，不依赖时序运气。
func TestFileTxSerializesSaveAndMigration(t *testing.T) {
	var (
		mu      sync.Mutex
		inside  int
		overlap bool
	)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = FileTx(func() error {
				mu.Lock()
				inside++
				if inside > 1 {
					overlap = true
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	if overlap {
		t.Fatal("FileTx 未能串行化配置文件的读改写事务")
	}
}
