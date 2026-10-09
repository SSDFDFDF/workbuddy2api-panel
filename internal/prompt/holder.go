// holder.go 「域 → 规则」快照的并发安全持有者（配置热改提示词用）。
//
// 背景：prompt.* 原先在装配期构建后按值透传给 handler，改配置必须重启。这里用
// 不可变快照 + atomic 指针：配置保存路径整体 Store 一份新规则，请求热路径 Load
// 拿到一致视图（不会读到半更新的 map），无锁无数据竞争。
//
// 与 scrub.Holder / media 策略快照同一模式；规则数只有个位数，克隆成本可忽略。
package prompt

import "sync/atomic"

// Holder 原子持有当前生效的分域规则快照。零值可用（Load 返回 nil = 全部透传）。
type Holder struct {
	p atomic.Pointer[map[string]Rule]
}

// NewHolder 以初始规则构建（nil 合法 = 全部透传）。
func NewHolder(rules map[string]Rule) *Holder {
	h := &Holder{}
	h.Store(rules)
	return h
}

// Store 整体替换规则快照（nil 与空 map 等价：无规则）。调用方为配置保存/装配路径。
func (h *Holder) Store(rules map[string]Rule) {
	if h == nil {
		return
	}
	var snap map[string]Rule
	if len(rules) > 0 {
		snap = make(map[string]Rule, len(rules))
		for k, v := range rules {
			snap[k] = v
		}
	}
	h.p.Store(&snap)
}

// Load 返回当前规则快照（nil = 无规则）。
func (h *Holder) Load() map[string]Rule {
	if h == nil {
		return nil
	}
	if p := h.p.Load(); p != nil {
		return *p
	}
	return nil
}
