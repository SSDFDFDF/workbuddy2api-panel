package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/upstream"
)

// TestIngressCountLimitAndRelease 计数上限：满额即拒，释放后恢复。
func TestIngressCountLimitAndRelease(t *testing.T) {
	l := newIngressLimiter(2, 0, time.Second)
	r1, ok := l.Acquire(context.Background(), 100)
	if !ok {
		t.Fatal("第一个名额应成功")
	}
	r2, ok := l.Acquire(context.Background(), 100)
	if !ok {
		t.Fatal("第二个名额应成功")
	}
	if _, ok := l.Acquire(context.Background(), 100); ok {
		t.Fatal("第三个名额应被拒（上限 2）")
	}
	if s := l.Stats(); s.Rejected != 1 || s.InFlight != 2 || s.PeakCount != 2 {
		t.Fatalf("stats=%+v", s)
	}
	r1()
	if _, ok := l.Acquire(context.Background(), 100); !ok {
		t.Fatal("释放后应能再次申请")
	}
	r2()
}

// TestIngressReleaseIdempotent release 必须幂等：handler 既 defer 又显式调用。
func TestIngressReleaseIdempotent(t *testing.T) {
	l := newIngressLimiter(1, 0, time.Second)
	r, ok := l.Acquire(context.Background(), 0)
	if !ok {
		t.Fatal("应成功")
	}
	r()
	r()
	r()
	if s := l.Stats(); s.InFlight != 0 || s.Bytes != 0 {
		t.Fatalf("重复 release 导致计数错乱: %+v", s)
	}
	if _, ok := l.Acquire(context.Background(), 0); !ok {
		t.Fatal("重复 release 后仍应可申请（不能被扣成负数）")
	}
}

// TestIngressByteBudget 字节预算：按 Content-Length 计入，超预算即拒。
func TestIngressByteBudget(t *testing.T) {
	// 4 MiB 预算
	l := newIngressLimiter(0, 4, time.Second)
	r1, ok := l.Acquire(context.Background(), 3<<20)
	if !ok {
		t.Fatal("3 MiB 应成功")
	}
	if _, ok := l.Acquire(context.Background(), 3<<20); ok {
		t.Fatal("再要 3 MiB 应被拒（预算 4 MiB）")
	}
	r1()
	if _, ok := l.Acquire(context.Background(), 4<<20); !ok {
		t.Fatal("释放后 4 MiB 单请求应成功（独占空闲预算）")
	}
}

// TestIngressUnknownLengthUsesDefaultWeight 无 Content-Length（chunked）按固定权重计入：
// 否则大量 chunked 请求可以零成本绕过字节预算。
func TestIngressUnknownLengthUsesDefaultWeight(t *testing.T) {
	l := newIngressLimiter(0, 4, time.Second) // 4 MiB
	if got := l.weightOf(-1); got != unknownRequestBytes {
		t.Fatalf("未知长度权重=%d want %d", got, unknownRequestBytes)
	}
	if got := l.weightOf(0); got != unknownRequestBytes {
		t.Fatalf("零长度权重=%d want %d（0 视同未知，避免免费占用）", got, unknownRequestBytes)
	}
	// 2 MiB/个 → 预算 4 MiB 只容得下 2 个。
	r1, ok1 := l.Acquire(context.Background(), -1)
	r2, ok2 := l.Acquire(context.Background(), -1)
	if !ok1 || !ok2 {
		t.Fatal("前两个 chunked 请求应成功")
	}
	if _, ok := l.Acquire(context.Background(), -1); ok {
		t.Fatal("第三个 chunked 请求应被拒")
	}
	r1()
	r2()
}

// TestIngressWaitThenTimeout 满载时等待；超时后拒绝（不永久阻塞）。
func TestIngressWaitThenTimeout(t *testing.T) {
	l := newIngressLimiter(1, 0, 150*time.Millisecond)
	held, _ := l.Acquire(context.Background(), 0)

	start := time.Now()
	if _, ok := l.Acquire(context.Background(), 0); ok {
		t.Fatal("满载时不应立刻成功")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("应在等待后超时，实际仅 %v", elapsed)
	}
	if s := l.Stats(); s.Waited != 1 || s.Rejected != 1 {
		t.Fatalf("应记录一次等待与一次拒绝: %+v", s)
	}
	held()
}

