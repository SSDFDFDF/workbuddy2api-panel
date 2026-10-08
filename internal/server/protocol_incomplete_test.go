package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
)

func TestPartialToolsAccountingAndNoReplay(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			for _, finish := range []string{"length", "content_filter", "", "tool_calls"} {
				t.Run(fmt.Sprint(path, stream, finish), func(t *testing.T) {
					calls := 0
					up := newFakeUpstream(t, func(string) (int, string, bool) {
						calls++
						return 200, `data: {"choices":[{"index":0,"delta":{"content":"checking"}}]}` + "\n\n" +
							fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"cut","type":"function","function":{"name":"lookup","arguments":"{\"n\":"}}]},"finish_reason":%q}]}`, finish) + "\n\n" +
							`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}` + "\n\ndata: [DONE]\n\n", true
					})
					body := fmt.Sprintf(`{"model":"cn:glm-5.2","stream":%t,"store":false,"input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}]}`, stream)
					if path == "/v1/messages" {
						body = fmt.Sprintf(`{"model":"cn:glm-5.2","stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, stream)
					}
					logs := reqlog.New(reqlog.Config{})
					h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999}, &auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999}), Upstream: up, RequestLog: logs})
					req := httptest.NewRequest("POST", path, strings.NewReader(body))
					req.Header.Set("Anthropic-Version", "2023-06-01")
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					success := path == "/v1/responses" && (finish == "length" || finish == "content_filter")
					if calls != 1 {
						t.Fatal("generation replayed", calls)
					}
					if success {
						if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"incomplete"`) || !strings.Contains(rec.Body.String(), `"call_id":"cut"`) {
							t.Fatal(rec.Code, rec.Body)
						}
					} else if !stream && rec.Code != 502 {
						t.Fatal(rec.Code, rec.Body)
					}
					for _, marker := range []string{"response.completed", "message_stop", "response.function_call_arguments", `"type":"tool_use"`} {
						if strings.Contains(rec.Body.String(), marker) {
							t.Fatal("executable tool leaked", rec.Body)
						}
					}
					entries := logs.Snapshot().Recent
					if len(entries) != 1 || entries[0].OK != success || entries[0].Attempts != 1 {
						t.Fatal(entries)
					}
					// Explicit cutoff consumed the usage tail; failures at finish
					// may legitimately stop before that tail was observed.
					if finish == "length" || finish == "content_filter" {
						if entries[0].PromptTokens != 5 || entries[0].CompletionTokens != 3 || entries[0].TotalTokens != 8 {
							t.Fatal("raw accounting changed", entries)
						}
					}
				})
			}
		}
	}
}
