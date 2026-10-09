package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// contentPolicyRe 提取 <content_policy> 正文（护栏变体比对用）。
var contentPolicyRe = regexp.MustCompile(`(?s)<content_policy>\n(.*?)\n</content_policy>`)

// systemRoles 提取 messages 中所有 role 值，用于断言改写后的角色序列。
func systemRoles(t *testing.T, body []byte) []string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	msgs, ok := obj["messages"].([]any)
	if !ok {
		t.Fatalf("messages not []any: %v", obj["messages"])
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("msg not map: %v", m)
		}
		r, _ := mm["role"].(string)
		out = append(out, r)
	}
	return out
}

func TestRewriteReplacesSystemAndDeveloper(t *testing.T) {
	in := []byte(`{
		"model":"glm-5.2",
		"messages":[
			{"role":"system","content":"被替换的旧提示词"},
			{"role":"developer","content":"开发者指令"},
			{"role":"user","content":"你好"}
		],
		"metadata":{"conversation_id":"c1"}
	}`)
	out := Rewrite(in, "我是自有提示词")
	roles := systemRoles(t, out)
	wantRoles := []string{"system", "user"}
	if len(roles) != len(wantRoles) {
		t.Fatalf("roles=%v want %v", roles, wantRoles)
	}
	for i, r := range roles {
		if r != wantRoles[i] {
			t.Fatalf("roles[%d]=%q want %q (all=%v)", i, r, wantRoles[i], roles)
		}
	}
	// 头部 system 内容恰为自有提示词，旧 system/developer 内容零残留。
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	first := msgs[0].(map[string]any)
	if first["content"] != "我是自有提示词" {
		t.Errorf("first system content=%v", first["content"])
	}
	if strings.Contains(string(out), "被替换的旧提示词") || strings.Contains(string(out), "开发者指令") {
		t.Errorf("old system/developer content leaked: %s", out)
	}
}

func TestRewriteKeepsUserAssistantToolUntouched(t *testing.T) {
	in := []byte(`{
		"messages":[
			{"role":"user","content":"u-content"},
			{"role":"assistant","content":"a-content"},
			{"role":"tool","tool_call_id":"t1","content":"tool-result"}
		]
	}`)
	out := Rewrite(in, "SYS")
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	// 预期：system(新) + user + assistant + tool，顺序保留。
	if len(msgs) != 4 {
		t.Fatalf("len=%d", len(msgs))
	}
	roles := systemRoles(t, out)
	want := []string{"system", "user", "assistant", "tool"}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles=%v want %v", roles, want)
		}
	}
	// user/assistant/tool 字段逐字不动。
	userMsg := msgs[1].(map[string]any)
	if userMsg["content"] != "u-content" {
		t.Errorf("user content changed: %v", userMsg["content"])
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["tool_call_id"] != "t1" || toolMsg["content"] != "tool-result" {
		t.Errorf("tool changed: %v", toolMsg)
	}
}

