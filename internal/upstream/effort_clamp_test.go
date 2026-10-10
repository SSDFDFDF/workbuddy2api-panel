package upstream

import (
	"encoding/json"
	"testing"

	"workbuddy_manager/internal/jsondoc"
)

// ClampEffort 继承 fork 前 payload.go:normalizeReasoningEffort 的语义：越界档位归一到
// ≤请求档位的最高支持档（降级优先、偏离最小），支持档全高于请求档时取最低支持档。
// 直接 400 会毁掉整次请求，而档位越界与账号无关（换号无用），只能在发送前归一。
func TestClampEffortDowngradesToNearestSupported(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
		supported []string
		want      string
		changed   bool
	}{
		{"已支持不改写", "high", []string{"low", "high", "max"}, "", false},
		{"顶档越界降级", "max", []string{"high", "xhigh"}, "xhigh", true},
		{"xhigh 越界降到 high", "xhigh", []string{"low", "medium", "high"}, "high", true},
		{"medium 越界降到 low", "medium", []string{"minimal", "low"}, "low", true},
		// 支持档全部高于请求档：只能升到最低支持档（比 400 更接近用户意图）。
		{"支持档全高于请求档", "minimal", []string{"high", "xhigh"}, "high", true},
		// 关闭思考是开关值而不是档位：把它归一到某个真实档位等于把用户关掉的思考又打开。
		{"off 不改写", "off", []string{"low", "high"}, "", false},
		{"none 不改写", "none", []string{"low", "high"}, "", false},
		// 未知档位/未知模型一律透传，由上游裁决（不透传等于替上游编造档位）。
		{"未知档位不改写", "extreme", []string{"low", "high"}, "", false},
		{"能力未知不改写", "max", nil, "", false},
		{"能力未知不改写（空数组）", "max", []string{}, "", false},
		{"空请求值不改写", "", []string{"low", "high"}, "", false},
		// 支持集里混入无法排序的值时忽略它，其余照常参与判定。
		{"忽略无法排序的支持值", "max", []string{"high", "experimental"}, "high", true},
		// 大小写/空白：真实客户端会发 "High"/"XHigh"/" max "，上游可能大小写敏感，
		// 因此受支持的档位也要按**声明形态**回写（否则就白丢一次 400）。
		{"大小写归一（越界）", " Max ", []string{"high", "xhigh"}, "xhigh", true},
		{"大小写归一（受支持）", "XHIGH", []string{"high", "xhigh"}, "xhigh", true},
		{"首尾空白归一（受支持）", " high", []string{"low", "high"}, "high", true},
		{"支持集只有关闭值时不改写", "high", []string{"off"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := ClampEffort(tc.requested, tc.supported)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("ClampEffort(%q,%v)=(%q,%v) want (%q,%v)", tc.requested, tc.supported, got, changed, tc.want, tc.changed)
			}
		})
	}
}

// 模型能力的三级来源必须与 /v1/models 宣告的一份口径：远端探测桶权威，缺失落本 realm
// 的静态兜底表，两处都不存在 → nil（调用方透传不降级）。若两处各写一份，客户端就会
// 选中一个「本网关自己宣告、却被自己改写」的档位。
func TestEffortSupportMatchesListing(t *testing.T) {
	c := &Client{}
	// CN 静态兜底（glm-5.3-flash: [low high max]，默认 high）。
	efforts, def := c.EffortSupport("cn", "glm-5.3-flash")
	if len(efforts) != 3 || def != "high" {
		t.Fatalf("cn/glm-5.3-flash=%v/%q", efforts, def)
	}
	// 同一模型名跨域可以不同：deepseek-v4.1-flash 国际版只认 high。
	if e, _ := c.EffortSupport("global", "deepseek-v4.1-flash"); len(e) != 1 || e[0] != "high" {
		t.Fatalf("global/deepseek-v4.1-flash=%v", e)
	}
	if e, _ := c.EffortSupport("cn", "deepseek-v4.1-flash"); len(e) != 3 {
		t.Fatalf("cn/deepseek-v4.1-flash=%v", e)
	}
	// 未收录模型 → 未知（nil），绝不能编造一个集合去改写请求。
	if e, _ := c.EffortSupport("cn", "no-such-model"); e != nil {
		t.Fatalf("unknown model=%v", e)
	}
	// 与 /v1/models 的同一函数对照（含远端桶权威覆盖）。
	c.storeEfforts("cn", map[string][]string{"no-such-model": {"low", "high"}}, map[string]string{"no-such-model": "low"})
	if e, d := c.EffortSupport("cn", "no-such-model"); len(e) != 2 || d != "low" {
		t.Fatalf("remote override=%v/%q", e, d)
	}
	if e, d := EffortListing("cn", "no-such-model", c.effortsSnapshot("cn")["no-such-model"], c.defaultEffortsSnapshot("cn")["no-such-model"]); len(e) != 2 || d != "low" {
		t.Fatalf("listing/effort-support drifted: %v/%q", e, d)
	}
}

