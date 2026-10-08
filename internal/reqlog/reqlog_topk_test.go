package reqlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// writeArchiveFile 直接落一个请求归档文件（绕过 writer，构造任意顺序的历史数据）。
func writeArchiveFile(tb testing.TB, dir, name string, events []Event) {
	tb.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			tb.Fatal(err)
		}
	}
}

// referenceReadTopK 旧实现语义的参照：全量收集匹配事件 → 按时间倒序稳定排序 → 截断。
// 同一时刻保留先扫描到的（文件名字典序 + 文件内行序），用于钉住新 top-K 的排序契约。
func referenceReadTopK(files [][]Event, limit int, filter Filter) []Event {
	var out []Event
	for _, rows := range files {
		for _, e := range rows {
			if filter.match(e) {
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// TestTopKMatchesReferenceOrdering 守护归档查询的内存有界改造不改变结果契约：
// 固定容量堆的输出必须与「全量收集 + stable sort + 截断」逐位一致，
// 包括时间相同的并列项（否则面板最近记录会抖动/错位）。
func TestTopKMatchesReferenceOrdering(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-2 * time.Hour)
	var files [][]Event
	const fileCount, perFile = 5, 200
	// 时间带大量并列：种子按 (i*7+j*13)%50 秒偏移，保证跨文件同刻场景存在。
	for f := 0; f < fileCount; f++ {
		rows := make([]Event, 0, perFile)
		for i := 0; i < perFile; i++ {
			off := time.Duration(((f*7+i*13)%50)*1000) * time.Millisecond
			e := Event{
				Time:      base.Add(off),
				RequestID: fmt.Sprintf("f%d-i%d", f, i),
				Model:     "glm-5.2",
				Status:    200,
				OK:        true,
				Outcome:   OutcomeSuccess,
			}
			rows = append(rows, e)
		}
		files = append(files, rows)
		writeArchiveFile(t, dir, fmt.Sprintf("requests-0%d.jsonl", f), rows)
	}

	w := &archiveWriter{cfg: Config{Enabled: true, Dir: dir}, done: make(chan struct{})}
	for _, limit := range []int{1, 7, 100, 500} {
		got, err := w.read(limit, Filter{})
		if err != nil {
			t.Fatalf("limit=%d read: %v", limit, err)
		}
		want := referenceReadTopK(files, limit, Filter{})
		gotIDs, wantIDs := make([]string, 0, len(got)), make([]string, 0, len(want))
		for _, e := range got {
			gotIDs = append(gotIDs, e.RequestID)
		}
		for _, e := range want {
			wantIDs = append(wantIDs, e.RequestID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("limit=%d 顺序不一致:\n got=%v\nwant=%v", limit, gotIDs, wantIDs)
		}
	}
}

// TestTopKFilterAndTieOrder 覆盖筛选 + 并列时间的取舍：筛选后不足 limit 时全返回，
// 超过时保留最新且同一时刻保留先扫描到的。
func TestTopKFilterAndTieOrder(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now()
	rows := []Event{
		{Time: ts, RequestID: "a", Model: "match"},
		{Time: ts, RequestID: "b", Model: "other"},
		{Time: ts, RequestID: "c", Model: "match"},
		{Time: ts.Add(time.Second), RequestID: "d", Model: "match"},
	}
	writeArchiveFile(t, dir, "requests-01.jsonl", rows)
	w := &archiveWriter{cfg: Config{Enabled: true, Dir: dir}, done: make(chan struct{})}

	got, err := w.read(2, Filter{Model: "match"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RequestID != "d" || got[1].RequestID != "a" {
		t.Fatalf("got=%+v, want [d a]（同刻保留先扫描到的 a）", got)
	}

	all, err := w.read(10, Filter{Model: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].RequestID != "b" {
		t.Fatalf("不足 limit 时应全返回：%+v", all)
	}
}

// BenchmarkReadArchiveLatest100 量化有界化的收益：10 万行归档只取最近 100 条。
// 修复前该操作会保留全部匹配记录（一次查询数百 MB 累计分配），现在为 O(limit)。
func BenchmarkReadArchiveLatest100(b *testing.B) {
	dir := b.TempDir()
	base := time.Now().Add(-24 * time.Hour)
	const perFile = 20000
	var files [][]Event
	for f := 0; f < 5; f++ {
		rows := make([]Event, 0, perFile)
		for i := 0; i < perFile; i++ {
			rows = append(rows, Event{
				Time:      base.Add(time.Duration(f*perFile+i) * time.Millisecond),
				RequestID: fmt.Sprintf("req-%d-%d", f, i),
				Model:     "glm-5.2",
				Status:    200,
				OK:        true,
				Outcome:   OutcomeSuccess,
			})
		}
		files = append(files, rows)
		writeArchiveFile(b, dir, fmt.Sprintf("requests-%02d.jsonl", f), rows)
	}
	w := &archiveWriter{cfg: Config{Enabled: true, Dir: dir}, done: make(chan struct{})}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := w.read(100, Filter{})
		if err != nil || len(rows) != 100 {
			b.Fatalf("rows=%d err=%v", len(rows), err)
		}
	}
}
