// Package prompt 提供网关自有系统提示词：内置预设库 + 文件/内联覆盖 + 多种组合位置。
//
// 背景：客户端（Claude Code/Codex 等 CLI）在 system prompt 注入固定模板句，
// 上游内容审核按逐字精确匹配误杀合法流量（issue #36/PR39 的 11128）。
// 方案：网关在出站前对 system/developer 消息做一次组合（替换 / 前置追加 / 后置组合 /
// 槽位合并），从源头消灭 system 来源的指纹误报。
//
// 素材优先级（见 Resolve）：内联 text > file > preset（按 realm 取正文）。
// 预设库与分域文件命名见 preset.go（presets/<name>[.<realm>].md）。
package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"workbuddy_manager/internal/jsondoc"
)

// 组合位置（config prompt.mode / prompt.profiles.<realm>.mode 取值）。
const (
	// ModeNone **显式**透传：不改写请求体（原样保留客户端 system/developer）。
	// 注意它不再是缺省值——空串现在归 ModeInject（见 NormalizeMode）。
	ModeNone = "none"
	// ModeReplace 删除全部 system/developer，只留网关提示词（指纹面最小）。
	ModeReplace = "replace"
	// ModeAppend 客户端开头连续 system/developer 块之后插网关提示词（客户端在前）。
	ModeAppend = "append"
	// ModeAfter 网关提示词置首，客户端开头连续 system/developer 块紧随其后（后组合）。
	// 与 ModeAppend 互为镜像：两者都逐字保留客户端内容，只差先后顺序。
	ModeAfter = "after"
	// ModeInject 包装合并：客户端开头连续 system/developer 块的正文套官方
	// <user_custom_instructions> 包装（clientInstructionsShell），追加到网关正文末尾，
	// 原开头块移除，单条 system 出站。客户端规则由此进入官方"用户自定义指令"位：
	// 模型按官方语义 MUST follow，唯一压得住它的是 <content_policy> 护栏
	// （官方包装语 "unless they conflict with safety rules"）。
	ModeInject = "inject"
)

// 内置预设名（config prompt.preset / prompt.profiles.<realm>.preset 取值）。
// 全部为**官方渲染产物逐字**：条件已按抓包实证解掉、变量已删除，无模板标记。
// 完整清单与说明见 preset.go 的 presetCatalog（Presets() 对面板输出）。
const (
	// PresetDefault 自设计位（**待设计**）：当前内容是抓包首屏前缀（身份 + 能力 + 护栏 +
	// 区域/语言段），仅作占位；正文由维护者自行重写，不要求与官方逐字。
	// 它是空 preset 的回落值，因此改动它等于改动"未配置时发什么"。
	PresetDefault = "default"
	// PresetOfficialCraft 官方 craft（默认）：5.6.2/5.7.6 实物抓包逐字。
	PresetOfficialCraft = "official-craft"
	// PresetOfficialAsk 官方 Ask 模式：只读分析与问答。
	PresetOfficialAsk = "official-ask"
	// PresetOfficialPlan 官方 Plan 模式：计划先行、逐步验证。
	PresetOfficialPlan = "official-plan"
	// PresetOfficialQuick 官方 Quick 模式：无工具纯问答（官方本体仅 2.3KB）。
	PresetOfficialQuick = "official-quick"
	// PresetOfficialExpert 官方 Expert 模式：专家人格由客户端 PluginAgentPrompt 槽注入。
	PresetOfficialExpert = "official-expert"
)

// ClientSlot 客户端 system 的注入锚点。
//
// 预设正文里出现这个标记时，inject 把客户端开头 system/developer 块的正文套官方
// <user_custom_instructions> 包装、**就地在标记处**写入；标记不存在则追加到正文末尾。
// 非 inject 模式一律剔除标记（StripSlot）——标记字面量永不出站。
//
// 内置 `default` 预设与 `official-*` 预设均可按需携带本标记（显式指定注入位置）；
// 未含标记时则默认追加到正文末尾。
const ClientSlot = "{{client_system}}"

// clientInstructionsShell 客户端正文的官方包装，逐字取自官方客户端
// user-context-identity.tpl 的 <user_custom_instructions> 块（cn/global 两域一致）。
// "MUST follow ... unless they conflict with safety rules" 是官方对用户自定义指令
// 的优先级声明：唯一压得住它的是 <content_policy> 护栏，其余官方样板皆居其下。
//
// 仅供 ModeInject 使用（见 ClientSlot / injectText）。
const clientInstructionsShell = `<user_custom_instructions>
The user has provided the following custom instructions. You MUST follow them in all responses unless they conflict with safety rules.

%s
</user_custom_instructions>`

