package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/reqlog"
)

// failWriter 模拟「客户端保持连接但不读」：写正文时返回错误。
// Header/WriteHeader 正常（响应头字节少，通常能进内核缓冲）。
type failWriter struct {
	header http.Header
	code   int
	write  bool // 是否已尝试写入
}

func (w *failWriter) Header() http.Header { return w.header }

func (w *failWriter) WriteHeader(code int) { w.code = code }

func (w *failWriter) Write([]byte) (int, error) {
	w.write = true
	return 0, errors.New("connection reset by peer")
}

// SetWriteDeadline 让 http.ResponseController 能设上写 deadline（非流式路径会用）。
func (w *failWriter) SetWriteDeadline(any) error { return nil }

// TestNonStreamDownstreamWriteFailureIsObservable 守护「上游成功但下游没收到」可观测：
//
// 非流式响应此前写失败被静默丢弃（writeJSON 的 `_, _ =`），请求记录里仍是
// 200 + success —— 上游已生成（可能已扣费）但客户端什么都没拿到，运维在
// 日志/面板里完全看不出这次请求丢在网关出口。修复后必须记为 interrupted/502。
//
// 反向约束同样重要：**不得**因为下游写失败而重放上游请求（重复生成/重复扣费），
// 所以这里同时断言账号只被真正请求过一次。
func TestNonStreamDownstreamWriteFailureIsObservable(t *testing.T) {
	const sse = "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"glm-5.2\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]," +
		"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"

	var upstreamCalls int
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		upstreamCalls++
		return 200, sse, true
	})
	logs := reqlog.New(reqlog.Config{})
	h := NewHandler(Config{
		Pool:       testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:   up,
		RequestLog: logs,
	})

	w := &failWriter{header: http.Header{}}
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`))
	h.ServeHTTP(w, req)

	if !w.write {
		t.Fatal("用例未触发下游写入，断言无效")
	}
	if upstreamCalls != 1 {
		t.Fatalf("上游被请求 %d 次，want 1（下游写失败不得重放上游：会重复生成/重复扣费）", upstreamCalls)
	}

	snap := logs.Snapshot()
	if len(snap.Recent) == 0 {
		t.Fatal("未记录请求事件")
	}
	e := snap.Recent[0]
	if e.Outcome != reqlog.OutcomeInterrupted {
		t.Fatalf("outcome=%q want %q（上游成功但下游未送达必须可区分）", e.Outcome, reqlog.OutcomeInterrupted)
	}
	// 事件里的 status 是**线上状态**：WriteHeader(200) 已随响应头发出，之后无法改写，
	// 所以这里断言 200 而不是 502。语义结果由 outcome=interrupted 承担，两者分工明确：
	// status 回答「客户端看到的 HTTP 码」，outcome 回答「这次请求到底成不成」。
	if e.Status != http.StatusOK {
		t.Fatalf("status=%d want %d（线上状态已发出，不可改写）", e.Status, http.StatusOK)
	}
	if e.OK {
		t.Fatal("交付失败的请求不得标记为 OK")
	}
	// 汇总口径：该请求不得计入成功。
	if snap.Succeeded != 0 || snap.Completed != 1 {
		t.Fatalf("succeeded=%d completed=%d，want 0/1", snap.Succeeded, snap.Completed)
	}
}

// TestNonStreamSuccessfulWriteStaysSuccess 反向守护：正常交付仍是 200 + success，
// 新增的失败判定不得误伤正常路径。
func TestNonStreamSuccessfulWriteStaysSuccess(t *testing.T) {
	const sse = "data: {\"id\":\"c2\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"glm-5.2\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]," +
		"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, sse, true })
	logs := reqlog.New(reqlog.Config{})
	h := NewHandler(Config{
		Pool:       testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream:   up,
		RequestLog: logs,
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if body, _ := io.ReadAll(rec.Body); !strings.Contains(string(body), "ok") {
		t.Fatalf("body=%q", body)
	}
	snap := logs.Snapshot()
	if len(snap.Recent) == 0 || snap.Recent[0].Outcome != reqlog.OutcomeSuccess || snap.Recent[0].Status != http.StatusOK {
		t.Fatalf("正常路径必须记 success/200，得到 %+v", snap.Recent)
	}
}
