package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestBalanceRefreshHotEnableAfterDisabledStart 守护「关闭时启动 + 面板热开启」：
// 旧实现 interval<=0 直接 return，不启动 goroutine；之后 SetBalanceInterval
// 只改原子值并发通知，没有任何消费者——面板显示已开启、实际永不刷新余额。
func TestBalanceRefreshHotEnableAfterDisabledStart(t *testing.T) {
	s := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 关闭状态启动（config schedule.balance_refresh_enabled=false）。
	s.StartBalanceRefresh(ctx, 0)

	var calls atomic.Int64
	oldHook := balanceRefreshNow
	balanceRefreshNow = func(*Scheduler) { calls.Add(1) }
	defer func() { balanceRefreshNow = oldHook }()

	// 面板热开启：间隔 10ms，等待若干轮。
	s.SetBalanceInterval(10 * time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("热开启后余额刷新循环未运行")
	}

	// 再热关闭：不再产生新调用。
	s.SetBalanceInterval(0)
	time.Sleep(30 * time.Millisecond)
	before := calls.Load()
	time.Sleep(60 * time.Millisecond)
	if got := calls.Load(); got != before {
		t.Fatalf("暂停后不应继续刷新：before=%d after=%d", before, got)
	}
}