// Spec 一段提示词的来源声明（未解析）。三个字段按优先级取用：Text > File > Preset。
// 零值合法：全部为空时 Resolve 回落 PresetDefault。
type Spec struct {
	Preset string // "" | 内置预设名（见 presetCatalog）
	File   string // 文件路径（非空且不可读 → Resolve 报错，调用方 fail fast）
	Text   string // 内联正文（非空优先，不再读盘）
}

// Rule 某 realm 生效的提示词规则（已解析文本）。
type Rule struct {
	Mode string `json:"mode"`
	Text string `json:"text"`
	// Source 素材来源标签（预设名 / "inline" / "file"），仅用于观测与面板显示，
	// 不参与组合。由配置层在构建规则时给出（见 config.buildPromptRule）。
	Source string `json:"source,omitempty"`
}

// EffectiveText 返回本次出站实际使用的正文。
//
// 预设是官方渲染产物的逐字拷贝，正文里**没有任何运行期变量**（首行模型名在
// official-craft 里就是官方自己在那次会话里的取值），因此这里只做一件事：
// 原样返回。保留此方法是为了让调用点语义清晰、便于日后需要时再引入加工。
func (r Rule) EffectiveText() string { return r.Text }

// NormalizeMode 归一化组合位置：小写去空白。只认 none/replace/append/after/inject。
//
// **空串 = inject**（缺省行为）：网关默认把客户端 system 套官方
// <user_custom_instructions> 包装并入网关正文（见 ModeInject），因为“不动客户端
// 指令”会让第三方 CLI 的指纹原样上流——那正是 11128 的成因。要回到逐字透传，
// 显式配 prompt.mode="none"。
//
// 旧取值（custom/passthrough/...）刻意不兼容：它们会直接报错，由用户显式改成新名——
// 避免“配了 custom 却没有生效”这类静默降级。
func NormalizeMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return ModeInject, true
	case ModeNone, ModeReplace, ModeAppend, ModeAfter, ModeInject:
		return strings.ToLower(strings.TrimSpace(v)), true
	default:
		return "", false
	}
}

// NormalizePreset 归一化预设名：空合法（= default），只认注册表里的名字。
func NormalizePreset(v string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return PresetDefault, true
	}
	for _, name := range presetNames() {
		if name == s {
			return s, true
		}
	}
	return "", false
}

// Builtin 返回内置预设正文（按 realm 选分域文件）。
// 预设名非法或文件缺失 → 返回空串（调用方应先用 NormalizePreset 校验）。
func Builtin(preset, realm string) string {
	name, ok := NormalizePreset(preset)
	if !ok {
		return ""
	}
	s, err := presetContent(name, realm)
	if err != nil {
		return ""
	}
	return s
}

// DefaultText 返回默认预设的 CN 正文——供测试与“未配置时用哪份”
// 这类断言使用，避免测试硬编码正文长度。
func DefaultText() string { return Builtin(PresetDefault, "cn") }

// NormalizeRealm 归一化 realm 键：只认 cn / global，其余（含空）归 cn。
func NormalizeRealm(realm string) string {
	if strings.EqualFold(strings.TrimSpace(realm), "global") {
		return "global"
	}
	return "cn"
}

// Resolve 解析提示词正文。优先级：Text > File > 内置预设（默认 defaultpreset）。
// realm 决定分域预设取哪份正文（cn / global），不参与自定义文件的路径拼接。
// File 非空但不可读 → 报错（调用方 fail fast，不静默回落）。
func Resolve(s Spec, realm string) (string, error) {
	if s.Text != "" {
		return s.Text, nil
	}
	if s.File != "" {
		raw, err := os.ReadFile(s.File)
		if err != nil {
			return "", fmt.Errorf("prompt file %s: %w", s.File, err)
		}
		return string(raw), nil
	}
	preset, ok := NormalizePreset(s.Preset)
	if !ok {
		return "", fmt.Errorf("prompt preset %q 不是合法值（%s）", s.Preset, PresetListHint())
	}
	return Builtin(preset, realm), nil
}

// Source 返回一位提示词素材的"来源标签"（面板预览用，不改语义）。
func Source(s Spec) string {
	switch {
	case s.Text != "":
		return "inline"
	case s.File != "":
		return "file"
	default:
		preset, _ := NormalizePreset(s.Preset)
		return preset
	}
}

// Compose 按 mode 组合请求体与网关提示词。
//   - ModeReplace → Rewrite（删全部 system/developer，只留网关提示词）；
//   - ModeAppend  → Append（客户端开头块在前，网关提示词其后）；
//   - ModeAfter   → ComposeAfter（网关提示词在前，客户端开头块其后）；
//   - ModeInject  → 客户端开头块正文并入网关正文槽位（素材无槽位则追加末尾），单条 system 出站；
//   - 其它（含 none）或 systemPrompt 空 → 原样返回。
//
// 所有分支均为"绝不失败"：坏 JSON / 空 body → 原样返回。
func Compose(body []byte, systemPrompt, mode string) []byte {
	return composeBytes(body, systemPrompt, mode)
}

