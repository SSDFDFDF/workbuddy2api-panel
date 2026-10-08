package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newLoginTTLPanel 构造可直接投喂 login 会话的面板（不需要 Pool/Upstream：
// 过期分支在解析 realm 之前就已返回，不会触上游）。
func newLoginTTLPanel() *Panel {
	return New(Config{Version: "test", APIKey: "test-key"})
}

// pollState 以面板鉴权调用 loginPoll 并返回解析后的响应体。
func pollState(t *testing.T, p *Panel, state string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/panel/api/login/poll?state="+state, nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// TestLoginPollRejectsExpiredSession 守护 loginTTL 真正生效：
//
// loginTTL（15 分钟）是「授权 URL 最长有效期」的公开语义，但此前只在**下一次
// loginStart** 的顺手清理里被检查——已经拿到 state 的轮询不受任何时限约束，
// 开弹窗后放着不管，过期的 state 仍能完成登录并落盘凭证。
//
// 本用例直接注入一个「15 分钟前创建」的会话，断言 poll 拒绝它并且把它从表里
// 回收。若实现回退为「只在 loginStart 清理」，这里会拿到 200 + done 缺失以外的
// 结果（继续走上游调用）从而失败。
func TestLoginPollRejectsExpiredSession(t *testing.T) {
	p := newLoginTTLPanel()
	p.loginMu.Lock()
	p.logins["stale-state"] = loginSession{created: time.Now().Add(-loginTTL - time.Minute), realm: "cn"}
	p.loginMu.Unlock()

	code, body := pollState(t, p, "stale-state")
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if body["expired"] != true {
		t.Fatalf("过期会话必须被拒绝：body=%v（说明 loginTTL 未在 poll 生效）", body)
	}
	if body["done"] != false {
		t.Fatalf("过期会话不得报告完成：body=%v", body)
	}
	if _, ok := body["message"]; !ok {
		t.Fatalf("过期响应应带可操作提示：body=%v", body)
	}

	// 已回收：再次轮询必须变成 unknown（不能因为反复 poll 而复活）。
	p.loginMu.Lock()
	_, stillThere := p.logins["stale-state"]
	p.loginMu.Unlock()
	if stillThere {
		t.Fatal("过期会话必须从表中回收，否则内存随打开的弹窗无界增长")
	}
	code2, body2 := pollState(t, p, "stale-state")
	if code2 != http.StatusNotFound {
		t.Fatalf("回收后再轮询应 404，得到 code=%d body=%v", code2, body2)
	}
}

// TestLoginSessionExpiredAtBoundary 确保 TTL 判定没有误伤（边界）。
//
// 用纯函数断言而不是发请求：未过期会话的 poll 会走到上游设备授权端点，
// 单测里真外呼会把用例变成网络依赖（慢、可能 flake），而这里要验的只是
// 「超期判据」本身——它同时被 loginStart 的清理与 loginPoll 的入口校验使用。
func TestLoginSessionExpiredAtBoundary(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		created time.Time
		want    bool
	}{
		{"刚创建", now, false},
		{"TTL 内", now.Add(-loginTTL + time.Second), false},
		{"恰好 TTL（不超期）", now.Add(-loginTTL), false},
		{"刚过 TTL", now.Add(-loginTTL - time.Second), true},
		{"远超 TTL", now.Add(-24 * time.Hour), true},
	}
	for _, c := range cases {
		if got := (loginSession{created: c.created}).expiredAt(now); got != c.want {
			t.Errorf("%s: expiredAt=%v want %v", c.name, got, c.want)
		}
	}
}