// TestIngressZeroWaitRejectsImmediately wait == 0（config 显式 "0" = 满载立即拒绝）时
// 必须立即返回 false，而不是阻塞等待。
//
// 这条守护一个真实缺陷：等待分支里 timeout 取自 time.Timer，wait==0 时该 channel 为
// nil（永不就绪），若实现走到 select 就会一直阻塞到有释放为止——表现为客户端请求
// 永久挂住（比 503 更糟：连接与 goroutine 都被占死）。
func TestIngressZeroWaitRejectsImmediately(t *testing.T) {
	l := newIngressLimiter(1, 0, 0)
	held, ok := l.Acquire(context.Background(), 0)
	if !ok {
		t.Fatal("首个名额应成功")
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := l.Acquire(context.Background(), 0)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("满载且 wait=0 时必须拒绝")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait=0 时不得阻塞等待（应立即拒绝）")
	}
	if s := l.Stats(); s.Rejected != 1 || s.Waited != 0 {
		t.Fatalf("wait=0 不应计入等待: %+v", s)
	}
	held()
}

// TestIngressWaitersWokenByRelease 等待中的请求应被释放唤醒并成功（这是「等待」而非
// 「直接拒绝」的意义所在）。多个等待者同时释放时不得漏唤醒（广播语义）。
func TestIngressWaitersWokenByRelease(t *testing.T) {
	l := newIngressLimiter(1, 0, 5*time.Second)
	held, _ := l.Acquire(context.Background(), 0)

	const waiters = 3
	var wg sync.WaitGroup
	okCh := make(chan bool, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, ok := l.Acquire(context.Background(), 0)
			if ok {
				okCh <- true
				// 立即释放，让下一个等待者也能拿到
				r()
				return
			}
			okCh <- false
		}()
	}
	time.Sleep(80 * time.Millisecond) // 让等待者都睡着
	held()
	wg.Wait()
	close(okCh)
	for ok := range okCh {
		if !ok {
			t.Fatal("持有者释放后等待者都应被唤醒（广播唤醒，不能漏）")
		}
	}
}

// TestIngressContextCancel 客户端断开时等待应立刻返回（不占用名额到超时）。
func TestIngressContextCancel(t *testing.T) {
	l := newIngressLimiter(1, 0, 5*time.Second)
	held, _ := l.Acquire(context.Background(), 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, ok := l.Acquire(ctx, 0); ok {
		t.Fatal("ctx 取消时不应成功")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ctx 取消应立即返回，实际 %v", elapsed)
	}
	held()
}

// TestIngressDisabled 未配置限制时不介入（保持旧行为）。
func TestIngressDisabled(t *testing.T) {
	for _, l := range []*ingressLimiter{nil, newIngressLimiter(0, 0, 0)} {
		for i := 0; i < 100; i++ {
			r, ok := l.Acquire(context.Background(), 32<<20)
			if !ok {
				t.Fatalf("未启用限制时应全部放行")
			}
			r()
		}
	}
}

// --- handler 集成 ---

// blockingBody 在读第一块时阻塞，用于把请求钉在「读取中」状态，从而稳定复现满额。
type blockingBody struct {
	release chan struct{}
	once    sync.Once
	done    chan struct{}
}

func newBlockingBody() *blockingBody {
	return &blockingBody{release: make(chan struct{}), done: make(chan struct{})}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.done) })
	<-b.release
	return 0, errBlockingBodyDone
}

func (b *blockingBody) Close() error { return nil }

var errBlockingBodyDone = &blockingReadError{}

type blockingReadError struct{}

func (*blockingReadError) Error() string { return "blocked body released" }

