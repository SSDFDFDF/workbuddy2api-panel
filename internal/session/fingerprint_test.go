// fingerprint_test.go 指纹救场的核心行为回归。
package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fpTestBody 构造 Chat 形态 body（messages 全量历史，与派生键路径同源）。
func fpTestBody(system, firstUser, secondUser string) []byte {
	return []byte(`{"model":"m","messages":[` +
		`{"role":"system","content":"` + system + `"},` +
		`{"role":"user","content":"` + firstUser + `"},` +
		`{"role":"assistant","content":"ok"},` +
		`{"role":"user","content":"` + secondUser + `"}]}`)
}

// newFPRouter 构建启用粘性 + 指纹救场的路由器。可用性按模型感知："blocked-"
// 前缀模型对所有账号不可用（用于验证救场的模型维度校验），其余模型全池可用。
func newFPRouter(t *testing.T, uids ...string) *Router {
	t.Helper()
	uids = append([]string(nil), uids...)
	r := New(Config{
		TTL:        time.Hour,
		GCInterval: time.Minute,
		Available:  func() []string { return uids },
		AvailableForModel: func(model string) []string {
			if strings.HasPrefix(model, "blocked-") {
				return nil
			}
			return uids
		},
	})
	r.ReconfigureFingerprint(time.Hour, time.Minute, true, true)
	if !r.FingerprintEnabled() {
		t.Fatal("FingerprintEnabled should be true after ReconfigureFingerprint(true)")
	}
	return r
}

// fpBindDerived 模拟一轮成功请求：把派生键绑到 uid 并记录指纹窗口。
func fpBindDerived(t *testing.T, r *Router, uid, key string, body []byte) {
	t.Helper()
	r.Bind(key, uid)
	obj := mustObject(t, body)
	r.RecordFingerprint(key, "", "m", obj)
}

func mustObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return obj
}

func TestHistoryFingerprintsExcludesSystem(t *testing.T) {
	obj := mustObject(t, fpTestBody("sys v1", "hello", "world"))
	fps := historyFingerprints(obj)
	if len(fps) != 3 {
		t.Fatalf("expect 3 fingerprints (user/assistant/user), got %d", len(fps))
	}
	// system 漂移：换 system 后 user/assistant 指纹应完全一致。
	obj2 := mustObject(t, fpTestBody("sys v2 with timestamp", "hello", "world"))
	fps2 := historyFingerprints(obj2)
	if len(fps) != len(fps2) {
		t.Fatalf("fingerprint count changed across system drift: %d vs %d", len(fps), len(fps2))
	}
	for i := range fps {
		if fps[i] != fps2[i] {
			t.Fatalf("fingerprint[%d] changed across system drift: %q vs %q", i, fps[i], fps2[i])
		}
	}
}

func TestHistoryFingerprintsEmptyRoles(t *testing.T) {
	obj := mustObject(t, []byte(`{"messages":[{"role":"system","content":"s"},{"role":"user","content":""}]}`))
	// 空内容 user 的签名为空 → 不入材料。
	if fps := historyFingerprints(obj); fps != nil {
		t.Fatalf("empty content user should yield nil, got %v", fps)
	}
	// 无 messages 键。
	obj2 := mustObject(t, []byte(`{"model":"m"}`))
	if fps := historyFingerprints(obj2); fps != nil {
		t.Fatalf("no messages should yield nil, got %v", fps)
	}
}

func TestFingerprintRescueAfterDrift(t *testing.T) {
	r := newFPRouter(t, "uid-a", "uid-b")
	// turn1：派生键 d-key1 绑到 uid-a（system 漂移场景：每轮键不同）。
	turn1 := fpTestBody("sys 2024-01-01", "hello", "")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)

	// turn2：system 变了 → 派生键 d-key2，无绑定 → 救场应找回 uid-a。
	turn2 := fpTestBody("sys 2024-01-02", "hello", "world")
	uid, ok := r.RescueDerivedKey(turn2, "", "m", "m")
	if !ok {
		t.Fatal("rescue should hit after system drift")
	}
	if uid != "uid-a" {
		t.Fatalf("rescue should return uid-a, got %q", uid)
	}
}

