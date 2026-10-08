package upstream

import "sync"

// inflight 极简单飞（singleflight）：把同一 key 的并发调用合并为一次执行，
// 其余调用等待并共享同一结果。
//
// 为什么需要：模型目录探测（CN 的 /v3/config + 企业端点两路并发、global 的
// /v3/config 多 UA + 企业端点家族）都是「缓存过期后第一个请求触发」的懒加载。
// 缓存过期的同一瞬间若有 N 个客户端同时拉 /v1/models（或面板与客户端同时拉），
// 此前会各自发起一次完整探测，把一次上游调用放大成 N 次——既浪费上游配额，
// 也是 WAF 风控里最不该出现的突发流量。
//
// 语义：leader 执行 fn，跟随者共享 (值, 错误)；fn panic 时跟随者拿到零值，
// 但不会永久挂住（defer 保证唤醒与键清理）。执行完成后立即释放 key，不做结果
// 缓存——缓存由调用方各自的 TTL 逻辑负责（探测结果与负缓存口径不同，见
// handler.dynamicModelsCache 与 fetchGlobalModelsCache）。
type inflight struct {
	mu   sync.Mutex
	call map[string]*inflightCall
}

type inflightCall struct {
	done chan struct{}
	val  any
	err  error
}

func (g *inflight) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.call == nil {
		g.call = map[string]*inflightCall{}
	}
	if c, ok := g.call[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.val, c.err
	}
	c := &inflightCall{done: make(chan struct{})}
	g.call[key] = c
	g.mu.Unlock()

	defer func() {
		// 先清键再唤醒：跟随者被唤醒后若立刻发起新的同类调用，能成为新 leader，
		// 而不是读到已完成的旧调用。
		g.mu.Lock()
		delete(g.call, key)
		g.mu.Unlock()
		close(c.done)
	}()
	c.val, c.err = fn()
	return c.val, c.err
}
