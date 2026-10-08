package pool

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

// TestNicknameConcurrentReadWrite 守护昵称的跨锁访问回归：
// 面板「刷新」会逐号同步昵称（Pool.SetNickname 在 auth.mu 内改写字段），
// 同时 /status、/panel/api/overview、定时任务日志都在读同一对象。
// 读侧若直读 a.Nickname（不经 auth.NicknameValue），-race 下必报竞争。
//
// 断言两点：并发读写不触发竞争（-race 生效时）+ 读到的昵称能跟上最新写入。
func TestNicknameConcurrentReadWrite(t *testing.T) {
	p := New("")
	a := &auth.Auth{
		UID:         "nick-race",
		AccessToken: "local-test-token",
		Nickname:    "before",
		FilePath:    filepath.Join(t.TempDir(), "workbuddy-nick-race.json"),
	}
	p.Add(a)

	var wg sync.WaitGroup
	start := make(chan struct{})
	write := func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			p.SetNickname(a.UID, fmt.Sprintf("nick-%d", i))
		}
	}
	read := func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			for _, st := range p.List() {
				if st.Nickname == "" {
					t.Errorf("nickname must never be empty")
					return
				}
			}
		}
	}
	wg.Add(3)
	go write()
	go read()
	go read()
	close(start)
	wg.Wait()

	if got := p.List()[0].Nickname; got != "nick-49" {
		t.Fatalf("last write must be visible via Status: got %q", got)
	}
}
