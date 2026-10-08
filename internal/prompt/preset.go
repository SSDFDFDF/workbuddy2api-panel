// preset.go 内置提示词预设库：注册表 + 文件加载。
//
// 目录约定（`presets/`）：
//
//	<name>.md           分域共用（当前仅保留给未来的中性预设）
//	<name>.<realm>.md   分域文件（cn / global），缺失的 realm 回落共用文件
//
// 当前库内所有预设都是分域文件（cn + global）：CN 与 Global 不是翻译关系，
// 而是发布分支差异（产品名 / 数据目录 / 区域段 / 语言段），因此必须两份。
//
// 新增预设只需放文件 + 在 catalog 里登记一行，面板会自动列出（列表由
// Presets() 生成，前端不硬编码）。
//
// 素材来源：官方 Windows 安装包里的明文 Nunjucks 模板（`resources/templates/*.tpl`、
// `resources/plugins/workbuddy-builtin/{welcomemode,interactionmode,prompt-common}/**`）。
// 原件归档、变量表、两阶段装配管线与重新导出方法见 `docs/official-templates/README.md`
// （注意 `docs/` 被 .gitignore 忽略，属于本地参考资料）。本目录的预设是**从抓包渲染产物
// 蒸馏的网关闭环正文**，不是模板原件的复制。
package prompt

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed presets/*.md
var presetFS embed.FS

// PresetInfo 一个内置预设的元信息（面板下拉/预览用）。
type PresetInfo struct {
	Name string `json:"name"` // 配置取值
	// Label 面板显示名（简短中文，避免前端再写一份映射）。
	Label string `json:"label"`
	// Description 一句话说明适用场景。
	Description string `json:"description"`
	// Realms 有独立分域文件的 realm；空表示各域共用同一份正文。
	Realms []string `json:"realms,omitempty"`
	// CharsCN / CharsGlobal 两份正文的字符数（无分域时两值相同）。
	CharsCN     int `json:"chars_cn"`
	CharsGlobal int `json:"chars_global"`
}

// presetCatalog 预设注册表（顺序即面板展示顺序）。
var presetCatalog = []struct {
	name, label, desc string
	realms            []string
}{
	{
		name:   PresetDefault,
		label:  "官方默认",
		desc:   "全文官方英文原文（CN 5.8K / Global 5.5K 字符）：抓包首屏前缀——身份 + 能力介绍 + 完整官方 content_policy + personal_files_safety + 区域与语言段",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficial,
		label:  "官方骨架",
		desc:   "全文官方英文原文（CN 9.4K / Global 9.0K 字符）：实机抓包 26 模块形态，仅换产品名/数据目录/区域/语言等域差异",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialCompact,
		label:  "官方精简",
		desc:   "全文官方英文原文（CN/Global 1.6K 字符）：完整官方 content_policy + 身份 + 工具纪律，省略其余模块",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialQuick,
		label:  "官方快速",
		desc:   "全文官方英文原文（CN 2.4K / Global 2.6K 字符）：无工具纯问答，对应官方 Quick 模式模板",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialAsk,
		label:  "官方只读",
		desc:   "全文官方英文原文（CN 5.8K / Global 5.5K 字符）：只读分析与问答，不落盘、不执行；对应官方 Ask 模式片段",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialPlan,
		label:  "官方计划",
		desc:   "全文官方英文原文（CN 7.2K / Global 6.9K 字符）：计划先行、逐步验证；对应官方 Plan 模式片段",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetMinimal,
		label:  "官方最小",
		desc:   "全文官方英文原文（CN 1.2K / Global 1.4K 字符）：只留官方 content_policy 护栏 + 语言段，Token 开销最低",
		realms: []string{"cn", "global"},
	},
}

// presetNames 合法预设名集合（含空 = 未设置，语义等于 default）。
func presetNames() []string {
	out := make([]string, 0, len(presetCatalog)+1)
	out = append(out, "")
	for _, p := range presetCatalog {
		out = append(out, p.name)
	}
	return out
}

// presetContent 读取预设正文。查找顺序：<name>.<realm>.md → <name>.md。
// 两者都不存在 → error（配置写了不存在的预设，fail fast 而不是静默用默认）。
func presetContent(name, realm string) (string, error) {
	realm = NormalizeRealm(realm)
	candidates := []string{
		path.Join("presets", name+"."+realm+".md"),
		path.Join("presets", name+".md"),
	}
	for _, c := range candidates {
		b, err := presetFS.ReadFile(c)
		if err == nil {
			return string(b), nil
		}
		if !isNotExist(err) {
			return "", fmt.Errorf("read preset %s: %w", c, err)
		}
	}
	return "", fmt.Errorf("preset %q 不存在（可用：%s）", name, strings.Join(presetNames()[1:], " / "))
}

// isNotExist 判断 embed.FS 的"文件不存在"。embed 对缺失路径返回 *fs.PathError
// 且 Err 为 fs.ErrNotExist；用 errors.Is 口径判定，不比较字符串。
func isNotExist(err error) bool {
	return err != nil && (strings.Contains(err.Error(), fs.ErrNotExist.Error()))
}

// Presets 返回内置预设清单（含各域字符数，供面板展示与预览）。
// 读取失败（嵌入资源缺失）的预设直接跳过，不让面板整体失败。
func Presets() []PresetInfo {
	out := make([]PresetInfo, 0, len(presetCatalog))
	for _, p := range presetCatalog {
		info := PresetInfo{Name: p.name, Label: p.label, Description: p.desc, Realms: p.realms}
		if cn, err := presetContent(p.name, "cn"); err == nil {
			info.CharsCN = len([]rune(cn))
		}
		if info.Realms == nil {
			info.CharsGlobal = info.CharsCN
		} else if g, err := presetContent(p.name, "global"); err == nil {
			info.CharsGlobal = len([]rune(g))
		}
		out = append(out, info)
	}
	return out
}

// PresetNames 返回全部合法预设名（排序后，用于错误提示与校验）。
func PresetNames() []string {
	names := presetNames()[1:]
	sort.Strings(names)
	return names
}

// PresetListHint 返回“合法取值”列表文本（错误信息用，单一事实来源在注册表）。
func PresetListHint() string { return strings.Join(PresetNames(), " / ") }
