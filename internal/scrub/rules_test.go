package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustBuild(t *testing.T, rules ...Rule) *Rewriter {
	t.Helper()
	w, err := Build(rules)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return w
}

// TestCustomLiteralReplace 自定义字面词/句：逐字替换，大小写敏感。
func TestCustomLiteralReplace(t *testing.T) {
	w := mustBuild(t, Rule{Match: "内部代号X", Replace: "项目A"})
	if got := w.Apply("请把内部代号X 的文档整理一下"); got != "请把项目A 的文档整理一下" {
		t.Errorf("literal replace = %q", got)
	}
	// 大小写敏感：变体不动。
	if got := w.Apply("internal CodeName"); got != "internal CodeName" {
		t.Errorf("literal 应大小写敏感: %q", got)
	}
	// 多处全部替换。
	if got := mustBuild(t, Rule{Match: "ab", Replace: "-"}).Apply("ab_ab_ab"); got != "-_-_-" {
		t.Errorf("多处替换 = %q", got)
	}
	// 单条规则内的多处命中也要全部替换。
	if got := mustBuild(t, Rule{Match: "x", Replace: "Y"}).Apply("x1x2x"); got != "Y1Y2Y" {
		t.Errorf("单规则多处替换 = %q", got)
	}
}

// TestCustomFoldReplace fold 模式忽略 ASCII 大小写。
func TestCustomFoldReplace(t *testing.T) {
	w := mustBuild(t, Rule{Match: "SecretSauce", Replace: "sauce", Mode: ModeFold})
	for _, in := range []string{"secretsauce", "SECRETSAUCE", "SecretSauce", "SeCrEtSaUcE"} {
		if got := w.Apply("前缀" + in + "后缀"); got != "前缀sauce后缀" {
			t.Errorf("fold(%q) = %q", in, got)
		}
	}
	// 替换文本原样插入（不做大小写模仿）——语义可预测。
	if got := mustBuild(t, Rule{Match: "abc", Replace: "XY", Mode: ModeFold}).Apply("ABC"); got != "XY" {
		t.Errorf("替换文本应原样插入: %q", got)
	}
}

// TestCustomRemove 删除动作。
func TestCustomRemove(t *testing.T) {
	w := mustBuild(t, Rule{Match: "x-anthropic-billing-header", Action: ActionRemove})
	if got := w.Apply("head x-anthropic-billing-header tail"); got != "head  tail" {
		t.Errorf("remove = %q", got)
	}
	// remove 时 Replace 被忽略（即使配了也不生效，避免"看起来配了却不删"）。
	w2 := mustBuild(t, Rule{Match: "gone", Replace: "kept", Action: ActionRemove})
	if got := w2.Apply("a gone b"); got != "a  b" {
		t.Errorf("remove 应忽略 replace: %q", got)
	}
}

// TestCustomRulesValidation 非法规则必须报错（配置错误要在保存时暴露）。
func TestCustomRulesValidation(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
	}{
		{"empty match", Rule{Match: ""}},
		{"blank match", Rule{Match: "   "}},
		{"bad mode", Rule{Match: "a", Mode: "regex"}},
		{"bad action", Rule{Match: "a", Action: "hash"}},
		{"replace contains match", Rule{Match: "abc", Replace: "xabcy"}},
		{"fold replace contains match", Rule{Match: "abc", Replace: "xABCy", Mode: ModeFold}},
		{"match too long", Rule{Match: strings.Repeat("a", maxRuleLen+1)}},
		{"replace too long", Rule{Match: "a", Replace: strings.Repeat("b", maxRuleLen+1)}},
	}
	for _, tc := range cases {
		if _, err := Build([]Rule{tc.rule}); err == nil {
			t.Errorf("%s: 应报错但通过了", tc.name)
		}
	}
	// 合法边界：等长上限、纯删除、fold 且 replace 不含 match。
	if _, err := Build([]Rule{{Match: strings.Repeat("a", maxRuleLen), Replace: "b"}}); err != nil {
		t.Errorf("边界长度应通过: %v", err)
	}
	if _, err := Build([]Rule{{Match: "a", Action: ActionRemove}}); err != nil {
		t.Errorf("纯删除应通过: %v", err)
	}
	if _, err := Build([]Rule{{Match: "abc", Replace: "xyz", Mode: ModeFold}}); err != nil {
		t.Errorf("fold 合法替换应通过: %v", err)
	}
}

