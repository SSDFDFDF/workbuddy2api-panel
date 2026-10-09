package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/reqlog"
)

func TestRetiredNamespaceRoutingDoesNotAuthorizeReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, forbidden := range []bool{false, true} {
			t.Run(fmt.Sprint(stream, forbidden), func(t *testing.T) {
				attempts := 0
				up := newFakeUpstream(t, func(string) (int, string, bool) {
					attempts++
					end := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
					if forbidden {
						end = `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"new","type":"function","function":{"name":"ns_3_crm_lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`
					}
					// Usage on an earlier valid frame remains observable even if
					// the following undeclared name aborts consumption immediately.
					return 200, `data: {"choices":[{"index":0,"delta":{"content":"checked"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}` + "\n\n" + end + "\n\n" +
						`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}` + "\n\ndata: [DONE]\n\n", true
				})
				body := fmt.Sprintf(`{"model":"cn:glm-5.2","store":false,"stream":%t,"input":[{"type":"function_call","call_id":"old","name":"lookup","namespace":"crm","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":[]}]}`, stream)
				logs := reqlog.New(reqlog.Config{})
				h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999}, &auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999}), Upstream: up, RequestLog: logs})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))
				if attempts != 1 || !forbidden && rec.Code != 200 || forbidden && !stream && rec.Code != 502 {
					t.Fatal(attempts, rec.Code, rec.Body)
				}
				if forbidden {
					for _, marker := range []string{"response.completed", "response.function_call", `"type":"function_call"`} {
						if strings.Contains(rec.Body.String(), marker) {
							t.Fatal("retired tool delivered", rec.Body)
						}
					}
					if stream && !strings.Contains(rec.Body.String(), "response.failed") {
						t.Fatal("missing stream failure", rec.Body)
					}
				}
				entries := logs.Snapshot().Recent
				if len(entries) != 1 || entries[0].OK == forbidden || entries[0].Attempts != 1 || entries[0].PromptTokens != 5 || entries[0].CompletionTokens != 3 {
					t.Fatal("incorrect accounting", entries)
				}
			})
		}
	}
}