func TestRewriteKeepsMetadataAndOtherFields(t *testing.T) {
	in := []byte(`{
		"model":"glm-5.2",
		"stream":true,
		"metadata":{"conversation_id":"c1","user_id":"u9"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	out := Rewrite(in, "SYS")
	var obj map[string]any
	json.Unmarshal(out, &obj)
	if obj["model"] != "glm-5.2" {
		t.Errorf("model changed: %v", obj["model"])
	}
	if obj["stream"] != true {
		t.Errorf("stream changed: %v", obj["stream"])
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok || meta["conversation_id"] != "c1" || meta["user_id"] != "u9" {
		t.Errorf("metadata changed: %v", obj["metadata"])
	}
}

func TestRewriteInjectsSystemWhenAbsent(t *testing.T) {
	in := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out := Rewrite(in, "SYS")
	roles := systemRoles(t, out)
	want := []string{"system", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles=%v want %v", roles, want)
	}
	for i, r := range roles {
		if r != want[i] {
			t.Fatalf("roles=%v want %v", roles, want)
		}
	}
}

func TestRewriteMultimodalContentUntouched(t *testing.T) {
	// user content 为多模态数组（text + image_url），Rewrite 只动 messages 层级，
	// 不应改动 content 内部结构。
	in := []byte(`{
		"messages":[
			{"role":"system","content":"old"},
			{"role":"user","content":[
				{"type":"text","text":"看图"},
				{"type":"image_url","image_url":{"url":"data:..."}}
			]}
		]
	}`)
	out := Rewrite(in, "SYS")
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("len=%d", len(msgs))
	}
	userMsg := msgs[1].(map[string]any)
	arr, ok := userMsg["content"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("multimodal content changed: %v", userMsg["content"])
	}
	textPart := arr[0].(map[string]any)
	if textPart["type"] != "text" || textPart["text"] != "看图" {
		t.Errorf("text part changed: %v", textPart)
	}
}

func TestRewriteInvalidJSONReturnedAsIs(t *testing.T) {
	in := []byte(`{not valid json`)
	out := Rewrite(in, "SYS")
	if string(out) != string(in) {
		t.Errorf("invalid json should return as-is: got %s", out)
	}
}

func TestRewriteEmptyBodyReturnedAsIs(t *testing.T) {
	out := Rewrite([]byte{}, "SYS")
	if len(out) != 0 {
		t.Errorf("empty body should return as-is: got %s", out)
	}
}

func TestRewriteEmptyPromptReturnedAsIs(t *testing.T) {
	in := []byte(`{"messages":[{"role":"system","content":"old"}]}`)
	out := Rewrite(in, "")
	// systemPrompt 空 → 不改写，原样返回。
	if string(out) != string(in) {
		t.Errorf("empty prompt should return as-is: got %s", out)
	}
}

func TestResolveDefaultPreset(t *testing.T) {
	for _, tc := range []struct {
		spec  Spec
		realm string
		want  string
	}{
		{Spec{}, "cn", DefaultText()},
		// default 与 official-* 都是分域预设：global 取 .global 文件，不与 cn 共用。
		{Spec{Preset: PresetDefault}, "global", Builtin(PresetDefault, "global")},
		{Spec{Preset: PresetDefault}, "cn", DefaultText()},
		{Spec{Preset: PresetOfficialCraft}, "cn", Builtin(PresetOfficialCraft, "cn")},
		{Spec{Preset: PresetOfficialCraft}, "global", Builtin(PresetOfficialCraft, "global")},
		{Spec{Preset: PresetOfficialCraft}, "", Builtin(PresetOfficialCraft, "cn")},
		{Spec{Preset: PresetOfficialCraft}, "bogus", Builtin(PresetOfficialCraft, "cn")},
	} {
		got, err := Resolve(tc.spec, tc.realm)
		if err != nil {
			t.Fatalf("Resolve(%+v,%q): %v", tc.spec, tc.realm, err)
		}
		if got != tc.want {
			t.Errorf("Resolve(%+v,%q) = %d bytes want %d", tc.spec, tc.realm, len(got), len(tc.want))
		}
	}
	// 官方预设必须*跨域有差异*：两域不是翻译关系，而是发布分支差异
	// （产品名 / 数据目录 / 区域段 / 语言段），相同就说明导出时把域搞错了。
	cnCraft, glCraft := Builtin(PresetOfficialCraft, "cn"), Builtin(PresetOfficialCraft, "global")
	if cnCraft == glCraft {
		t.Error("official-craft presets must differ per realm")
	}
}

func TestResolveTextBeatsFileAndPreset(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.md")
	os.WriteFile(fp, []byte("from-file"), 0o600)
	got, err := Resolve(Spec{Preset: PresetOfficialCraft, File: fp, Text: "inline"}, "cn")
	if err != nil || got != "inline" {
		t.Fatalf("text must win: %q err=%v", got, err)
	}
	got, err = Resolve(Spec{Preset: PresetOfficialCraft, File: fp}, "cn")
	if err != nil || got != "from-file" {
		t.Fatalf("file must beat preset: %q err=%v", got, err)
	}
	if src := Source(Spec{Preset: PresetOfficialCraft}); src != PresetOfficialCraft {
		t.Errorf("Source=%q want %s", src, PresetOfficialCraft)
	}
}

func TestNormalizeModeStrict(t *testing.T) {
	// 旧取值不兼容：custom/passthrough/别名一律非法（不迁移、不静默降级）。
	for _, v := range []string{"custom", "passthrough", "client_after", "prepend", "bogus"} {
		if _, ok := NormalizeMode(v); ok {
			t.Errorf("NormalizeMode(%q) must be rejected", v)
		}
	}
	for v, want := range map[string]string{"": "inject", "REPLACE": "replace", " append ": "append", "after": "after", "INJECT": "inject", " inject ": "inject", "NONE": "none"} {
		if got, ok := NormalizeMode(v); !ok || got != want {
			t.Errorf("NormalizeMode(%q)=%q,%v want %q", v, got, ok, want)
		}
	}
	for _, v := range []string{"builtin", "small", "bogus"} {
		if _, ok := NormalizePreset(v); ok {
			t.Errorf("NormalizePreset(%q) must be rejected", v)
		}
	}
}

func TestLoadFileOverride(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "my.md")
	want := "这是我的自定义人格入口。"
	os.WriteFile(fp, []byte(want), 0o600)
	got, err := Resolve(Spec{File: fp}, "cn")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Resolve()=%q want %q", got, want)
	}
}

func TestLoadFileMissingFailsFast(t *testing.T) {
	if _, err := Resolve(Spec{File: "/nonexistent/promp.md"}, "cn"); err == nil {
		t.Fatal("missing file should return error (fail fast)")
	}
}

// TestPresetCatalogComplete 注册表里的每个预设都必须能读到 cn/global 正文，
// 且有分域的预设两份正文必须不同（否则"分域"是假的）。
func TestPresetCatalogComplete(t *testing.T) {
	infos := Presets()
	if len(infos) < 4 {
		t.Fatalf("catalog too small: %d", len(infos))
	}
	for _, in := range infos {
		for _, realm := range []string{"cn", "global"} {
			text, err := presetContent(in.Name, realm)
			if err != nil {
				t.Errorf("preset %s/%s: %v", in.Name, realm, err)
				continue
			}
			if len(strings.TrimSpace(text)) < 40 {
				t.Errorf("preset %s/%s too short: %d bytes", in.Name, realm, len(text))
			}
		}
		if len(in.Realms) > 0 {
			cn, _ := presetContent(in.Name, "cn")
			gl, _ := presetContent(in.Name, "global")
			if cn == gl {
				t.Errorf("preset %s declares realms but both files are identical", in.Name)
			}
		}
		if in.Label == "" || in.Description == "" {
			t.Errorf("preset %s missing label/description", in.Name)
		}
	}
	// 预设正文不得含已知上游指纹串（那正是要清理的东西）。
	banned := []string{"You are Claude Code", "x-anthropic-billing-header", "11128", "codex CLI"}
	for _, in := range infos {
		for _, realm := range []string{"cn", "global"} {
			text, _ := presetContent(in.Name, realm)
			for _, b := range banned {
				if strings.Contains(text, b) {
					t.Errorf("preset %s/%s contains banned fingerprint %q", in.Name, realm, b)
				}
			}
		}
	}
}

// TestPresetStaticNoTemplateMarkers 预设正文不得残留模板标记（`{{ }}` / `{% %}` /
// `{# #}`）——预设是官方渲染产物的逐字拷贝（或 `default` 的手写正文），任何
// 未渲染标记都意味着导出环节出错。
//
// 唯一豁免：`{{client_system}}`（prompt.ClientSlot）——它**不是**官方的未渲染变量，
// 而是网关自己的注入锚点（位置由 MD 决定，见 prompt.injectText）。
// 官方预设与自设计预设均可按需包含本锚点。
func TestPresetStaticNoTemplateMarkers(t *testing.T) {
	markers := []string{"{{", "{%", "{#"}
	for _, in := range Presets() {
		for _, realm := range []string{"cn", "global"} {
			text, err := presetContent(in.Name, realm)
			if err != nil {
				t.Errorf("preset %s/%s: %v", in.Name, realm, err)
				continue
			}
			// 先剔除网关自己的注入锚点，再查其余模板标记。
			text = strings.ReplaceAll(text, ClientSlot, "")
			for _, m := range markers {
				if strings.Contains(text, m) {
					t.Errorf("preset %s/%s contains template marker %q", in.Name, realm, m)
				}
			}
		}
	}
}

// TestPresetDefaultCarriesClientSlot `default` 预设自带注入锚点，且每域恰好一个。
// 这是“注入位置由 MD 决定”的契约：标记在→就地注入，标记不在→追加末尾。
func TestPresetDefaultCarriesClientSlot(t *testing.T) {
	for _, realm := range []string{"cn", "global"} {
		text, err := presetContent(PresetDefault, realm)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(text, ClientSlot); n != 1 {
			t.Errorf("default/%s: %s 出现 %d 次，期望恰好 1 次", realm, ClientSlot, n)
		}
		if !strings.HasSuffix(strings.TrimRight(text, "\n"), ClientSlot) {
			t.Errorf("default/%s: 锚点应在正文末尾（当前在别处）", realm)
		}
	}
}

// TestPresetNoMachinePaths 预设不得含具体机器的绝对路径 / 用户名 / 本地 UUID。
// 历史教训：抓包正文里 binary_context 与工作区记忆 Layer 3 全是
// `C:\Users\<用户名>\...`，直接抄下来等于把这台机器的指纹放进每一次请求。
// 注意 `user-facing` / `user-level` 这类普通词不算，所以要匹配具体形状。
func TestPresetNoMachinePaths(t *testing.T) {
	patterns := []struct {
		re   *regexp.Regexp
		what string
	}{
		{regexp.MustCompile(`[A-Za-z]:\\(Users|Program)`), `盘符绝对路径（C:\Users / D:\Program…）`},
		{regexp.MustCompile(`/Users/[A-Za-z]`), `macOS 家目录（/Users/…）`},
		{regexp.MustCompile(`user-[0-9a-fA-F]{8}-`), `本地用户 UUID（user-xxxxxxxx-…）`},
	}
	for _, in := range Presets() {
		for _, realm := range []string{"cn", "global"} {
			text, err := presetContent(in.Name, realm)
			if err != nil {
				continue
			}
			for _, p := range patterns {
				if loc := p.re.FindString(text); loc != "" {
					t.Errorf("preset %s/%s contains %s: %q", in.Name, realm, p.what, loc)
				}
			}
		}
	}
}

// TestPresetBlankLinesNormalized 空行必须已折叠：不允许连续两个以上空行，
// 不允许首部空行，结尾恰好一个换行，行尾不留多余空白。
//
// 为什么守：官方模板里到处是 `{% if %}` 包裹的块，条件不成立时“块没了、换行还在”。
// 导出时若不做归一，未经抓包对照的模式会残留巨大空档（实测 Global
// `official-quick` 出现过 20 个连续空行、`official-ask` 出现过 8 个）。
// 生成侧由 scripts/render-official-presets.py 的 finalize 负责。
func TestPresetBlankLinesNormalized(t *testing.T) {
	multiBlank := regexp.MustCompile(`\n{3,}`)
	for _, in := range Presets() {
		for _, realm := range []string{"cn", "global"} {
			text, err := presetContent(in.Name, realm)
			if err != nil {
				t.Errorf("preset %s/%s: %v", in.Name, realm, err)
				continue
			}
			where := in.Name + "/" + realm
			if strings.HasPrefix(text, "\n") {
				t.Errorf("%s: 不应以空行开头", where)
			}
			if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
				t.Errorf("%s: 结尾应恰好一个换行", where)
			}
			if m := multiBlank.FindString(text); m != "" {
				t.Errorf("%s: 出现 %d 个连续换行（最多允许 2）", where, len(m))
			}
			for i, line := range strings.Split(text, "\n") {
				if line != strings.TrimRight(line, " \t") {
					t.Errorf("%s: L%d 行尾有多余空白", where, i+1)
				}
			}
		}
	}
}

// TestPresetNamesStable 非法名被拒，空名归 default（自设计位）。
func TestPresetNamesStable(t *testing.T) {
	if got, ok := NormalizePreset(""); !ok || got != PresetDefault {
		t.Errorf("empty preset = %q,%v want default", got, ok)
	}
	if _, ok := NormalizePreset("nope"); ok {
		t.Error("unknown preset must be rejected")
	}
	for _, n := range PresetNames() {
		if _, ok := NormalizePreset(n); !ok {
			t.Errorf("PresetNames() returned invalid name %q", n)
		}
	}
}

// ---- inject 槽位合并 ----

// injectBody 构造带开头连续 system/developer 块的请求体。
func injectBody(t *testing.T, prefix, user string) []byte {
	t.Helper()
	return []byte(`{
		"model":"glm-5.2",
		"messages":[` + prefix + `,
			{"role":"user","content":"你好"}
		],
		"metadata":{"conversation_id":"c1"}
	}`)
}

// TestInjectMergesClientSystemIntoSlot inject 主路径（**无锚点**素材）：开头连续
// system/developer 块正文套官方 <user_custom_instructions> 包装**追加到正文末尾**
// （原块删除、单条 system 出站、其余消息与字段逐字不动）。
func TestInjectMergesClientSystemIntoSlot(t *testing.T) {
	in := injectBody(t, `{"role":"system","content":"CLIENT-RULES-1"},{"role":"developer","content":"CLIENT-RULES-2"}`, "你好")
	gw := "GW-HEAD\nGW-TAIL"
	out := Compose(in, gw, ModeInject)
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	msgs := obj["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("len=%d want 2 (merged system + user): %s", len(msgs), out)
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first role=%v", first["role"])
	}
	content, _ := first["content"].(string)
	// 网关正文原文俱在；客户端两块正文进官方包装。
	for _, want := range []string{
		"GW-HEAD", "GW-TAIL",
		"<user_custom_instructions>",
		"The user has provided the following custom instructions. You MUST follow them in all responses unless they conflict with safety rules.",
		"CLIENT-RULES-1", "CLIENT-RULES-2",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("merged system missing %q:\n%s", want, content)
		}
	}
	// 顺序：网关正文在前、包装块在后（tail recency 留给客户端规则）。
	if strings.Index(content, "GW-TAIL") > strings.Index(content, "<user_custom_instructions>") {
		t.Errorf("gateway text must precede wrapper:\n%s", content)
	}
	// 网关正文逐字保留（只去掉尾部换行再加一个）。
	if !strings.HasPrefix(content, gw+"\n") {
		t.Errorf("gateway text must be kept verbatim:\n%s", content)
	}
	user := msgs[1].(map[string]any)
	if user["role"] != "user" || user["content"] != "你好" {
		t.Errorf("user changed: %v", user)
	}
	meta, _ := obj["metadata"].(map[string]any)
	if meta["conversation_id"] != "c1" {
		t.Errorf("metadata changed: %v", obj["metadata"])
	}
	if obj["model"] != "glm-5.2" {
		t.Errorf("model changed: %v", obj["model"])
	}
}

// TestInjectWithOfficialPreset 端到端：官方预设 + inject —— 正文逐字保留 + 包装块。
func TestInjectWithOfficialPreset(t *testing.T) {
	gw := Builtin(PresetOfficialCraft, "cn")
	if !strings.Contains(gw, "<content_policy>") {
		t.Fatal("official-craft must keep content_policy guardrail")
	}
	in := injectBody(t, `{"role":"system","content":"You are Claude Code rules"}`, "hi")
	out := Compose(in, gw, ModeInject)
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("len=%d want 2: %s", len(msgs), out)
	}
	content, _ := msgs[0].(map[string]any)["content"].(string)
	for _, want := range []string{"<content_policy>", "<user_custom_instructions>", "You are Claude Code rules"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q", want)
		}
	}
	// 预设原文必须逐字在内（注入不得改写官方正文）。
	if !strings.HasPrefix(content, strings.TrimRight(gw, "\n")) {
		t.Error("gateway preset text must be a verbatim prefix")
	}
}