func TestFingerprintRescueScopeIsolation(t *testing.T) {
	r := newFPRouter(t, "uid-a", "uid-b")
	// principal1 的会话。
	turn1 := fpTestBody("sys v1", "hello", "")
	fpBindDerived(t, r, "uid-a", "p1:cn:m:d-key1", turn1)

	// principal2 同内容请求不得命中 p1 的候选（scope 隔离）。
	turn2 := fpTestBody("sys v2", "hello", "world")
	if _, ok := r.RescueDerivedKey(turn2, "p2", "m", "m"); ok {
		t.Fatal("rescue must not cross principal scope")
	}
	// 模型维度隔离同理。
	if _, ok := r.RescueDerivedKey(turn2, "", "other", "other"); ok {
		t.Fatal("rescue must not cross model scope")
	}
}

func TestFingerprintRescueRequiresAccountAvailable(t *testing.T) {
	r := newFPRouter(t, "uid-a")
	turn1 := fpTestBody("sys v1", "hello", "")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)

	// 候选账号冷却（对模型不可用）→ 救场不命中。
	turn2 := fpTestBody("sys v2", "hello", "world")
	if uid, ok := r.RescueDerivedKey(turn2, "", "m", "blocked-model"); ok {
		t.Fatalf("rescue must not return account unavailable for model, got %q", uid)
	}
}

func TestFingerprintRescueDisabled(t *testing.T) {
	r := newFPRouter(t, "uid-a")
	turn1 := fpTestBody("sys v1", "hello", "")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)
	turn2 := fpTestBody("sys v2", "hello", "world")

	// 关停开关 → 恒不命中（索引清空）。
	r.setFingerprint(false)
	if _, ok := r.RescueDerivedKey(turn2, "", "m", "m"); ok {
		t.Fatal("rescue must not hit when disabled")
	}
	if r.FingerprintEnabled() {
		t.Fatal("FingerprintEnabled should be false after setFingerprint(false)")
	}
	// 重新开启：索引已清空，也不命中。
	r.setFingerprint(true)
	if _, ok := r.RescueDerivedKey(turn2, "", "m", "m"); ok {
		t.Fatal("rescue must not hit right after re-enable (index cleared)")
	}
}

func TestFingerprintRescueBodyTooLarge(t *testing.T) {
	r := newFPRouter(t, "uid-a")
	turn1 := fpTestBody("sys v1", "hello", "")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)

	big := fpTestBody("sys v2", strings.Repeat("x", fpBodyMaxBytes), "world")
	if _, ok := r.RescueDerivedKey(big, "", "m", "m"); ok {
		t.Fatal("rescue must skip oversized body")
	}
}

func TestFingerprintRecordWindowSlide(t *testing.T) {
	r := newFPRouter(t, "uid-a")
	key := "p:cn:m:d-key1"

	// turn1 记录后 turn2 记录：旧指纹（已滑出窗口）应从 byMsg 摘除，新指纹入索引。
	turn1 := fpTestBody("sys v1", "hello", "")
	r.Bind(key, "uid-a")
	r.RecordFingerprint(key, "", "m", mustObject(t, turn1))

	turn2 := fpTestBody("sys v2", "hello", "world")
	r.RecordFingerprint(key, "", "m", mustObject(t, turn2))

	scope := "\x00m"
	c := r.fp.byKey[key]
	if c == nil {
		t.Fatal("candidate should exist after record")
	}
	turn1FPs := historyFingerprints(mustObject(t, turn1))
	// turn2 窗口 = [user hello, assistant ok, user world]：turn1 的首条 user 指纹
	// 仍在窗口内，但 turn1 的旧窗口整体（含旧 assistant 内容）不该完整保留。
	// 直接验证：候选 fps 集等于 turn2 窗口集。
	turn2FPs := historyFingerprints(mustObject(t, turn2))
	if len(c.fps) != len(turn2FPs) {
		t.Fatalf("candidate fps should track latest window: %d vs %d", len(c.fps), len(turn2FPs))
	}
	for _, fp := range turn2FPs {
		if _, ok := c.fps[fp]; !ok {
			t.Fatalf("candidate missing turn2 fingerprint %q", fp)
		}
	}
	// byMsg 键数量有界：scope 下所有键都属于当前窗口集 ∪ 残留（hello 仍重叠）。
	msgKeys := 0
	for k := range r.fp.byMsg {
		if strings.HasPrefix(k, scope+"\x00") {
			msgKeys++
		}
	}
	if msgKeys == 0 {
		t.Fatal("byMsg should contain fingerprints for the window")
	}
	_ = turn1FPs
}

