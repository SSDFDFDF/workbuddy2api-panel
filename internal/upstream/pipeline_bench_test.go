// pipeline_bench_test.go JSON 处理链路的量化基准。
//
// 为什么单独建这套基准：一个原生 Chat 请求在网关与上游之间会被反复编解码——
// 入站解析、出站校验再解析一次、Encode 内部 marshal→解析→marshal 的深拷贝舞、
// prompt_cache_key 注入再解析+marshal。大上下文/图片请求（base64 在 JSON 里占多数
// 字节）会被这套「遍数」放大成可观的 CPU 与分配 churn。
//
// 基准覆盖两个层次，便于定位收益来自哪一级：
//   - BenchmarkParseOnly*：只跑一次出站校验解析（链路里的一次「遍」）；
//   - BenchmarkChatBodyPipeline*：出站完整链路（buildOutbound：解析+改写+唯一一次序列化）；
//   - BenchmarkChatBodyPipelineParsed*：调用方已解析（handler 路径，省掉一遍整包解析）。
//
// 按 0 / 1MB / 5MB 图片三档测量：小请求受固定开销主导，大请求受字节数主导。
package upstream

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"workbuddy_manager/internal/forwarding"
)

// benchPayloadSize 与 media.MaxImageBytes 对齐的档位（0 = 纯文本）。
var benchPayloadSizes = []int{0, 1 << 20, 5 << 20}

func benchName(imageBytes int) string {
	if imageBytes == 0 {
		return "text"
	}
	return fmt.Sprintf("image=%dMB", imageBytes>>20)
}

// chatBodyWithImage 构造与真实客户端同形的原生 Chat 请求体：
// 文本 part + 内联 data URL 图片 part（base64 占据绝大部分字节）。
func chatBodyWithImage(imageBytes int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"cn:glm-5.2","stream":true,"temperature":0.7,"messages":[`)
	b.WriteString(`{"role":"system","content":"You are a helpful assistant."},`)
	text := fmt.Sprintf("%q", strings.Repeat("请分析这张图片里的内容。", 30))
	if imageBytes > 0 {
		b.WriteString(`{"role":"user","content":[{"type":"text","text":` + text + `},`)
		b.WriteString(`{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,`)
		b.WriteString(base64.StdEncoding.EncodeToString(make([]byte, imageBytes)))
		b.WriteString(`"}}]}`)
	} else {
		b.WriteString(`{"role":"user","content":[{"type":"text","text":` + text + `}]}`)
	}
	b.WriteString(`,{"role":"assistant","content":"好的"}]}`)
	return []byte(b.String())
}

// benchClient 返回带 cache secret 的客户端（injectCacheKey 生效所必需）。
func benchClient() *Client {
	c := New()
	c.CacheSecret = make([]byte, 32)
	return c
}

// BenchmarkParseOnly 出站校验解析（forwarding.Parse）的单遍成本。
func BenchmarkParseOnly(b *testing.B) {
	for _, size := range benchPayloadSizes {
		body := chatBodyWithImage(size)
		b.Run(benchName(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := forwarding.Parse(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkChatBodyPipeline 出站完整链路（只给字节：panel/scheduler 路径）。
// 这是每个（重试）请求都要付的成本。
func BenchmarkChatBodyPipeline(b *testing.B) {
	c := benchClient()
	for _, size := range benchPayloadSizes {
		body := chatBodyWithImage(size)
		b.Run(benchName(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := c.buildOutbound(ChatInput{Body: body}, "cn", "u1", "conv-1", "principal")
				if err != nil || len(out) == 0 {
					b.Fatalf("err=%v len=%d", err, len(out))
				}
			}
		})
	}
}

// BenchmarkChatBodyPipelineParsed 出站完整链路（handler 路径：文档已解析）。
// 与上面的差值就是「省掉一遍整包解析」的收益，随 payload 字节数线性增长。
func BenchmarkChatBodyPipelineParsed(b *testing.B) {
	c := benchClient()
	for _, size := range benchPayloadSizes {
		body := chatBodyWithImage(size)
		parsed, err := forwarding.Parse(body)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(benchName(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := c.buildOutbound(ChatInput{Req: parsed, Raw: body, Model: "cn:glm-5.2"}, "cn", "u1", "conv-1", "principal")
				if err != nil || len(out) == 0 {
					b.Fatalf("err=%v len=%d", err, len(out))
				}
			}
		})
	}
}