// TestInjectSlotPositionDeterminesPlacement 注入位置**由素材决定，不是代码写死**：
//
//	素材含 {{client_system}} → 就地在标记处写入（标记在哪，包装块就在哪）
//	素材不含标记            → 包装块追加到正文末尾
//
// 同一条网关正文，只把标记放到中间，包装块位置就跟着变。
func TestInjectSlotPositionDeterminesPlacement(t *testing.T) {
	in := injectBody(t, `{"role":"system","content":"CLIENT"}`, "hi")
	const wrapper = "<user_custom_instructions>"

	for _, tc := range []struct {
		name          string
		gwdoc         string
		wantBefore    string // 包装块必须出现在这个标记之后
		wantAfter     string // ……且在这个标记之前
		absentBetween []string
	}{
		{
			name:       "标记在中间 → 就地注入",
			gwdoc:      "HEAD-A\n\n{{client_system}}\n\nTAIL-B",
			wantBefore: "HEAD-A",
			wantAfter:  "TAIL-B",
		},
		{
			name:      "标记在开头 → 包装块在开头",
			gwdoc:     "{{client_system}}\n\nBODY",
			wantAfter: "BODY",
		},
		{
			name:       "无标记 → 追加末尾",
			gwdoc:      "HEAD-A\n\nBODY",
			wantBefore: "BODY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := Compose(in, tc.gwdoc, ModeInject)
			var obj map[string]any
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatal(err)
			}
			content, _ := obj["messages"].([]any)[0].(map[string]any)["content"].(string)
			wi := strings.Index(content, wrapper)
			if wi < 0 {
				t.Fatalf("缺少包装块:\n%s", content)
			}
			if tc.wantBefore != "" {
				if bi := strings.Index(content, tc.wantBefore); bi < 0 || bi > wi {
					t.Errorf("%q 应在包装块之前:\n%s", tc.wantBefore, content)
				}
			}
			if tc.wantAfter != "" {
				if ai := strings.Index(content, tc.wantAfter); ai < 0 || ai < wi {
					t.Errorf("%q 应在包装块之后:\n%s", tc.wantAfter, content)
				}
			}
			if strings.Contains(content, ClientSlot) {
				t.Errorf("标记泄漏:\n%s", content)
			}
			if !strings.Contains(content, "CLIENT") {
				t.Errorf("客户端正文丢失:\n%s", content)
			}
		})
	}
}

