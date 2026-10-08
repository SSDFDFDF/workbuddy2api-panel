package redisstore

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingFirst 构造一个「第一笔写阻塞在 release 上」的实例（不触网络）。
func blockingFirst(release <-chan struct{}, entered chan<- struct{}) *Upstash {
	u := &Upstash{shardCount: 1}
	u.ensure()
	u.goWrite("k", func() {
		close(entered)
		<-release
	})
	return u
}

// TestWriteQueueIsBounded 守护「队列有界」：
// 旧实现是「每笔写一个 goroutine + 信号量限执行」，槽位 1 时阻塞首写后提交 2000 笔，
// 会立刻多出 2000 个等待 goroutine（实测）。新实现固定 worker + 有界队列：
// goroutine 增量必须与提交量无关，超出容量的写被丢弃并计数。
func TestWriteQueueIsBounded(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	u := blockingFirst(release, entered)
	<-entered

	before := runtime.NumGoroutine()
	const submitted = 5000
	for i := 0; i < submitted; i++ {
		u.goWrite("k", func() {})
	}
	growth := runtime.NumGoroutine() - before
	t.Logf("队列容量=%d，提交 %d 笔后 goroutine 增量=%d，丢弃=%d", writeQueuePerShard, submitted, growth, u.DroppedWrites())

	if growth > 4 {
		t.Fatalf("goroutine 增量 %d 说明队列没有真正有界", growth)
	}
	if u.DroppedWrites() == 0 {
		t.Fatalf("超出容量的写必须计入丢弃（容量=%d，提交=%d）", writeQueuePerShard, submitted)
	}
	if u.DroppedWrites() >= submitted {
		t.Fatalf("全部丢弃不合理（丢弃=%d）", u.DroppedWrites())
	}
	close(release)
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestSameKeyOrderingPreserved 同 key 的写必须按提交顺序执行：
// SetBind / DelBind 乱序会让已删除的绑定在 Redis 里复活（重启后粘性指向过期账号）。
// 用单分片实例验证：即使首笔阻塞，后续同 key 写也只能排在其后。
func TestSameKeyOrderingPreserved(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	u := blockingFirst(release, entered)
	<-entered

	var mu sync.Mutex
	var order []int
	for i := 1; i <= 20; i++ {
		i := i
		u.goWrite("same-key", func() {
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
		})
	}
	close(release)
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 20 {
		t.Fatalf("执行了 %d 笔，want 20", len(order))
	}
	for i, v := range order {
		if v != i+1 {
			t.Fatalf("同 key 顺序被破坏：order=%v", order)
		}
	}
}

// TestCloseDrainsQueuedWrites Close 必须排空已入队的写（停机镜像完整性）。
func TestCloseDrainsQueuedWrites(t *testing.T) {
	u := &Upstash{shardCount: 1}
	u.ensure()
	var n atomic.Int64
	for i := 0; i < 50; i++ {
		u.goWrite("k", func() { n.Add(1) })
	}
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 50 {
		t.Fatalf("Close 只执行了 %d/50 笔已入队写", got)
	}
}

// TestWriteAfterCloseIsDropped Close 后提交的写直接丢弃且不 panic，Close 幂等。
func TestWriteAfterCloseIsDropped(t *testing.T) {
	u := &Upstash{shardCount: 1}
	u.ensure()
	var n atomic.Int64
	u.goWrite("k", func() { n.Add(1) })
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	u.goWrite("k", func() { n.Add(1) }) // 关停后提交：丢弃
	if err := u.Close(); err != nil {   // 幂等
		t.Fatal(err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("执行了 %d 笔，want 1（关停后写入应丢弃）", got)
	}
}

// TestZeroValueUpstashUsable 零值构造（测试/异常路径）不能 panic。
func TestZeroValueUpstashUsable(t *testing.T) {
	u := &Upstash{}
	var n atomic.Int64
	u.goWrite("k", func() { n.Add(1) })
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("零值实例未执行写入：%d", n.Load())
	}
}

// TestConcurrentSubmitAndClose 并发提交 + Close（-race 下的关停竞态回归）：
// 提交方在锁内检查 closed，Close 在锁内置 closed 并关队列，二者不得交错出 panic。
func TestConcurrentSubmitAndClose(t *testing.T) {
	for round := 0; round < 20; round++ {
		u := &Upstash{shardCount: 2}
		u.ensure()
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						u.goWrite("bind:x", func() { time.Sleep(time.Microsecond) })
					}
				}
			}()
		}
		time.Sleep(time.Millisecond)
		if err := u.Close(); err != nil {
			t.Fatal(err)
		}
		close(stop)
		wg.Wait()
	}
}
