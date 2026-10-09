// save_config_test.go 面板保存路径的端到端测试（校验 → 合并 → 落盘 → 热应用）。
//
// 这些用例需要真实的 pool/scheduler/upstream 实例与 config/runtime 快照，因此留在
// cmd/server；纯配置解析/校验的用例见 internal/config/config_test.go。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbuddy_manager/internal/config"
	"workbuddy_manager/internal/config/runtime"
	"workbuddy_manager/internal/media"
	"workbuddy_manager/internal/pool"
	"workbuddy_manager/internal/scheduler"
	"workbuddy_manager/internal/upstream"
)

// TestUnknownConfigKeySurvivesSave unknown 键在保存回写时保留（不静默删用户数据）。
func TestUnknownConfigKeySurvivesSave(t *testing.T) {
	start := map[string]any{"config_version": float64(config.CurrentVersion), "listen": ":1", "my_note": "keep me"}
	incoming := map[string]any{"listen": ":2"}
	merged := config.MergeConfigMaps(start, incoming)
	if _, err := config.ParseConfig(config.MergedJSON(merged)); err != nil {
		t.Fatalf("save path must accept existing unknown keys: %v", err)
	}
	if merged["my_note"] != "keep me" {
		t.Fatalf("unknown key dropped on save: %v", merged)
	}
}

// TestSaveConfigMediaPolicyRoundTrip 面板保存 + 热生效往返（media 段）。
//
// 后端走 merge → PruneUnknownKeys → ParseConfig → 热应用。这里用与前端 collectConfig
// 相同形态的 payload（bool + number）钉住 media 段：
//   - 三个键都不是未知键（不被剪掉）；
//   - 数值档位落到 int 字段（不是字符串）；
//   - 热应用后进程级策略真的生效。
func TestSaveConfigMediaPolicyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":":1","media":{"tool_images":"auto"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prevTool := media.CurrentToolPolicy()
	prevImage := media.CurrentImagePolicy()
	t.Cleanup(func() {
		_ = media.SetToolPolicy(prevTool)
		_ = media.SetImagePolicy(prevImage)
	})

	payload := []byte(`{"listen":":1","media":{"tool_images":"hoist","image_transcode":true,"image_max_dimension":1080}}`)
	if _, err := saveConfig(payload, path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{})); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := config.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Media.ToolImages != "hoist" || !parsed.Media.ImageTranscode || parsed.Media.ImageMaxDimension != 1080 {
		t.Fatalf("media policy not persisted: %+v", parsed.Media)
	}
	if got := media.CurrentToolPolicy(); got != media.ToolPolicyHoist {
		t.Fatalf("tool policy not hot-applied: %q", got)
	}
	if got := media.CurrentImagePolicy(); !got.Transcode || got.MaxDimension != 1080 {
		t.Fatalf("image policy not hot-applied: %+v", got)
	}
}

// TestSaveConfigRejectsInvalidMediaPolicy 非法档位保存时报错且不改动热状态。
func TestSaveConfigRejectsInvalidMediaPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":":1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prevImage := media.CurrentImagePolicy()
	t.Cleanup(func() { _ = media.SetImagePolicy(prevImage) })
	if err := media.SetImagePolicy(media.ImagePolicy{MaxDimension: 2000}); err != nil {
		t.Fatal(err)
	}
	_, err := saveConfig([]byte(`{"listen":":1","media":{"image_max_dimension":4096}}`), path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{}))
	if err == nil || !strings.Contains(err.Error(), "media.image_max_dimension") {
		t.Fatalf("invalid dimension accepted: %v", err)
	}
	if got := media.CurrentImagePolicy(); got.MaxDimension != 2000 {
		t.Fatalf("rejected save must not change the live policy: %+v", got)
	}
}

// TestSaveConfigIngressRoundTrip 面板保存路径的类型往返（server.* 入站准入四项）：
// 面板提交的是 JSON（number → int，时长 → 字符串），后端走
// merge → PruneUnknownKeys → ParseConfig。钉住三件事：
//   - 四项都不是未知键（不被剪掉）；
//   - 数值落到 int 字段（不是字符串），且 **0 是合法取值**（"不限制"）——前端把
//     0 与空串区分开正是为此（见 internal/panel/frontend_test.go 的 CollectIngressPolicy）；
//   - 时长字面量（含裸 "0"）被解析成对应 duration，而不是被当成缺省值回落。
func TestSaveConfigIngressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":":1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// 与前端 collectConfig 相同形态：两个上限是 Number，两个时长是字符串。
	payload := []byte(`{"listen":":1","server":{"max_inflight_requests":0,"max_inflight_bytes_mb":64,"ingress_wait":"0","read_timeout":"600s"}}`)
	restart, err := saveConfig(payload, path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{}))
	if err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "max_inflight_unset") {
		t.Fatal("未知键混入落盘内容")
	}
	parsed, err := config.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 0 必须原样保留（显式关掉并发上限），不能回落成默认 64。
	if parsed.Server.MaxInflightRequests != 0 {
		t.Errorf("max_inflight_requests=%d want 0（显式 0 = 不限制，不得回落默认）", parsed.Server.MaxInflightRequests)
	}
	if parsed.Server.MaxInflightBytesMB != 64 {
		t.Errorf("max_inflight_bytes_mb=%d want 64", parsed.Server.MaxInflightBytesMB)
	}
	// 裸 "0" 解析为 0 时长（= 满载立即拒绝），不是缺省 5s。
	if parsed.Server.IngressWait != "0" || parsed.IngressWaitDur != 0 {
		t.Errorf("ingress_wait=%q dur=%v want \"0\"/0（裸 0 = 立即拒绝，非缺省 5s）",
			parsed.Server.IngressWait, parsed.IngressWaitDur)
	}
	if parsed.ServerReadTimeoutDur != 600*time.Second {
		t.Errorf("read_timeout dur=%v want 600s", parsed.ServerReadTimeoutDur)
	}
	// 装配期字段：面板必须提示这四项需重启（否则用户改完以为即时生效）。
	need := []string{"server.max_inflight_requests", "server.max_inflight_bytes_mb",
		"server.ingress_wait", "server.read_timeout"}
	got := map[string]bool{}
	for _, f := range restart {
		got[f] = true
	}
	for _, f := range need {
		if !got[f] {
			t.Errorf("restart_required 缺 %s（实际 %v）", f, restart)
		}
	}
}