// TestInjectDefaultPresetUsesItsSlot 端到端：`default` 预设自带锚点 →
// 客户端指令落在 MD 里标记的那个位置（而不是代码写死的末尾），
// 且护栏仍排在客户端指令之前（安全性不因注入位置而下降）。
func TestInjectDefaultPresetUsesItsSlot(t *testing.T) {
	gw := Builtin(PresetDefault, "cn")
	if !strings.Contains(gw, ClientSlot) {
		t.Fatal("default 预设应自带锚点")
	}
	in := injectBody(t, `{"role":"system","content":"CLIENT-RULES"}`, "hi")
	out := Compose(in, gw, ModeInject)
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	content, _ := obj["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(content, ClientSlot) {
		t.Error("锚点标记泄漏到出站正文")
	}
	// 锚点在 default 末尾 → 正文里的顺序应与模板一致。
	if strings.Index(content, "<content_policy>") > strings.Index(content, "<user_custom_instructions>") {
		t.Error("护栏必须在客户端指令之前")
	}
	for _, want := range []string{"<content_policy>", "<user_custom_instructions>", "CLIENT-RULES"} {
		if !strings.Contains(content, want) {
			t.Errorf("缺少 %q", want)
		}
	}
	// 正文前缀必须与预设逐字一致（除锚点被替换）。
	prefix := strings.Split(gw, ClientSlot)[0]
	if !strings.HasPrefix(content, prefix) {
		t.Error("锚点之前的正文必须逐字保留")
	}
}

