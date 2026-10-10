package panel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 面板表单 → 保存 payload 的**行为**守护。
//
// 背景（三次踩同一坑）：后端保存是「提交叠加到磁盘现值」——payload 里没出现的键，
// 磁盘上的旧值原样保留。所以「把某项恢复成默认」必须把空值显式发出去，
// 不下发 = 静默不生效：
//   - 密钥门（主入口漏接回调）；
//   - 数字字段类型（14 项漏登记 NUMERIC_CFG）；
//   - 提示词预设选「default 内置默认」后回显成旧值（prompt_preset 不在 CLEARABLE_CFG；
//     分域覆盖清空时又被整体删掉，而提交里没出现的键会保持磁盘现值）。
//
// 类型清单（configSchema.ts 的 CFG_MAP / NUMERIC_CFG / CLEARABLE_CFG）只能靠人工维护，
// 所以这里直接跑**真实的** collectConfig（node 直接 import .ts），按行为断言。
// node 不可用或版本不支持 TS 直载时跳过（与 TestWebJSIsParsable 同口径，不阻塞构建机）。

const schemaRel = "../../frontend/src/configSchema.ts"

// collectHarness 读取 cases JSON，调真实 collectConfig，输出 [{name, payload}]。
const collectHarness = `
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';

const [schemaPath, casesPath] = process.argv.slice(2);
const m = await import(pathToFileURL(schemaPath).href);
const cases = JSON.parse(readFileSync(casesPath, 'utf8'));
const out = cases.map((c) => {
  const form = {};
  for (const [k, v] of Object.entries(c.values || {})) {
    form[k] = typeof v === 'boolean' ? { type: 'checkbox', checked: v } : { value: String(v) };
  }
  return { name: c.name, payload: m.collectConfig(form) };
});
process.stdout.write(JSON.stringify(out));
`

type collectCase struct {
	Name   string         `json:"name"`
	Values map[string]any `json:"values"`
}

// nodeTSRunner 找到「能让 node 直接 import .ts」的调用方式。
// 支持顺序：默认（Node ≥ 22.18 / 23+ 自带类型擦除）→ --experimental-strip-types（22.6+）。
func nodeTSRunner(t *testing.T, schemaAbs string) (bin string, args []string, ok bool) {
	t.Helper()
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Log("未找到 node，跳过（前端映射的行为测试需要 node ≥ 22.6）")
		return "", nil, false
	}
	probe := `import(process.argv[1]).then(() => process.exit(0), (e) => { console.error(e.message); process.exit(3); })`
	candidates := [][]string{{}, {"--experimental-strip-types"}}
	for _, extra := range candidates {
		argv := append(append([]string{}, extra...), "-e", probe)
		argv = append(argv, "file://"+filepath.ToSlash(schemaAbs))
		out, err := exec.Command(bin, argv...).CombinedOutput()
		if err == nil {
			return bin, extra, true
		}
		t.Logf("node %v 无法直接载入 .ts：%s", extra, firstLine(string(out)))
	}
	t.Log("当前 node 不支持直接载入 TS（类型擦除），跳过前端映射的行为测试")
	return "", nil, false
}

