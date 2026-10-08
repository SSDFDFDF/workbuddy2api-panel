package protocol

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func longCompletion(chunks int, tool bool) string {
	var b strings.Builder
	if tool {
		b.WriteString(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"text\":\""}}]}}]}` + "\n\n")
	}
	for i := 0; i < chunks; i++ {
		if tool {
			fmt.Fprintf(&b, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":%q}}]}}]}\n\n", strings.Repeat("x", 128))
		} else {
			fmt.Fprintf(&b, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", strings.Repeat("x", 128))
		}
	}
	if tool {
		b.WriteString(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")
	} else {
		b.WriteString(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

type discardResponseWriter struct{ header http.Header }

func (w discardResponseWriter) Header() http.Header         { return w.header }
func (w discardResponseWriter) WriteHeader(int)             {}
func (w discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w discardResponseWriter) Flush()                      {}

// Fixed 128-byte payload deltas expose quadratic copying independently of
// model latency. Report allocations, not just peak retained state.
func BenchmarkLongCompletion(b *testing.B) {
	for _, tool := range []bool{false, true} {
		for _, chunks := range []int{1000, 2000, 4000} {
			raw := longCompletion(chunks, tool)
			for _, stream := range []bool{false, true} {
				b.Run(fmt.Sprintf("tool=%t/stream=%t/chunks=%d", tool, stream, chunks), func(b *testing.B) {
					req, err := Decode(Responses, []byte(`{"model":"test","store":false,"input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}]}`))
					if err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(raw)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if stream {
							err = Stream(discardResponseWriter{http.Header{}}, strings.NewReader(raw), req, nil)
						} else {
							_, err = Aggregate(strings.NewReader(raw), req)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
