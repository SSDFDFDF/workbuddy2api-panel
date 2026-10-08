// Package redisstore 封装 Upstash（Redis）持久化，并提供内存降级（Noop）。
//
// 设计约束：Upstash 走公网 TLS，单次 RTT 可能 50~300ms，因此所有写操作都是
// fire-and-forget（固定 worker 消费有界队列 + 失败仅 debug 日志），读操作只发生在启动时
// （加载粘性会话镜像、恢复冷却/熔断快照）。内存为主、Redis 为辅。
//
// 队列有界是硬约束：队满时丢弃并计数（绝不新建 goroutine、绝不阻塞聊天热路径）——
// 否则 Redis 变慢时等待写会把 goroutine/内存堆到失控（详见 Upstash 注释）。
//
// 未配置 url / 连接失败时降级为 Noop：一切功能照常工作（纯内存模式），
// 上层只打一条启动警告日志。
package redisstore

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyTTL 粘性会话镜像 + 状态快照的默认 TTL（redis 侧兜底，防脏数据长期滞留）。
const keyTTL = 7 * 24 * time.Hour

// writeConcurrencyLimit / writeQueuePerShard / writeOp 的完整设计说明见下方
// Upstash 类型区的常量与注释。

// Store 只放本期需要的方法。上下文由实现内部构造（读操作配短超时，写操作 fire-and-forget）。
type Store interface {
	// SetBind 异步镜像粘性会话绑定（key→uid），带 TTL。
	SetBind(key, uid string, ttl time.Duration)
	// DelBind 异步删除粘性会话绑定。
	DelBind(key string)
	// LoadBinds 全量读取粘性会话绑定（key→uid，key 已剥前缀）；仅在启动时调用（同步）。
	// 供冷启动恢复粘性映射（防重启丢粘性）。
	LoadBinds() map[string]string
	// SaveState 异步写池状态 JSON 快照（与本地 state.json 并存，仅作恢复备份）。
	SaveState(data []byte)
	// LoadState 读池状态快照；仅在启动时调用（同步）。
	LoadState() ([]byte, bool)
	// Close 关停 Store：Upstash 停止接收新写并排空**已入队**的写，再关底层连接
	// （停机语义：最后一笔 Redis 镜像必须写完），之后新提交的写直接丢弃；幂等。
	// Noop 为空操作。进程退出前在 pool.Close() 之后调用。
	// 注：队满时被丢弃的写不在此列（丢弃时即已计数并告警）。
	Close() error
}

const (
	bindPrefix  = "wb2api:bind:"
	stateKey    = "wb2api:state"
	readTimeout = 3 * time.Second
)

// loadBindsTimeout LoadBinds 的专用上限（仅启动时同步调用）。
//
// 比 readTimeout 宽松很多的原因：启动恢复要读完所有绑定，而 Upstash 公网单次 RTT
// 50~300ms。旧实现是「SCAN 后逐键 GET」的 N+1，在 3s 上限下绑定稍多就会半途超时、
// 静默丢弃剩余绑定（粘性重启丢失）。改为批量 MGET 后调用次数与绑定数不再是线性
// 关系，这里给的是「启动阶段允许慢」的配额，而不是每次 RPC 的上限。
const loadBindsTimeout = 30 * time.Second

// bindMGetBatch 单次 MGET 的键数量上限（与 SCAN count 同量级）。
const bindMGetBatch = 200

