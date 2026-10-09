// prompt.go 系统提示词配置的解析与分域规则构建。
//
// 配置形状（顶层 + 分域覆盖）：
//
//	"prompt": {
//	  "mode":   "after",            // none | replace | append | after | inject
//	  "preset": "official-craft",   // 见 preset.go 注册表（官方渲染产物逐字）
//	  "file":   "",                 // 素材：文件路径
//	  "text":   "",                 // 素材：内联正文（优先级最高）
//	  "profiles": {
//	    "cn":     { "mode": "replace", "text": "…" },
//	    "global": { "preset": "official-quick" }
//	  }
//	}
//
// 两层语义刻意不同（避免"素材半继承"这类难解释的组合）：
//
//	mode —— 逐项回落：realm 未设时用顶层（行为开关通常两域一致）。
//	素材 —— 整体覆盖：realm 只要显式设置了 preset/file/text 中任意一项，
//	        该域素材就完全由自己的三项决定，不再回落顶层。
//
// 为什么素材不逐项回落：若顶层设了 text、realm 只设 preset=official-quick，逐项回落会
// 让 realm 实际拿到顶层的 text（preset 被静默忽略），用户以为切换了却没有——
// 整体覆盖让"我设了素材"这件事本身就有确定含义。
//
// 预设正文是官方渲染产物的**逐字拷贝**，没有任何运行期占位：加载即使用，
// 既不改写也不拼接（见 preset.go 目录说明与 prompt.Rule.EffectiveText）。
package config

import (
	"fmt"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/prompt"
)

// PromptProfile 单个账号域的提示词覆盖项（键 cn / global）。
// 空字段的语义见文件头注释（mode 逐项回落，素材整体覆盖）。
type PromptProfile struct {
	Mode   string `json:"mode"`
	Preset string `json:"preset"`
	File   string `json:"file"`
	Text   string `json:"text"`
}

// hasSource 报告该覆盖项是否显式设置了素材（preset/file/text 任一非空）。
func (p PromptProfile) hasSource() bool {
	return p.Preset != "" || p.File != "" || p.Text != ""
}

// normalizePrompt 归一化 prompt 段并构建各域生效规则：
//   - 归一 mode/preset，非法值报错（fail fast）；
//   - 未知 realm 键告警并剔除（与 upstream.profiles 同口径，不阻断启动）；
//   - 为 cn / global 各解析一份已定文本的规则（预设据此选分域文件）；
//   - 仅当该域 mode 需要正文时才解析素材（none 不读盘，零回归，且与
//     "none 不加载 PromptText" 的既有约定一致）。
func (c *Config) normalizePrompt() error {
	// 1) 顶层归一。
	mode, err := c.normalizePromptMode(c.Prompt.Mode, "prompt.mode")
	if err != nil {
		return err
	}
	c.Prompt.Mode = mode
	if err := c.normalizePromptPreset("prompt.preset", &c.Prompt.Preset); err != nil {
		return err
	}

	// 2) 分域覆盖：先剔除未知 realm（剔除后再建规则，避免用错域的键）。
	for realm := range c.Prompt.Profiles {
		if prompt.NormalizeRealm(realm) != realm {
			c.addWarning("prompt.profiles." + realm + " —— 未知 realm，已忽略（只支持 cn / global）")
			delete(c.Prompt.Profiles, realm)
		}
	}

	rules := make(map[string]prompt.Rule, 3)
	// 默认规则（未知 realm 的兜底）：顶层配置，语言按 cn。
	def, err := c.buildPromptRule("", prompt.Spec{
		Preset: c.Prompt.Preset, File: c.Prompt.File, Text: c.Prompt.Text,
	}, c.Prompt.Mode)
	if err != nil {
		return err
	}
	rules[""] = def
	// 各域规则：顶层 → 该域覆盖。
	for _, realm := range []string{"cn", "global"} {
		spec := prompt.Spec{Preset: c.Prompt.Preset, File: c.Prompt.File, Text: c.Prompt.Text}
		rmode := c.Prompt.Mode
		if over, ok := c.Prompt.Profiles[realm]; ok {
			if over.hasSource() {
				spec = prompt.Spec{Preset: over.Preset, File: over.File, Text: over.Text}
			}
			if strings.TrimSpace(over.Mode) != "" {
				m, err := c.normalizePromptMode(over.Mode, "prompt.profiles."+realm+".mode")
				if err != nil {
					return err
				}
				rmode = m
				// 回写归一值，让面板/日志看到的就是生效值。
				over.Mode = m
				c.Prompt.Profiles[realm] = over
			}
		}
		if err := c.normalizePromptPresetSpec("prompt.profiles."+realm+".preset", &spec); err != nil {
			return err
		}
		r, err := c.buildPromptRule(realm, spec, rmode)
		if err != nil {
			return err
		}
		rules[realm] = r
	}
	c.PromptRules = rules
	// 历史字段：默认域文本（none 模式为空）。
	c.PromptText = def.Text
	return nil
}

