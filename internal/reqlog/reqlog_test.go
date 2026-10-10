package reqlog

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecorderMetricsAndRecent(t *testing.T) {
	r := New(Config{})
	r.Begin()
	r.Record(Event{RequestID: "ok", Status: 200, OK: true, Outcome: OutcomeSuccess, DurationMs: 10})
	r.Begin()
	r.Record(Event{RequestID: "bad", Status: 429, OK: false, Outcome: OutcomeHTTPError, DurationMs: 30})

	s := r.Snapshot()
	if s.Completed != 2 || s.InFlight != 0 || s.Succeeded != 1 || s.Failed != 1 {
		t.Fatalf("counts = %+v", s)
	}
	if s.SuccessRate != 50 || s.HTTPSuccessRate != 50 || s.AvgDurationMs != 20 {
		t.Fatalf("rates = success:%v http:%v avg:%v", s.SuccessRate, s.HTTPSuccessRate, s.AvgDurationMs)
	}
	if len(s.Recent) != 2 || s.Recent[0].RequestID != "bad" || s.Recent[1].RequestID != "ok" {
		t.Fatalf("recent = %+v, want newest first", s.Recent)
	}
}

func TestArchiveRotationReadAndFilter(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Enabled: true, Dir: dir, FileMaxBytes: 120, MaxBytes: 1 << 20, RetentionDays: 7})
	base := time.Now().Add(-time.Minute)
	for i := 0; i < 12; i++ {
		r.Record(Event{
			Time:       base.Add(time.Duration(i) * time.Second),
			RequestID:  "multi-" + string(rune('a'+i)),
			Model:      "glm-5.3",
			Account:    "账号(uid8)",
			Status:     200,
			OK:         true,
			Outcome:    OutcomeSuccess,
			DurationMs: int64(i + 1),
		})
	}
	r.Record(Event{
		Time:       base.Add(20 * time.Second),
		RequestID:  "other",
		Model:      "other-model",
		Status:     500,
		Outcome:    OutcomeHTTPError,
		DurationMs: 99,
	})
	r.Close()

	stats := r.Snapshot().Archive
	if !stats.Enabled || stats.Files < 2 || stats.Bytes == 0 || stats.DroppedWrites != 0 {
		t.Fatalf("archive stats = %+v", stats)
	}
	rows, err := r.ReadArchive(5, Filter{Model: "glm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || rows[0].RequestID != "multi-l" || rows[4].RequestID != "multi-h" {
		t.Fatalf("filtered rows = %+v", rows)
	}
}

