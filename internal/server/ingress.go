// ingress.go 服务级入站准入：限制「同时处于读取 + 整包解析 + 图片校验」阶段的请求。
//
// 为什么需要这道闸门（这是它存在的唯一理由，理解了就不会想去掉）：
// 网关最贵的一段工作发生在**账号租约之前**——io.ReadAll 最多 32 MiB 请求体，
// 接着是整包 JSON 解析（实测 5 MiB 图片的请求体解析约 25 ms / 24 MB 分配），
// 以及图片 base64 的形状校验/转码。而 Pool.Acquire（单账号在途上限）在选号之后
// 才申请，也就是说账号级限流对「读取与解析」阶段的并发**毫无约束**：N 个并发大请求
// 可以同时把 32 MiB 读进内存、各自建一棵解析树——内存放大与并发数成正比，与账号数无关。
//
// 名额范围（刻意只覆盖解析阶段，不含上游流式转发）：
//   - 解析完成即释放。长 SSE 流**不占名额**——否则几十路正常并发流会把网关锁死。
//   - 解析完成后的文档虽然仍被 handler 持有，但其数量已被账号租约限制
//     （每账号 maxInFlight 个在途），因此保留期的内存本来就有界。
//     真正无界的就是这里覆盖的 pre-lease 阶段。
//
// 计数口径（保守但有界）：
//   - 计数上限：并发请求数；
//   - 字节预算：按 Content-Length 计入（已知时）；chunked/无长度请求按
//     unknownRequestBytes 计入——它可能实际更大，故这是**风险削减**而非硬保证，
//     单请求的硬上限始终由 MaxBytesReader（32 MiB）承担。
//
// 满载行为：等待至多 wait，仍无空间则立即 503（快速失败优于排队堆积）。
package server

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// 默认参数（config server.* 可覆盖）。
const (
	// defaultMaxInflightRequests 并发解析上限。取 64：远高于个人自用场景的正常并发，
	// 又能在异常突发（脚本打满/并发大请求）时把峰值内存钉住。
	defaultMaxInflightRequests = 64
	// defaultMaxInflightBytes 解析阶段并发字节预算（按 Content-Length 计）。
	defaultMaxInflightBytesMB = 256
	// defaultIngressWaitSeconds 满载时的等待上限（秒）：给瞬时抖动一点缓冲，又不至于
	// 把客户端拖到超时（等待期间不持有任何内存，只占连接）。
	// 由 config 层归一化（server.ingress_wait 缺省 "5s"，显式 "0" = 立即拒绝）。
	defaultIngressWaitSeconds = 5
	// unknownRequestBytes 无 Content-Length（chunked）时的字节计入量。
	unknownRequestBytes = 2 << 20
)

// ingressLimiter 入站并发/字节预算闸门（零值 = 不限制）。
type ingressLimiter struct {
	maxCount int
	maxBytes int64
	wait     time.Duration

	mu     sync.Mutex
	count  int
	bytes  int64
	notify chan struct{} // 释放时关闭并换新：广播唤醒全部等待者

	// 观测（/status 透出 + 日志）
	rejected  atomic.Uint64
	waited    atomic.Uint64
	peakCount atomic.Int64
	peakBytes atomic.Int64
}

func newIngressLimiter(maxCount int, maxBytesMB int, wait time.Duration) *ingressLimiter {
	l := &ingressLimiter{
		maxCount: maxCount,
		maxBytes: int64(maxBytesMB) << 20,
		wait:     wait,
	}
	if l.maxCount < 0 {
		l.maxCount = 0
	}
	if l.maxBytes < 0 {
		l.maxBytes = 0
	}
	if l.wait < 0 {
		l.wait = 0
	}
	return l
}

// enabled 报告是否启用限制（未配置或两项上限都为 0 = 不限）。
func (l *ingressLimiter) enabled() bool {
	return l != nil && (l.maxCount > 0 || l.maxBytes > 0)
}

