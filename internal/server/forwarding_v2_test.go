package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/upstream"
)

// 阶段超时/错误语义/重试门的新契约端到端回归。

// 上游以 SSE error 帧报错：下游必须看到 error，不得伪造 stop 的成功回答。
func TestV2StreamErrorFrameIsFailure(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		return 200, `data: {"error":{"code":6004,"message":"rate limited"}}` + "\n\ndata: [DONE]\n\n", true
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
	body := rec.Body.String()
	if !strings.Contains(body, "rate limited") {
		t.Fatalf("stream must surface upstream error: %s", body)
	}
	if strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("stream must not fabricate a successful answer: %s", body)
	}
	if st, _ := p.Status("u1"); st.TokenUsage.UsageCount != 0 {
		t.Fatalf("errored stream must not record usage success: %+v", st.TokenUsage)
	}
}

// 非流式同样语义：200 + error 帧 → 下游错误状态，而不是空回答。
func TestV2NonStreamErrorFrameIsFailure(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		return 200, `data: {"error":{"code":6004,"message":"rate limited"}}` + "\n\ndata: [DONE]\n\n", true
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code == http.StatusOK {
		t.Fatalf("false success: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "rate limited") {
		t.Fatalf("upstream error must be visible: %s", rec.Body)
	}
}

// 已产生输出的流被截断：不得换号重放（可能重复执行/计费），只报一次错误。
func TestV2NoReplayAfterGeneration(t *testing.T) {
	var calls atomic.Int32
	up := &upstream.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(`data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n")),
			}, nil
		})},
		ChatBaseCN: "https://fake.example",
	}
	p := testPoolWith(
		&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999},
		&auth.Auth{UID: "u2", AccessToken: "at2", ExpiresAt: 9999999999},
	)
	h := NewHandler(Config{Pool: p, Upstream: up, MaxRotate: 3})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("truncated stream must fail: %d %s", rec.Code, rec.Body)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream calls=%d want 1 (no replay after generation)", n)
	}
}

// n>1 非流式：每个 choice 独立聚合、按 index 排序，不再拼接成一个回答。
func TestV2MultiChoiceAggregation(t *testing.T) {
	raw := `data: {"id":"x","model":"glm-5.2","choices":[{"index":1,"delta":{"content":"B"},"finish_reason":"length"},{"index":0,"delta":{"content":"A"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, raw, true })
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","n":2,"messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 2 || resp.Choices[0].Message.Content != "A" || resp.Choices[1].Message.Content != "B" {
		t.Fatalf("choices merged or reordered: %s", rec.Body)
	}
	if resp.Choices[1].Finish != "length" {
		t.Fatalf("finish_reason must be preserved per choice: %s", rec.Body)
	}
}

// 阶段超时：首模型事件超时后必须失败，不得把 role/心跳当成功无限等待。
func TestV2FirstModelEventTimeout(t *testing.T) {
	up := &upstream.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(": ping\n\n")),
			}, nil
		})},
		ChatBaseCN:     "https://fake.example",
		StreamTimeouts: upstream.StreamTimeoutConfig{FirstModelEvent: 20 * time.Millisecond},
	}
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat-only stream hung past first-model-event deadline")
	}
	if rec.Code != http.StatusOK { // 未提交时也允许 JSON 错误；这里断言"必须结束且非成功语义"
		var env map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if _, ok := env["error"]; !ok {
			t.Fatalf("timeout must produce an error: %d %s", rec.Code, rec.Body)
		}
	} else if !strings.Contains(rec.Body.String(), "timeout") {
		t.Fatalf("timeout must be reported: %s", rec.Body)
	}
}