// New 根据 url+token 构建 Store。
//   - url 为空 → Noop（纯内存模式）
//   - url 已是完整 rediss:// URL 则直接 ParseURL；否则用 token 组装 rediss://default:token@host:6379
//   - Ping 失败 → Noop + 启动警告（硬性降级要求：不因 Redis 不可用而失败）
func New(url, token string) Store {
	if url == "" {
		log.Printf("[redisstore] upstash 未配置，进入纯内存模式（Noop 降级）")
		return Noop{}
	}

	full := normalizeURL(url, token)
	opt, err := redis.ParseURL(full)
	if err != nil {
		log.Printf("[redisstore] 警告: redis 连接串解析失败 (%v)，降级 Noop", err)
		return Noop{}
	}
	opt.ReadTimeout = readTimeout
	opt.WriteTimeout = readTimeout
	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("[redisstore] 警告: upstash 连接失败 (%v)，降级 Noop（纯内存模式）", err)
		_ = client.Close()
		return Noop{}
	}
	log.Printf("[redisstore] upstash 已连接 (addr=%s)", opt.Addr)
	u := &Upstash{client: client}
	u.ensure()
	return u
}

// normalizeURL 把 url+token 归一化为可直接 ParseURL 的完整 rediss:// URL。
// 若 url 本身已含 scheme（rediss://、redis://、https://...upstash.io 等）：
//   - rediss:// 或 redis:// 原样返回（已是完整连接串）
//   - 其余（如 https://xxx.upstash.io）剥掉 "://" 前缀只取 host，再按
//     "rediss://default:<token>@<host>:6379" 组装
func normalizeURL(url, token string) string {
	if len(url) >= 8 && (url[:8] == "rediss:/" || url[:7] == "redis:/") {
		return url
	}
	host := url
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	return "rediss://default:" + token + "@" + host + ":6379"
}

// writeConcurrencyLimit 写分片数（固定 worker 数）。
//
// 为什么不是「信号量 + 每写一个 goroutine」：那种写法只限制了**同时执行**的写，
// 等待中的 goroutine 数量仍然随写入速率无限增长（实测：槽位 1、阻塞首写后提交
// 2000 次写 → 2000 个等待 goroutine + 各自闭包），QPS 高或 Redis 变慢时内存
// 与调度开销都会失控。改为固定 worker + 有界队列。
const writeConcurrencyLimit = 8

// writeQueuePerShard 每个分片的排队上限。满载时丢弃并计数，绝不阻塞调用方
// （聊天热路径），也不新建 goroutine。8×64=512 笔待写已远超单次 flush 周期
// 的写量，触顶只可能是 Redis 长时间不可用。
const writeQueuePerShard = 64

// writeOp 一笔异步写。key 决定分片：同一 key 的写落在同一分片，由单消费者
// 串行执行，避免「SetBind 与 DelBind 乱序导致已删绑定被复活」。
type writeOp struct {
	key string
	fn  func()
}

// Upstash 真实现：redis.Client 封装。
//
// 写语义（fire-and-forget，永不阻塞调用方）：
//   - 固定 worker 消费有界队列，队列满则丢弃并计数（log 节流输出）；
//   - 同一 key 保序（分片单消费者）；
//   - Close 前**已入队**的写保证执行完，Close 后新提交的写直接丢弃。
type Upstash struct {
	client *redis.Client
	// shards 写分片：每片一个队列 + 一个消费者。
	shards []chan writeOp
	// done 关停标志（Close 关闭）。
	done chan struct{}
	// shardCount 仅供测试覆写（<=0 用 writeConcurrencyLimit；=1 便于断言同 key 串行）。
	shardCount int
	// ensureOnce 保证零值 Upstash（测试直接构造）也能初始化分片与 worker。
	ensureOnce sync.Once
	// closeOnce 保证 Close 幂等（多次调用只关一次队列）。
	closeOnce sync.Once
	// submitMu 串行化「提交 / 关停」：提交在锁内检查 closed 并入队，
	// Close 在锁内置 closed 并关队列——两侧互斥后，不会出现「关闭后仍入队」
	// 或「入队后队列已关」的 panic。
	submitMu sync.Mutex
	closed   bool
	// wg 已入队未完成的写（Close 排空用）。
	wg sync.WaitGroup
	// workers 消费者 goroutine（Close 等待其退出）。
	workers sync.WaitGroup
	// dropped 队满丢弃计数（可观测）。
	dropped atomic.Uint64
}

