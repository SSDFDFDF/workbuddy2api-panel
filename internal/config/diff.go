// diff.go 配置差异计算：反射逐叶子比较两次解析结果。
//
// 用途：面板保存配置时回答"这次到底改了什么"。在这之前 restartRequiredFields
// 只看新配置、只判非空，于是只改一个热字段也会回"20 项需重启"——用户很快学会
// 忽略这个提示，真正需要重启的改动反而被淹没。差异必须**真的算出来**。
//
// 路径表示与 catalog.go / keyshape.go 共用一套：点分路径（拼接用 keyshape.go
// 的 joinPath），map 的键位展开成具体键名（upstream.profiles.cn.client_version）。
// 切片整体视为叶子（DeepEqual）——配置里的切片都是取值型列表（排程小时、
// 指纹规则），没有"逐元素热改"语义。
package config

import (
	"reflect"
	"sort"
	"strings"
)

// PathSet 点分路径集合。
// PathSet 点分路径集合。
//
// 只提供"读"操作（Has/Slice/Len）：产出方是 DiffConfig，消费方是
// restartRequiredFields 与面板提示。集合运算（并/交/差）等到真有第二个消费方
// （如 P3 的 applier 认领校验）再按需加，不提前预留。
type PathSet map[string]struct{}

// Add 加入一个路径（空串忽略）。
func (s PathSet) Add(p string) {
	if p != "" {
		s[p] = struct{}{}
	}
}

// Has 报告路径是否存在。
func (s PathSet) Has(p string) bool { _, ok := s[p]; return ok }

// Len 路径数。
func (s PathSet) Len() int { return len(s) }

// Slice 返回排序后的路径列表。
func (s PathSet) Slice() []string {
	out := make([]string, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// DiffConfig 返回 old 与 cur 的叶子差异路径集合。
//
// nil 视同零值：调用方在"旧配置不可解析/不存在"时传 nil，会得到"全部叶子都变了"，
// 从而退回保守的"全量需重启清单"，而不是漏报。
func DiffConfig(old, cur *Config) PathSet {
	out := PathSet{}
	diffValue(reflect.ValueOf(old), reflect.ValueOf(cur), "", out)
	return out
}

// diffValue 递归比较同类型的两个值，把差异记在 out 上。
func diffValue(a, b reflect.Value, path string, out PathSet) {
	a, b = deref(a), deref(b)
	if !a.IsValid() || !b.IsValid() {
		return
	}
	switch a.Kind() {
	case reflect.Struct:
		t := a.Type()
		for i := 0; i < t.NumField(); i++ {
			name, ok := jsonFieldName(t.Field(i))
			if !ok {
				continue // json:"-" 派生态 / 未导出字段不参与差异
			}
			diffValue(a.Field(i), b.Field(i), joinPath(path, name), out)
		}
	case reflect.Map:
		// 键的并集：只在一边出现的键按零值对待（= 该键下所有叶子都变了）。
		keys := map[string]reflect.Value{}
		for _, k := range a.MapKeys() {
			keys[k.String()] = k
		}
		for _, k := range b.MapKeys() {
			if _, ok := keys[k.String()]; !ok {
				keys[k.String()] = k
			}
		}
		for name, k := range keys {
			av, bv := a.MapIndex(k), b.MapIndex(k)
			if !av.IsValid() {
				av = reflect.Zero(a.Type().Elem())
			}
			if !bv.IsValid() {
				bv = reflect.Zero(b.Type().Elem())
			}
			diffValue(av, bv, joinPath(path, name), out)
		}
	default:
		// 叶子（标量、切片、数组）：整体比较。
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			out.Add(path)
		}
	}
}

// deref 解引用指针；nil 指针返回其元素类型的零值。
func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Zero(v.Type().Elem())
		}
		v = v.Elem()
	}
	return v
}

// jsonFieldName 返回结构体字段的配置键名与是否参与配置。
// json:"-" 与未导出字段返回 false。
func jsonFieldName(f reflect.StructField) (string, bool) {
	if !f.IsExported() {
		return "", false
	}
	tag := strings.Split(f.Tag.Get("json"), ",")[0]
	if tag == "-" {
		return "", false
	}
	if tag == "" {
		tag = f.Name
	}
	return tag, true
}

// leafPatterns 由类型形状推导叶子路径模式（map 的键位写 "*"）。
func leafPatterns(t reflect.Type, path string, out *[]string) {
	switch t.Kind() {
	case reflect.Pointer:
		leafPatterns(t.Elem(), path, out)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			name, ok := jsonFieldName(t.Field(i))
			if !ok {
				continue
			}
			leafPatterns(t.Field(i).Type, joinPath(path, name), out)
		}
	case reflect.Map:
		leafPatterns(t.Elem(), joinPath(path, "*"), out)
	default:
		// 标量、切片、数组都是叶子（与 diffValue 的口径一致）。
		*out = append(*out, path)
	}
}

// cfgType 返回 Config 的类型（集中一处，便于测试与 LeafPaths 复用）。
func cfgType() reflect.Type { return reflect.TypeOf(Config{}) }
