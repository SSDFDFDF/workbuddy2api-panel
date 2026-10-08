package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestIngressNoLostWakeup 守护漏唤醒缺陷：占位失败的尝试与「取得等待通道」必须
// 原子完成（见 ingressLimiter.tryAcquireOrWait）。
//
// 反例（修复前：tryAcquire 之后**另起一次加锁**读 l.notify）：释放若落在两次加锁
// 之间，等待者拿到的是释放后新建的通道——空间已经空闲，它却要一直等到超时。
// 默认等待 5s，表现为客户端白等 5 秒后收到 503，而此时网关其实空闲（同时污染
// rejected 计数，把观测指标也带偏）。
//
// 为什么注入 contend 个抢同一把锁的 goroutine：该窗口在无竞争时只有纳秒级，
// 实测 20000 轮才复现 1 次；而有真实竞争（其它 Acquire/release/Stats 调用）时
// 释放极易挤进窗口——这正是生产环境并发下的真实形态。实测旧实现：竞争 4 路
// 复现 38/20000、16 路 481/20000；本用例取 8 路，判别力充足。
//
// 断言的可靠性：全程只有一个名额，且**等待者一开始等待就立即归还**，因此不存在
// 合法拒绝——任何一次拒绝都只能是丢失唤醒（修复后原子取通道，释放不可能被漏掉）。
func TestIngressNoLostWakeup(t *testing.T) {
	const (
		rounds    = 1500
		contend   = 8
		waitLimit = 100 * time.Millisecond
	)
	l := newIngressLimiter(1, 0, waitLimit)

	// 名额充足时先热身，确保 contend goroutine 已进入锁竞争。
	var stop atomic.Bool
	done := make(chan struct{}, contend)
	for i := 0; i < contend; i++ {
		go func() {
			for !stop.Load() {
				_ = l.Stats()
			}
			done <- struct{}{}
		}()
	}
	defer func() {
		stop.Store(true)
		for i := 0; i < contend; i++ {
			<-done
		}
	}()

	for i := 0; i < rounds; i++ {
		rel, ok := l.Acquire(context.Background(), 1)
		if !ok {
			t.Fatalf("round %d: 首个名额必须成功", i)
		}
		admitted := make(chan bool, 1)
		go func() {
			r2, ok2 := l.Acquire(context.Background(), 1)
			if ok2 {
				r2()
			}
			admitted <- ok2
		}()
		time.Sleep(30 * time.Microsecond) // 让等待者先落到"首次尝试失败"
		rel()
		if !<-admitted {
			t.Fatalf("round %d: 名额已归还却被拒（丢失唤醒），当前在途 %d",
				i, l.Stats().InFlight)
		}
	}
}