// ReadArchive 的契约是「按事件时间倒序返回最近记录」，与归档文件的 mtime 无关。
// 快速轮转（FileMaxBytes 很小）时同一秒内会写出多个文件，Linux 下这些文件的 mtime
// 可能完全相同；把归档目录整体拷贝 / 恢复备份也会打乱 mtime。此用例把所有归档文件的
// mtime 改成与事件时间相反的顺序，钉住「只能按事件时间排序」。
func TestReadArchiveOrdersByEventTimeNotFileMtime(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Enabled: true, Dir: dir, FileMaxBytes: 1, MaxBytes: 1 << 20, RetentionDays: 7})
	base := time.Now().Add(-time.Hour)
	const n = 5
	for i := 0; i < n; i++ {
		r.Record(Event{
			Time:      base.Add(time.Duration(i) * time.Second),
			RequestID: "ev-" + string(rune('a'+i)),
			Model:     "glm-5.3",
			Status:    200,
			OK:        true,
			Outcome:   OutcomeSuccess,
		})
	}
	r.Close()

	// FileMaxBytes=1 让每个事件独占一个归档文件：基准文件 requests-<day>.jsonl 装的是
	// 最早的事件，字典序却排在 requests-<day>.N.jsonl 之后。按 os.ReadDir 顺序递增地
	// 设置 mtime，最早事件所在文件就拿到最大 mtime、被放在最后读取。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Fatalf("归档文件数 = %d, want %d", len(entries), n)
	}
	stamp := time.Unix(1700000000, 0)
	for i, e := range entries {
		ts := stamp.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(filepath.Join(dir, e.Name()), ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := r.ReadArchive(n, Filter{Model: "glm"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, e := range rows {
		got = append(got, e.RequestID)
	}
	if len(rows) != n {
		t.Fatalf("rows = %d, want %d", len(rows), n)
	}
	for i := range rows {
		want := "ev-" + string(rune('a'+n-1-i))
		if rows[i].RequestID != want {
			t.Fatalf("rows[%d] = %s, want %s（须按事件时间倒序，与文件 mtime 无关）\n got %v", i, rows[i].RequestID, want, got)
		}
	}
}

// 来源字段随事件落盘并可被 client_ip / user_agent 过滤（包含匹配、大小写不敏感）。
func TestArchiveFilterByClientInfo(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Enabled: true, Dir: dir, MaxBytes: 1 << 20, RetentionDays: 7})
	base := time.Now().Add(-time.Minute)
	rows := []Event{
		{RequestID: "a", Model: "glm-5.3", Status: 200, OK: true, Outcome: OutcomeSuccess,
			ClientIP: "203.0.113.7", UserAgent: "python-requests/2.31.0"},
		{RequestID: "b", Model: "glm-5.3", Status: 200, OK: true, Outcome: OutcomeSuccess,
			ClientIP: "198.51.100.4", UserAgent: "Mozilla/5.0 Chrome/120.0.0.0"},
		{RequestID: "c", Model: "glm-5.3", Status: 200, OK: true, Outcome: OutcomeSuccess},
	}
	for i, e := range rows {
		e.Time = base.Add(time.Duration(i) * time.Second)
		r.Record(e)
	}
	r.Close()

	got, err := r.ReadArchive(10, Filter{ClientIP: "203.0.113."})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "a" {
		t.Fatalf("client ip filter = %+v", got)
	}
	if got[0].UserAgent != "python-requests/2.31.0" {
		t.Fatalf("user agent not persisted: %+v", got[0])
	}

	// UA 片段过滤大小写不敏感（面板搜索框直接传用户输入）。
	got, err = r.ReadArchive(10, Filter{UserAgent: "chrome/120"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "b" {
		t.Fatalf("ua filter = %+v", got)
	}

	// 来源为空的旧事件不该被非空来源条件误命中。
	got, err = r.ReadArchive(10, Filter{ClientIP: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unmatched ip filter = %+v", got)
	}
}

// 时间区间过滤（「今天」/「自定义」）：闭区间，任一侧为零值即该侧不设界。
func TestArchiveFilterByTimeRange(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Enabled: true, Dir: dir, MaxBytes: 1 << 20, RetentionDays: 30})
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Minute)
	ids := []string{"d3", "d2", "d1", "now"}
	for i, id := range ids {
		r.Record(Event{
			Time: base.Add(time.Duration(i) * 24 * time.Hour), RequestID: id,
			Status: 200, OK: true, Outcome: OutcomeSuccess,
		})
	}
	r.Close()

	rows, err := r.ReadArchive(10, Filter{From: base.Add(24 * time.Hour), To: base.Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, e := range rows {
		got = append(got, e.RequestID)
	}
	// 倒序返回，闭区间命中 d2、d1。
	if len(got) != 2 || got[0] != "d1" || got[1] != "d2" {
		t.Fatalf("时间区间过滤 = %v, want [d1 d2]", got)
	}

	onlyFrom, err := r.ReadArchive(10, Filter{From: base.Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyFrom) != 2 || onlyFrom[0].RequestID != "now" {
		t.Fatalf("仅 From 过滤 = %+v, want 2 条（d1、now）", onlyFrom)
	}

	onlyTo, err := r.ReadArchive(10, Filter{To: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyTo) != 1 || onlyTo[0].RequestID != "d3" {
		t.Fatalf("仅 To 过滤 = %+v, want 仅 d3", onlyTo)
	}

	// 时间区间与其它条件是与关系。
	combined, err := r.ReadArchive(10, Filter{
		From: base.Add(24 * time.Hour), To: base.Add(48 * time.Hour), Outcome: OutcomeHTTPError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(combined) != 0 {
		t.Fatalf("区间+结果组合应为空（这几条都是成功）: %+v", combined)
	}
}

func TestArchiveQueueDropCounter(t *testing.T) {
	w := &archiveWriter{
		cfg:  Config{Enabled: true, Dir: t.TempDir()},
		ch:   make(chan Event, 1),
		done: make(chan struct{}),
	}
	w.ch <- Event{RequestID: "occupied"}
	w.enqueue(Event{RequestID: "dropped"})
	if got := w.dropped.Load(); got != 1 {
		t.Fatalf("dropped=%d want 1", got)
	}
}

func TestArchivePruneHonorsSize(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"requests-2026-09-20.jsonl", "requests-2026-09-21.jsonl", "requests-2026-09-22.jsonl"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().AddDate(0, 0, -10+i)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	w := &archiveWriter{cfg: Config{Enabled: true, Dir: dir, RetentionDays: 30, MaxBytes: 50}, done: make(chan struct{})}
	w.prune()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "requests-2026-09-22.jsonl" {
		t.Fatalf("remaining = %+v, want newest file only", entries)
	}
}

// TestRecorderReconfigureArchive 归档参数热改：关闭 → 开启 → 换目录 → 再关闭。
// 内存指标跨重配置保留；旧 writer 排空后新事件只落新目录；同配置重复提交是空操作。
func TestRecorderReconfigureArchive(t *testing.T) {
	r := New(Config{Enabled: false})
	if s := r.Snapshot().Archive; s.Enabled {
		t.Fatalf("初始应为关闭：%+v", s)
	}
	r.Record(Event{RequestID: "off-1", Status: 200, OK: true}) // 关闭态不落盘

	dir1 := t.TempDir()
	if !r.Reconfigure(Config{Enabled: true, Dir: dir1, RetentionDays: 7, MaxBytes: 1 << 20}) {
		t.Fatal("启用归档应返回 changed=true")
	}
	if !r.Snapshot().Archive.Enabled {
		t.Fatal("启用后 Archive.Enabled 应为 true")
	}
	r.Record(Event{RequestID: "on-1", Status: 200, OK: true})

	dir2 := t.TempDir()
	if !r.Reconfigure(Config{Enabled: true, Dir: dir2, RetentionDays: 3, MaxBytes: 1 << 20}) {
		t.Fatal("换目录应返回 changed=true")
	}
	r.Record(Event{RequestID: "on-2", Status: 200, OK: true})

	if r.Reconfigure(Config{Enabled: true, Dir: dir2, RetentionDays: 3, MaxBytes: 1 << 20}) {
		t.Fatal("同配置重复提交应返回 changed=false")
	}

	if !r.Reconfigure(Config{Enabled: false}) {
		t.Fatal("关闭归档应返回 changed=true")
	}
	r.Record(Event{RequestID: "off-2", Status: 200, OK: true})
	if s := r.Snapshot().Archive; s.Enabled {
		t.Fatalf("关闭后 Archive.Enabled 应为 false：%+v", s)
	}
	r.Close() // 排空两代 writer

	if got := r.Snapshot().Completed; got != 4 {
		t.Fatalf("内存指标应跨重配置保留：completed=%d want 4", got)
	}
	if _, err := r.ReadArchive(100, Filter{}); err != nil {
		t.Fatalf("关闭后仍应可读历史归档：%v", err)
	}

	ids := func(dir string) []string {
		t.Helper()
		var out []string
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if err := readFile(filepath.Join(dir, entry.Name()), Filter{}, func(e Event) {
				out = append(out, e.RequestID)
			}); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	d1, d2 := ids(dir1), ids(dir2)
	if len(d1) != 1 || d1[0] != "on-1" {
		t.Fatalf("dir1 事件 = %v want [on-1]", d1)
	}
	if len(d2) != 1 || d2[0] != "on-2" {
		t.Fatalf("dir2 事件 = %v want [on-2]", d2)
	}
}

// TestReconfigureConcurrentRecord 归档参数热改与请求记录并发：不得数据竞争或 panic。
func TestReconfigureConcurrentRecord(t *testing.T) {
	r := New(Config{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			r.Record(Event{RequestID: "x", Status: 200, OK: true})
		}
	}()
	dir1, dir2 := t.TempDir(), t.TempDir()
	for i := 0; i < 50; i++ {
		if i%2 == 0 {
			r.Reconfigure(Config{Enabled: true, Dir: dir1, RetentionDays: 7, MaxBytes: 1 << 20})
		} else {
			r.Reconfigure(Config{Enabled: true, Dir: dir2, RetentionDays: 7, MaxBytes: 1 << 20})
		}
		r.Snapshot()
	}
	r.Reconfigure(Config{Enabled: false})
	close(stop)
	wg.Wait()
	r.Close()
}

// TestArchiveWriterRetiredRejectsEvents 已退休 writer 必须拒收事件：
// 拒收（返回 false）让 Record 能改投当前 writer；若静默收下就会丢进无人消费的
// 缓冲（Reconfigure 期间的事件凭空消失）。
func TestArchiveWriterRetiredRejectsEvents(t *testing.T) {
	w := newArchiveWriter(normalizeArchiveConfig(Config{Enabled: true, Dir: t.TempDir(), RetentionDays: 7, MaxBytes: 1 << 20}))
	if !w.enqueue(Event{RequestID: "live"}) {
		t.Fatal("活跃 writer 应收下事件")
	}
	w.close()
	if w.enqueue(Event{RequestID: "retired"}) {
		t.Fatal("已退休 writer 必须拒收事件（否则事件静默丢失）")
	}
	// 退休后仍可观测（stats/read 走 enabled 口径，供面板看历史）。
	if !w.stats().Enabled {
		t.Fatal("退休 writer 仍应报告 enabled（历史归档可读）")
	}
}

// TestRecorderReconfigureAfterCloseIsNoop 关停后再保存配置不得新建归档 goroutine。
func TestRecorderReconfigureAfterCloseIsNoop(t *testing.T) {
	r := New(Config{Enabled: true, Dir: t.TempDir(), RetentionDays: 7, MaxBytes: 1 << 20})
	if !r.Reconfigure(Config{Enabled: false}) {
		t.Fatal("关闭归档应生效")
	}
	r.Close()
	if r.Reconfigure(Config{Enabled: true, Dir: t.TempDir(), RetentionDays: 7, MaxBytes: 1 << 20}) {
		t.Fatal("Close 之后不得再新建 writer")
	}
}

// 改写在归档里必须与丢弃一样可往返（面板「丢弃 / 改写」列与搜索直接读归档 JSONL）。
// 这两个字段语义不同且上限独立，所以序列化契约要各自锁一条。
func TestArchiveRoundTripsRewrittenAndPath(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Dir: dir, Enabled: true})
	defer r.Close()
	r.Record(Event{
		RequestID: "req-1", Path: "/v1/responses", Status: 200, OK: true,
		Dropped:   []string{"include", "reasoning.summary"},
		Rewritten: []string{"reasoning.effort:max→xhigh"},
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows, err := r.ReadArchive(10, Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			e := rows[0]
			if e.Path != "/v1/responses" {
				t.Fatalf("path=%q", e.Path)
			}
			if len(e.Rewritten) != 1 || e.Rewritten[0] != "reasoning.effort:max→xhigh" {
				t.Fatalf("rewritten=%v", e.Rewritten)
			}
			if len(e.Dropped) != 2 {
				t.Fatalf("dropped=%v", e.Dropped)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("归档未在期限内落盘")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
