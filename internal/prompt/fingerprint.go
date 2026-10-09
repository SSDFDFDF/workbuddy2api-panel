// fingerprint.go 出站提示词的最小观测工具。
//
// 预设正文是官方渲染产物的逐字拷贝，没有任何运行期加工，所以这里只需要一个
// 「同一份内容 → 同一个短摘要」的函数，供 reqlog 在 11128 类复盘时回答
// “这次发出去的到底是哪一份正文”。
//
// 正文本身不入档（体积 + 隐私），但摘要可复现：同 mode/source/sha256 即同一份内容，
// 配合面板预览即可还原逐字全文。
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
)

// TextSHA 返回正文的 sha256 前 12 位十六进制（空串 → 空串，便于 omitempty）。
//
// 12 位 = 48 bit：归档去重与“是否同一份”判定足够，且日志可读。
func TextSHA(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:6])
}

// FirstLinePrefix 官方 system prompt 的固定首行前缀（运行期填入模型显示名）：
//
//	This conversation is powered by <模型显示名>
//
// 使用它的地方仅剩测试与诊断——网关不再改写这一行（预设里就是官方当时的取值）。
const FirstLinePrefix = "This conversation is powered by "

// HasFirstLine 报告正文是否以官方首行形态开篇。
func HasFirstLine(text string) bool {
	return len(text) >= len(FirstLinePrefix) && text[:len(FirstLinePrefix)] == FirstLinePrefix
}

// FirstLineValue 取正文首行里的模型显示名；不是官方首行形态或无值 → 空串。
func FirstLineValue(text string) string {
	if !HasFirstLine(text) {
		return ""
	}
	rest := text[len(FirstLinePrefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\n' {
			return rest[:i]
		}
	}
	return rest
}
