// Package prompt 提供网关自有系统提示词：内置预设库 + 文件/内联覆盖 + 多种组合位置。
//
// 背景：客户端（Claude Code/Codex 等 CLI）在 system prompt 注入固定模板句，
// 上游内容审核按逐字精确匹配误杀合法流量（issue #36/PR39 的 11128）。
// 方案：网关在出站前对 system/developer 消息做一次组合（替换 / 前置追加 / 后置组合），
// 从源头消灭 system 来源的指纹误报。
//
// 素材优先级（见 Resolve）：内联 text > file > preset（按 realm 取正文）。
// 预设库与分域文件命名见 preset.go（presets/<name>[.<realm>].md）。
package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
)

// 组合位置（config prompt.mode / prompt.profiles.<realm>.mode 取值）。
const (
	// ModeNone 不改写请求体（透传客户端原始 system）。
	ModeNone = "none"
	// ModeReplace 删除全部 system/developer，只留网关提示词（指纹面最小）。
	ModeReplace = "replace"
	// ModeAppend 客户端开头连续 system/developer 块之后插网关提示词（客户端在前）。
	ModeAppend = "append"
	// ModeAfter 网关提示词置首，客户端开头连续 system/developer 块紧随其后（后组合）。
	// 与 ModeAppend 互为镜像：两者都逐字保留客户端内容，只差先后顺序。
	ModeAfter = "after"
)

// 内置预设名（config prompt.preset / prompt.profiles.<realm>.preset 取值）。
// 完整清单与说明见 preset.go 的 presetCatalog（Presets() 对面板输出）。
const (
	// PresetDefault 官方默认：抓包首屏前缀（presets/default.<realm>.md，官方逐字）。
	PresetDefault = "default"
	// PresetMinimal 官方最小：仅官方 content_policy 护栏 + 语言段（presets/minimal.<realm>.md）。
	PresetMinimal = "minimal"
	// PresetOfficial 官方骨架：实机抓包 craft 模式 26 模块的全量形态（presets/official.<realm>.md）。
	PresetOfficial = "official"
	// PresetOfficialCompact 官方骨架缩略版（presets/official-compact.<realm>.md）。
	PresetOfficialCompact = "official-compact"
	// PresetOfficialQuick 官方 Quick 模式：无工具纯问答（presets/official-quick.<realm>.md）。
	PresetOfficialQuick = "official-quick"
	// PresetOfficialAsk 官方 Ask 模式：只读分析与问答（presets/official-ask.<realm>.md）。
	PresetOfficialAsk = "official-ask"
	// PresetOfficialPlan 官方 Plan 模式：计划先行、逐步验证（presets/official-plan.<realm>.md）。
	PresetOfficialPlan = "official-plan"
)

// Spec 一段提示词的来源声明（未解析）。三个字段按优先级取用：Text > File > Preset。
// 零值合法：全部为空时 Resolve 回落 PresetDefault。
type Spec struct {
	Preset string // "" | default | minimal
	File   string // 文件路径（非空且不可读 → Resolve 报错，调用方 fail fast）
	Text   string // 内联正文（非空优先，不再读盘）
}

// Rule 某 realm 生效的提示词规则（已解析文本）。
type Rule struct {
	Mode string `json:"mode"`
	Text string `json:"text"`
}

// NormalizeMode 归一化组合位置：小写去空白。只认 none/replace/append/after；
// 空串视为 none（零值即不缺省改写）。旧取值（custom/passthrough/...）刻意不兼容：
// 它们会直接报错，由用户显式改成新名——避免“配了 custom 却没有生效”这类静默降级。
func NormalizeMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", ModeNone:
		return ModeNone, true
	case ModeReplace:
		return ModeReplace, true
	case ModeAppend:
		return ModeAppend, true
	case ModeAfter:
		return ModeAfter, true
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
// 这类断言使用，避免测试硬编码正文长度。default 已分域，global 侧请用 Builtin(PresetDefault, "global")。
func DefaultText() string { return Builtin(PresetDefault, "cn") }

// NormalizeRealm 归一化 realm 键：只认 cn / global，其余（含空）归 cn。
func NormalizeRealm(realm string) string {
	if strings.EqualFold(strings.TrimSpace(realm), "global") {
		return "global"
	}
	return "cn"
}

// Resolve 解析提示词正文。优先级：Text > File > 内置预设（默认 defaultpreset）。
// realm 决定 minimal 预设的语言与自定义文件的默认语义（不参与路径拼接）。
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

// composeObject 三条组合规则的共用实现（就地修改 obj）。
//
//   - ModeReplace：删全部 system/developer，网关提示词置于首位；
//   - ModeAppend ：[客户端开头 system 块] [网关] [其余]；
//   - ModeAfter  ：[网关] [客户端开头 system 块] [其余]。
//
// "开头连续块"只认 messages 头部连续的 system/developer，遇第一条其它角色即停；
// 中途的 system 消息位置不变、不重排（避免扰动 tool 结果与文本的相邻性）。
func composeObject(obj map[string]any, systemPrompt, mode string) bool {
	if obj == nil || systemPrompt == "" {
		return false
	}
	switch mode {
	case ModeReplace, ModeAppend, ModeAfter:
	default:
		return false // none / 未知：不改写
	}
	msgs, ok := obj["messages"].([]any)
	if !ok {
		// 无 messages 字段或类型不符 → 插入单条 system 后原样保留其余字段。
		obj["messages"] = []any{systemMessage(systemPrompt)}
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
	}
	return true
}

func systemMessage(text string) map[string]any {
	return map[string]any{"role": "system", "content": text}
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