// weightOf 计算一次请求的字节计入量。未知长度按 unknownRequestBytes。
func (l *ingressLimiter) weightOf(declared int64) int64 {
	if declared <= 0 {
		return unknownRequestBytes
	}
	if l.maxBytes > 0 && declared > l.maxBytes {
		// 单请求已超总预算：按总预算计入（否则它永远拿不到名额）。
		return l.maxBytes
	}
	return declared
}

// Acquire 申请一个入站名额。成功返回**幂等**的释放函数。
//
// ctx 取消或等待超时后仍无空间 → ok=false，调用方应立即回 503（快速失败优于排队堆积）。
// 未启用限制时返回空操作释放函数，调用方无需分支。
func (l *ingressLimiter) Acquire(ctx context.Context, declared int64) (func(), bool) {
	if !l.enabled() {
		return func() {}, true
	}
	weight := l.weightOf(declared)

	// wait == 0：满载立即拒绝（fail fast，不排队）。
	// 注意必须在这里显式返回：下面的 select 在 wait==0 时 timeout 为 nil
	// （nil channel 永不就绪），若继续走循环就会一直阻塞到有释放为止。
	if l.wait <= 0 {
		if l.tryAcquire(weight) {
			return l.releaseFunc(weight), true
		}
		l.rejected.Add(1)
		return nil, false
	}

	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	timeout := timer.C

	// 首次尝试与后续等待分开计数：只有"确实需要排队"才计入 waited。
	// 注意用 tryAcquireOrWait 而非 tryAcquire + 单独取通道：后者是两次加锁，
	// 释放若恰好落在两次加锁之间，等待者会拿到释放后新建的通道而在空间已空闲时
	// 干等到超时（实测 1/20000 轮可复现，默认等待 5s 时就是白等 5 秒再收 503）。
	rel, ch, ok := l.tryAcquireOrWait(weight)
	if ok {
		return rel, true
	}
	l.waited.Add(1)
	for {
		// 在锁外等待已取到的广播通道：释放会关闭它并换新，全部等待者被唤醒
		// 重新竞争（同步释放多个名额时不会漏唤醒）。
		select {
		case <-ch:
		case <-ctx.Done():
			l.rejected.Add(1)
			return nil, false
		case <-timeout:
			l.rejected.Add(1)
			return nil, false
		}
		if rel, ch, ok = l.tryAcquireOrWait(weight); ok {
			return rel, true
		}
	}
}

// tryAcquireOrWait 原子地「尝试占位，失败则取走当前广播通道」。
//
// 占位判定与取通道**必须同一次加锁**：拆成两次加锁会留下漏唤醒窗口——释放恰好
// 发生在「占位失败」之后、「取通道」之前时，等待者拿到的是释放后新建的通道，
// 空间已经空闲却要一直等到超时（表现为空闲期里无谓的 503；已用压力用例复现）。
func (l *ingressLimiter) tryAcquireOrWait(weight int64) (func(), <-chan struct{}, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.fitsLocked(weight) {
		if l.notify == nil {
			l.notify = make(chan struct{})
		}
		return nil, l.notify, false
	}
	l.acquireLocked(weight)
	return l.releaseFunc(weight), nil, true
}

// tryAcquire 在锁内判定并占位。
func (l *ingressLimiter) tryAcquire(weight int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.fitsLocked(weight) {
		return false
	}
	l.acquireLocked(weight)
	return true
}

// releaseFunc 返回幂等的释放函数（handler 既 defer 又在解析完成后显式调用）。
func (l *ingressLimiter) releaseFunc(weight int64) func() {
	var once sync.Once
	return func() { once.Do(func() { l.release(weight) }) }
}

func (l *ingressLimiter) fitsLocked(weight int64) bool {
	if l.maxCount > 0 && l.count >= l.maxCount {
		return false
	}
	if l.maxBytes > 0 && l.bytes+weight > l.maxBytes {
		// 注：weight 已被 weightOf 钳到 maxBytes，因此独占空闲预算的单个大请求
		// 仍能通过（否则超过总预算的请求会永远 503——它的硬上限由 MaxBytesReader 负责）。
		return false
	}
	return true
}

