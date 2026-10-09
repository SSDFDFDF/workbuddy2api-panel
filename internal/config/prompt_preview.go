package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"workbuddy_manager/internal/prompt"
)

// previewTextLimit 预览返回的正文上限（字节）。超过则截断并置 truncated=true：
// 面板只需让用户确认"发出去的大致是什么"，不必为一份几 MB 的提示词文件
// 把整个 HTTP 响应撑大（面板 JSON 响应无流式，一次全量返回）。
const previewTextLimit = 16 << 10

// PromptPreview 解析草稿 prompt 段并返回各域生效规则（面板"立即预览"用）。
//
// 与 Load 路径完全同一套解析（normalizePrompt → PromptRules），因此预览所见
// 就是保存后生效的内容；与保存的唯一差别是不落盘、不校验配置其余字段。
//
// 输入形状与 config.json 的 prompt 段一致（顶层四项 + profiles），
// 例如 {"prompt":{"mode":"after","preset":"official-quick"}}。
type PromptPreview struct {
	// Default/CN/Global 各域解析结果（键名即域名；default 是未知域兜底）。
	Default PromptPreviewRule `json:"default"`
	CN      PromptPreviewRule `json:"cn"`
	Global  PromptPreviewRule `json:"global"`
	// Presets 可用内置预设目录（名称/标签/说明/各域字符数），前端据此渲染下拉，
	// 不再硬编码预设名单。
	Presets []prompt.PresetInfo `json:"presets"`
	// Modes 可用组合位置（面板下拉的数据源）。
	Modes []string `json:"modes"`
	// Note 一行口径说明（素材优先级 / mode 语义 / 需重启）。
	Note string `json:"note"`
	// Warnings 解析期告警（未知 realm 被忽略等），与配置页同口径。
	Warnings []string `json:"warnings,omitempty"`
}

// PromptPreviewRule 单个域的解析结果。
type PromptPreviewRule struct {
	Mode string `json:"mode"`
	// Source 素材来源：inline / file / <preset>（见 prompt.Source）。
	Source string `json:"source"`
	// Preset 该域实际使用的预设名（Source=inline/file 时仍给出回落的预设，
	// 便于面板显示“清空覆盖后会回到哪份”）。
	Preset string `json:"preset"`
	// Inherited 是否完全继承顶层配置（false = 该域有自己的 mode 或素材覆盖）。
	Inherited bool `json:"inherited"`
	// Chars 正文字符数（rune 计，中文按 1 个字符计），Bytes 字节数。
	Chars int `json:"chars"`
	Bytes int `json:"bytes"`
	// Text 正文（超过 previewTextLimit 截断）。
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	// Error 该域解析失败原因（如文件不可读）；此时 Text 为空，其余域照常返回。
	Error string `json:"error,omitempty"`
}

// PreviewPromptConfig 面板预览入口：只解析 prompt 段，其余字段一律忽略。
func PreviewPromptConfig(raw []byte) (*PromptPreview, error) {
	draft := &Config{}
	if err := json.Unmarshal(raw, draft); err != nil {
		return nil, fmt.Errorf("parse prompt draft: %w", err)
	}
	if err := draft.normalizePrompt(); err != nil {
		return nil, err
	}
	out := &PromptPreview{
		Warnings: draft.Warnings,
		Presets:  prompt.Presets(),
		Modes:    []string{prompt.ModeNone, prompt.ModeReplace, prompt.ModeAppend, prompt.ModeAfter, prompt.ModeInject},
		Note:     promptPreviewNote(),
	}
	out.Default = previewRule(draft, "")
	out.CN = previewRule(draft, "cn")
	out.Global = previewRule(draft, "global")
	return out, nil
}

// previewRule 组装单域预览。刻意直接读 draft.Prompt（而非 PromptRules）——
// mode=none 时 PromptRules 里没有正文（热路径不需要），但预览要让用户
// 看到"切到 replace 会发什么"，所以按素材独立解析一次。
func previewRule(c *Config, realm string) PromptPreviewRule {
	rule := c.PromptRules[realm]
	out := PromptPreviewRule{Mode: rule.Mode, Inherited: true}
	preset := c.Prompt.Preset
	spec := prompt.Spec{Preset: c.Prompt.Preset, File: c.Prompt.File, Text: c.Prompt.Text}
	if realm != "" {
		if over, ok := c.Prompt.Profiles[realm]; ok {
			if over.hasSource() {
				spec = prompt.Spec{Preset: over.Preset, File: over.File, Text: over.Text}
				out.Inherited = false
				// preset 列只在“本域自己设了预设”时改：只设了 text/file 时，
				// 保留顶层预设——那是清空本域覆盖后会回落的那个（素材整体覆盖语义，
				// 见 prompt_config.go 文件头）。
				if strings.TrimSpace(over.Preset) != "" {
					preset = over.Preset
				}
			}
			if strings.TrimSpace(over.Mode) != "" {
				out.Inherited = false
			}
		}
	}
	// 预设列：本域显式设了就用它，否则显示“清空本域覆盖后会回落到哪份”——
	// 后者是顶层预设（素材整体覆盖语义，见 prompt_config.go 文件头）。
	if p, ok := prompt.NormalizePreset(preset); ok {
		preset = p
	}
	out.Preset = preset
	out.Source = prompt.Source(spec)
	// none 模式热路径不加载正文；预览仍解析出来，便于"先看再切"。
	text, err := prompt.Resolve(spec, prompt.NormalizeRealm(realm))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	// 槽位标记永不出站：inject 把它替换为客户端正文（见 prompt.injectText），
	// 其余模式配置期剔除（prompt.StripSlot）——预览与 buildPromptRule 同口径，
	// 所见即出站所得。
	if rule.Mode != prompt.ModeInject {
		text = prompt.StripSlot(text)
	}
	out.Bytes = len(text)
	out.Chars = len([]rune(text))
	if len(text) > previewTextLimit {
		out.Text = text[:previewTextLimit]
		out.Truncated = true
	} else {
		out.Text = text
	}
	return out
}

// promptPreviewNote 给前端的一行口径说明（避免面板重复硬编码文案）。
func promptPreviewNote() string {
	return strings.Join([]string{
		"素材优先级：内联正文 > 文件 > 预设",
		"mode：replace 只留网关提示词；after 网关在前、客户端在后；append 反之；inject 把客户端 system 套官方 <user_custom_instructions> 包装后追加到正文末尾",
		"预设是官方渲染产物逐字（无模板标记、无运行期变量），加载即使用",
		"保存后即时生效（PromptHold 快照整体替换，无需重启）",
	}, "；")
}
