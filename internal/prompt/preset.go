// preset.go 内置提示词预设库：注册表 + 文件加载。
//
// 目录约定（`presets/`）：
//
//	<name>.md           分域共用（default）
//	<name>.<realm>.md   分域文件（cn / global），缺失的 realm 回落共用文件
//
// 新增预设只需放文件 + 在 catalog 里登记一行，面板会自动列出（列表由
// Presets() 生成，前端不硬编码）。
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
		label:  "通用工程助手",
		desc:   "约 2 KB，工程立场/最小改动/验证闭环；中英共用一份正文",
		realms: nil,
	},
	{
		name:   PresetMinimal,
		label:  "极简",
		desc:   "约 10 行，量级对齐官方 Quick 模式模板；按域取中/英文",
		realms: []string{"cn", "global"},
	},
	{
		name:   "coding",
		label:  "编码代理",
		desc:   "工具调用纪律 + 最小改动 + 自证闭环；贴近交互式编码代理的行为契约",
		realms: []string{"cn", "global"},
	},
	{
		name:   "tool-agent",
		label:  "函数调用",
		desc:   "面向 function calling 会话：schema 精确、参数不猜、不叙述调用细节",
		realms: []string{"cn", "global"},
	},
	{
		name:   "assistant",
		label:  "通用助手",
		desc:   "非工程向：问答/写作/解释；结论先行、区分事实与判断",
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