func TestFingerprintRescueYoungSessionSingleHit(t *testing.T) {
	r := newFPRouter(t, "uid-a", "uid-b")
	// turn1：单条 user（首轮窗口 1 条）。
	turn1 := []byte(`{"model":"m","messages":[{"role":"system","content":"s1"},{"role":"user","content":"hi"}]}`)
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)

	// turn2：3 条（user/assistant/user），窗口 3 条 → 年轻会话单命中规则。
	// 只有首条 user 与 turn1 重叠（assistant 是新内容）。
	turn2 := []byte(`{"model":"m","messages":[` +
		`{"role":"system","content":"s2"},` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":"resp"},` +
		`{"role":"user","content":"next"}]}`)
	uid, ok := r.RescueDerivedKey(turn2, "", "m", "m")
	if !ok {
		t.Fatal("young session rescue should hit with single overlap")
	}
	if uid != "uid-a" {
		t.Fatalf("young session rescue should return uid-a, got %q", uid)
	}
}

func TestFingerprintRescueMultiHitThreshold(t *testing.T) {
	r := newFPRouter(t, "uid-a", "uid-b")
	// turn1 多轮会话（窗口 3 条）。
	turn1 := fpTestBody("sys v1", "hello", "world")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)

	// 查询：与候选只重叠 1 条（短查询 "hello" 复用）——窗口 >3 → 阈值 2 → 不命中。
	turn2 := []byte(`{"model":"m","messages":[` +
		`{"role":"system","content":"s"},` +
		`{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":"different1"},` +
		`{"role":"user","content":"different2"},` +
		`{"role":"assistant","content":"different3"},` +
		`{"role":"user","content":"different4"}]}`)
	if _, ok := r.RescueDerivedKey(turn2, "", "m", "m"); ok {
		t.Fatal("rescue must not hit below multi-hit threshold (1/3 overlap)")
	}

	// 与候选重叠 2 条 → 命中。
	turn3 := []byte(`{"model":"m","messages":[` +
		`{"role":"system","content":"s"},` +
		`{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":"ok"},` +
		`{"role":"user","content":"world"},` +
		`{"role":"assistant","content":"new1"},` +
		`{"role":"user","content":"new2"}]}`)
	uid, ok := r.RescueDerivedKey(turn3, "", "m", "m")
	if !ok {
		t.Fatal("rescue should hit at threshold (2 overlaps)")
	}
	if uid != "uid-a" {
		t.Fatalf("rescue should return uid-a, got %q", uid)
	}
}

func TestFingerprintReconfigureDisabledSticky(t *testing.T) {
	r := newFPRouter(t, "uid-a")
	turn1 := fpTestBody("sys v1", "hello", "")
	fpBindDerived(t, r, "uid-a", "p:cn:m:d-key1", turn1)
	turn2 := fpTestBody("sys v2", "hello", "world")

	// 粘性整体关停 → 救场连带关停（材料对禁用的粘性路由无意义）。
	r.ReconfigureFingerprint(time.Hour, time.Minute, false, true)
	if r.FingerprintEnabled() {
		t.Fatal("fingerprint must be disabled when sticky disabled")
	}
	if _, ok := r.RescueDerivedKey(turn2, "", "m", "m"); ok {
		t.Fatal("rescue must not hit when sticky disabled")
	}
}

func TestFingerprintLegacyReconfigureSignature(t *testing.T) {
	// 历史三参 Reconfigure：救场默认关，编译与行为兼容回归。
	r := New(Config{TTL: time.Hour, GCInterval: time.Minute, Available: func() []string { return nil }})
	r.Reconfigure(time.Hour, time.Minute, true)
	if r.FingerprintEnabled() {
		t.Fatal("fingerprint should default off via legacy Reconfigure")
	}
	// nil Router 安全。
	var nr *Router
	if nr.FingerprintEnabled() {
		t.Fatal("nil router should report disabled")
	}
	if _, ok := nr.RescueDerivedKey([]byte("{}"), "", "m", "m"); ok {
		t.Fatal("nil router rescue should miss")
	}
	nr.RecordFingerprint("k", "", "m", map[string]any{})
}
