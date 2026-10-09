// preset.go 内置提示词预设库：注册表 + 文件加载。
//
// 目录约定（`presets/`）：
//
//	<name>.md           分域共用（保留给未来的中性预设）
//	<name>.<realm>.md   分域文件（cn / global），缺失的 realm 回落共用文件
//
// 库内预设都是分域文件（cn + global）：CN 与 Global 不是翻译关系，而是发布分支差异
// （产品名 / 数据目录 / 区域段 / 语言段），必须两份。
//
// # 预设口径：官方渲染产物的逐字拷贝
//
// 每个预设是一份**静态 MD 正文**：直接 `go:embed` 读文件，**不做任何运行期渲染**。
// 内容源自官方安装包里的明文 Nunjucks 模板与实物抓包，但落到本目录时已经：
//
//   - 条件已解（`{% if %}` 按实物抓包取值内联，仅保留命中分支的原文）；
//   - 变量已删/已字面化（域常量 productName / dataFolderName / ResponseLanguage
//     写成字面值；客户端本地变量如 BinaryContext / WorkbuddyMemoryDir 所在整段删除）；
//   - 无 `{{ }}`、无 `{% %}`、无绝对路径、无机器名。
//
// 因此阅读本目录不需要理解官方的两阶段装配管线：官方跑完那套机器后
// 得到什么字节，这里就是什么字节。导出方式见
// `scripts/render-official-presets.py`（构建期工具，不参与编译）。
//
// craft 两份是**逐字节校验过**的：`--verify` 与实物抓包对比，差异仅限上方
// 列出的两类故意删除；ask/plan/quick/expert 无实物抓包，来自模板渲染。
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
//
// `default` 是**自设计位**（不要求官方逐字，正文待维护者重写）；
// `official-*` 是官方渲染产物逐字（详见 prompt.go 的预设名常量）。
var presetCatalog = []struct {
	name, label, desc string
	realms            []string
}{
	{
		name:   PresetDefault,
		label:  "默认（自设计）",
		desc:   "空 preset 的回落值；正文由维护者自行设计（当前为抓包首屏前缀占位）",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialCraft,
		label:  "官方 Craft",
		desc:   "官方 craft 模式渲染产物逐字（实物抓包校验）：身份 + 能力 + 全部 26 模块 + 护栏；免封信号最强",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialAsk,
		label:  "官方 Ask",
		desc:   "官方 ask 模式逐字：只读模式说明 + 只读工具契约（无写作/执行块；护栏同 craft，因为路径 B 的 content_policy 不分模式）",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialPlan,
		label:  "官方 Plan",
		desc:   "官方 plan 模式逐字：计划先行 + 逐步验证，任务管理段强度高于 craft",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialQuick,
		label:  "官方 Quick",
		desc:   "官方 quick 模式逐字：无工具、无工作区的纯问答形态（官方本体仅 2.3KB）",
		realms: []string{"cn", "global"},
	},
	{
		name:   PresetOfficialExpert,
		label:  "官方 Expert",
		desc:   "官方 expert 模式逐字：专家人格由客户端 PluginAgentPrompt 槽注入（网关侧该槽为空）",
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