func firstLine(s string) string {
	line := strings.Join(strings.Fields(s), " ")
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

// runCollectConfig 用真实 collectConfig 跑一批用例，返回 name → payload。
func runCollectConfig(t *testing.T, cases []collectCase) map[string]map[string]any {
	t.Helper()
	schemaAbs, err := filepath.Abs(schemaRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(schemaAbs); err != nil {
		t.Fatalf("前端口径文件缺失：%v", err)
	}
	bin, args, ok := nodeTSRunner(t, schemaAbs)
	if !ok {
		t.Skip("node 不可用或不支持 TS 直载")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "collect.mjs")
	if err := os.WriteFile(harness, []byte(collectHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	casesFile := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(casesFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	argv := append(append([]string{}, args...), harness, schemaAbs, casesFile)
	out, err := exec.Command(bin, argv...).Output()
	if err != nil {
		t.Fatalf("跑 collectConfig 失败：%v\n%s", err, firstLine(string(out)))
	}
	var got []struct {
		Name    string         `json:"name"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("解析 harness 输出失败：%v\n%s", err, firstLine(string(out)))
	}
	res := map[string]map[string]any{}
	for _, g := range got {
		res[g.Name] = g.Payload
	}
	return res
}

// digPayload 按点分路径取值；missing=true 表示路径上任意一层缺失。
func digPayload(m map[string]any, path ...string) (v any, missing bool) {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, true
		}
		cur, ok = obj[k]
		if !ok {
			return nil, true
		}
	}
	return cur, false
}

// TestCollectConfigExpressesClearIntent 保存 payload 必须能表达「恢复默认」。
//
// 分工（改名/重构时最容易两边都以为对方负责的地方）：
//   - 前端负责**把清空表达出来**：选「默认/继承顶层」= 空串，必须照发，不能因为
//     为空就不下发——不下发等于「不动这个键」，用户会看到自己改的选择没生效；
//   - 后端负责**收口**：保存是「叠加到磁盘现值再整份覆盖」，分域覆盖全空的对象
//     由归一化阶段剪掉（config 的 pruneEmptyPromptProfiles，见 cmd/server 的
//     TestSaveConfigPrunesEmptyPromptProfiles），
//     所以前端不需要判断「磁盘上原本有没有覆盖」。
func TestCollectConfigExpressesClearIntent(t *testing.T) {
	const clearCase = "顶层预设从 official-craft 改回 default（内置默认 = 空串）"
	const realmCase = "CN 分域预设从 official-craft 改回「继承顶层」"

	got := runCollectConfig(t, []collectCase{
		{
			Name:   clearCase,
			Values: map[string]any{"prompt_preset": ""},
		},
		{
			Name:   realmCase,
			Values: map[string]any{"prompt_cn_preset": "", "prompt_cn_mode": "", "prompt_cn_text": ""},
		},
	})

	// 1) 顶层：必须把空串发出去（否则保存后仍是旧预设，面板回显旧值）。
	v, missing := digPayload(got[clearCase], "prompt", "preset")
	if missing {
		t.Errorf("%s：payload 里没有 prompt.preset，清空意图丢失", clearCase)
	} else if v != "" {
		t.Errorf("%s：prompt.preset = %#v，期望空串", clearCase, v)
	}

	// 2) 分域：清空覆盖时同样必须显式发空串（后端据此把旧覆盖剪掉）。
	for _, k := range []string{"mode", "preset", "text"} {
		v, missing := digPayload(got[realmCase], "prompt", "profiles", "cn", k)
		if missing {
			t.Errorf("%s：payload 里没有 prompt.profiles.cn.%s，清空意图丢失", realmCase, k)
			continue
		}
		if v != "" {
			t.Errorf("%s：cn.%s = %#v，期望空串", realmCase, k, v)
		}
	}
}

// TestCollectConfigNumericFieldsSendNumbers 行为验证数字字段转 Number
// （静态守护见 TestPanelFormTypesMatchGoConfig；这里确认真的发出 JSON 数字）。
func TestCollectConfigNumericFieldsSendNumbers(t *testing.T) {
	const c = "数字字段"
	got := runCollectConfig(t, []collectCase{{
		Name:   c,
		Values: map[string]any{"max_in_flight": "3", "credit_floor": "100", "idle_weight_per_hour": "0.5", "checkin_hours": "9, 21"},
	}})
	for _, path := range [][]string{{"pool", "max_in_flight"}, {"pool", "credit_floor"}, {"pool", "idle_weight_per_hour"}} {
		v, missing := digPayload(got[c], path...)
		if missing {
			t.Errorf("%v 没出现在 payload 里", path)
			continue
		}
		if _, ok := v.(float64); !ok {
			t.Errorf("%v = %#v（%T），期望 JSON 数字", path, v, v)
		}
	}
	v, missing := digPayload(got[c], "schedule", "checkin_hours")
	if missing {
		t.Fatal("schedule.checkin_hours 没出现在 payload 里")
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("checkin_hours = %#v，期望两个数字", v)
	}
	for i, e := range arr {
		if n, ok := e.(float64); !ok || n != float64([]int{9, 21}[i]) {
			t.Errorf("checkin_hours[%d] = %#v，期望 %d", i, e, []int{9, 21}[i])
		}
	}
}

// TestClearableCfgCoversTopLevelPromptPreset 静态兜底：不依赖 node 也能拦住
// 「顶部预设被从 CLEARABLE_CFG 里删掉」这种回归。
func TestClearableCfgCoversTopLevelPromptPreset(t *testing.T) {
	clearable := parseConfigSchemaTS(t).clearable
	for _, name := range []string{"prompt_preset", "prompt_file", "prompt_text"} {
		if !clearable[name] {
			t.Errorf("CLEARABLE_CFG 缺少 %q：选「默认/清空」时不会下发空值，保存后回显旧值", name)
		}
	}
}