// TestInjectNoClientTextRemovesSlot 客户端无 system 块：锚点整体移除，
// 不出空包装语（官方该块本就是条件渲染）。
func TestInjectNoClientTextRemovesSlot(t *testing.T) {
	out := Compose([]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"HEAD\n\n{{client_system}}\n\nTAIL\n", ModeInject)
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	content, _ := obj["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(content, ClientSlot) || strings.Contains(content, "user_custom_instructions") {
		t.Errorf("无客户端正文时不应出锚点/包装块:\n%s", content)
	}
	for _, want := range []string{"HEAD", "TAIL"} {
		if !strings.Contains(content, want) {
			t.Errorf("缺少 %q:\n%s", want, content)
		}
	}
}

// TestInjectPartsContent content 为 parts 数组的开头 system 块：各 text 段均并入。
func TestInjectPartsContent(t *testing.T) {
	in := injectBody(t, `{"role":"system","content":[{"type":"text","text":"PART-A"},{"type":"text","text":"PART-B"},{"type":"image_url","image_url":{"url":"data:..."}}]}`, "你好")
	out := Compose(in, "HEAD", ModeInject)
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	content, _ := msgs[0].(map[string]any)["content"].(string)
	for _, want := range []string{"PART-A", "PART-B", "HEAD", "<user_custom_instructions>"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q:\n%s", want, content)
		}
	}
}