// ComposeObject 就地把网关提示词按 mode 组合进 obj（调用方拥有的可变副本），
// 返回是否有改动。这是三条组合规则的唯一实现——Compose/Rewrite/Append/
// ComposeAfter 与出站对象链路都收敛到这里，避免"字节版与对象版各写一份"再次分叉。
//
// mode 语义见 Compose 注释；none/未知取值不动文档。
func ComposeObject(obj map[string]any, systemPrompt, mode string) bool {
	return composeObject(obj, systemPrompt, mode)
}

// composeBytes 字节入口：解析 → 对象级组合 → 序列化。
// 解析失败或无需改动时原样返回（绝不失败：出站改写不得阻塞转发）。
func composeBytes(body []byte, systemPrompt, mode string) []byte {
	if len(body) == 0 || systemPrompt == "" {
		return body
	}
	obj, err := jsondoc.Object(body)
	if err != nil {
		return body
	}
	if !composeObject(obj, systemPrompt, mode) {
		return body
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// composeObject 三条组合规则 + 槽位合并的共用实现（就地修改 obj）。
//
//   - ModeReplace：删全部 system/developer，网关提示词置于首位；
//   - ModeAppend ：[客户端开头 system 块] [网关] [其余]；
//   - ModeAfter  ：[网关] [客户端开头 system 块] [其余]；
//   - ModeInject ：客户端开头块正文并入网关正文槽位（见 injectText）；素材无槽位时
//     包装块追加正文末尾。出站形态为单条 system + [其余]。
//
// "开头连续块"只认 messages 头部连续的 system/developer，遇第一条其它角色即停；
// 中途的 system 消息位置不变、不重排（避免扰动 tool 结果与文本的相邻性）。
func composeObject(obj map[string]any, systemPrompt, mode string) bool {
	if obj == nil || systemPrompt == "" {
		return false
	}
	switch mode {
	case ModeReplace, ModeAppend, ModeAfter, ModeInject:
	default:
		return false // none / 未知：不改写
	}
	msgs, ok := obj["messages"].([]any)
	if !ok {
		// 无 messages 字段或类型不符 → 插入单条 system 后原样保留其余字段。
		text := systemPrompt
		if mode == ModeInject {
			// 无客户端块可并入：槽位整体移除（官方该块本就是条件渲染，
			// 无用户指令时不出块，不发出空包装语）。
			text = injectText(systemPrompt, "")
		}
		obj["messages"] = []any{systemMessage(text)}
		return true
	}
	// 开头连续 system/developer 块长度（ModeReplace 不需要，但计算成本可忽略）。
	prefix := 0
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			break
		}
		role, _ := mm["role"].(string)
		if role != "system" && role != "developer" {
			break
		}
		prefix++
	}
	gw := systemMessage(systemPrompt)
	switch mode {
	case ModeReplace:
		kept := make([]any, 0, len(msgs)+1)
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				if role, _ := mm["role"].(string); role == "system" || role == "developer" {
					continue
				}
			}
			kept = append(kept, m)
		}
		obj["messages"] = append([]any{gw}, kept...)
	case ModeAppend:
		out := make([]any, 0, len(msgs)+1)
		out = append(out, msgs[:prefix]...)
		out = append(out, gw)
		out = append(out, msgs[prefix:]...)
		obj["messages"] = out
	case ModeAfter:
		out := make([]any, 0, len(msgs)+1)
		out = append(out, gw)
		out = append(out, msgs[:prefix]...)
		out = append(out, msgs[prefix:]...)
		obj["messages"] = out
	case ModeInject:
		// 客户端开头块正文并入槽位后成为首条 system；原开头块移除
		// （内容已在包装内逐字保留，与 replace 的差别即"不丢客户端规则"）。
		client := clientSystemText(msgs, prefix)
		obj["messages"] = append([]any{systemMessage(injectText(systemPrompt, client))}, msgs[prefix:]...)
	}
	return true
}

func systemMessage(text string) map[string]any {
	return map[string]any{"role": "system", "content": text}
}

// clientSystemText 提取 messages 前 n 条（开头连续 system/developer 块）的正文。
// content 兼容 string 与 parts 数组两种形态（parts 取各段 text，跳过非文本段）；
// 多块/多段之间以空行连接。任何形态解析不出正文都不报错（组合绝不失败）。
func clientSystemText(msgs []any, n int) string {
	var b strings.Builder
	for i := 0; i < n && i < len(msgs); i++ {
		mm, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			if c != "" {
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(c)
			}
		case []any:
			for _, p := range c {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := pm["text"].(string); t != "" {
					if b.Len() > 0 {
						b.WriteString("\n\n")
					}
					b.WriteString(t)
				}
			}
		}
	}
	return b.String()
}