// TestCustomRuleCountLimit 规则数超上限报错。
func TestCustomRuleCountLimit(t *testing.T) {
	rules := make([]Rule, maxRuleCount+1)
	for i := range rules {
		rules[i] = Rule{Match: "m" + string(rune('a'+i%26)) + strconvItoa(i), Replace: "x"}
	}
	if _, err := Build(rules); err == nil {
		t.Error("超量规则应报错")
	}
}

// TestCustomCleanPathNoAlloc 干净路径零分配（自定义层同样要满足）。
func TestCustomCleanPathNoAlloc(t *testing.T) {
	w := mustBuild(t,
		Rule{Match: "内部代号X", Replace: "项目A"},
		Rule{Match: "secret", Replace: "s", Mode: ModeFold},
		Rule{Match: "dropme", Action: ActionRemove},
	)
	clean := strings.Repeat("ordinary text with nothing to rewrite\n", 40)
	if n := testing.AllocsPerRun(200, func() { _ = w.Apply(clean) }); n != 0 {
		t.Errorf("干净文本 Apply 分配 %v 次，期望 0", n)
	}
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"content": clean}}})
	if n := testing.AllocsPerRun(200, func() { _ = w.Sentinel(body) }); n != 0 {
		t.Errorf("Sentinel 分配 %v 次，期望 0", n)
	}
}

// TestCustomSentinelIsSuperset 自定义哨兵必须是超集（含 fold 全大小写变体）。
func TestCustomSentinelIsSuperset(t *testing.T) {
	w := mustBuild(t,
		Rule{Match: "内部代号X", Replace: "A"},
		Rule{Match: "secret", Replace: "s", Mode: ModeFold},
	)
	for _, in := range []string{"内部代号X", "SECRET", "secret", "SeCrEt", "a secret here"} {
		if w.Apply(in) == in {
			t.Fatalf("夹具无效，规则未命中: %q", in)
		}
		body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"content": in}}})
		if !w.Sentinel(body) {
			t.Errorf("Sentinel 漏判: %q", in)
		}
	}
	clean, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"content": "毫无关联的普通文本"}}})
	if w.Sentinel(clean) {
		t.Error("干净请求体被自定义哨兵误报")
	}
}

// TestNilRewriterSafe nil 规则集的所有方法必须安全（热路径判空省成本）。
func TestNilRewriterSafe(t *testing.T) {
	var w *Rewriter
	if w.Len() != 0 || w.Sentinel([]byte("x")) || w.Apply("x") != "x" || w.Rules() != nil {
		t.Error("nil Rewriter 行为不安全")
	}
	built, err := Build(nil)
	if err != nil || built != nil {
		t.Errorf("Build(nil) = %v, %v；期望 nil, nil", built, err)
	}
}

// TestLayerCombinesBuiltinAndCustom 层语义：内置与自定义都生效，可分别开关。
func TestLayerCombinesBuiltinAndCustom(t *testing.T) {
	custom := mustBuild(t, Rule{Match: "内部代号X", Replace: "项目A"})
	both := NewLayer(true, custom)
	if !both.Enabled() || both.CustomRules() == nil || len(both.CustomRules()) != 1 {
		t.Fatal("层未正确持有自定义规则")
	}
	obj := func() map[string]any {
		return map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "内部代号X 与 code=11128"},
		}}
	}
	// 两个都开：内置与自定义同时生效。
	o := obj()
	if !both.Dirty([]byte(mustJSON(t, o))) || !both.Object(o) {
		t.Fatal("builtin+custom 未生效")
	}
	got, _ := json.Marshal(o)
	if strings.Contains(string(got), "11128") || strings.Contains(string(got), "内部代号X") {
		t.Errorf("改写不完整: %s", got)
	}
	if !strings.Contains(string(got), "11-128") || !strings.Contains(string(got), "项目A") {
		t.Errorf("改写结果不对: %s", got)
	}

	// 关掉总开关：Dirty=false / Object=false，逐字透传。
	off := NewLayer(false, custom)
	o2 := obj()
	if off.Dirty([]byte(mustJSON(t, o2))) || off.Object(o2) {
		t.Error("关闭的层不应有任何动作")
	}
	if !strings.Contains(mustJSON(t, o2), "11128") {
		t.Errorf("关闭时内容被改了: %s", mustJSON(t, o2))
	}

	// 无自定义规则（只有内置）：自定义哨兵不命中，内置照常生效。
	builtinOnly := NewLayer(true, nil)
	if builtinOnly.CustomRules() != nil {
		t.Error("无自定义规则时应为空")
	}
	o3 := obj()
	if !builtinOnly.Dirty([]byte(mustJSON(t, o3))) {
		t.Fatal("内置哨兵应命中")
	}
	if !builtinOnly.Object(o3) {
		t.Fatal("只启用内置层时应改写")
	}
	s3 := mustJSON(t, o3)
	if strings.Contains(s3, "11128") {
		t.Errorf("内置层未生效: %s", s3)
	}
	// 自定义规则不生效（未配置）——「加入」语义：没有的就是没有。
	if !strings.Contains(s3, "内部代号X") {
		t.Errorf("未配置的自定义规则不应生效: %s", s3)
	}
}