func (l *ingressLimiter) acquireLocked(weight int64) {
	l.count++
	l.bytes += weight
	if l.notify == nil {
		l.notify = make(chan struct{})
	}
	if n := int64(l.count); n > l.peakCount.Load() {
		l.peakCount.Store(n)
	}
	if b := l.bytes; b > l.peakBytes.Load() {
		l.peakBytes.Store(b)
	}
}

// release 归还名额并广播唤醒等待者。
func (l *ingressLimiter) release(weight int64) {
	l.mu.Lock()
	if l.count > 0 {
		l.count--
		l.bytes -= weight
		if l.bytes < 0 {
			l.bytes = 0
		}
	}
	// 广播：关闭当前通道并换新，所有等待者被唤醒后重新竞争
	//（单个缓冲 token 的写法会漏唤醒：多个名额同时释放时只唤醒一个等待者）。
	if l.notify == nil {
		l.notify = make(chan struct{})
	} else {
		close(l.notify)
		l.notify = make(chan struct{})
	}
	l.mu.Unlock()
}

// rejectLog 输出一次被拒观测（节流：每太多条就刷屏，故按数量级抽样打点）。
// 拒绝是客户端可见的 503，必须在日志里留痕，否则运维只看到客户端重试。
func (l *ingressLimiter) rejectLog() {
	n := l.rejected.Load()
	if n == 1 || n%100 == 0 {
		s := l.Stats()
		log.Printf("[ingress] 入站满载拒绝：累计 %d 次（当前 %d 请求 / %d 字节，上限 %d / %d）",
			n, s.InFlight, s.Bytes, s.MaxCount, s.MaxBytes)
	}
}

// ingressStats 一次读取的准入观测快照。
type ingressStats struct {
	MaxCount   int    `json:"max_requests"`
	MaxBytes   int64  `json:"max_bytes"`
	InFlight   int    `json:"in_flight"`
	Bytes      int64  `json:"bytes"`
	PeakCount  int64  `json:"peak_requests"`
	PeakBytes  int64  `json:"peak_bytes"`
	Waited     uint64 `json:"waited"`
	Rejected   uint64 `json:"rejected"`
	WaitMillis int64  `json:"wait_ms"`
}

func (l *ingressLimiter) Stats() ingressStats {
	if l == nil {
		return ingressStats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return ingressStats{
		MaxCount:   l.maxCount,
		MaxBytes:   l.maxBytes,
		InFlight:   l.count,
		Bytes:      l.bytes,
		PeakCount:  l.peakCount.Load(),
		PeakBytes:  l.peakBytes.Load(),
		Waited:     l.waited.Load(),
		Rejected:   l.rejected.Load(),
		WaitMillis: l.wait.Milliseconds(),
	}
}

// StartIngressLog 周期性输出满载观测（仅在确实发生过等待/拒绝时打印，避免常态刷屏）。
// 返回停止函数（main 退出时调用）。未启用准入时返回空操作。
func (h *Handler) StartIngressLog(interval time.Duration) func() {
	l := h.ingress
	return l.startIngressLog(interval)
}

// startIngressLog 是 StartIngressLog 的内部实现（见 ingressLimiter）。
func (l *ingressLimiter) startIngressLog(interval time.Duration) func() {
	if !l.enabled() {
		return func() {}
	}
	if interval <= 0 {
		interval = 30 * time.Second // 防御 time.NewTicker 对非正周期 panic
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		var lastWaited, lastRejected uint64
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s := l.Stats()
				if s.Waited == lastWaited && s.Rejected == lastRejected {
					continue
				}
				log.Printf("[ingress] 满载观测：正在解析 %d 请求 / %d 字节（峰值 %d / %d），累计等待 %d 次、拒绝 %d 次",
					s.InFlight, s.Bytes, s.PeakCount, s.PeakBytes, s.Waited, s.Rejected)
				lastWaited, lastRejected = s.Waited, s.Rejected
			}
		}
	}()
	return func() { close(stop) }
}
