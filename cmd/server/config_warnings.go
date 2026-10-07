// config_warnings.go 配置容错读取：识别不了的键忽略并告警，只有「认识的键值非法」
// 才报错。
//
// 为什么不直接拒收未知键：
//   - 面板保存先把旧文件深合并再解析，历史遗留键会让整个保存失败（配置写不回去）；
//   - 向前兼容：新版本新增的键被旧版本读到时应降级运行，而不是拒绝启动；
//   - 笔误仍要可见：未知键进 Warnings（启动日志 + 面板 `_warnings`），不静默吞掉。
//
// 退役键额外给出「已失效/替代项」文案，避免用户以为旧开关还在生效。
package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// unknownConfigKeys 对比原始配置与 Config 结构已知键，返回未知键的点分路径。
// 对象逐层下钻（数组元素也下钻）；map 允许任意键，但其值类型继续下钻，
// 因此 `upstream.profiles.cn.client_verison` 这类笔误也能被发现。
func unknownConfigKeys(raw map[string]any, c *Config) []string {
	known, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var tree map[string]any
	if err := json.Unmarshal(known, &tree); err != nil {
		return nil
	}
	shape := configShape(reflect.TypeOf(Config{}))
	var out []string
	collectUnknown(raw, tree, shape, "", &out)
	sort.Strings(out)
	return out
}

// shapeNode 配置结构在某一层的已知键形状。
type shapeNode struct {
	fields  map[string]*shapeNode // struct 字段
	mapElem *shapeNode            // map 值类型：任意键允许，值按此形态下钻
	elem    *shapeNode            // 数组元素形态
}

// configShape 由 Go 类型构建已知键形状（json tag 为准，跳过 `-`）。
func configShape(t reflect.Type) *shapeNode {
	switch t.Kind() {
	case reflect.Pointer:
		return configShape(t.Elem())
	case reflect.Struct:
		n := &shapeNode{fields: map[string]*shapeNode{}}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
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
			n.fields[tag] = configShape(f.Type)
		}
		return n
	case reflect.Map:
		return &shapeNode{mapElem: configShape(t.Elem())}
	case reflect.Slice, reflect.Array:
		return &shapeNode{elem: configShape(t.Elem())}
	default:
		return &shapeNode{}
	}
}

// collectUnknown 在 raw 与 known 两棵树上同步下钻；raw 里 known 没有的键即未知。
// shape 提供「这一层允许任意键吗」的额外信息（map 允许，struct 不允许）。
func collectUnknown(raw, known any, shape *shapeNode, path string, out *[]string) {
	switch rv := raw.(type) {
	case map[string]any:
		kv, _ := known.(map[string]any)
		for k, v := range rv {
			p := k
			if path != "" {
				p = path + "." + k
			}
			childShape := shape
			if shape != nil {
				if shape.mapElem != nil { // map：任意键合法（realm 名、用途名等）
					childShape = shape.mapElem
				} else {
					childShape = shape.fields[k]
				}
			}
			knownChild, exists := kv[k]
			if !exists && (shape == nil || shape.mapElem == nil) {
				*out = append(*out, p)
				continue
			}
			collectUnknown(v, knownChild, childShape, p, out)
		}
	case []any:
		ka, _ := known.([]any)
		var elemShape *shapeNode
		if shape != nil && shape.elem != nil {
			elemShape = shape.elem
		}
		for i, v := range rv {
			var kc any
			if i < len(ka) {
				kc = ka[i]
			}
			p := path + "[]"
			collectUnknown(v, kc, elemShape, p, out)
		}
	}
}

// retiredConfigKeys 已退役/改名的键 → 用户可执行的说明。命中即告警（不阻断启动）。
var retiredConfigKeys = map[string]string{
	"features.sanitize_blacklist_fingerprints": "已移除：网关不再改写请求内容（消息文本/工具参数/推理原样转发）",
	"upstream.user_agent":                      "已移除：改用 upstream.profiles.<realm>.user_agents.<用途>",
	"upstream.client_version":                  "已移除：改用 upstream.profiles.<realm>.client_version",
	"upstream.cli_version":                     "已移除：改用 upstream.profiles.<realm>.cli_version",
	"cooldown.hard_credit":                     "已退役：积分耗尽固定冷却到次日 04:00",
	"schedule.travel_interval_minutes":         "已退役：改用 schedule.travel_hours",
}

// retiredKeysIn 返回命中"已退役键"的说明（按路径排序，输出稳定）。
func retiredKeysIn(raw map[string]any) []string {
	if len(raw) == 0 {
		return nil
	}
	paths := make([]string, 0, len(retiredConfigKeys))
	for p := range retiredConfigKeys {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		if _, ok := lookupConfigPath(raw, strings.Split(p, ".")); ok {
			out = append(out, p+" —— "+retiredConfigKeys[p])
		}
	}
	return out
}

// lookupConfigPath 按路径取值；任一层缺失/类型不符返回 ok=false。
func lookupConfigPath(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, seg := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
