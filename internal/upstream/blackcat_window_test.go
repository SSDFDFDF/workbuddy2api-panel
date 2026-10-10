// blackcat_window_test.go 计数窗口判定的守护：upstream.InWindow 是夜猫子窗口的
// **唯一实现**，排程器（scheduler.inBlackcatWindow）与面板侧任务动作
// （panel.inCountWindow）都调它——两侧对同一份配置必须给出同一答案。
//
// 覆盖三类窗口语义：同日（start<end）、跨零点（start>end）、全天（start==end），
// 以及边界（左闭右开）与内置口径（23:00–08:00 与旧实现 `h>=23||h<8` 等价）。
package upstream

import (
	"testing"
	"time"
)

func at(h, m int) time.Time {
	return time.Date(2026, 10, 10, h, m, 0, 0, time.Local)
}

func TestInWindowSameDay(t *testing.T) {
	// 09:00-18:00（同日，左闭右开）
	cases := []struct {
		h, m int
		want bool
	}{
		{8, 59, false}, {9, 0, true}, {12, 0, true}, {17, 59, true}, {18, 0, false}, {23, 0, false},
	}
	for _, c := range cases {
		if got := InWindow(9*60, 18*60, at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d inWindow(09:00-18:00)=%v want %v", c.h, c.m, got, c.want)
		}
	}
}

func TestInWindowCrossMidnight(t *testing.T) {
	// 23:00-08:00（跨零点）
	cases := []struct {
		h, m int
		want bool
	}{
		{22, 59, false}, {23, 0, true}, {23, 59, true}, {0, 0, true}, {7, 59, true}, {8, 0, false}, {12, 0, false},
	}
	for _, c := range cases {
		if got := InWindow(23*60, 8*60, at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d inWindow(23:00-08:00)=%v want %v", c.h, c.m, got, c.want)
		}
	}
}

func TestInWindowAllDay(t *testing.T) {
	// start == end = 全天（配置写 00:00-00:00 的语义）
	for _, h := range []int{0, 6, 12, 18, 23} {
		if !InWindow(0, 0, at(h, 0)) {
			t.Errorf("%02d:00 inWindow(00:00-00:00) 应为全天", h)
		}
	}
	if !InWindow(600, 600, at(3, 0)) {
		t.Error("任意 start==end 都应为全天")
	}
}

// 内置口径：InNightWindow 与历史实现（h >= 23 || h < 8）逐时刻一致。
func TestInNightWindowMatchesLegacyRule(t *testing.T) {
	for h := 0; h < 24; h++ {
		legacy := h >= 23 || h < 8
		if got := InNightWindow(at(h, 30)); got != legacy {
			t.Errorf("%02d:30 InNightWindow=%v want %v", h, got, legacy)
		}
	}
}
