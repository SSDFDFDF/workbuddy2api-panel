package redisstore

import (
	"reflect"
	"testing"
)

// TestDecodeBindValues MGET 返回值解码：正常值、键已过期（nil）、非 string、
// 空串、返回值短于键列表（MGET 契约异常）都必须安全且不产生脏绑定。
func TestDecodeBindValues(t *testing.T) {
	keys := []string{
		bindPrefix + "sess-a",
		bindPrefix + "sess-b", // 过期 → nil
		bindPrefix + "sess-c", // 非 string
		bindPrefix + "sess-d", // 空串
	}
	vals := []any{"uid-a", nil, 42, ""}
	got := decodeBindValues(keys, vals)
	want := map[string]string{"sess-a": "uid-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestDecodeBindValuesShortResult 防御 MGET 返回值少于键数（协议异常/驱动异常）：
// 不得 panic 越界，也不得给缺失的键编造值。
func TestDecodeBindValuesShortResult(t *testing.T) {
	keys := []string{bindPrefix + "a", bindPrefix + "b", bindPrefix + "c"}
	vals := []any{"uid-a"}
	got := decodeBindValues(keys, vals)
	want := map[string]string{"a": "uid-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// 反向：值多于键（不应发生）也不得 panic。
	if got := decodeBindValues([]string{bindPrefix + "a"}, []any{"v", "extra", "extra2"}); len(got) != 1 || got["a"] != "v" {
		t.Fatalf("got %v", got)
	}
}

// TestDecodeBindValuesEmpty 空输入的边界。
func TestDecodeBindValuesEmpty(t *testing.T) {
	if got := decodeBindValues(nil, nil); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
	if got := decodeBindValues([]string{bindPrefix + "x"}, nil); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestLoadBindsTimeoutCoversBulkRead 回退防护：批量读的专用上限必须显著大于
// 单次 RPC 上限。旧实现用 readTimeout(3s) 覆盖「逐键 GET」的整个循环，绑定稍多
// 就半途超时并静默丢弃剩余绑定——这正是「粘性重启丢失」的成因之一。
func TestLoadBindsTimeoutCoversBulkRead(t *testing.T) {
	if loadBindsTimeout <= readTimeout {
		t.Fatalf("loadBindsTimeout=%v 必须大于单次 RPC 上限 %v", loadBindsTimeout, readTimeout)
	}
	if bindMGetBatch < 2 {
		t.Fatalf("bindMGetBatch=%d 必须成批（否则退化为逐键 GET）", bindMGetBatch)
	}
}