// TestSaveConfigStampsCurrentVersionAndMigratesLegacy 保存路径也必须跑迁移：
// 面板提交的配置与磁盘上的旧文件合并后，写下去的文件永远是**当前版本**，
// 否则每次启动都要迁移一遍（且用户永远看到"已自动迁移"的提示）。
func TestSaveConfigStampsCurrentVersionAndMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// 磁盘上是带历史遗留键的旧文件（无版本号）。
	legacy := `{"prompt":{"mode":"custom"},"cooldown":{"hard_credit":"1h","soft_rate":"30s"},"my_note":"keep"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// 面板提交一个热字段（与前端 collectConfig 同形态）。
	payload := []byte(`{"pool":{"max_in_flight":9}}`)
	if _, err := saveConfig(payload, path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{})); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := config.ParseConfig(raw)
	if err != nil {
		t.Fatalf("落盘文件必须能被当前版本解析: %v\n%s", err, raw)
	}
	if disk.ConfigVersion != config.CurrentVersion {
		t.Errorf("config_version=%d want %d（保存必须归一版本）", disk.ConfigVersion, config.CurrentVersion)
	}
	for _, gone := range []string{"hard_credit", `"custom"`} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("旧形态 %s 仍在落盘内容里:\n%s", gone, raw)
		}
	}
	// 迁移后的值生效：custom → replace 被保留为 prompt.mode。
	if disk.Prompt.Mode != "replace" {
		t.Errorf("prompt.mode=%q want replace（旧取值应被迁移而不是丢弃）", disk.Prompt.Mode)
	}
	if disk.Pool.MaxInFlight != 9 {
		t.Errorf("面板提交的值没生效: max_in_flight=%d", disk.Pool.MaxInFlight)
	}
	// 未知键被**清掉**：这是保存路径的既有语义（面板保存 = 用当前结构覆盖
	// 配置文件，用户拼错/未登记的键就此消失），与版本迁移回写刻意不同——
	// 迁移是自动发生的，不能删用户的东西；保存是用户显式动作，可以收口。
	if strings.Contains(string(raw), "my_note") {
		t.Errorf("保存路径应丢弃未知键（持久化只留当前结构）:\n%s", raw)
	}
}

// TestSaveConfigDropsRuntimeMetaAndHandlesCorruptOld 两个边界：
//   - 运行期元数据（_warnings/_migrations）不得被保存路径持久化（否则每次启动
//     都把上一轮的告警/迁移提示写回文件，越滚越多）；
//   - 旧文件是坏 JSON 时保存仍可成功（用提交内容覆盖），且重启清单退回保守全量。
func TestSaveConfigDropsRuntimeMetaAndHandlesCorruptOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// 磁盘上是带运行期元数据的正常文件。
	if err := os.WriteFile(path, []byte(`{"listen":":1","_warnings":["stale"],"_migrations":["stale"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig([]byte(`{"pool":{"max_in_flight":4}}`), path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{})); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "stale") {
		t.Errorf("运行期元数据被持久化了:\n%s", raw)
	}

	// 旧文件坏 JSON：保存仍应成功，且重启清单为保守全量（listen 等装配期字段在内）。
	if err := os.WriteFile(path, []byte(`{ this is not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	restart, err := saveConfig([]byte(`{"pool":{"max_in_flight":5}}`), path, runtime.New(runtime.Snapshot{}), pool.New(""), &upstream.Client{}, scheduler.New(scheduler.Config{}))
	if err != nil {
		t.Fatalf("坏旧文件不该阻断保存: %v", err)
	}
	if len(restart) < 5 {
		t.Errorf("旧文件不可解析时重启清单应退回保守全量，实际 %v", restart)
	}
	got := map[string]bool{}
	for _, f := range restart {
		got[f] = true
	}
	for _, want := range []string{"listen", "auth_dir", "state_file"} {
		if !got[want] {
			t.Errorf("保守全量清单缺 %s（实际 %v）", want, restart)
		}
	}
	// 落盘内容必须是完整可解析的当前版本配置。
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.ParseConfig(after)
	if err != nil {
		t.Fatalf("落盘内容必须可解析: %v\n%s", err, after)
	}
	if c.ConfigVersion != config.CurrentVersion || c.Pool.MaxInFlight != 5 {
		t.Errorf("落盘内容不对: version=%d max_in_flight=%d", c.ConfigVersion, c.Pool.MaxInFlight)
	}
}