// TestInjectNoClientSystem 开头无 system/developer 块：不出空包装语（官方该块本
// 就是条件渲染），正文只做“去掉尾部换行 + 补一个换行”。
func TestInjectNoClientSystem(t *testing.T) {
	in := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out := Compose(in, "HEAD\n\n", ModeInject)
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("len=%d want 2", len(msgs))
	}
	content, _ := msgs[0].(map[string]any)["content"].(string)
	if content != "HEAD\n" {
		t.Errorf("content=%q want %q", content, "HEAD\n")
	}
	if strings.Contains(content, "user_custom_instructions") {
		t.Errorf("empty client must not emit wrapper shell:\n%s", content)
	}
}

// TestInjectNoMessagesField 无 messages 字段：单条 system 置入。
func TestInjectNoMessagesField(t *testing.T) {
	out := Compose([]byte(`{"model":"m"}`), "A\n\n", ModeInject)
	var obj map[string]any
	json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("len=%d want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["role"] != "system" || m["content"] != "A\n" {
		t.Errorf("msg=%v", m)
	}
}

// TestInjectGuards 守卫与 none 一致：空 body / 空 systemPrompt 原样返回。
func TestInjectGuards(t *testing.T) {
	if got := Compose([]byte{}, "A", ModeInject); len(got) != 0 {
		t.Errorf("empty body must return as-is")
	}
	in := []byte(`{"messages":[{"role":"user","content":"u"}]}`)
	if got := Compose(in, "", ModeInject); string(got) != string(in) {
		t.Errorf("empty prompt must return as-is: %s", got)
	}
	// none / 未知 mode 不改写。
	if got := Compose(in, "A", ModeNone); string(got) != string(in) {
		t.Errorf("none must return as-is: %s", got)
	}
	if got := Compose(in, "A", "bogus"); string(got) != string(in) {
		t.Errorf("unknown mode must return as-is: %s", got)
	}
}