// TestLayerEnabledMeansBuiltinPlusCustom 固定「启用 = 内置 + 自定义」的叠加语义
// （自定义是「加入」而非「替换」；曾考虑过单独的 builtin 开关，被否决：
// 那会造出静默丢掉 11128 防护的脚枪）。
func TestLayerEnabledMeansBuiltinPlusCustom(t *testing.T) {
	custom := mustBuild(t, Rule{Match: "ZZZ", Replace: "Y"})
	l := NewLayer(true, custom)
	o := map[string]any{"messages": []any{map[string]any{"content": "ZZZ and 11128"}}}
	if !l.Object(o) {
		t.Fatal("应改写")
	}
	s := mustJSON(t, o)
	if strings.Contains(s, "ZZZ") || strings.Contains(s, "11128") {
		t.Errorf("叠加语义失效: %s", s)
	}
}

// TestLayerCoversSameChannelsAsBuiltin 自定义层必须覆盖与内置层完全相同的
// 字段通道（曾出现过"内置修了 tool_calls 盲区、自定义层漏了"这类漂移）。
func TestLayerCoversSameChannelsAsBuiltin(t *testing.T) {
	custom := mustBuild(t, Rule{Match: "TOKEN", Replace: "X"})
	l := NewLayer(true, custom)
	// 每个通道各放一处 "TOKEN"，断言全部被改掉。
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "TOKEN"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "TOKEN"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:TOKEN"}},
		}},
		map[string]any{"role": "assistant", "content": nil, "reasoning_content": "TOKEN", "reasoning": "TOKEN",
			"tool_calls": []any{map[string]any{"function": map[string]any{"arguments": `{"a":"TOKEN"}`}}}},
	}}
	if !l.Object(obj) {
		t.Fatal("自定义层未报告改动")
	}
	s := mustJSON(t, obj)
	// image part 必须原样保留（数据不能被改）。
	imageKept := strings.Count(s, "data:TOKEN")
	if imageKept != 1 {
		t.Errorf("image part 被误改: %s", s)
	}
	if n := strings.Count(s, "TOKEN") - imageKept; n != 0 {
		t.Errorf("仍有 %d 处通道未被自定义层覆盖: %s", n, s)
	}
}

func TestHolderAtomicSwap(t *testing.T) {
	h := NewHolder(NewLayer(true, mustBuild(t, Rule{Match: "a", Replace: "b"})))
	if h.Load() == nil || h.Load().CustomRules()[0].Match != "a" {
		t.Fatal("初始层不对")
	}
	h.Store(NewLayer(true, mustBuild(t, Rule{Match: "c", Replace: "d"})))
	if got := h.Load().CustomRules(); len(got) != 1 || got[0].Match != "c" {
		t.Fatalf("热改后规则不对: %+v", got)
	}
	// nil holder 安全。
	var nilH *Holder
	if nilH.Load() != nil {
		t.Error("nil holder Load 应为 nil")
	}
	nilH.Store(NewLayer(true, nil)) // 不 panic
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func strconvItoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
