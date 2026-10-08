package usage

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func usageBucketCount(r *Recorder) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}

func usageRequestTotal(r *Recorder) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var total int64
	for _, b := range r.buckets {
		total += b.Req
	}
	return total
}

func usageHourScopes(r *Recorder) (hourly, daily int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.buckets {
		if strings.HasPrefix(b.Scope, "h:") {
			hourly++
		} else {
			daily++
		}
	}
	return
}

// TestRetentionFoldsExpiredBucketsUnderCap 守护保留期策略真正生效：
// 旧实现只在 len(buckets) > maxBuckets 时才折叠，未触顶的实例里 90 天前的小时桶
// 永不折叠（保留期承诺形同虚设，内存与 JSON 持续膨胀）。enforceRetention 必须
// 在未超上限时也折叠过期小时桶，且只降分辨率、不丢计数。
func TestRetentionFoldsExpiredBucketsUnderCap(t *testing.T) {
	r := New("")
	// 基点在 100 天前的同一天 10:00（本地时区），+0..4h 不会跨日历日，
	// 保证折叠结果恰好是 1 个日桶（跨零点会拆成 2 个，与本用例断言无关）。
	past := time.Now().AddDate(0, 0, -100)
	old := time.Date(past.Year(), past.Month(), past.Day(), 10, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		r.Add(old.Add(time.Duration(i)*time.Hour), "cn", "u1", "glm-5.2", Delta{TotalTokens: 3, HasTotal: true}, true)
	}
	r.Add(time.Now(), "cn", "u1", "glm-5.2", Delta{TotalTokens: 4, HasTotal: true}, true)

	if h, d := usageHourScopes(r); h != 6 || d != 0 {
		t.Fatalf("前置状态异常：hourly=%d daily=%d", h, d)
	}
	r.enforceRetention(time.Now())

	if h, d := usageHourScopes(r); h != 1 || d != 1 {
		t.Fatalf("过期小时桶应折叠为 1 个日桶、保留 1 个近期小时桶：hourly=%d daily=%d", h, d)
	}
	if got := usageRequestTotal(r); got != 6 {
		t.Fatalf("折叠不得丢数据：requests=%d want 6", got)
	}
	// 24h 窗口不含 100 天前的日桶；全部历史（hours=0）仍能看到它。
	if s := r.Snapshot(24, nil); s.Totals.Requests != 1 {
		t.Fatalf("24h 窗口 requests=%d want 1", s.Totals.Requests)
	}
	s := r.Snapshot(0, nil)
	if s.Totals.Requests != 6 || s.Totals.TotalTokens != 19 {
		t.Fatalf("全部历史 requests=%d tokens=%d want 6/19", s.Totals.Requests, s.Totals.TotalTokens)
	}
}

// TestEnforceRetentionFoldsOldestWhenOverCap 守护超上限时的自适应折叠：
// 上限内已无过期小时桶，仍超出容量时从最老的小时桶折叠（降分辨率），
// 任何情况下都不得丢计数。
func TestEnforceRetentionFoldsOldestWhenOverCap(t *testing.T) {
	oldLimit := maxBuckets
	maxBuckets = 60
	t.Cleanup(func() { maxBuckets = oldLimit })

	r := New("")
	now := time.Now().Truncate(time.Hour)
	const hours = 200
	for i := 0; i < hours; i++ {
		r.Add(now.Add(-time.Duration(i)*time.Hour), "cn", "u1", "glm-5.2",
			Delta{TotalTokens: 1, HasTotal: true}, true)
	}
	before := usageBucketCount(r)
	r.enforceRetention(now)
	after := usageBucketCount(r)

	if after >= before {
		t.Fatalf("超上限时必须折叠降数量：before=%d after=%d", before, after)
	}
	if after > maxBuckets {
		t.Fatalf("折叠后仍超上限：after=%d limit=%d", after, maxBuckets)
	}
	if got := usageRequestTotal(r); got != hours {
		t.Fatalf("自适应折叠不得丢数据：requests=%d want %d", got, hours)
	}
	if h, d := usageHourScopes(r); d == 0 || h == 0 {
		t.Fatalf("应同时保留小时桶与折叠出的日桶：hourly=%d daily=%d", h, d)
	}
}

// TestEnforceRetentionWithoutHourlyBucketsWarnsOnly 全为日桶时不能再降：
// 不 panic、不删数据、不重复告警（小时级节流）。
func TestEnforceRetentionWithoutHourlyBucketsWarnsOnly(t *testing.T) {
	oldLimit := maxBuckets
	maxBuckets = 2
	t.Cleanup(func() { maxBuckets = oldLimit })

	r := New("")
	old := time.Now().AddDate(0, 0, -100)
	for i := 0; i < 10; i++ {
		r.Add(old.Add(time.Duration(i)*24*time.Hour), "cn", fmt.Sprintf("u%d", i), "glm-5.2", Delta{}, true)
	}
	r.enforceRetention(time.Now())
	before := usageBucketCount(r)
	r.enforceRetention(time.Now().Add(time.Minute))
	if after := usageBucketCount(r); after != before {
		t.Fatalf("无法再折叠时不得改变桶数：before=%d after=%d", before, after)
	}
	if got := usageRequestTotal(r); got != 10 {
		t.Fatalf("不得丢数据：requests=%d", got)
	}
	if r.lastCapWarn.IsZero() {
		t.Fatal("超上限无法折叠时必须留下告警时间戳（可观测）")
	}
}

// BenchmarkSnapshotWindowOnlyCopiesWindow 量化窗口查询的复制范围：
// 85% 的桶在窗口之外时，24h 查询应只复制窗口内的桶（旧实现全量拷贝）。
func BenchmarkSnapshotWindowOnlyCopiesWindow(b *testing.B) {
	r := New("")
	now := time.Now().Truncate(time.Hour)
	const recent, oldHours = 500, 3000
	for i := 0; i < recent; i++ {
		r.Add(now.Add(-time.Duration(i)*time.Minute), "cn", "u1", "glm-5.2", Delta{}, true)
	}
	for i := 0; i < oldHours; i++ {
		// 旧的桶散落在不同 (uid, model) 上，确保它们不会被折叠压缩掉。
		r.Add(now.Add(-time.Duration(i+48)*time.Hour), "cn", fmt.Sprintf("u%d", i), "glm-5.2", Delta{}, true)
	}
	b.ReportMetric(float64(usageBucketCount(r)), "buckets")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s := r.Snapshot(24, nil); s.Totals.Requests == 0 {
			b.Fatal("窗口内应有数据")
		}
	}
}
