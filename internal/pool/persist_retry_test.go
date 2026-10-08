package pool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

// hasPendingSave 报告是否存在尚未成功落盘的变更（版本号口径）。
func (p *Pool) hasPendingSave() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rev != p.savedRev
}

// TestFlushFailureKeepsDirtyForRetry 守护池状态落盘的重试语义：
// 旧实现提前清脏标记后写盘失败即丢标记——磁盘满/路径不可写时，若此后没有新的
// 状态变更，内存状态再也不会落盘（重启回到旧 state.json）。修复后失败必须
// 保持待落盘版本，路径恢复后自动补写。
func TestFlushFailureKeepsDirtyForRetry(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateFp := filepath.Join(blocked, "state.json")

	p := New(stateFp)
	defer p.Close()
	p.Add(&auth.Auth{UID: "flush-retry-uid"})
	p.SetCredits("flush-retry-uid", 123, 200)
	if !p.hasPendingSave() {
		t.Fatal("SetCredits 后必须存在待落盘版本")
	}

	p.Flush()
	if !p.hasPendingSave() {
		t.Fatal("落盘失败后必须保持待落盘版本，供下一轮重试")
	}
	if _, err := os.Stat(stateFp); err == nil {
		t.Fatal("失败路径不应产生 state.json")
	}

	// 恢复可写路径：删除阻塞文件并建目录 → 下一次 Flush 必须补写成功。
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	p.Flush()
	if p.hasPendingSave() {
		t.Fatal("落盘成功后必须清空待落盘版本")
	}
	raw, err := os.ReadFile(stateFp)
	if err != nil {
		t.Fatalf("重试后必须写出 state.json: %v", err)
	}
	if !strings.Contains(string(raw), "flush-retry-uid") {
		t.Fatalf("state.json 缺少账号内容: %s", raw)
	}
}

// TestSaveSnapshotsRevisionSoConcurrentChangesSurvive 守护「落盘期间的变更不丢」：
// 落盘在锁外进行，若成功后无脑清空待落盘状态，save 期间产生的变更会被误标为
// 已落盘（重启丢更新）。正确的推进口径是「推进到快照当时的版本」。
// 用 persistWriteHook 模拟慢磁盘：写盘瞬间注入一次新的状态变更。
func TestSaveSnapshotsRevisionSoConcurrentChangesSurvive(t *testing.T) {
	stateFp := filepath.Join(t.TempDir(), "state.json")
	p := New(stateFp)
	defer p.Close()
	p.Add(&auth.Auth{UID: "rev-uid"})
	p.SetCredits("rev-uid", 10, 100)

	var once sync.Once
	persistWriteHook = func() {
		once.Do(func() {
			// 快照已经取走（rev 已被记录），此刻发生的新变更必须仍然待落盘。
			p.SetCredits("rev-uid", 999, 100)
		})
	}
	defer func() { persistWriteHook = nil }()

	p.Flush()
	if !p.hasPendingSave() {
		t.Fatal("落盘期间的新变更必须仍为待落盘（不能推进到更新的版本）")
	}

	// 第二次落盘把新值写出去。
	p.Flush()
	if p.hasPendingSave() {
		t.Fatal("第二次落盘后应无待落盘版本")
	}
	raw, _ := os.ReadFile(stateFp)
	if !strings.Contains(string(raw), "999") {
		t.Fatalf("最终 state.json 应包含最新 credits=999: %s", raw)
	}
}

// TestSaveDoesNotHoldPoolLockDuringDiskIO 守护「磁盘 I/O 不占路由锁」：
// 写盘期间必须仍能并发读取池状态（List/健康检查/选号），否则慢磁盘会阻塞在途请求。
// 注：读侧必须放在单独 goroutine——若实现仍持锁写盘，主测试 goroutine 自己去读会
// 直接挂在锁上（用例变成挂死而不是失败）。
func TestSaveDoesNotHoldPoolLockDuringDiskIO(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	p.Add(&auth.Auth{UID: "lock-uid"})
	p.SetCredits("lock-uid", 5, 100)

	started := make(chan struct{})
	release := make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	persistWriteHook = func() {
		close(started)
		<-release
	}
	defer func() { persistWriteHook = nil; unblock() }()

	writerDone := make(chan struct{})
	go func() { defer close(writerDone); p.Flush() }()
	<-started

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for i := 0; i < 200; i++ {
			_ = p.List()
			_ = p.ServableNow()
		}
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		unblock()
		<-writerDone
		t.Fatal("写盘期间池锁被占用：读路径被阻塞（磁盘 I/O 必须在锁外）")
	}
	unblock()
	<-writerDone
}
