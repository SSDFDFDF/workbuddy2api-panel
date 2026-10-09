package panel

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// panelJSCharts 按分片横幅取出图表分片（js/14-chart.js）源码。
//
// 图表基元收敛后，凡是要调 renderUsageChart / renderUsage / renderExpiry 的切片用例
// 都必须把图表分片一并求值（segBar 与 stackedBarsSVG 在这里定义）。按横幅切比记
// 函数名稳：分片改名时只要横幅同步，多条用例不用各自改切片标记。
const panelJSCharts = `const csStart = src.indexOf('/* ==== js/14-chart.js ==== */');
const csEnd = src.indexOf('/* ==== js/20-accounts.js ==== */');
if (csStart < 0 || csEnd < 0) throw new Error('chart shard not found');
const CHART = src.slice(csStart, csEnd);
`

// panelJSFile 把服务端 init 时拼接好的 app.js 落到临时文件并返回路径。
//
// 前端源码在 js/ 分片里，node 用例统一对“实际下发的那份脚本”求值——分片布局对
// 用例透明，以后调整分片不需要动任何断言。
func panelJSFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(path, panelJS, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPanelJSPartsLayout 守护前端分片清单（js/*.js → app.js）：
//
//   - 分片名必须形如 <两位数字>-<名字>.js：数字前缀既是排序键也是执行顺序；
//   - js/ 下不允许子目录 / 非 .js 文件：go:embed js/*.js 会静默漏掉它们，
//     Go 编译器不报错，面板直接白屏；
//   - 嵌入式清单必须与磁盘目录一致（embed 模式漂移时在这里失败）；
//   - 拼接产物必须严格等于“分片之间夹横幅”的结构，防止漏拼 / 重拼 / 错序。
func TestPanelJSPartsLayout(t *testing.T) {
	nameRe := regexp.MustCompile(`^\d\d-[a-z0-9-]+\.js$`)
	entries, err := os.ReadDir("js")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !nameRe.MatchString(e.Name()) {
			t.Fatalf("js/ 下出现不合规条目 %q（约定：<两位数字>-<名字>.js，且不允许子目录）", e.Name())
		}
		names = append(names, e.Name())
	}
	if len(names) < 2 {
		t.Fatalf("只有 %d 个分片，分片目录疑似被清空", len(names))
	}
	sort.Strings(names)

	embedded, err := fs.Glob(panelJSParts, "js/*.js")
	if err != nil {
		t.Fatal(err)
	}
	embedNames := make([]string, 0, len(embedded))
	for _, n := range embedded {
		embedNames = append(embedNames, strings.TrimPrefix(n, "js/")) // go:embed 名字带 js/ 前缀
	}
	if strings.Join(embedNames, "\n") != strings.Join(names, "\n") {
		t.Fatalf("嵌入式分片清单与磁盘不一致：\nembed=%v\ndisk =%v", embedNames, names)
	}

	bundle, off := string(panelJS), 0
	for i, name := range names {
		src, err := os.ReadFile(filepath.Join("js", name))
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			banner := "/* ==== js/" + name + " ==== */\n"
			if !strings.HasPrefix(bundle[off:], banner) {
				head := bundle[off:]
				if len(head) > 40 {
					head = head[:40]
				}
				t.Fatalf("分片 %s 之前缺少预期横幅，实际开头 %q", name, head)
			}
			off += len(banner)
		}
		if !strings.HasPrefix(bundle[off:], string(src)) {
			t.Fatalf("拼接结果在 %s 处与磁盘分片不一致（顺序不符或内容被改动）", name)
		}
		off += len(src)
	}
	if off != len(bundle) {
		t.Fatalf("拼接结果 %d 字节，已归属 %d 字节：有未归属内容（漏拼 / 重拼）", len(bundle), off)
	}
}

// TestAppJSSyntax 服务端拼接产物与每个 js/ 分片都必须通过 JS 解析器语法校验。
//
// 为什么需要：分片是 go:embed 进二进制的静态资源，Go 编译器不检查其内容——
// 一次对象字面量键名未加引号（Model_chat_GLM5.2 被解析成属性访问 + 数字字面量）
// 就让整个面板白屏，而所有 Go 测试依然全绿。逐分片校验把错误定位到具体文件；
// 整体校验再兜住“分片各自合法、拼起来不合法”（缺分号 / 粘行）的情况。
// 无 node 环境时跳过（不阻塞无 Node 的构建机）。
func TestAppJSSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping JS syntax check")
	}
	entries, err := os.ReadDir("js")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		path := filepath.Join("js", e.Name())
		if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
			t.Fatalf("%s syntax error:\n%s", path, out)
		}
	}
	if out, err := exec.Command(node, "--check", panelJSFile(t)).CombinedOutput(); err != nil {
		t.Fatalf("拼接后的 app.js syntax error:\n%s", out)
	}
}

// TestIndexHTMLNoInlineScript index.html 不得含内联 <script> 块：
// 严格 CSP（script-src 'self'）会拦截内联脚本，页面将完全不可用。
// 外链形式 <script src="..."> 允许。
func TestIndexHTMLNoInlineScript(t *testing.T) {
	p := newTestPanel()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest("GET", "/panel/", nil))
	body := rec.Body.String()

	rest := body
	for {
		idx := strings.Index(rest, "<script")
		if idx < 0 {
			break
		}
		rest = rest[idx:]
		end := strings.Index(rest, ">")
		if end < 0 {
			break
		}
		tag := rest[:end+1]
		if !strings.Contains(tag, "src=") {
			t.Fatalf("index.html contains inline <script> (blocked by CSP): %s", tag)
		}
		rest = rest[end:]
	}
}

