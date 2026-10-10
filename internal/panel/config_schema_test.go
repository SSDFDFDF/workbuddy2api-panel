package panel

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"workbuddy_manager/internal/config"
)

// 面板配置表单的类型契约守护。
//
// 背景（真实事故）：React 版表单把所有非布尔字段的 DOM 值当字符串下发，只有
// NUMERIC_CFG 里的字段才转 Number。迁移时只登记了 `media_image_max_dimension`
// 一个，于是 panel.package_detail_limit 等 14 个数字字段一保存就报
// 「parse config: json: cannot unmarshal string into Go struct field … of type int」，
// 而且**整张表单一起提交**——任意一个字段类型错，保存就全盘失败（用户只改了
// 一个开关也会看到某条无关字段的报错）。
//
// 两个清单（TS 表单映射 / Go 结构体）天然会漂移，所以这里按 Go 侧的真实类型
// 核对 TS 侧声明的映射：漏登记、多登记、路径拼错都会在这里失败。

// ── 解析 frontend/src/configSchema.ts ─────────────────────────────────────

type tsSchema struct {
	// mapName 表单字段名 → config 叶子路径（点分）。
	mapPath   map[string]string
	numeric   map[string]bool // NUMERIC_CFG
	strList   map[string]bool // STR_LIST_CFG
	clearable map[string]bool // CLEARABLE_CFG
}

var (
	reCfgEntry  = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*:\s*\[([^\]]*)\],`)
	reTsString  = regexp.MustCompile(`'([^']+)'`)
	reTsSetHead = regexp.MustCompile(`export const (NUMERIC_CFG|STR_LIST_CFG|CLEARABLE_CFG)\s*=\s*new Set\(\[`)
)

func parseConfigSchemaTS(t *testing.T) tsSchema {
	t.Helper()
	raw, err := os.ReadFile("../../frontend/src/configSchema.ts")
	if err != nil {
		t.Fatalf("读取前端口径文件失败（源码树不完整？测试需在仓库内运行）：%v", err)
	}
	src := string(raw)
	out := tsSchema{mapPath: map[string]string{}, numeric: map[string]bool{}, strList: map[string]bool{}, clearable: map[string]bool{}}

	// CFG_MAP：只认顶层条目（形如 `name: ['a', 'b'],`），嵌套行是逗号结尾的单行。
	lines := strings.Split(src, "\n")
	inMap := false
	for _, ln := range lines {
		if strings.Contains(ln, "export const CFG_MAP") {
			inMap = true
			continue
		}
		if !inMap {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "}") {
			inMap = false
			continue
		}
		m := reCfgEntry.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		var segs []string
		for _, s := range reTsString.FindAllStringSubmatch(m[2], -1) {
			segs = append(segs, s[1])
		}
		if len(segs) > 0 {
			out.mapPath[m[1]] = strings.Join(segs, ".")
		}
	}

	// 三个 Set：按 "export const X = new Set([" 到匹配的 "]);" 取块。
	for _, m := range reTsSetHead.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		rest := src[m[1]:]
		end := strings.Index(rest, "]);")
		if end < 0 {
			t.Fatalf("%s 定义未闭合", name)
		}
		dst := out.numeric
		switch name {
		case "STR_LIST_CFG":
			dst = out.strList
		case "CLEARABLE_CFG":
			dst = out.clearable
		}
		for _, s := range reTsString.FindAllStringSubmatch(rest[:end], -1) {
			dst[s[1]] = true
		}
	}

	if len(out.mapPath) < 50 {
		t.Fatalf("CFG_MAP 解析出 %d 项，明显偏少（解析器漂移？）", len(out.mapPath))
	}
	if len(out.numeric) == 0 {
		t.Fatalf("NUMERIC_CFG 一项都没解析出来（解析器漂移？）")
	}
	return out
}

// ── 反射取 Go 侧叶子类型 ──────────────────────────────────────────────────

