package usage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestFlushFailureRetriesOnNextFlush 守护「落盘失败不推进已落盘版本」：
// 旧实现提前清 dirty，磁盘满/父路径不可写时一次失败即永久停写内存态
// （直到下一次数据变更），重启即丢历史。修复后失败保留待落盘版本，
// 路径恢复后下一次 flush 必须补写成功。
func TestFlushFailureRetriesOnNextFlush(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocked, "usage.json") // 父路径是文件 → MkdirAll/WriteFile 必失败

	r := New(path)
	r.Add(time.Now(), "cn", "u1", "glm-5.2", Delta{TotalTokens: 7, HasTotal: true}, true)
	if r.rev == r.flushedRev {
		t.Fatal("Add 后必须存在未落盘版本")
	}
	r.flush(true)
	if r.flushedRev != 0 {
		t.Fatalf("落盘失败不得推进已落盘版本，flushedRev=%d", r.flushedRev)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("失败路径不应产生文件")
	}

	// 让路径恢复可写：删掉阻塞文件并建目录 → 下一次 flush 应补写。
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	r.flush(false)
	if r.flushedRev != r.rev {
		t.Fatalf("恢复后必须把待落盘版本写出：rev=%d flushedRev=%d", r.rev, r.flushedRev)
	}
	r2 := New(path)
	if s := r2.Snapshot(0, nil); s.Totals.Requests != 1 || s.Totals.TotalTokens != 7 {
		t.Fatalf("重放后 totals = %d/%d, want 1/7", s.Totals.Requests, s.Totals.TotalTokens)
	}
}

// TestConcurrentSaveAndAddKeepsAllData 守护落盘串行化与版本推进：
// 面板手动 Save 与后台防抖 flush 并发时共用同一 .tmp，旧实现互相踩文件；
// 同时验证成功落盘后所有已记录数据都在（不因快照时机丢计数）。
func TestConcurrentSaveAndAddKeepsAllData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	r := New(path)

	const base = 40
	for i := 0; i < base; i++ {
		r.Add(time.Now(), "cn", "u1", "glm-5.2", Delta{TotalTokens: 1, HasTotal: true}, true)
	}

	const extra = 160
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				r.Save()
			}
		}()
	}
	for a := 0; a < 4; a++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < extra/4; i++ {
				r.Add(time.Now(), "cn", "u1", "glm-5.2", Delta{TotalTokens: 1, HasTotal: true}, true)
			}
		}()
	}
	wg.Wait()
	r.Save()

	r2 := New(path)
	s := r2.Snapshot(0, nil)
	if s.Totals.Requests != base+extra {
		t.Fatalf("落盘后请求数 = %d, want %d", s.Totals.Requests, base+extra)
	}
}

// TestFlushSkipsUnchanged 守护“无变更不写盘”的省电语义仍然成立（force=false）。
func TestFlushSkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	r := New(path)
	r.flush(false) // 空快照且无变更：直接返回，不创建文件
	if _, err := os.Stat(path); err == nil {
		t.Fatal("无变更时不应落盘")
	}
	r.Add(time.Now(), "cn", "u1", "glm-5.2", Delta{}, true)
	r.flush(false)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("有变更时必须落盘: %v", err)
	}
	fi1, _ := os.Stat(path)
	r.flush(false) // 已落盘且无新变更：不重写
	fi2, _ := os.Stat(path)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("无新变更时不应重写文件")
	}
}