// TestIngressHandlerRejectsWhileIngestSaturated 集成：名额被「读取中」的请求占满时，
// 新请求立即得到 503 server_busy（而不是也去分配 32 MiB 缓冲）。
func TestIngressHandlerRejectsWhileIngestSaturated(t *testing.T) {
	up := &upstream.Client{}
	h := NewHandler(Config{
		Pool:                testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:            up,
		MaxInflightRequests: 1,
		IngressWait:         50 * time.Millisecond, // 短暂等待后拒绝
	})
	blocked := newBlockingBody()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Body = blocked
	req.ContentLength = 1 << 20

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}()
	<-blocked.done // 第一个请求已进入读取阶段并持有名额

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("满额时应回 503，得到 %d body=%s", rec2.Code, rec2.Body)
	}
	if !strings.Contains(rec2.Body.String(), "server_busy") {
		t.Fatalf("错误码应为 server_busy: %s", rec2.Body)
	}

	close(blocked.release) // 放行第一个请求
	// 名额释放后新请求必须恢复可用
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := h.ingress.Stats(); s.InFlight == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest("GET", "/status", nil))
	if rec3.Code == http.StatusServiceUnavailable {
		t.Fatalf("释放后 /status 不应被拒: code=%d", rec3.Code)
	}
	if s := h.ingress.Stats(); s.InFlight != 0 {
		t.Fatalf("请求结束后名额必须归还: %+v", s)
	}
}

// TestIngressReleasedBeforeUpstreamStream 关键语义：名额只覆盖「读取 + 解析」，
// 上游流式转发期间必须已释放——否则几十路正常长连接会把网关锁死。
// 用 maxCount=1 验证：第一个请求进入上游（流被阻塞）时，第二个请求必须仍能完成解析。
func TestIngressReleasedBeforeUpstreamStream(t *testing.T) {
	streamRelease := make(chan struct{})
	upstreamEntered := make(chan struct{})
	var once sync.Once
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		once.Do(func() { close(upstreamEntered) })
		<-streamRelease // 钉住上游"流"
		return 200, gatewayBenchSSE, true
	})
	h := NewHandler(Config{
		Pool:                testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:            up,
		MaxInflightRequests: 1,
		IngressWait:         0, // 立即拒绝：若名额没释放，第二个请求必然被拒
	})

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	}()
	<-upstreamEntered

	if s := h.ingress.Stats(); s.InFlight != 0 {
		t.Fatalf("进入上游转发后准入名额必须已释放，实际 in_flight=%d", s.InFlight)
	}
	// maxCount=1 且 wait=0：若名额被流占用，这里必然 503。
	secondDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
		secondDone <- rec.Code
	}()
	close(streamRelease)
	code := <-secondDone
	if code == http.StatusServiceUnavailable {
		t.Fatalf("长流不应占用入站名额（否则并发流会被自己的限流锁死）")
	}
}

// TestIngressReleasedOnDecodeError 解析失败（400）也必须归还名额，不能泄漏。
func TestIngressReleasedOnDecodeError(t *testing.T) {
	h := NewHandler(Config{
		Pool:                testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:            &upstream.Client{},
		MaxInflightRequests: 1,
		IngressWait:         0,
	})
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":`))) // 坏 JSON
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("坏 JSON 应 400，得到 %d", rec.Code)
		}
	}
	if s := h.ingress.Stats(); s.InFlight != 0 {
		t.Fatalf("解析失败路径泄漏了名额: %+v", s)
	}
	// 超大 body（413 路径）同样必须归还
	h2 := NewHandler(Config{
		Pool:                testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:            &upstream.Client{},
		MaxInflightRequests: 1,
		IngressWait:         0,
	})
	huge := strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"` +
		strings.Repeat("x", 33<<20) + `"}]}`)
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", huge))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限 body 应 413，得到 %d", rec.Code)
	}
	if s := h2.ingress.Stats(); s.InFlight != 0 {
		t.Fatalf("413 路径泄漏了名额: %+v", s)
	}
}
