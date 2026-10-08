package reqlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeStaleFile 落一个「按保留期该被清理」的归档文件（mtime 早于保留期）。
func writeStaleFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "requests-2000-01-01.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPruneNotRunEverySecond 守护目录扫描降频：
// prune（ReadDir + 逐文件 Info + 排序）此前与 flush 共用 1s ticker，等于每秒白扫
// 一遍目录；保留期是天级、容量是 MB 级，都不需要秒级判定。这里把 pruneInterval
// 设得很长，断言过期文件在若干秒内**不会**被清掉——即 prune 不再每秒执行。
// 若实现回退到「每秒 prune」，本用例会因文件被删而失败。
func TestPruneNotRunEverySecond(t *testing.T) {
	oldInterval := pruneInterval
	pruneInterval = time.Hour
	t.Cleanup(func() { pruneInterval = oldInterval })

	dir := t.TempDir()
	stale := writeStaleFile(t, dir)

	r := New(Config{Enabled: true, Dir: dir, RetentionDays: 7, MaxBytes: 1 << 20})
	defer r.Close()

	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stale); err != nil {
			t.Fatalf("pruneInterval=%v 时过期文件不应被清理（说明仍在每秒扫描）: %v", pruneInterval, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestPruneStillRunsOnInterval 降频不能变成"不再清理"：
// 间隔到点后保留期清理必须照常生效。
func TestPruneStillRunsOnInterval(t *testing.T) {
	oldInterval := pruneInterval
	pruneInterval = 100 * time.Millisecond
	t.Cleanup(func() { pruneInterval = oldInterval })

	dir := t.TempDir()
	stale := writeStaleFile(t, dir)

	r := New(Config{Enabled: true, Dir: dir, RetentionDays: 7, MaxBytes: 1 << 20})
	defer r.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stale); os.IsNotExist(err) {
			return // 已按保留期清理
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("间隔到点后保留期清理未生效")
}

// TestFlushStillTimely 降频只应作用于 prune：写入的数据仍必须在秒级落盘
// （1s 缓冲窗口是既有契约，面板读归档依赖它）。
func TestFlushStillTimely(t *testing.T) {
	oldInterval := pruneInterval
	pruneInterval = time.Hour
	t.Cleanup(func() { pruneInterval = oldInterval })

	dir := t.TempDir()
	r := New(Config{Enabled: true, Dir: dir, RetentionDays: 7, MaxBytes: 1 << 20})
	defer r.Close()
	r.Record(Event{Time: time.Now(), RequestID: "flush-timeliness", Status: 200, OK: true})

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if rows, err := r.ReadArchive(10, Filter{}); err == nil && len(rows) > 0 {
			if rows[0].RequestID != "flush-timeliness" {
				t.Fatalf("unexpected row: %+v", rows[0])
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("归档写入未在秒级落盘（flush 时效被降频影响）")
}