// leafKindOf 返回 config.Config 里 path 对应叶子的 Go 类别：
// "number" / "bool" / "string" / "int[]" / "string[]" / ""（不存在）。
// map 键位在反射侧写作 "*"，调用方可传具体键名（自动回退匹配）。
func leafKindOf(t *testing.T, path string) string {
	t.Helper()
	kinds := configLeafKinds(t)
	if k, ok := kinds[path]; ok {
		return k
	}
	// upstream.profiles.cn.X → upstream.profiles.*.X
	segs := strings.Split(path, ".")
	for i := range segs {
		probe := append([]string(nil), segs...)
		probe[i] = "*"
		if k, ok := kinds[strings.Join(probe, ".")]; ok {
			return k
		}
	}
	return ""
}

func configLeafKinds(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		switch rt.Kind() {
		case reflect.Pointer:
			walk(rt.Elem(), path)
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if !f.IsExported() {
					continue
				}
				tag := strings.Split(f.Tag.Get("json"), ",")[0]
				if tag == "-" {
					continue
				}
				if tag == "" {
					tag = f.Name
				}
				walk(f.Type, joinLeafPath(path, tag))
			}
		case reflect.Map:
			walk(rt.Elem(), joinLeafPath(path, "*"))
		case reflect.Slice:
			switch rt.Elem().Kind() {
			case reflect.Int, reflect.Int64, reflect.Float64:
				out[path] = "int[]"
			default:
				out[path] = "string[]"
			}
		default:
			switch rt.Kind() {
			case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Float32, reflect.Float64:
				out[path] = "number"
			case reflect.Bool:
				out[path] = "bool"
			default:
				out[path] = "string"
			}
		}
	}
	walk(reflect.TypeOf(config.Config{}), "")
	if len(out) < 80 {
		t.Fatalf("反射出 %d 个叶子，明显偏少", len(out))
	}
	return out
}

func joinLeafPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// panelSends 面板保存时下发的 JSON 类型（由表单控件形态决定）。
func panelSends(name, goKind string, s tsSchema) string {
	switch {
	case name == "fingerprint_rules_text":
		return "string[]" // 结构化字段：collectConfig 单独转成规则数组
	case strings.HasSuffix(name, "_hours"):
		return "int[]"
	case s.numeric[name]:
		return "number"
	case s.strList[name]:
		return "string[]"
	case goKind == "bool":
		return "bool"
	default:
		return "string"
	}
}

// TestPanelFormTypesMatchGoConfig 逐个核对：面板下发的类型 == Go 结构的类型。
func TestPanelFormTypesMatchGoConfig(t *testing.T) {
	s := parseConfigSchemaTS(t)

	names := make([]string, 0, len(s.mapPath))
	for n := range s.mapPath {
		names = append(names, n)
	}
	sort.Strings(names)

	var mismatched, unknown []string
	for _, name := range names {
		path := s.mapPath[name]
		k := leafKindOf(t, path)
		if k == "" {
			unknown = append(unknown, name+" → "+path)
			continue
		}
		// fingerprint_rules_text 不是 1:1 叶子（collectConfig 走 parseRules），
		// 单独豁免：Go 侧是 []struct，测试无法从类型直接比对。
		if name == "fingerprint_rules_text" {
			continue
		}
		want := panelSends(name, k, s)
		if want != k {
			mismatched = append(mismatched, name+" ("+path+"): Go 期望 "+k+"，面板下发 "+want)
		}
	}

	for _, m := range unknown {
		t.Errorf("CFG_MAP 里的路径在 config.Config 中不存在（改名/拼错？）：%s", m)
	}
	for _, m := range mismatched {
		t.Errorf("表单类型与 Go 结构不符（保存必然 400）：%s", m)
	}
}

// TestNumericCfgHasNoStaleEntry 反向核对：NUMERIC_CFG 里不能有非数字字段，
// 也不能有 CFG_MAP 里不存在的名字（防止改名后留下孤儿项）。
func TestNumericCfgHasNoStaleEntry(t *testing.T) {
	s := parseConfigSchemaTS(t)
	names := make([]string, 0, len(s.numeric))
	for n := range s.numeric {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		path, ok := s.mapPath[name]
		if !ok {
			t.Errorf("NUMERIC_CFG 含 CFG_MAP 中不存在的字段 %q（改名后残留？）", name)
			continue
		}
		if k := leafKindOf(t, path); k != "number" {
			t.Errorf("NUMERIC_CFG 含非数字字段 %q（%s 的 Go 类型是 %s）：会发成数字导致解析失败", name, path, k)
		}
	}
}