// ensure 初始化分片与消费者（幂等）。零值 Upstash（测试构造）也适用。
func (u *Upstash) ensure() {
	u.ensureOnce.Do(func() {
		n := u.shardCount
		if n <= 0 {
			n = writeConcurrencyLimit
		}
		u.done = make(chan struct{})
		u.shards = make([]chan writeOp, n)
		for i := range u.shards {
			u.shards[i] = make(chan writeOp, writeQueuePerShard)
		}
		for i := range u.shards {
			u.workers.Add(1)
			go u.worker(u.shards[i])
		}
	})
}

// worker 消费一个分片：串行执行，关闭后退出（已入队的写先执行完）。
func (u *Upstash) worker(ch chan writeOp) {
	defer u.workers.Done()
	for op := range ch {
		op.fn()
		u.wg.Done()
	}
}

// shardFor 按 key 选分片（FNV-1a，同 key 恒同片）。
func (u *Upstash) shardFor(key string) chan writeOp {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return u.shards[int(h%uint32(len(u.shards)))]
}

// goWrite 以 fire-and-forget 方式提交一笔写：入队即返回（不阻塞调用方），
// 队满丢弃并计数，Close 前入队的写保证执行完成。
func (u *Upstash) goWrite(key string, fn func()) {
	u.ensure()
	u.submitMu.Lock()
	if u.closed {
		u.submitMu.Unlock()
		return // Close 后提交：进程已在退出，直接丢弃
	}
	// 先登记再入队：消费者可能在入队瞬间就执行完并调 Done，
	// 后 Add 会导致 WaitGroup 计数为负 panic。
	u.wg.Add(1)
	select {
	case u.shardFor(key) <- writeOp{key: key, fn: fn}:
		u.submitMu.Unlock()
	default:
		u.wg.Done()
		u.submitMu.Unlock()
		u.noteDrop(key)
	}
}

// dropLogEvery 丢弃日志节流：每 N 次丢一条，避免 Redis 长时间不可用时刷屏。
const dropLogEvery = 100

func (u *Upstash) noteDrop(key string) {
	n := u.dropped.Add(1)
	if n == 1 || n%dropLogEvery == 0 {
		log.Printf("[redisstore] WARN: 写队列已满，累计丢弃 %d 笔异步写（key=%s）；"+
			"本地状态仍为权威来源，Redis 仅作恢复镜像", n, key)
	}
}

// DroppedWrites 报告因队满丢弃的异步写笔数（运维观测；Noop 恒 0）。
func (u *Upstash) DroppedWrites() uint64 {
	if u == nil {
		return 0
	}
	return u.dropped.Load()
}

// Close 停止接收新写、排空已入队写，再关底层 redis 连接；幂等。
// 停机路径在 pool.Close() 之后调用：pool 的最后一次 Flush→SaveState 已入队，
// 本方法保证它写完才返回。
func (u *Upstash) Close() error {
	u.ensure()
	u.submitMu.Lock()
	u.closeOnce.Do(func() {
		u.closed = true
		close(u.done)
		for _, sh := range u.shards {
			close(sh) // worker 会把已入队的写执行完再退出
		}
	})
	u.submitMu.Unlock()

	done := make(chan struct{})
	go func() {
		u.wg.Wait()
		u.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// 兜底超时（单写上限 5s，10s 富余）：卡死的写不应阻塞进程退出。
		log.Printf("[redisstore] WARN: Close 等待在途写超时，放弃（镜像可能未写完）")
	}
	return u.closeClient()
}

// closeClient 关底层 redis 连接（client 为 nil——测试构造——时跳过）。
func (u *Upstash) closeClient() error {
	if u.client == nil {
		return nil
	}
	return u.client.Close()
}

func bindKey(key string) string { return bindPrefix + key }

