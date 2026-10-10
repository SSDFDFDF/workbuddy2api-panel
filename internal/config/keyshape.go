// keyshape.go 配置键识别：只认 Config 结构里存在的键。
//
// 口径：
//   - 读取时只认已知键（反序列化天然忽略其余键），不认识的键只进 Warnings 提示，
//     让用户能发现自己拼错了键名；值一律不参与配置语义（没有别名、没有旧键兼容）；
//   - 保存时整份文件按当前结构全量覆盖，未知键/历史遗留键自然消失，
//     因此不需要写侧剪枝逻辑。
//
// 只有「认识的键、值非法」才报错。
package config

import (
	"reflect"
	"sort"
	"strings"
)

// unknownConfigKeys 返回原始配置里 Config 不认识的键（点分路径）。
//
// 判定完全由 Go 类型形状驱动（configShape），不依赖"把 Config 序列化一遍"：
// 零值 Config 的 nil map 会序列化成 null，那条路径上所有子键都会被误判为未知
// （曾把 prompt.profiles.cn.mode 这种合法键当成未知键删掉）。
//
// 对象逐层下钻（数组元素也下钻）；map 允许任意键，但其值类型继续下钻，
// 因此 `upstream.profiles.cn.client_verison` 这类笔误也能被发现。
func unknownConfigKeys(raw map[string]any) []string {
	var out []string
	collectUnknown(raw, configShape(reflect.TypeOf(Config{})), "", &out)
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

// collectUnknown 按形状定义下钻原始配置，收集未知键的点分路径。
// shape 为 nil（走到了不认识的分支）时不再下钻——未知本身已经记过。
func collectUnknown(raw any, shape *shapeNode, path string, out *[]string) {
	switch rv := raw.(type) {
	case map[string]any:
		if shape == nil {
			return
		}
		if shape.mapElem != nil { // map：任意键合法，值按元素形态下钻
			for k, v := range rv {
				collectUnknown(v, shape.mapElem, joinPath(path, k), out)
			}
			return
		}
		for k, v := range rv {
			p := joinPath(path, k)
			child, ok := shape.fields[k]
			if !ok {
				*out = append(*out, p)
				continue
			}
			collectUnknown(v, child, p, out)
		}
	case []any:
		var elem *shapeNode
		if shape != nil {
			elem = shape.elem
		}
		for _, v := range rv {
			collectUnknown(v, elem, path+"[]", out)
		}
	}
}

// joinPath 拼接点分路径（顶层键不带前导点）。
func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