// TestAppJSTopLevelSmoke 顶层求值冒烟（v1.11.3/1.11.4 两连炸后补的运行时闸门）：
// node + DOM 桩执行**服务端拼接后的 app.js**（含按 hash 落到各视图的 go() 顶层
// 调用），抓 TDZ/ReferenceError 类运行时错误——Go 侧 frontend_test 不执行 JS，
// 语法层检查对此全盲；分片顺序 / 词法环境顺序错了也在这里炸。
// 无 node 的环境跳过（CI/精简机不受影响）；harness 与 app.js 同判（app.js 顶层
// start() 的 setInterval 会让 node 事件循环不退出，故成功路径显式 exit(0)）。
func TestAppJSTopLevelSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; JS smoke skipped")
	}
	harness := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const inert = new Proxy(function () {}, {
  get(t, k) { if (k === Symbol.toPrimitive) return () => ''; return inert; },
  set() { return true; },
  apply() { return inert; },
  construct() { return inert; },
  has() { return true; },
});
const sandbox = new Proxy({
  location: { hash: process.env.SMOKE_HASH || '#taskscenter' },
  history: { replaceState() {} },
  localStorage: { getItem: () => null, setItem() {} },
  navigator: { clipboard: { writeText: () => Promise.resolve() } },
  document: { querySelectorAll: () => [], querySelector: () => inert, getElementById: () => inert, addEventListener() {}, documentElement: inert, head: inert, body: inert, createElement: () => inert, cookie: '' },
  fetch: () => new Promise(() => {}),
  addEventListener() {}, removeEventListener() {},
  matchMedia: () => ({ matches: false, addEventListener() {} }),
  setInterval, clearInterval, setTimeout, clearTimeout,
  console, JSON, Math, Date, Number, String, Boolean, Object, Array, Promise, Map, Set, RegExp, Error, TypeError, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent, URL, Symbol, Proxy, Reflect,
}, { get(t, k) { return t[k]; }, has() { return true; } });
sandbox.window = sandbox; sandbox.globalThis = sandbox;
vm.createContext(sandbox);
try {
  vm.runInContext(src, sandbox, { filename: 'app.js' });
  console.log('SMOKE OK');
  process.exit(0);
} catch (e) {
  console.log('SMOKE FAIL:', (e && e.stack ? e.stack : e).toString().split('\n').slice(0, 5).join('\n'));
  process.exit(1);
}
`
	hf, err := os.CreateTemp(t.TempDir(), "smoke-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hf.WriteString(harness); err != nil {
		t.Fatal(err)
	}
	hf.Close()
	bundle := panelJSFile(t)
	for _, hash := range []string{"#taskscenter", "#accounts", "#usage", "#models", "#config", "#logs", "#packages"} {
		cmd := exec.Command(node, hf.Name(), bundle) // 绝对路径，无需 cmd.Dir
		cmd.Env = append(os.Environ(), "SMOKE_HASH="+hash)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("app.js 顶层求值 %s 崩溃: %v\n%s", hash, err, out)
		}
		if !bytes.Contains(out, []byte("SMOKE OK")) {
			t.Fatalf("app.js smoke %s 未通过:\n%s", hash, out)
		}
	}
}

// 积分扣除维度的格式必须稳定，且缺样本/缺匹配 Token 时不能伪造比例。
func TestAppJSCreditDimensionFormatting(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; credit formatting test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
	const start = src.indexOf('function trimFixed');
const end = src.indexOf('function usKpi');
if (start < 0 || end < 0) throw new Error('credit helpers not found');
const ctx = { Number, String, RegExp };
vm.createContext(ctx);
vm.runInContext(
  src.slice(start, end) +
  '\nthis.fmtCredit=fmtCredit; this.fmtCreditRatio=fmtCreditRatio; this.fmtModelRate=fmtModelRate;',
  ctx
);
process.stdout.write(JSON.stringify({
  credit: ctx.fmtCredit(1.25),
  zero: ctx.fmtCredit(0),
  hundred: ctx.fmtCredit(100),
  ratio: ctx.fmtCreditRatio(12.5, 2, 400),
  noSamples: ctx.fmtCreditRatio(12.5, 0, 400),
  noTokens: ctx.fmtCreditRatio(12.5, 2, 0),
  rate: ctx.fmtModelRate('0.5'),
  noRate: ctx.fmtModelRate(''),
}));`
	f, err := os.CreateTemp(t.TempDir(), "credit-format-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("credit formatting node test failed: %v\n%s", err, out)
	}
	const want = `{"credit":"1.25","zero":"0","hundred":"100","ratio":"12.5 / 1M","noSamples":"—","noTokens":"—","rate":"x0.5","noRate":"—"}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("credit formatting=%s want %s", out, want)
	}
}

// 模型限流时间必须同时支持上游 reset_at、网关 until 和无重置时间三种形态。
func TestAppJSRateLimitMeta(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; rate limit formatting test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function dur(');
const end = src.indexOf('function rateLimitRowsHtml');
if (start < 0 || end < 0) throw new Error('rate limit helpers not found');
const ctx = { Date, Number, String, Math };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.rateLimitMeta=rateLimitMeta;', ctx);
const now = new Date(2026, 8, 28, 14, 0, 0).getTime();
const reset = new Date(2026, 8, 28, 16, 0, 0).getTime();
const until = new Date(2026, 8, 28, 15, 0, 0).getTime();
const rate = ctx.rateLimitMeta({ model: 'glm-5.3', kind: 'rate_limit', reset_at: new Date(reset).toISOString(), until: new Date(until).toISOString() }, now);
const unavailable = ctx.rateLimitMeta({ model: 'missing', kind: 'model_unavailable', until: new Date(until).toISOString() }, now);
const unknown = ctx.rateLimitMeta({ model: 'glm-5.3', kind: 'rate_limit' }, now);
process.stdout.write(JSON.stringify({
  rate: rate.detail,
  unavailable: unavailable.detail,
  unknown: unknown.detail,
}));`
	f, err := os.CreateTemp(t.TempDir(), "rate-limit-format-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("rate-limit formatting node test failed: %v\n%s", err, out)
	}
	const want = `{"rate":"预计 2026-09-28 16:00 解封（剩余 2时00分） · 网关最快 1时00分 后重试","unavailable":"预计 1时00分 后重试","unknown":"预计解封时间未知"}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("rate-limit formatting=%s want %s", out, want)
	}
}

// 请求记录行必须紧凑、可读，并带上调用来源（IP / UA）；来源缺失时以 — 兜底。
func TestAppJSRequestLogFormatting(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; request log formatting test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const escStart = src.indexOf('function esc(');
const escEnd = src.indexOf('function ago(');
const fmtStart = src.indexOf('function fmtTok(');
const fmtEnd = src.indexOf('function usKpi(');
const reqStart = src.indexOf('function requestLogText');
const reqEnd = src.indexOf('function fmtBytes');
if ([escStart, escEnd, fmtStart, fmtEnd, reqStart, reqEnd].some(v => v < 0)) throw new Error('request log helpers not found');
const ctx = { Date, Number, String, Math, RegExp, isNaN };
vm.createContext(ctx);
vm.runInContext(
  src.slice(escStart, escEnd) + src.slice(fmtStart, fmtEnd) + src.slice(reqStart, reqEnd) +
  '\nthis.requestLogText=requestLogText;',
  ctx
);
const time = new Date(2026, 8, 28, 14, 5, 6).toISOString();
const good = { time, status: 200, outcome: 'success', model: 'glm-5.3', account: '账号(uid8)', duration_ms: 1250, total_tokens: 2300, credit_known: true, credit: 0.12, request_id: 'req-1', client_ip: '203.0.113.7', user_agent: 'python-requests/2.31.0' };
const noSource = { ...good, request_id: 'req-3', client_ip: '', user_agent: '' };
const cached = { ...good, request_id: 'req-2', cache_hit_tokens: 2257, cache_miss_tokens: 43 };
process.stdout.write(JSON.stringify({
  good: ctx.requestLogText(good),
  noSource: ctx.requestLogText(noSource),
  cached: ctx.requestLogText(cached),
}));`
	f, err := os.CreateTemp(t.TempDir(), "request-log-format-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("request log formatting node test failed: %v\n%s", err, out)
	}
	text := "14:05:06 | 200 成功 | glm-5.3 | 账号(uid8) | 203.0.113.7 | python-requests/2.31.0 | 1.25s | 2.3k tok | 0.12 credit | req-1"
	noSource := "14:05:06 | 200 成功 | glm-5.3 | 账号(uid8) | — | — | 1.25s | 2.3k tok | 0.12 credit | req-3"
	cached := "14:05:06 | 200 成功 | glm-5.3 | 账号(uid8) | 203.0.113.7 | python-requests/2.31.0 | 1.25s | 2.3k tok | 0.12 credit | 命中 98.1% | req-2"
	want := `{"good":` + strconv.Quote(text) + `,"noSource":` + strconv.Quote(noSource) + `,"cached":` + strconv.Quote(cached) + `}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("request log formatting=%s want %s", out, want)
	}
}

// 请求记录筛选：IP / UA / 模型 / 账号 / 请求 ID 的包含匹配（空格分词 AND）+ 结果精确匹配。
func TestAppJSRequestMatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; request filter test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function reqMatch');
const end = src.indexOf('function reqOutcomeTag');
if (start < 0 || end < 0) throw new Error('reqMatch not found');
const ctx = {};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.reqMatch=reqMatch;', ctx);
const base = { outcome: 'success', client_ip: '203.0.113.7', user_agent: 'python-requests/2.31.0', model: 'cn:glm-5.3', account: '示例(uid8)', request_id: 'req-1' };
const other = { outcome: 'http_error', client_ip: '198.51.100.4', user_agent: 'Mozilla/5.0 Chrome/120', model: 'global:hy3', account: '甲(uid9)', request_id: 'req-2' };
const rows = [base, other];
const pick = f => rows.filter(e => ctx.reqMatch(e, f)).map(e => e.request_id);
process.stdout.write(JSON.stringify({
  all: pick({ q: '', outcome: '' }),
  byIP: pick({ q: '203.0.113', outcome: '' }),
  byUA: pick({ q: 'chrome/120', outcome: '' }),
  byModel: pick({ q: 'glm', outcome: '' }),
  multiKw: pick({ q: 'glm success', outcome: '' }),
  multiMiss: pick({ q: 'glm chrome', outcome: '' }),
  byOutcome: pick({ q: '', outcome: 'http_error' }),
  combined: pick({ q: '198.51', outcome: 'http_error' }),
}));`
	f, err := os.CreateTemp(t.TempDir(), "request-filter-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("request filter node test failed: %v\n%s", err, out)
	}
	// q 对 outcome 不参与匹配（outcome 有独立下拉），multiKw 里的 success 命中不了任何字段。
	const want = `{"all":["req-1","req-2"],"byIP":["req-1"],"byUA":["req-2"],"byModel":["req-1"],"multiKw":[],"multiMiss":[],"byOutcome":["req-2"],"combined":["req-2"]}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("request filter=%s want %s", out, want)
	}
}

// 模型按条件查询：域 / 能力 / 档位 / 价格 / 关键词，以及倍率、上下文、输出排序。
func TestAppJSModelFilter(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; model filter test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function mdRateValue');
const end = src.indexOf('function mdRowHtml');
if (start < 0 || end < 0) throw new Error('model filter helpers not found');
const ctx = { Number, String, Array, Object, isFinite, parseFloat };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.mdMatch=mdMatch; this.mdSortList=mdSortList; this.mdRateValue=mdRateValue;', ctx);
const models = [
  { id: 'cn:glm-5.2', name: 'GLM-5.2', vendor: 'Zhipu', tags: ['视觉'], supports_tool_call: true, supports_images: true, supports_reasoning: true, can_disable_thinking: true, supported_efforts: ['high', 'xhigh'], default_effort: 'high', is_default: false, credits: '0.79', promo_factor: 0.5, promo_credits: '0.40', promo_label: '夜间折扣', context_length: 1000000, max_output_tokens: 131000 },
  { id: 'cn:hy3', name: 'Hy3', supports_tool_call: true, supports_images: true, supports_reasoning: true, can_disable_thinking: false, supported_efforts: ['low', 'high'], default_effort: 'high', is_default: false, credits: '0', promo_factor: 0, promo_credits: '0', promo_label: '限时免费', context_length: 192000, max_output_tokens: 64000 },
  { id: 'global:hy3', name: 'Hy3 Global', supports_tool_call: false, supports_images: false, supports_reasoning: false, supported_efforts: [], is_default: false, credits: '0.11', context_length: 1000000, max_output_tokens: 393000 },
  { id: 'cn:auto', name: 'Auto', supports_tool_call: true, supports_images: true, supports_reasoning: true, is_default: true, credits: null, context_length: 256000, max_output_tokens: 32000 },
];
const ids = list => list.map(m => m.id);
const filter = f => ids(ctx.mdSortList(models.filter(m => ctx.mdMatch(m, f)), f));
process.stdout.write(JSON.stringify({
  all: ids(models),
  realm: filter({ realm: 'cn' }),
  tool: filter({ cap: 'tool' }),
  vision: filter({ cap: 'vision' }),
  reasoning: filter({ cap: 'reasoning' }),
  isDefault: filter({ cap: 'default' }),
  effortOff: filter({ effort: 'off' }),
  effortLow: filter({ effort: 'low' }),
  free: filter({ promo: 'free' }),
  promo: filter({ promo: 'promo' }),
  discount: filter({ promo: 'discount' }),
  q: filter({ q: 'glm zhipu' }),
  qMiss: filter({ q: 'glm nosuch' }),
  sortRate: filter({ sort: 'rate' }),
  sortContext: filter({ sort: 'context' }),
  sortOutput: filter({ sort: 'output' }),
  sortName: filter({ sort: 'name' }),
  rateFree: ctx.mdRateValue(models[1]),
  rateMissing: ctx.mdRateValue(models[3]),
}));`
	f, err := os.CreateTemp(t.TempDir(), "model-filter-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("model filter node test failed: %v\n%s", err, out)
	}
	const want = `{"all":["cn:glm-5.2","cn:hy3","global:hy3","cn:auto"],` +
		`"realm":["cn:glm-5.2","cn:hy3","cn:auto"],` +
		`"tool":["cn:glm-5.2","cn:hy3","cn:auto"],` +
		`"vision":["cn:glm-5.2","cn:hy3","cn:auto"],` +
		`"reasoning":["cn:glm-5.2","cn:hy3","cn:auto"],` +
		`"isDefault":["cn:auto"],` +
		`"effortOff":["cn:glm-5.2"],` +
		`"effortLow":["cn:hy3"],` +
		`"free":["cn:hy3"],` +
		`"promo":["cn:glm-5.2","cn:hy3"],` +
		`"discount":["cn:glm-5.2"],` +
		`"q":["cn:glm-5.2"],` +
		`"qMiss":[],` +
		`"sortRate":["cn:hy3","global:hy3","cn:glm-5.2","cn:auto"],` +
		`"sortContext":["cn:glm-5.2","global:hy3","cn:auto","cn:hy3"],` +
		`"sortOutput":["global:hy3","cn:glm-5.2","cn:hy3","cn:auto"],` +
		`"sortName":["cn:auto","cn:glm-5.2","cn:hy3","global:hy3"],` +
		`"rateFree":0,"rateMissing":null}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("model filter=%s\nwant %s", out, want)
	}
}

// 用量明细表的列数必须与 US_DIMS 的 span 一致：空数据行用 colspan=span 撑满整行，
// 表头与 span 一旦不同步，空表就会错位（或撑不满）。此前列数是手写的 10/7/7，
// 且 prompt / completion / 合计三列共存；简化成单列「Token」（明细在构成条 title）后
// 必须同步 span，否则这个回归只会等人肉点开空表才能看到。
func TestAppJSUsageDimColumnsMatchSpan(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; usage dim columns test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const US_DIMS');
const end = src.indexOf('function renderUsageDim');
if (start < 0 || end < 0 || end < start) throw new Error('usage dim region not found');
const ctx = { esc: s => String(s == null ? '' : s) };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.US_DIMS = US_DIMS; this.usDimHead = usDimHead;', ctx);
const out = {};
for (const dim of ['account', 'model', 'realm']) {
  out[dim] = {
    cols: (ctx.usDimHead(dim).match(/<th/g) || []).length,
    span: ctx.US_DIMS[dim].span,
  };
}
process.stdout.write(JSON.stringify(out));`
	f, err := os.CreateTemp(t.TempDir(), "dim-cols-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("usage dim columns node test failed: %v\n%s", err, out)
	}
	const want = `{"account":{"cols":8,"span":8},"model":{"cols":5,"span":5},"realm":{"cols":5,"span":5}}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("usage dim columns=%s\nwant %s", strings.TrimSpace(string(out)), want)
	}
}

// 用量统计的「简化」不得丢信息：卡片只留结论级指标（主统计 3 张 / 积分 4 张），
// 但 prompt、completion 的绝对量与占比必须仍能看到（副标题 + 构成条 title）。
// 这个用例钉住的是“合并重复展示”与“删掉信息”的区别。
func TestAppJSUsageStatsCarrySplitDetail(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; usage stats test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const fmtStart = src.indexOf('function fmtTok(');
const fmtEnd = src.indexOf('/* ==== js/13-shell.js');
const csStart = src.indexOf('/* ==== js/14-chart.js ==== */');
const csEnd = src.indexOf('/* ==== js/20-accounts.js ==== */');
if (csStart < 0 || csEnd < 0) throw new Error('chart shard not found');
const CHART = src.slice(csStart, csEnd);
const usageStart = src.indexOf('function renderUsage(');
const usageEnd = src.indexOf('function parsePointTime');
if ([fmtStart, fmtEnd, usageStart, usageEnd].some(v => v < 0)) throw new Error('usage stats region not found');
const sinks = {};
// 桩元素带 addEventListener：切片会带上本分片尾部的顶层事件绑定（维度切换等），
// 它们只需能挂监听即可。
const sink = id => (sinks[id] = sinks[id] || {
  innerHTML: '', textContent: '', title: '', addEventListener() {},
});
const ctx = {
  Date, Number, String, Math, RegExp, isNaN,
  $: sink,
  esc: s => String(s == null ? '' : s),
  trangeLabel: () => '今天',
  cacheRateText: () => '98.1%',
  renderUsageDim() {}, renderCreditDim() {}, renderUsageChart() {},
};
vm.createContext(ctx);
vm.runInContext(CHART + src.slice(fmtStart, fmtEnd) + src.slice(usageStart, usageEnd) +
  '\nthis.renderUsage = renderUsage;', ctx);
ctx.renderUsage({
  totals: { requests: 200, errors: 2, total_tokens: 3000000, prompt_tokens: 2000000,
            completion_tokens: 1000000, avg_latency_ms: 900, avg_tokens_per_second: 12.5,
            credits: 1.5, credit_tokens: 1000000, credit_samples: 3, credits_per_1m_tokens: 1.5,
            cache_hit_tokens: 90, cache_miss_tokens: 10 },
  series: [], by_account: [], by_model: [], by_realm: [], credit_by_account: [], credit_by_model: [],
});
const stats = sinks.usStats.innerHTML;
const credit = sinks.usCreditStats.innerHTML;
process.stdout.write(JSON.stringify({
  mainCards: (stats.match(/class="kpi /g) || []).length,
  creditCards: (credit.match(/class="kpi /g) || []).length,
  hasPromptAbs: stats.includes('prompt 2M'),
  hasCompletionAbs: stats.includes('completion 1M'),
  hasPctTip: /title="prompt 66\.7%"/.test(stats) && /title="completion 33\.3%"/.test(stats),
  hasSuccessAndErrs: stats.includes('成功率 99.0% · 失败 2 次'),
  hasSampleCount: credit.includes('3 个有效样本'),
}));`
	f, err := os.CreateTemp(t.TempDir(), "usage-stats-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("usage stats node test failed: %v\n%s", err, out)
	}
	const want = `{"mainCards":3,"creditCards":4,"hasPromptAbs":true,"hasCompletionAbs":true,` +
		`"hasPctTip":true,"hasSuccessAndErrs":true,"hasSampleCount":true}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("usage stats=%s\nwant %s", strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSSegBarIsTheOnlyBar 柱/条只有一套实现：segBar（js/14-chart.js）。
//
// 为什么需要：全站曾有六处各自手写条形（账号池积分条 .bar、KPI .kbar、用量表 .us-mix、
// 积分构成 .mixbar、账号到期条 .expirybar、到期分布图 .pk-expiry-*），同一件事六个实现，
// 改一处样式要翻六个地方，宽度算法也会慢慢跑偏。这里钉住两件事：
//  1. 前端源码里不再出现手写的条形标记；
//  2. segBar 自身行为稳定：按 total 算宽度、空/全零数据返回空串、title 转义。
func TestAppJSSegBarIsTheOnlyBar(t *testing.T) {
	for _, legacy := range []string{`class="bar"`, `class="kbar"`, `class="mixbar"`,
		`class="expirybar"`, `class="us-mix"`, `pk-expiry-seg`} {
		if strings.Contains(string(panelJS), legacy) {
			t.Errorf("前端仍存在手写条形 %s；条形一律走 segBar（js/14-chart.js）", legacy)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; segBar test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function segBar(');
const end = src.indexOf('/* stackedBarsSVG');
if (start < 0 || end < 0) throw new Error('segBar not found');
// esc 桩与真实实现同形（5 个字符都转）：断言 segBar 的 title 确实过转义，
// 而不是把它原样拼进属性。
const ctx = { esc: s => String(s == null ? '' : s).replace(/&/g, '&amp;')
  .replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;') };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.segBar = segBar;', ctx);
const two = ctx.segBar([{ value: 1, color: 'red', title: 'a<b' }, { value: 3, color: 'blue' }]);
const capped = ctx.segBar([{ value: 1 }, { value: 1 }], { total: 10, cls: 'xx' });
process.stdout.write(JSON.stringify({
  widths: (two.match(/width:([0-9.]+)%/g) || []),
  escaped: two.includes('&lt;b'),
  cls: capped.includes('class="segbar xx"'),
  cappedSum: (capped.match(/width:([0-9.]+)%/g) || []).join(),
  empty: ctx.segBar([], {}),
  zero: ctx.segBar([{ value: 0 }], {}),
  aria: ctx.segBar([{ value: 1 }], { aria: '紧迫度' }).includes('aria-label="紧迫度"'),
}));`
	f, err := os.CreateTemp(t.TempDir(), "segbar-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("segBar node test failed: %v\n%s", err, out)
	}
	const want = `{"widths":["width:25.000%","width:75.000%"],"escaped":true,"cls":true,"cappedSum":"width:10.000%,width:10.000%","empty":"","zero":"","aria":true}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("segBar=%s\nwant %s", strings.TrimSpace(string(out)), want)
	}
}

// 用量时序图的柱体类名不得叫 bar：账号池的积分条曾叫 .bar{height:3px}，而 SVG2 里
// height 是 rect 的 CSS 几何属性——同名类会把每根柱子压成 3px 高，图看起来"没数据"。
// 这个坑只能在浏览器里看出来，所以在这里钉住类名。
func TestAppJSUsageChartBarClass(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; usage chart test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function parsePointTime');
const end = src.indexOf('async function warmUsageModelRates');
const csStart = src.indexOf('/* ==== js/14-chart.js ==== */');
const csEnd = src.indexOf('/* ==== js/20-accounts.js ==== */');
if (csStart < 0 || csEnd < 0) throw new Error('chart shard not found');
const CHART = src.slice(csStart, csEnd);
const escStart = src.indexOf('function esc(');
const escEnd = src.indexOf('function ago(');
const fmtStart = src.indexOf('function fmtTok(');
const fmtEnd = src.indexOf('function usKpi(');
if ([start, end, escStart, escEnd, fmtStart, fmtEnd].some(v => v < 0)) throw new Error('usage chart helpers not found');
const host = { innerHTML: '', textContent: '' };
const ctx = {
  Date, Number, String, Math, RegExp, isNaN, Set, Array, Object, Infinity,
  document: { getElementById: () => host },
  $: () => host,
};
vm.createContext(ctx);
vm.runInContext(CHART + src.slice(escStart, escEnd) + src.slice(fmtStart, fmtEnd) + src.slice(start, end) +
  '\nthis.renderUsageChart=renderUsageChart;', ctx);
const series = [
  { t: '2026-09-30T09', scope: 'hour', prompt_tokens: 35, completion_tokens: 16, total_tokens: 51, requests: 1 },
  { t: '2026-09-30T11', scope: 'hour', prompt_tokens: 978324, completion_tokens: 20621, total_tokens: 998945, requests: 39 },
  { t: '2026-09-30T13', scope: 'hour', prompt_tokens: 27400952, completion_tokens: 104913, total_tokens: 27505865, requests: 200 },
];
ctx.renderUsageChart(series);
const svg = host.innerHTML;
process.stdout.write(JSON.stringify({
  hasUsbar: svg.includes('class="usbar"'),
  hasBareBar: /class="bar"/.test(svg),
  hasGradient: svg.includes('usGradP') && svg.includes('usGradC'),
  barCount: (svg.match(/class="usbar"/g) || []).length,
  hasPeak: svg.includes('峰值'),
  hasAvg: svg.includes('均值'),
  emptyState: (function () { ctx.renderUsageChart([]); return host.innerHTML.includes('us-empty'); })(),
}));`
	f, err := os.CreateTemp(t.TempDir(), "usage-chart-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("usage chart node test failed: %v\n%s", err, out)
	}
	const want = `{"hasUsbar":true,"hasBareBar":false,"hasGradient":true,"barCount":6,"hasPeak":true,"hasAvg":true,"emptyState":true}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("usage chart=%s\nwant %s", out, want)
	}
}

// 时间范围控件：预设 → 查询参数的映射。要点：
//   - 「今天」必须发浏览器本地时区的 00:00（服务端时区未必一致），且不带 to；
//   - 滚动预设 rolling=true 发 hours（服务端整点对齐），rolling=false 折算成 from；
//   - 「全部历史」两者都不发；「自定义」发用户挑的 from/to。
func TestAppJSTimeRangeQuery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; time range test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const TRANGE_PRESETS');
const end = src.indexOf('function rateLimitMeta');
if (start < 0 || end < 0 || end < start) throw new Error('trange helpers not found');
const host = { innerHTML: '' };
const ctx = {
  Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams,
  document: { getElementById: () => host },
  $: () => host,
  esc: s => String(s == null ? '' : s),
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) +
  '\nthis.trangeState=trangeState; this.trangeQuery=trangeQuery; this.trangeLabel=trangeLabel; this.trangeMidnight=trangeMidnight;', ctx);
const q = (preset, rolling) => {
  ctx.trangeState('t').preset = preset;
  return ctx.trangeQuery('t', rolling).toString();
};
const secOf = d => String(Math.floor(d.getTime() / 1000));
const approx = (qs, wantSec) => {
  const m = /(?:^|&)from=(\d+)/.exec(qs);
  return m && Math.abs(Number(m[1]) - wantSec) < 120;
};
const now = Date.now();
const todayQ = q('today', true);
process.stdout.write(JSON.stringify({
  todayIsMidnight: todayQ === 'from=' + secOf(ctx.trangeMidnight()),
  todayNoTo: !/to=/.test(todayQ),
  rolling24: q('24', true),
  rolling72: q('72', true),
  rolling0: q('0', true),
  log0: q('0', false),
  log24From: approx(q('24', false), Math.floor((now - 24 * 3600e3) / 1000)),
  log24HasHours: /hours=/.test(q('24', false)),
  log7dFrom: approx(q('168', false), Math.floor((now - 168 * 3600e3) / 1000)),
  custom: (function () {
    const st = ctx.trangeState('t');
    st.preset = 'custom';
    st.from = new Date(2026, 8, 30, 9, 0, 0);
    st.to = new Date(2026, 8, 30, 18, 30, 0);
    return ctx.trangeQuery('t', true).toString();
  })(),
  labelCustom: ctx.trangeLabel('t'),
  labelToday: (function () { ctx.trangeState('t').preset = 'today'; return ctx.trangeLabel('t'); })(),
}));`
	f, err := os.CreateTemp(t.TempDir(), "trange-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("time range node test failed: %v\n%s", err, out)
	}
	local := func(h, m int) string {
		return strconv.FormatInt(time.Date(2026, 9, 30, h, m, 0, 0, time.Local).Unix(), 10)
	}
	want := `{"todayIsMidnight":true,"todayNoTo":true,` +
		// rolling0 必须是 "hours=0"（显式全部历史）。此前期望值是空串——那恰好把
		// issue #121 的错误行为固化成了断言：空 query 会被后端的 72 小时缺省接管，
		// 于是「全部历史」显示成「近 3 天」。
		// log0 仍为空：请求记录端点没有缺省窗口，不传 from/to 就是全部历史。
		`"rolling24":"hours=24","rolling72":"hours=72","rolling0":"hours=0","log0":"",` +
		`"log24From":true,"log24HasHours":false,"log7dFrom":true,` +
		`"custom":"from=` + local(9, 0) + `&to=` + local(18, 30) + `",` +
		`"labelCustom":"9-30 09:00 → 9-30 18:30","labelToday":"今天"}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("time range=%s\nwant %s", out, want)
	}
}

// 配置表单与 CFG_MAP 必须一一对应，且面板声称"可在线改"的热生效键必须真的
// 出现在表单里。
//
// 为什么需要：`logging.request_client_info` 曾经在表单里存在过，后来在某次改动中
// 被连带删掉，而 Go 侧的配置键、config/runtime 热生效通路、README 的描述都还在——面板
// 少了一个开关而 Go 测试全绿，只有人肉点开配置页才会发现。这里把"表单字段 ↔
// CFG_MAP"与"关键热改键必须在表单里"两条都钉住。
func TestConfigFormMatchesCFGMap(t *testing.T) {
	// 校验对象就是实际下发的那两份资源：分片拼出的 app.js 与 embed 的页面。
	js := string(panelJS)
	html := string(indexHTML)

	// CFG_MAP 块（下面两条检查共用）。
	mapBlock := js[strings.Index(js, "const CFG_MAP = {"):]
	mapBlock = mapBlock[:strings.Index(mapBlock, "\n};")]
	// 不能按行首匹配：CFG_MAP 里多个键写在同一行（`a: [...], b: [...]`），只有行首
	// 那个带换行缩进。按「前面是行首或分隔符」判定才不漏。
	inMap := func(name string) bool {
		return regexp.MustCompile(`(?:^|[\s,{])` + regexp.QuoteMeta(name) + `:\s*\[`).MatchString(mapBlock)
	}

	// 1) 表单里的每个 name 都要有 CFG_MAP 条目（否则收集/回填都拿不到它）。
	form := html[strings.Index(html, `<form id="cfgForm">`):]
	form = form[:strings.Index(form, "</form>")]
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`name="([a-z_0-9]+)"`).FindAllStringSubmatch(form, -1) {
		names[m[1]] = true
	}
	if len(names) == 0 {
		t.Fatal("未从配置表单解析出任何 name 字段")
	}
	for n := range names {
		if !inMap(n) {
			t.Errorf("表单字段 %q 在 CFG_MAP 里没有条目（保存时会被静默丢弃）", n)
		}
	}

	// 2) CFG_MAP 里的每个键都要在表单里有控件（否则回填/保存是空转）。
	for _, m := range regexp.MustCompile(`(?:^|[\s,{])([a-z_0-9]+):\s*\[`).FindAllStringSubmatch(mapBlock, -1) {
		if !names[m[1]] {
			t.Errorf("CFG_MAP 键 %q 在配置表单里没有对应控件", m[1])
		}
	}

	// 3) 明确断言这一个键：后端有配置项、README 说面板可改，UI 不能少。
	if !strings.Contains(js, "request_client_info: ['logging', 'request_client_info']") {
		t.Error("CFG_MAP 缺 request_client_info 条目")
	}
	if !names["request_client_info"] {
		t.Error("配置表单缺「记录调用来源」开关（logging.request_client_info）")
	}
}

// 同到期时间按面额降序；其余未用完包与零/负余额包分别聚合。
func TestAppJSDetailGroups(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; detail groups test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const PK_DEFAULT_DETAIL_LIMIT');
const end = src.indexOf('function renderPackages');
if (start < 0 || end < 0) throw new Error('detail group functions not found');
const ctx = { Date, Math, Number, String, Map, Array, Object, isFinite };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.pkDetailGroups = pkDetailGroups; this.pkDetailLimit = pkDetailLimit;', ctx);
const input = [
  { id: 'small-late', size: 100, remain: 1, expires_at: 400 },
  { id: 'zero-early-b', size: 200, remain: 0, expires_at: 200 },
  { id: 'small-early', size: 100, remain: 2, expires_at: 200 },
  { id: 'large-unknown', size: 300, remain: 3, end_time: '' },
  { id: 'zero-early-a', size: 200, remain: -1, expires_at: 200 },
  { id: 'small-unknown', size: 100, remain: 1, end_time: '' },
  { id: 'large-early', size: 300, remain: 4, expires_at: 200 },
  { id: 'zero-late', size: 300, remain: 0, expires_at: 300 },
];
const before = input.map(p => p.id).join(',');
const out = ctx.pkDetailGroups(input, 2);
process.stdout.write(JSON.stringify({
  visible: out.visible.map(p => p.id),
  rest: out.rest.map(p => p.id),
  used: out.used.map(p => p.id),
  restSize: out.restSize,
  restRemain: out.restRemain,
  usedSize: out.usedSize,
  defaultLimit: ctx.pkDetailLimit({}),
  configuredLimit: ctx.pkDetailLimit({ panel: { package_detail_limit: 7 } }),
  unchanged: input.map(p => p.id).join(',') === before,
}));`
	f, err := os.CreateTemp(t.TempDir(), "detail-groups-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("detail groups node test failed: %v\n%s", err, out)
	}
	const want = `{"visible":["large-early","small-early"],"rest":["small-late","large-unknown","small-unknown"],"used":["zero-early-b","zero-early-a","zero-late"],"restSize":500,"restRemain":5,"usedSize":700,"defaultLimit":5,"configuredLimit":7,"unchanged":true}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("detail groups=%s want %s", out, want)
	}
}

// 精确剩余天数聚合、账号内按总余额钳制、无到期批次不计入（首页紧迫度分桶只用有效到期批次）。
func TestAppJSExpirySummary(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; expiry summary test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const PK_DEFAULT_DETAIL_LIMIT');
const end = src.indexOf('function renderPackages');
if (start < 0 || end < 0) throw new Error('expiry summary functions not found');
const ctx = { Date, Math, Number, String, Map, Array, Object, isFinite };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.summarizeCreditDays = summarizeCreditDays;', ctx);
const day = 86400000, now = 100000;
const out = ctx.summarizeCreditDays([
  { uid: 'a', remain: 100, packages: [
    { name: 'soon-a', remain: 30, expires_at: now + day },
    { name: 'later', remain: 70, expires_at: now + 7 * day },
  ] },
  { uid: 'b', remain: 55, packages: [
    { name: 'soon-b', remain: 20, expires_at: now + day },
    { name: 'unknown', remain: 5, end_time: '' },
  ] },
  { uid: 'err', error: 'offline' },
], now);
process.stdout.write(JSON.stringify({
  rows: out.rows.map(row => ({ days: row.days, credits: row.credits })),
  accountCount: out.accountCount,
  unavailable: out.unavailable,
}));`
	f, err := os.CreateTemp(t.TempDir(), "expiry-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("expiry summary node test failed: %v\n%s", err, out)
	}
	// 账号配色不再断言：per-account 配色随「积分到期分布」图一起下线，
	// 到期视图现在只有首页的紧迫度分桶条（按 3/7 天档位上色，与账号无关）。
	const want = `{"rows":[{"days":1,"credits":50},{"days":7,"credits":70}],"accountCount":3,"unavailable":1}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("expiry summary=%s want %s", out, want)
	}
}

// TestAppJSCollectConfigClearable 钉住 collectConfig 的空串语义。
//
// 覆盖型字段（user_agent / prompt_file）空串必须照发：漏发会让面板显示"已保存"
// 而 config.json 里的值没变（issue #102 附带发现 2）。
//
// 同时钉住反面：其余文本字段空串仍然不下发。这条同样重要——若哪天为了修上面那个
// 问题改成"所有空串都发"，表单里任何一个没填的框都会变成"请清空"，静默抹掉配置。
func TestAppJSCollectConfigClearable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; collectConfig test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const CFG_MAP');
const end = src.indexOf('/* Go 时长字段即时校验');
if (start < 0 || end < 0 || end < start) throw new Error('collectConfig region not found');
const mk = v => ({ type: 'text', value: v });
const cfgForm = { elements: {
  listen: mk(''),
  api_key: mk('secret'),
  user_agent: mk(''),
  prompt_file: mk(''),
  prompt_text: mk(''),
  prompt_cn_mode: mk('replace'),
  prompt_cn_preset: mk('official-quick'),
  prompt_cn_text: mk('CN 覆盖'),
  prompt_global_mode: mk(''),
  prompt_global_preset: mk(''),
  prompt_global_text: mk(''),
  checkin_hours: mk(''),
}};
const ctx = {
  Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams, Set,
  document: { getElementById: id => (id === 'cfgForm' ? cfgForm : null) },
  $: id => (id === 'cfgForm' ? cfgForm : null),
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.collectConfig = collectConfig;', ctx);
const out = ctx.collectConfig();
const has = (o, k) => Object.prototype.hasOwnProperty.call(o || {}, k);
const prof = (out.prompt || {}).profiles || {};
process.stdout.write(JSON.stringify([
  has(out.upstream, 'user_agent'), (out.upstream || {}).user_agent,
  has(out.prompt, 'file'), (out.prompt || {}).file,
  has(out, 'listen'),
  has(out.schedule, 'checkin_hours'),
  out.api_key,
  // 分域覆盖：cn 三个字段齐发；global 全空 → 整个 cn/global 键都不该下发
  // （否则后端会以为该域有自己的空配置），顶层素材也一并被删除
  !!prof.cn, has(prof.cn || {}, 'mode'), (prof.cn || {}).preset, (prof.cn || {}).text,
  has(prof, 'global'), has(out.prompt || {}, 'preset'), has(out.prompt || {}, 'text')
]));`
	f, err := os.CreateTemp(t.TempDir(), "cfgc-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("collectConfig node test failed: %v\n%s", err, out)
	}
	// [user_agent 已发, 其值, prompt.file 已发, 其值, listen 未发, checkin_hours 未发, api_key,
	//  cn 覆盖齐发, cn.mode, cn.preset, cn.text, global 未发, 顶层 preset 未发,
	//  顶层 text 已发（覆盖型字段：空串照发，见 CLEARABLE_CFG）]
	const want = `[false,null,true,"",false,false,"secret",true,true,"official-quick","CN 覆盖",false,false,true]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("collectConfig=%s want %s", strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSFillFetchedVersions 钉住「一键填入已拉取版本」的语义：
//  1. 填入官方 feed 已拉取的客户端版本（国内 / 海外）；
//  2. **不动** CLI 版本——feed 不下发内置 CLI 号，填旧值会造出错误指纹；
//  3. 占位符来自后端内置基线（前端不硬编码版本，避免升级后漂移）。
func TestAppJSFillFetchedVersions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; version fill test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const CFG_MAP');
const end = src.indexOf('/* Go 时长字段即时校验');
if (start < 0 || end < 0 || end < start) throw new Error('config region not found');
const mk = v => ({ type: 'text', value: v, placeholder: '' });
const cfgForm = { elements: {
  cn_client_version: mk(''), cn_cli_version: mk(''),
  global_client_version: mk(''), global_cli_version: mk(''),
}};
const hint = { textContent: '' };
const $ = id => (id === 'cfgForm' ? cfgForm : (id === 'cfgVersionHint' ? hint : null));
const ctx = { Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams, Set,
  document: { getElementById: $ }, $,
  toast: (m, k) => { globalThis.__toast = [m, k]; } };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.fillFetchedVersions = fillFetchedVersions; this.applyVersionInfo = applyVersionInfo;', ctx);
ctx.applyVersionInfo({ checked: true, latest_cn: '5.8.0', latest_global: '5.7.0',
  builtin_cn_client: '5.7.6', builtin_cn_cli: '2.156.0',
  builtin_global_client: '5.6.2', builtin_global_cli: '2.147.0' }, cfgForm);
cfgForm.elements.cn_cli_version.value = '2.160.0'; // 用户手工设定的 CLI 版本
ctx.fillFetchedVersions();
process.stdout.write(JSON.stringify([
  cfgForm.elements.cn_client_version.value,
  cfgForm.elements.cn_cli_version.value,
  cfgForm.elements.global_client_version.value,
  cfgForm.elements.global_cli_version.value,
  cfgForm.elements.cn_client_version.placeholder,
  cfgForm.elements.global_cli_version.placeholder,
  globalThis.__toast[1],
]));`
	f, err := os.CreateTemp(t.TempDir(), "verfill-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("version fill node test failed: %v\n%s", err, out)
	}
	// [cn 客户端已填, cn CLI 保持不动, global 客户端已填, global CLI 保持不动,
	//  占位符=内置基线, toast 级别=err（客户端版本与内置 CLI 基线不再成对，需提醒）]
	const want = `["5.8.0","2.160.0","5.7.0","",  "5.7.6","2.147.0","err"]`
	got, w := strings.TrimSpace(string(out)), strings.TrimSpace(want)
	norm := func(s string) string { return strings.Join(strings.Fields(s), "") }
	if norm(got) != norm(w) {
		t.Fatalf("fillFetchedVersions=%s want %s", got, w)
	}
}

// TestAppJSRulesCodec 钉住自定义指纹规则的文本编解码：
// 面板用"一行一条"编辑，后端要结构化数组——两边不一致会静默丢规则。
func TestAppJSRulesCodec(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; rules codec test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('/* ---------- 自定义指纹规则');
const end = src.indexOf('function dig(obj, path)');
if (start < 0 || end < 0 || end < start) throw new Error('rules region not found');
const ctx = { String, Array, RegExp, Object };
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.parseRules = parseRules; this.formatRules = formatRules;', ctx);

const text = [
  '# 注释行',
  '',
  '内部代号X => 项目A',
  '  留白应被去掉   =>   B   ',
  '/SecretSauce => sauce',
  '!x-legacy-tag',
  '/!FoldDrop',
  'bareword',
].join('\n');
const parsed = ctx.parseRules(text);

// 往返：文本 → 数组 → 文本 → 数组，必须稳定（幂等）。
const round = ctx.parseRules(ctx.formatRules(parsed));
const out = {
  parsed: parsed,
  stable: JSON.stringify(round) === JSON.stringify(parsed),
  empty: ctx.parseRules(''),
  comments: ctx.parseRules('# a\n# b'),
  removeIgnoresReplace: ctx.parseRules('!gone => kept'),
};
process.stdout.write(JSON.stringify(out));`
	f, err := os.CreateTemp(t.TempDir(), "rules-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("rules codec node test failed: %v\n%s", err, out)
	}
	var got struct {
		Parsed []struct {
			Match   string `json:"match"`
			Replace string `json:"replace"`
			Mode    string `json:"mode"`
			Action  string `json:"action"`
		} `json:"parsed"`
		Stable               bool `json:"stable"`
		Empty                []any
		Comments             []any
		RemoveIgnoresReplace []struct {
			Match   string `json:"match"`
			Replace string `json:"replace"`
			Action  string `json:"action"`
		} `json:"removeIgnoresReplace"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %s: %v\n%s", out, err, out)
	}
	if !got.Stable {
		t.Errorf("文本往返不幂等: %s", out)
	}
	if len(got.Parsed) != 6 {
		t.Fatalf("解析出 %d 条，期望 6: %s", len(got.Parsed), out)
	}
	want := []struct{ match, replace, mode, action string }{
		{"内部代号X", "项目A", "literal", "replace"},
		{"留白应被去掉", "B", "literal", "replace"},
		{"SecretSauce", "sauce", "fold", "replace"},
		{"x-legacy-tag", "", "literal", "remove"},
		{"FoldDrop", "", "fold", "remove"},
		{"bareword", "", "literal", "replace"},
	}
	for i, w := range want {
		g := got.Parsed[i]
		if g.Match != w.match || g.Replace != w.replace || g.Mode != w.mode || g.Action != w.action {
			t.Errorf("第 %d 条 = %+v，期望 %+v", i+1, g, w)
		}
	}
	if len(got.Empty) != 0 || len(got.Comments) != 0 {
		t.Errorf("空文本/纯注释应解析为空数组: %s", out)
	}
	if n := len(got.RemoveIgnoresReplace); n != 1 {
		t.Fatalf("remove 行应解析为 1 条: %s", out)
	}
	if got.RemoveIgnoresReplace[0].Action != "remove" || got.RemoveIgnoresReplace[0].Replace != "" {
		t.Errorf("remove 应忽略 replace: %s", out)
	}
}

// TestAppJSExpirySortedByExpiry 到期提醒按「最近到期」升序，且不分域（issue #125）。
//
// 后端 /panel/api/packages 是按**余额降序**返回的，恰好把 CN 账号都排在前面、
// global 排在末尾，看上去像"按域分组"，实际只是余额顺序。本用例刻意按这个形态构造
// 输入（余额高的到期最晚、global 余额最低），若前端不重排就会保持该顺序而失败。
//
// 日期按「今天 +N 天」生成而不是写死：写死的话过了那天用例就会自己失效。
func TestAppJSExpirySortedByExpiry(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; expiry sort test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function expBatches');
const pkStart = src.indexOf('const PK_DEFAULT_DETAIL_LIMIT');
const pkEnd = src.indexOf('function renderPackages');
if (pkStart < 0 || pkEnd < 0) throw new Error('packages region not found');
const csStart = src.indexOf('/* ==== js/14-chart.js ==== */');
const csEnd = src.indexOf('/* ==== js/20-accounts.js ==== */');
if (csStart < 0 || csEnd < 0) throw new Error('chart shard not found');
const CHART = src.slice(csStart, csEnd);
const end = src.indexOf('async function loadExpiry');
if (start < 0 || end < 0 || end < start) throw new Error('expiry region not found');
const sink = { innerHTML: '', textContent: '', hidden: true };
const ctx = {
  esc: s => String(s == null ? '' : s),
  fmtTok: v => String(v == null ? 0 : v),
  $: () => sink,
  Date, Math, Number, String, Map, Array, Object, isNaN,
  lastPackages: null, lastPackagesAt: 0,
};
vm.createContext(ctx);
vm.runInContext(src.slice(pkStart, pkEnd) + CHART + src.slice(start, end) + '\nthis.renderExpiry = renderExpiry;', ctx);
const iso = n => {
  const d = new Date(); d.setHours(0, 0, 0, 0); d.setDate(d.getDate() + n);
  const p = x => String(x).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
};
const pack = (days, amount) => ({ end_time: iso(days) + ' 00:00:00', remain: amount });
// 模拟后端顺序：余额降序 → CN 高余额在前且到期最晚，global 最少且最快到期。
const d = { accounts: [
  { uid: 'a', nickname: 'cn-late',  realm: 'cn',     packages: [pack(16, 900)] },
  { uid: 'b', nickname: 'cn-mid',   realm: 'cn',     packages: [pack(12, 700)] },
  { uid: 'c', nickname: 'gl-soon',  realm: 'global', packages: [pack(2, 500)] },
  { uid: 'd', nickname: 'no-expiry', realm: 'cn',    packages: [pack(-3, 100)] },
  { uid: 'e', nickname: 'broken',   realm: 'cn',     error: 'offline' },
] };
ctx.renderExpiry(d);
const names = [...sink.innerHTML.matchAll(/<span class="exp-nm">([^<]*)<\/span>/g)].map(m => m[1]);
process.stdout.write(JSON.stringify(names));`
	f, err := os.CreateTemp(t.TempDir(), "expsort-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("expiry sort node test failed: %v\n%s", err, out)
	}
	// 最快到期的排最前（跨域）；无 7 天内到期的与查询失败的排最后。
	const want = `["gl-soon","cn-mid","cn-late","no-expiry","broken"]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("expiry order=%s\nwant %s", strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSChartTooltipInsideBar 图表 tooltip 必须挂在每根柱子内部（issue #128）。
//
// <title> 在 SVG 里描述的是**父元素**。此前它被平铺在 <svg> 根下（<rect> 是自闭合的，
// 无法包含子节点），于是整张图共用一个 tooltip —— 浏览器取第一个 —— 悬停任何柱子都
// 显示同一份数据。这类问题不报错、不影响渲染，只靠肉眼看很容易漏。
//
// 断言结构：<g> 数量 == 数据点数，且每个 <g> 紧跟一个 <title>；同时确认没有
// 游离在 <g> 之外的 <title>。
func TestAppJSChartTooltipInsideBar(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; chart tooltip test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('function parsePointTime');
const chartStart = src.indexOf('function renderUsageChart');
const csStart = src.indexOf('/* ==== js/14-chart.js ==== */');
const csEnd = src.indexOf('/* ==== js/20-accounts.js ==== */');
if (csStart < 0 || csEnd < 0) throw new Error('chart shard not found');
const CHART = src.slice(csStart, csEnd);
if (start < 0 || chartStart < 0) throw new Error('chart functions not found');
// 切片止于本分片最后一个顶层函数之前：用正则匹配「行首 function / async function」，
// 避免 "\nfunction " 这种写法漏掉 async 声明（漏掉会把后面的顶层事件绑定一起求值）。
let end = src.length;
const nextTop = /\n(?:async )?function /g;
nextTop.lastIndex = chartStart + 10;
const mTop = nextTop.exec(src);
if (mTop) end = mTop.index;
const sinks = {};
const mk = id => (sinks[id] = { innerHTML: '', textContent: '' });
const ctx = {
  esc: s => String(s == null ? '' : s),
  fmtTok: v => String(v == null ? 0 : v),
  $: id => (sinks[id] || mk(id)),
  Date, Math, Number, String, Map, Array, Object, isNaN, Infinity, isFinite, Set,
};
vm.createContext(ctx);
vm.runInContext(CHART + src.slice(start, end) + '\nthis.renderUsageChart = renderUsageChart;', ctx);
const series = [];
for (let h = 0; h < 5; h++) {
  const d = new Date(); d.setHours(d.getHours() - (4 - h), 0, 0, 0);
  const iso = d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' +
              String(d.getDate()).padStart(2, '0') + 'T' + String(d.getHours()).padStart(2, '0');
  series.push({ t: iso, scope: 'hour', prompt_tokens: (h + 1) * 100,
                completion_tokens: (h + 1) * 10, total_tokens: (h + 1) * 110, requests: h + 1 });
}
ctx.renderUsageChart(series);
const svg = sinks['usChart'] ? sinks['usChart'].innerHTML : '';
const groups = svg.match(/<g><title>/g) || [];
const titles = svg.match(/<title>[^<]*<\/title>/g) || [];
// 游离的 <title>：前面不是 <g>（即仍平铺在根下）
const loose = (svg.match(/(?:<rect[^>]*\/>|<\/g>)<title>/g) || []).length;
process.stdout.write(JSON.stringify({
  points: series.length, groups: groups.length, titles: titles.length, loose: loose,
}));`
	f, err := os.CreateTemp(t.TempDir(), "charttip-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("chart tooltip node test failed: %v\n%s", err, out)
	}
	const want = `{"points":5,"groups":5,"titles":5,"loose":0}`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("chart tooltip structure=%s\nwant %s（groups 应等于数据点数，loose 应为 0）",
			strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSCollectMediaPolicy 钉住多模态与图片策略表单的收集类型：
//   - media_tool_images 是字符串枚举（select）；
//   - media_image_transcode 是布尔（checkbox）；
//   - media_image_max_dimension 是**数字**（Go 侧 int）。select 的 value 天生是
//     字符串，漏掉 Number 转换会让 config.json 写成 "1080"、Go 侧解码报类型错——
//     这类漂移不会让 go 测试失败，只有人肉点保存才会发现。
//
// 同时把「控件类型 ↔ Go 字段类型」钉在 HTML 上：把布尔开关改成 select（值为
// "true"/"false" 字符串）或把数字下拉改成普通文本，都会让上面的收集断言失去意义，
// 所以直接从下发的页面里断言控件本身的形态。
func TestAppJSCollectMediaPolicy(t *testing.T) {
	html := string(indexHTML)
	form := html[strings.Index(html, `<form id="cfgForm">`):]
	form = form[:strings.Index(form, "</form>")]
	if !regexp.MustCompile(`<input[^>]*type="checkbox"[^>]*name="media_image_transcode"`).MatchString(form) {
		t.Fatal("media_image_transcode 必须是 checkbox（Go 侧 bool；select 会下发字符串）")
	}
	if !regexp.MustCompile(`<select[^>]*name="media_image_max_dimension"`).MatchString(form) {
		t.Fatal("media_image_max_dimension 期望下拉（数值由 collectConfig 转 Number）")
	}
	if !regexp.MustCompile(`<select[^>]*name="media_tool_images"`).MatchString(form) {
		t.Fatal("media_tool_images 期望下拉（字符串枚举）")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; collect config test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const CFG_MAP');
const end = src.indexOf('/* Go 时长字段即时校验');
if (start < 0 || end < 0 || end < start) throw new Error('collectConfig region not found');
const cfgForm = { elements: {
  media_tool_images: { type: 'select-one', value: 'hoist' },
  media_image_transcode: { type: 'checkbox', checked: true },
  media_image_max_dimension: { type: 'select-one', value: '1080' },
}};
const ctx = {
  Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams, Set,
  document: { getElementById: () => cfgForm },
  $: () => cfgForm,
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.collectConfig = collectConfig;', ctx);
const out = ctx.collectConfig();
const media = out.media || {};
process.stdout.write(JSON.stringify([media.tool_images, media.image_transcode,
  typeof media.image_max_dimension, media.image_max_dimension]));`
	f, err := os.CreateTemp(t.TempDir(), "cfgmedia-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("collectConfig node test failed: %v\n%s", err, out)
	}
	const want = `["hoist",true,"number",1080]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("collectConfig media=%s want %s", strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSCollectIngressPolicy 钉住 server.* 入站准入四项在面板上的保存口径：
//
//   - 两个上限是 number（Go 侧 int），必须走 Number()；尤其 **0 不能被当成"没填"**：
//     空 → undefined（不下发），而 "0" → 0（要下发，它是"不限制"的合法取值）。
//     若把 0 与空串混为一谈，用户想关掉限制时保存后其实没改。
//   - ingress_wait / read_timeout 是 Go 时长字符串，照原样下发（裸 "0" 合法）。
func TestAppJSCollectIngressPolicy(t *testing.T) {
	html := string(indexHTML)
	form := html[strings.Index(html, `<form id="cfgForm">`):]
	form = form[:strings.Index(form, "</form>")]
	for _, name := range []string{"max_inflight_requests", "max_inflight_bytes_mb"} {
		// 属性顺序无关：先取出该 input 标签，再在标签内断言 type=number
		// （Go 侧是 int，text 会下发 "64" 导致 json 解码失败）。
		tag := regexp.MustCompile(`<input[^>]*name="` + name + `"[^>]*>`).FindString(form)
		if tag == "" {
			t.Errorf("配置表单缺 %s 控件", name)
			continue
		}
		if !strings.Contains(tag, `type="number"`) {
			t.Errorf("%s 必须是 type=number，实际：%s", name, tag)
		}
	}
	for _, name := range []string{"ingress_wait", "read_timeout"} {
		if !regexp.MustCompile(`<input[^>]*name="` + name + `"`).MatchString(form) {
			t.Errorf("配置表单缺 %s 控件", name)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; collect config test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('const CFG_MAP');
const end = src.indexOf('/* Go 时长字段即时校验');
if (start < 0 || end < 0 || end < start) throw new Error('collectConfig region not found');
const mk = v => ({ type: 'text', value: v });
const num = v => ({ type: 'number', value: v });
const cfgForm = { elements: {
  max_inflight_requests: num('0'),          // 显式 0 = 关掉并发上限
  max_inflight_bytes_mb: num('64'),         // 数字原样
  ingress_wait: mk('0'),                    // 裸 0 = 满载立即拒绝
  read_timeout: mk('600s'),
  max_inflight_unset: num(''),              // 空 = 不下发（对照组）
}};
const ctx = {
  Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams, Set,
  document: { getElementById: () => cfgForm },
  $: () => cfgForm,
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.collectConfig = collectConfig;', ctx);
const out = ctx.collectConfig();
const srv = out.server || {};
process.stdout.write(JSON.stringify([
  typeof srv.max_inflight_requests, srv.max_inflight_requests,
  typeof srv.max_inflight_bytes_mb, srv.max_inflight_bytes_mb,
  srv.ingress_wait, srv.read_timeout,
  Object.prototype.hasOwnProperty.call(srv, 'max_inflight_unset')
]));`
	f, err := os.CreateTemp(t.TempDir(), "cfgingress-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("collectConfig node test failed: %v\n%s", err, out)
	}
	// [0 是 number 且为 0（未被当成"没填"）, 64 是 number, 两个时长原样下发, 空值不下发]
	const want = `["number",0,"number",64,"0","600s",false]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("collectConfig server=%s want %s", strings.TrimSpace(string(out)), want)
	}
}

// TestAppJSDurationZeroAllowed 钉住时长校验器放行裸 "0"。
//
// server.read_timeout / server.ingress_wait 都用 "0" 表示"不限制 / 满载立即拒绝"，
// 而 Go 的 time.ParseDuration("0") 是合法的。若前端正则要求"必须带单位"，用户填 0
// 会被当场标红、提示格式错误，而后端其实收得下——一处纯前端的假告警。
func TestAppJSDurationZeroAllowed(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; duration validator test skipped")
	}
	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const start = src.indexOf('/* Go 时长字段即时校验');
const end = src.indexOf("$('cfgForm').addEventListener('input'");
if (start < 0 || end < 0 || end < start) throw new Error('duration region not found');
const mk = v => ({ type: 'text', value: v, classList: { toggle() {} }, title: '' });
const cfgForm = { elements: {
  ingress_wait: mk('0'), read_timeout: mk('0'),
  soft_rate: mk('0'), ttl: mk('30m'),
}};
const ctx = {
  Date, Number, String, Math, Map, Array, Object, isNaN, URLSearchParams, Set,
  document: { getElementById: () => cfgForm },
  $: () => cfgForm,
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) +
  '\nthis.bad = n => durationBad(n); this.mark = () => markDurationFields();', ctx);
const f = cfgForm.elements;
f.ingress_wait.value = '5s';   const ok5s = ctx.bad('ingress_wait');
f.ingress_wait.value = '5';    const noUnit = ctx.bad('ingress_wait');
f.ingress_wait.value = 'abc';  const junk = ctx.bad('ingress_wait');
f.ingress_wait.value = '0';    ctx.mark();          // 合法：不应标红
const zeroClean = !f.ingress_wait.title && !f.read_timeout.title;
f.ingress_wait.value = '';     const empty = ctx.bad('ingress_wait');
process.stdout.write(JSON.stringify([ok5s, noUnit, junk, zeroClean, empty, ctx.bad('ttl')]));`
	f, err := os.CreateTemp(t.TempDir(), "cfgdur-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command(node, f.Name(), panelJSFile(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("duration validator node test failed: %v\n%s", err, out)
	}
	// [5s 合法, "5" 缺单位被标红, abc 被标红, 裸 0 不标红, 空值放行, ttl=30m 合法]
	const want = `[false,true,true,true,false,false]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("durationBad=%s want %s", strings.TrimSpace(string(out)), want)
	}
}