// SetBind 异步镜像粘性会话绑定。
func (u *Upstash) SetBind(key, uid string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = keyTTL
	}
	u.goWrite(bindKey(key), func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := u.client.Set(ctx, bindKey(key), uid, ttl).Err(); err != nil {
			log.Printf("[redisstore] debug: SetBind %s: %v", key, err)
		}
	})
}

// DelBind 异步删除粘性会话绑定。
func (u *Upstash) DelBind(key string) {
	u.goWrite(bindKey(key), func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := u.client.Del(ctx, bindKey(key)).Err(); err != nil {
			log.Printf("[redisstore] debug: DelBind %s: %v", key, err)
		}
	})
}

// SaveState 异步写池状态 JSON 快照。
func (u *Upstash) SaveState(data []byte) {
	u.goWrite(stateKey, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := u.client.Set(ctx, stateKey, data, keyTTL).Err(); err != nil {
			log.Printf("[redisstore] debug: SaveState: %v", err)
		}
	})
}

// LoadState 同步读池状态快照。
func (u *Upstash) LoadState() ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	v, err := u.client.Get(ctx, stateKey).Bytes()
	if err != nil {
		return nil, false
	}
	return v, true
}

// LoadBinds 全量读取粘性会话绑定（SCAN 列举 + 分批 MGET 取值）。
//
// 为什么不用逐键 GET：旧实现 SCAN 到的每个 key 都单独 GET 一次，是典型 N+1。
// Upstash 公网单次 RTT 50~300ms，500 条绑定就是 25~150s——远超 readTimeout(3s)，
// 结果是启动恢复半途超时、**静默**丢掉剩余绑定（这就是「粘性重启丢失」的成因之一）。
// MGET 把 N 次 RTT 收敛为 N/200 次。
func (u *Upstash) LoadBinds() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), loadBindsTimeout)
	defer cancel()

	var keys []string
	iter := u.client.Scan(ctx, 0, bindPrefix+"*", bindMGetBatch).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("[redisstore] WARN: LoadBinds 列举失败（已取到 %d 个键）: %v", len(keys), err)
	}
	if len(keys) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(keys))
	for i := 0; i < len(keys); i += bindMGetBatch {
		end := i + bindMGetBatch
		if end > len(keys) {
			end = len(keys)
		}
		vals, err := u.client.MGet(ctx, keys[i:end]...).Result()
		if err != nil {
			log.Printf("[redisstore] WARN: LoadBinds 批量取值失败（键 %d..%d）: %v", i, end-1, err)
			continue
		}
		for k, v := range decodeBindValues(keys[i:end], vals) {
			out[k] = v
		}
	}
	if len(out) < len(keys) {
		// 缺失不一定是错误（键可能在列举与取值之间过期），但差异较大时值得看见。
		log.Printf("[redisstore] LoadBinds: 列举 %d 个键，取到 %d 个绑定", len(keys), len(out))
	}
	return out
}

// decodeBindValues 将 MGET 的返回值解为 key→uid。
// 值为 nil（键已过期/不存在）或非 string 的条目跳过；键前缀已剥。
// 抽成纯函数以便直接测试 nil/异常值的处理（真实 Redis 才能造出 nil）。
func decodeBindValues(keys []string, vals []any) map[string]string {
	out := make(map[string]string, len(keys))
	for i, v := range vals {
		if i >= len(keys) {
			break
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		out[strings.TrimPrefix(keys[i], bindPrefix)] = s
	}
	return out
}

// Noop 纯内存降级：所有方法空实现。
type Noop struct{}

func (Noop) SetBind(string, string, time.Duration) {}
func (Noop) DelBind(string)                        {}
func (Noop) LoadBinds() map[string]string          { return nil }
func (Noop) SaveState([]byte)                      {}
func (Noop) LoadState() ([]byte, bool)             { return nil, false }
func (Noop) Close() error                          { return nil }
