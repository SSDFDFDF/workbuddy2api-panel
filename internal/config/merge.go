// merge.go 保存路径的原始 map 合并与序列化。
//
// 保存 = 用面板提交的键覆盖磁盘原文，而不是整体替换：面板表单只提交它管理的
// 键，未提交的兄弟键（含用户手写的注释性字段/未知键）必须保持原样，否则
// 用户手写的内容会被一次「保存」洗掉。
package config

import "encoding/json"

// MergeConfigMaps 把 incoming 深合并进 cur（原地），返回 cur。
// 对嵌套对象逐键覆盖而不是整体替换：未提交的兄弟键保持原样。
func MergeConfigMaps(cur, incoming map[string]any) map[string]any {
	for k, v := range incoming {
		if inMap, ok := v.(map[string]any); ok {
			if curMap, ok := cur[k].(map[string]any); ok {
				cur[k] = MergeConfigMaps(curMap, inMap)
				continue
			}
		}
		cur[k] = v
	}
	return cur
}

// MergedJSON 把合并后的 map 序列化回 JSON（供 ParseConfig 校验）。
// 紧凑输出：它只用于解析，不用于落盘。
func MergedJSON(m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// MarshalConfig 把配置对象序列化为**落盘形态**（两空格缩进，便于用户直接编辑）。
//
// 版本迁移会重写用户的配置文件，必须保持可读：紧凑单行会把一份手写配置文件变成
// 一整行，用户下次打开基本无法维护。（键序会被 json.Marshal 按字典序重排，
// 这是不可避免的代价，已在迁移说明里告知。）
func MarshalConfig(m map[string]any) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