// StripSlot 剔除正文中的槽位标记（非 inject 模式用）：标记行连同前导空行一起
// 删，不残留悬挂空行；标记不存在 → 原样返回。配置期（buildPromptRule）与
// 预览期（previewRule）对 replace/append/after 均已剔除，因此 `default`
// 预设里的标记不会在那些模式下泄漏到出站正文。
func StripSlot(text string) string {
	if !strings.Contains(text, ClientSlot) {
		return text
	}
	text = strings.Replace(text, "\n\n"+ClientSlot, "", 1)
	text = strings.Replace(text, "\n"+ClientSlot, "", 1)
	return strings.Replace(text, ClientSlot, "", 1)
}

// injectText 把客户端正文并入网关正文：
//   - 素材自带槽位（`default` 预设）→ **就地在槽位处**写入包装块；客户端无正文时
//     槽位整体移除（官方该块本就是条件渲染，不发出空包装语）；
//   - 素材无槽位（`official-*` / 自定义素材）→ 包装块追加到正文末尾。
//
// 两种落点都保持“已有的首行不动、结尾恰好一个换行”的约定。
func injectText(gatewayText, clientText string) string {
	if strings.Contains(gatewayText, ClientSlot) {
		if clientText == "" {
			return strings.TrimRight(StripSlot(gatewayText), "\n") + "\n"
		}
		return strings.Replace(gatewayText, ClientSlot, fmt.Sprintf(clientInstructionsShell, clientText), 1)
	}
	text := strings.TrimRight(gatewayText, "\n")
	if clientText == "" {
		return text + "\n"
	}
	return text + "\n\n" + fmt.Sprintf(clientInstructionsShell, clientText) + "\n"
}

// Rewrite 解析 OpenAI 请求体并替换系统提示词：
//   - 删除 messages 中所有 role 为 system/developer 的消息；
//   - 在 messages 头部插入一条 {"role":"system","content":systemPrompt}；
//   - 其余字段与 user/assistant/tool 消息逐字不动。
//
// 解析失败 → 原样返回（绝不失败）：Rewrite 是出站改写的关键路径，
// 任何解析错误都不应阻塞请求转发，让上游按其原始语义处理。
func Rewrite(body []byte, systemPrompt string) []byte {
	return composeBytes(body, systemPrompt, ModeReplace)
}

// ComposeAfter 在 messages 头部插入网关 system，而客户端"开头连续
// system/developer 块"逐字保留并紧随其后（后组合：客户端提示词在网关提示词之后）。
//
// 与 Append 互为镜像（两者不变量相同：既有消息逐字不动、只动插入位置）：
//
//	Append      : [客户端 system 块] [网关 system] [其余]
//	ComposeAfter: [网关 system] [客户端 system 块] [其余]
//
// 差别在于谁先被模型读到：后组合让网关提示词占据首位，同时客户端
// 工具约定/项目规范仍留在 system 位置生效（不被丢弃）。
//
// 边界判定与 Append 完全一致（只认开头连续块，遇第一条非 system/developer
// 即停；中途 system 消息位置不变、不重排——避免扰动 tool 结果的相邻性）。
// 守卫与 Rewrite/Append 逐条一致：空 body / 空 systemPrompt / 坏 JSON → 原样返回。
func ComposeAfter(body []byte, systemPrompt string) []byte {
	return composeBytes(body, systemPrompt, ModeAfter)
}

// Append 解析 OpenAI 请求体并在"开头连续 system/developer 块"之后插入一条
// 网关自有 system 提示词（issue #129 append 模式）：
//   - 开头连续块 = 从 messages[0] 起向后 role 为 system/developer（精确字符串
//     匹配，与 Rewrite 删除口径一致）的消息；遇第一条非 system/developer
//     消息（含非 map 消息、无 role 消息）即停；
//   - 插入点 = 连续块末尾之后（块长 0 时即 messages 最前）；
//   - 所有既有消息（含开头块、中途 system、user/assistant/tool）逐字不动
//     ——客户端项目规范/工具约定与网关提示词并用。
//
// 守卫与 Rewrite 逐条一致：空 body / 空 systemPrompt / 坏 JSON → 原样返回
// （绝不失败）；无 messages 字段 → messages=[网关 system]，其余字段原样。
//
// 边界判定须显式同时匹配 system 与 developer：归一（developer→system）在
// 下游 prepareBody 的 normalizeRoles，Append 执行时开头块里的 developer
// 还是 developer。网关消息角色用 system 而非 developer——上游 role 白名单
// 不含 developer，插 developer 等于制造一次必然归一与多余的 11128 风险窗口。
func Append(body []byte, systemPrompt string) []byte {
	return composeBytes(body, systemPrompt, ModeAppend)
}