// normalizePromptPresetSpec 校验分域素材里的 preset（就地归一）。
func (c *Config) normalizePromptPresetSpec(path string, spec *prompt.Spec) error {
	if strings.TrimSpace(spec.Preset) == "" {
		spec.Preset = ""
		return nil
	}
	return c.normalizePromptPreset(path, &spec.Preset)
}

// normalizePromptPreset 校验并归一 preset 名（就地写回，空值保持空=default 语义）。
func (c *Config) normalizePromptPreset(path string, v *string) error {
	p, ok := prompt.NormalizePreset(*v)
	if !ok {
		return fmt.Errorf("%s: %q 不是合法值（%s）", path, *v, prompt.PresetListHint())
	}
	// 顶层/分域都显式落成规范名，便于面板回显稳定（空 → 空，表示"未设置/用默认"）。
	if strings.TrimSpace(*v) == "" {
		*v = ""
	} else {
		*v = p
	}
	return nil
}

// normalizePromptMode 校验并归一 mode。只认当前取值（none/replace/append/after/inject）；
// 旧取值（custom/passthrough）由 migrate.go 在读文件时一次性改名，因此走到这里
// 仍是非法的取值就是用户手写的错值，直接报错（不静默降级）。
func (c *Config) normalizePromptMode(v, path string) (string, error) {
	m, ok := prompt.NormalizeMode(v)
	if !ok {
		return "", fmt.Errorf("%s: %q 不是合法值（none / replace / append / after / inject）", path, v)
	}
	return m, nil
}

// buildPromptRule 组装某域生效规则。mode 为 none（或空）时不读盘、不解析素材——
// 没有正文需求就没有失败面（与 TestPromptExplicitPassthrough 的既有约定一致）。
//
// 槽位标记（prompt.ClientSlot）按模式处置：
//   - inject：保留标记——组合期就地在标记处写入客户端正文（见 prompt.injectText）；
//     `default` 预设自带标记；`official-*` 与自定义素材没有标记也合法（组合期
//     追加到正文末尾）。
//   - 其余组合模式：标记在规则文本里剔除（prompt.StripSlot）——replace/append/after
//     下标记字面量永不出站。
func (c *Config) buildPromptRule(realm string, spec prompt.Spec, mode string) (prompt.Rule, error) {
	r := prompt.Rule{Mode: mode, Source: prompt.Source(spec)}
	if mode == prompt.ModeNone || mode == "" {
		return r, nil
	}
	where := "prompt"
	if realm != "" {
		where = "prompt.profiles." + realm
	}
	text, err := prompt.Resolve(spec, prompt.NormalizeRealm(realm))
	if err != nil {
		return prompt.Rule{}, fmt.Errorf("%s: %w", where, err)
	}
	if mode != prompt.ModeInject {
		text = prompt.StripSlot(text)
	} else if (spec.Text != "" || spec.File != "") && !strings.Contains(text, prompt.ClientSlot) {
		// 用户自带素材没写槽位：合法（组合期追加到末尾），但提一句，
		// 因为“注入位置”是靠 MD 里的标记决定的（不是代码写死的）。
		c.addWarning(where + "：inject 素材未含槽位标记 " + prompt.ClientSlot +
			"——客户端指令将套 <user_custom_instructions> 包装追加到正文末尾")
	}
	r.Text = text
	return r, nil
}
