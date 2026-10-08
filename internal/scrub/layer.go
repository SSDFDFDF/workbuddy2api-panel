// layer.go 出站指纹改写的**单一入口**：内置指纹表 + 自定义规则。
//
// 为什么要有这一层而不是直接暴露 scrub.Text：
//
//  1. 改写是**整包级**决策。请求体级预检（Sentinel）一旦判定无指纹，
//     整个 messages 遍历都可以跳过——如果把内置与自定义拆成两次遍历，
//     这个省法就没了。Layer 把两层预检合并成一次判断。
//  2. 自定义规则热改需要原子换代。Layer 用 atomic.Pointer 持有不可变的
//     规则集，读路径无锁；改规则 = 构一个新的 Layer 整体替换。
//  3. 调用方（upstream.prepareBody）只需问一句 "要改写吗"，不必知道
//     内部有几层规则、各自怎么匹配。
//
// 零值可用：Enabled 为 false 时所有方法都是空操作（默认严格逐字透传）。
package scrub

import (
	"sync/atomic"
)

// Layer 一次改写决策所需的全部规则（不可变；整体替换以实现热改）。
//
// 语义：**启用即含内置指纹表**，自定义规则在其上叠加（"加入"而非"替换"）。
// 内置表（7 类实测指纹）是这个功能存在的理由；把它做成可单独关闭的开关
// 只会造出一个静默丢掉 11128 防护的脚枪。
type Layer struct {
	// enabled 总开关。false → 所有方法零成本返回。
	enabled bool
	// custom 自定义规则（可为 nil = 只有内置规则）。
	custom *Rewriter
}

// NewLayer 构建一层改写器。custom 为 nil 表示只有内置指纹表。
func NewLayer(enabled bool, custom *Rewriter) *Layer {
	return &Layer{enabled: enabled, custom: custom}
}

// Enabled 报告该层是否启用。
func (l *Layer) Enabled() bool { return l != nil && l.enabled }

// CustomRules 返回自定义规则副本（面板展示用）。
func (l *Layer) CustomRules() []Rule {
	if l == nil {
		return nil
	}
	return l.custom.Rules()
}

// Dirty 报告请求体是否可能含指纹（内置或自定义）。false 等价于"确定干净"。
//
// 内置哨兵与自定义哨兵都扫原始字节（~13 GB/s），典型请求在这里就返回，
// 后续 JSON 遍历与逐字段改写全部省掉。
func (l *Layer) Dirty(body []byte) bool {
	if !l.Enabled() {
		return false
	}
	return Sentinel(body) || l.custom.Sentinel(body)
}

// Object 就地改写请求体文档（内置 + 自定义）。返回是否有改动。
// 调用前应先用 Dirty 预检；此处不重复预检（调用方已付过一次扫描成本）。
func (l *Layer) Object(obj map[string]any) bool {
	if !l.Enabled() {
		return false
	}
	changed := Object(obj)
	// 自定义层独立遍历：内置层已就地的字段不必再看一遍，
	// 但两条规则的匹配集通常不重叠，重复遍历的代价远小于维护"哪些字段
	// 已被内置层改过"的簿记；且这里只在 Dirty=true 时执行。
	if l.custom.Len() > 0 && objectWith(obj, l.custom.Apply) {
		changed = true
	}
	return changed
}

// objectWith 用给定的单段改写函数遍历全部指纹承载字段。
//
// 与 Object 结构相同、只换了改写函数——两处刻意保持同构，避免
// "内置层覆盖了某字段、自定义层漏了"这类漂移（内部编译器无法消除
// 这种重复，但测试可以：TestLayerCoversSameChannelsAsBuiltin）。
func objectWith(obj map[string]any, apply func(string) string) bool {
	msgs, _ := obj["messages"].([]any)
	if len(msgs) == 0 {
		return false
	}
	changed := false
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if c, ok := m["content"]; ok {
			switch v := c.(type) {
			case string:
				if n := apply(v); n != v {
					m["content"] = n
					changed = true
				}
			case []any:
				for _, p := range v {
					part, ok := p.(map[string]any)
					if !ok {
						continue
					}
					if t, ok := part["text"].(string); ok {
						if nt := apply(t); nt != t {
							part["text"] = nt
							changed = true
						}
					}
				}
			}
		}
		for _, k := range [2]string{"reasoning_content", "reasoning"} {
			if s, ok := m[k].(string); ok {
				if ns := apply(s); ns != s {
					m[k] = ns
					changed = true
				}
			}
		}
		if tc, ok := m["tool_calls"].([]any); ok {
			for _, raw := range tc {
				call, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				args, ok := fn["arguments"].(string)
				if !ok {
					continue
				}
				if na := apply(args); na != args {
					fn["arguments"] = na
					changed = true
				}
			}
		}
	}
	return changed
}

// Holder 原子持有当前 Layer（面板热改 + chat 热路径并发）。
type Holder struct {
	p atomic.Pointer[Layer]
}

// NewHolder 构建 holder；nil 初始层等价于"未启用"。
func NewHolder(l *Layer) *Holder {
	h := &Holder{}
	h.Store(l)
	return h
}

// Load 返回当前层（未 Store / nil holder 时返回 nil，调用方判空即可）。
func (h *Holder) Load() *Layer {
	if h == nil {
		return nil
	}
	return h.p.Load()
}

// Store 整体替换当前层。
func (h *Holder) Store(l *Layer) {
	if h == nil {
		return
	}
	h.p.Store(l)
}
