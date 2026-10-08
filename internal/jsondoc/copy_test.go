package jsondoc

import (
	"encoding/json"
	"strings"
	"testing"
	"unsafe"
)

// TestCopyDeepCopiesContainers 守护深拷贝的「隔离」语义：改动副本不得影响原文，
// 改动原文不得影响副本（出站改写依赖这一点才能在不污染调用方文档的前提下就地修改）。
func TestCopyDeepCopiesContainers(t *testing.T) {
	src, err := Object([]byte(`{"a":{"b":[1,2,{"c":"x"}]},"n":1.5e3,"s":"t","flag":true,"nul":null}`))
	if err != nil {
		t.Fatal(err)
	}
	cp := CopyObject(src)

	// 改副本的每一层
	inner := cp["a"].(map[string]any)
	arr := inner["b"].([]any)
	arr[0] = json.Number("999")
	arr[2].(map[string]any)["c"] = "changed"
	cp["new"] = "added"

	// 原文必须原封不动
	if got := src["a"].(map[string]any)["b"].([]any)[0]; got != json.Number("1") {
		t.Errorf("原文数组元素被污染: %v", got)
	}
	if got := src["a"].(map[string]any)["b"].([]any)[2].(map[string]any)["c"]; got != "x" {
		t.Errorf("原文嵌套对象被污染: %v", got)
	}
	if _, ok := src["new"]; ok {
		t.Error("原文被新增键污染")
	}

	// 反向：改原文不影响副本
	src["s"] = "mutated"
	if cp["s"] != "t" {
		t.Errorf("副本被原文改动影响: %v", cp["s"])
	}
}

// TestCopyPreservesNumberPrecision 守护数值精度：json.Number 必须按值共享而不是
// 被转成 float64——大整数（如 unix 纳秒时间戳/长 ID）一旦过 float64 就会失真，
// 而这是本项目明确承诺的透传契约。
func TestCopyPreservesNumberPrecision(t *testing.T) {
	raw := []byte(`{"big":1234567890123456789,"frac":0.1,"exp":1.5e3}`)
	src, err := Object(raw)
	if err != nil {
		t.Fatal(err)
	}
	cp := CopyObject(src)

	// 逐值断言精度（不比较整体字节：map 序列化键序由 Go 排序决定，与拷贝无关）。
	if got := cp["big"]; got != json.Number("1234567890123456789") {
		t.Fatalf("大整数失真: %v (%T)", got, got)
	}
	if got := cp["frac"]; got != json.Number("0.1") {
		t.Fatalf("小数失真: %v (%T)", got, got)
	}

	// 更强的判据：对「原文档」与「副本」分别序列化，结果必须逐字节一致
	// （序列化是键序无关的同一遍，因此这等价于深拷贝未改变任何值）。
	wantBytes, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("深拷贝后序列化结果变化:\n got=%s\nwant=%s", gotBytes, wantBytes)
	}
	if _, ok := cp["big"].(json.Number); !ok {
		t.Fatalf("数值类型必须保持 json.Number，得到 %T", cp["big"])
	}
	// 大整数必须以字面量形式出现在输出里（未被科学计数/浮点化）。
	if !strings.Contains(string(gotBytes), "1234567890123456789") {
		t.Fatalf("大整数在输出中不是精确字面量: %s", gotBytes)
	}
}

// TestCopySharesLeafValues 守护成本特征：叶子（字符串等）按值共享，不做 memcpy。
// 大 payload（base64 图片）因此与拷贝成本解耦。
func TestCopySharesLeafValues(t *testing.T) {
	big := make([]byte, 1<<20)
	for i := range big {
		big[i] = 'a'
	}
	s := string(big)
	src := map[string]any{"payload": s, "list": []any{s, s}}
	cp := CopyObject(src)
	if cp["payload"].(string) != s {
		t.Fatal("字符串内容不一致")
	}
	// 共享同一底层数组（不复制字节）：比较 unsafe 指针等价物 —— 用 data 指针判断。
	if stringDataPtr(cp["payload"].(string)) != stringDataPtr(s) {
		t.Fatal("字符串应共享底层数据（否则大 payload 拷贝会退化为 memcpy）")
	}
}

// TestCopyNilAndScalars nil 与不可变标量的边界。
func TestCopyNilAndScalars(t *testing.T) {
	if got := CopyObject(nil); got != nil {
		t.Fatalf("CopyObject(nil)=%v want nil", got)
	}
	for _, v := range []any{nil, "s", true, false, json.Number("7"), 3.5} {
		if got := Copy(v); got != v {
			t.Errorf("Copy(%v)=%v want 原值", v, got)
		}
	}
	// 未识别的类型原样返回（防御：Decode 不产出它们，但调用方可能塞入）
	type custom struct{ X int }
	c := custom{X: 1}
	if got := Copy(c); got != any(c) {
		t.Errorf("Copy(custom)=%v want 原值", got)
	}
}

// stringDataPtr 取字符串底层数据指针（仅用于测试比较共享性）。
func stringDataPtr(s string) uintptr {
	if len(s) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}
