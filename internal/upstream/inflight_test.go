package upstream

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"workbuddy_manager/internal/auth"
)

// TestInflightCoalescesConcurrentCalls 单飞语义：并发同 key 只执行一次，结果共享。
func TestInflightCoalescesConcurrentCalls(t *testing.T) {
	var g inflight
	var calls atomic.Int64
	release := make(chan struct{})
	started := make(chan struct{})

	const n = 30
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := g.do("k", func() (any, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				return "shared", nil
			})
			if err != nil {
				errs[i] = err
				return
			}
			results[i], _ = v.(string)
		}(i)
	}
	<-started
	// 让跟随者先都挂到等待上，再放行 leader。
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn 执行 %d 次，want 1（并发同 key 必须合并）", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil || results[i] != "shared" {
			t.Fatalf("第 %d 个调用未共享结果: val=%q err=%v", i, results[i], errs[i])
		}
	}
}

// TestInflightSharesErrorAndReleasesKey 错误共享 + 执行完成后立刻释放 key
// （不做结果缓存：后续调用必须重新执行，缓存 TTL 由调用方负责）。
func TestInflightSharesErrorAndReleasesKey(t *testing.T) {
	var g inflight
	wantErr := errors.New("probe failed")
	var calls atomic.Int64
	fn := func() (any, error) { calls.Add(1); return nil, wantErr }

	release := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := g.do("k", func() (any, error) {
				if calls.Load() == 0 {
					close(started)
					<-release
				}
				return fn()
			})
			errs[i] = err
		}(i)
	}
	<-started
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, wantErr) {
			t.Fatalf("第 %d 个调用未共享错误: %v", i, err)
		}
	}

	// key 已释放：下一次调用必须是新的 leader（重新执行）。
	if _, err := g.do("k", fn); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls=%d want 2（首次合并 1 次 + 之后新的 1 次）", got)
	}
}

// TestFetchModelsCoalescesConcurrentProbes 守护真实影响面：
// 缓存过期瞬间 N 个并发 /v1/models 此前会各自发起完整 CN 探测（每路 2 个上游请求），
// 现在必须收敛为 1 路。
func TestFetchModelsCoalescesConcurrentProbes(t *testing.T) {
	var v3Calls, enterpriseCalls atomic.Int64
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v3/config"):
			v3Calls.Add(1)
			once.Do(func() { close(entered) })
			<-release // 阻塞探测，制造并发窗口
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"data":{"models":[{"id":"glm-5.2","name":"GLM-5.2","maxOutputTokens":8192}]}}`)
		case strings.Contains(r.URL.Path, "enterprises/personal/models"):
			enterpriseCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"data":{"models":[{"id":"glm-5.2"}],"agents":[{"name":"cli","models":["glm-5.2"]}]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New()
	c.ChatBaseCN = srv.URL
	c.HTTP = srv.Client()

	const n = 20
	var wg sync.WaitGroup
	got := make([][]ModelInfo, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = c.FetchModels(&auth.Auth{UID: "u1", AccessToken: "t"})
		}(i)
	}
	<-entered
	time.Sleep(80 * time.Millisecond) // 让跟随者挂到单飞等待上
	close(release)
	wg.Wait()

	if v3Calls.Load() != 1 {
		t.Fatalf("/v3/config 调用 %d 次，want 1（并发探测必须合并）", v3Calls.Load())
	}
	if enterpriseCalls.Load() != 1 {
		t.Fatalf("企业端点调用 %d 次，want 1", enterpriseCalls.Load())
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil || len(got[i]) == 0 || got[i][0].ID != "glm-5.2" {
			t.Fatalf("第 %d 个调用结果异常: err=%v infos=%v", i, errs[i], got[i])
		}
	}
}

// TestInflightPanicPropagatesAsError 守护：leader 的 fn panic 时，跟随者必须拿到
// 错误，而不是 (nil, nil) —— 后者与「成功但结果为空」无法区分（模型目录调用点会
// 把空结果当探测失败写负缓存，语义上仍是静默错误值）。
func TestInflightPanicPropagatesAsError(t *testing.T) {
	var g inflight
	entered := make(chan struct{})
	release := make(chan struct{})

	const n = 2
	var wg sync.WaitGroup
	errs := make([]error, n)
	vals := make([]any, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = g.do("boom", func() (any, error) {
				if i == 0 {
					close(entered)
					<-release
					panic("probe exploded")
				}
				return nil, nil
			})
		}(i)
		<-entered
	}
	time.Sleep(50 * time.Millisecond) // 让第 2 个调用挂到单飞等待上
	close(release)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] == nil {
			t.Fatalf("第 %d 个调用未收到错误（val=%v）：panic 被静默成空结果", i, vals[i])
		}
	}
	// 单飞执行完必须清键，后续同 key 调用能重新成为 leader。
	if _, err := g.do("boom", func() (any, error) { return "ok", nil }); err != nil {
		t.Fatalf("panic 后同 key 调用不应继承旧错误: %v", err)
	}
}
