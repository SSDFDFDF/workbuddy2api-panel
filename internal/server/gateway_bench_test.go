package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/upstream"
)

// gatewayBenchSSE 一个最小的合法上游 SSE 响应（非流式聚合路径会消费它）。
const gatewayBenchSSE = "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"glm-5.2\"," +
	"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]," +
	"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_credits\":0,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"

// gatewayBenchBody 构造与真实客户端同形的请求体：可选内联图片 + 系统提示词消息。
func gatewayBenchBody(imageBytes int, withRealmPrefix bool) string {
	model := "glm-5.2"
	if withRealmPrefix {
		model = "cn:glm-5.2"
	}
	text := fmt.Sprintf("%q", strings.Repeat("请分析这张图片里的内容。", 30))
	var b strings.Builder
	b.WriteString(`{"model":"` + model + `","stream":false,"temperature":0.7,"messages":[`)
	b.WriteString(`{"role":"system","content":"You are a helpful assistant."},`)
	if imageBytes > 0 {
		b.WriteString(`{"role":"user","content":[{"type":"text","text":` + text + `},`)
		b.WriteString(`{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,`)
		b.WriteString(base64.StdEncoding.EncodeToString(make([]byte, imageBytes)))
		b.WriteString(`"}}]}`)
	} else {
		b.WriteString(`{"role":"user","content":[{"type":"text","text":` + text + `}]}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// benchmarkGatewayRequest 让一个请求走完整 handler→upstream 链路（含 JSON 处理、
// 选号、租约、上游往返与本地聚合），所有耗时都只来自网关自身。
//
// 这是「一遍 JSON 到底要花多少」的端到端判据：它同时覆盖入站解析与出站构造，
// 因此能反映压缩 JSON 遍数带来的真实收益（单看某个函数会漏掉链路里的其它遍）。
func benchmarkGatewayRequest(b *testing.B, imageBytes int, withRealmPrefix bool) {
	up := &upstream.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(gatewayBenchSSE)),
			}, nil
		})},
		ChatBaseCN:    "https://fake.example",
		BillingBaseCN: "https://fake.example",
	}
	h := NewHandler(Config{
		Pool:     testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream: up,
	})
	body := gatewayBenchBody(imageBytes, withRealmPrefix)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			b.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkGatewayChatText(b *testing.B)                { benchmarkGatewayRequest(b, 0, false) }
func BenchmarkGatewayChatImage1MB(b *testing.B)            { benchmarkGatewayRequest(b, 1<<20, false) }
func BenchmarkGatewayChatImage5MB(b *testing.B)            { benchmarkGatewayRequest(b, 5<<20, false) }
func BenchmarkGatewayChatImage5MBRealmPrefix(b *testing.B) { benchmarkGatewayRequest(b, 5<<20, true) }