// RequestEffortClamp 读三种别名（桥写的 snake、OpenAI 系 camel、reasoning.effort 对象），
// 且**只读不写**：出站文档在轮转循环里被多个 attempt 共享，就地改会污染后续降级判定。
func TestRequestEffortClampReadsAliasesAndDoesNotMutate(t *testing.T) {
	c := &Client{}
	for _, tc := range []struct {
		name, obj, wantKey, wantFrom, wantTo string
	}{
		// cn/glm-5.2 的档位集是 [high xhigh]：max 越界 → 降到 ≤max 的最高支持档 xhigh。
		{"snake 越界", `{"reasoning_effort":"max"}`, "reasoning_effort", "max", "xhigh"},
		{"camel 越界", `{"reasoningEffort":"max"}`, "reasoningEffort", "max", "xhigh"},
		{"对象形态越界", `{"reasoning":{"effort":"max"}}`, "reasoning.effort", "max", "xhigh"},
		{"对象形态已支持", `{"reasoning":{"effort":"xhigh"}}`, "reasoning.effort", "xhigh", "xhigh"},
		{"无档位字段", `{"messages":[]}`, "", "", ""},
		{"已支持", `{"reasoning_effort":"high"}`, "reasoning_effort", "high", "high"},
		{"受支持但大小写不同", `{"reasoning_effort":"HIGH"}`, "reasoning_effort", "HIGH", "high"},
		// 支持档全部高于请求档：只能升到最低支持档（可见地记入归档，不静默）。
		{"支持档全高于请求档时升到最低档", `{"reasoning_effort":"low"}`, "reasoning_effort", "low", "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := mustObject(t, tc.obj)
			key, from, to := c.RequestEffortClamp(obj, "cn", "glm-5.2")
			if key != tc.wantKey || from != tc.wantFrom || to != tc.wantTo {
				t.Fatalf("(%q,%q,%q) want (%q,%q,%q)", key, from, to, tc.wantKey, tc.wantFrom, tc.wantTo)
			}
			// 未改写：原文档必须逐字保持原样。
			if raw := mustJSON(t, obj); raw != tc.obj {
				t.Fatalf("mutated: %s want %s", raw, tc.obj)
			}
		})
	}
}

// 写回必须落到原本读取的那个键位：对象形态写错位置会让出站 body 同时出现两个档位
// （forwarding.reasoning 会以「冲突别名」直接 400）。
func TestSetRequestEffortRoundTrip(t *testing.T) {
	c := &Client{}
	obj := mustObject(t, `{"reasoning":{"effort":"max"}}`)
	key, _, to := c.RequestEffortClamp(obj, "cn", "glm-5.2")
	SetRequestEffort(obj, key, to)
	if raw := mustJSON(t, obj); raw != `{"reasoning":{"effort":"xhigh"}}` {
		t.Fatalf("nested write: %s", raw)
	}
	obj = mustObject(t, `{"reasoning_effort":"max"}`)
	key, _, to = c.RequestEffortClamp(obj, "cn", "glm-5.2")
	SetRequestEffort(obj, key, to)
	if raw := mustJSON(t, obj); raw != `{"reasoning_effort":"xhigh"}` {
		t.Fatalf("snake write: %s", raw)
	}
	// 键位缺失时 SetRequestEffort 仍要落成合法形态（防御：调用方按 from!="" 判断）。
	obj = mustObject(t, `{}`)
	SetRequestEffort(obj, "reasoning.effort", "high")
	if raw := mustJSON(t, obj); raw != `{"reasoning":{"effort":"high"}}` {
		t.Fatalf("nested create: %s", raw)
	}
}

// mustObject / mustJSON 测试辅助（effort_clamp_test.go 专用）。
func mustObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	obj, err := jsondoc.Object([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return obj
}

func mustJSON(t *testing.T, obj map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