// TestContentPolicyVariants 官方 content_policy 变体各自钉死（字符数=rune，
// 与 Presets() 面板口径一致；字节数会因中文多出 2 字节/字）。
//
// 当前全部预设都是 **999 字符短版（sha 40f2b0b4）**：走的是路径 B（插件组合），
// 其 content_policy 写在 welcomemode/*/prompt.tpl 里、不分模式。官方另有 2,116
// 字符长版只存在于路径 A 的 `workbuddy-ask-*.tpl`（未被抓包证实使用）。
func TestContentPolicyVariants(t *testing.T) {
	const (
		wantCraft = "40f2b0b4"
		craftLen  = 999
	)
	sum := func(text string) string {
		m := contentPolicyRe.FindStringSubmatch(text)
		if m == nil {
			return ""
		}
		h := sha256.Sum256([]byte(m[1]))
		return hex.EncodeToString(h[:])[:8]
	}
	for _, realm := range []string{"cn", "global"} {
		for _, name := range PresetNames() {
			text, err := presetContent(name, realm)
			if err != nil {
				t.Fatal(err)
			}
			m := contentPolicyRe.FindStringSubmatch(text)
			if m == nil {
				t.Errorf("%s/%s: content_policy missing", name, realm)
				continue
			}
			if n := utf8.RuneCountInString(m[1]); n != craftLen || sum(text) != wantCraft {
				t.Errorf("%s/%s: content_policy runes=%d sha=%s want runes=%d sha=%s",
					name, realm, n, sum(text), craftLen, wantCraft)
			}
		}
	}
}
