package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
)

func TestToolNameResolutionRoutingAndNoReplay(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			for _, names := range [][]string{{"lookup"}, {"lookup", "lookuplookup"}, nil} {
				t.Run(fmt.Sprint(path, stream, names), func(t *testing.T) {
					calls := 0
					up := newFakeUpstream(t, func(string) (int, string, bool) {
						calls++
						return 200, `data: {"choices":[{"index":0,"delta":{"content":"checking"}}]}` + "\n\n" +
							`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}` + "\n\n" +
							`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
							`data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n", true
					})
					body := map[string]any{"model": "cn:glm-5.2", "stream": stream}
					tools := []any{}
					for _, name := range names {
						if path == "/v1/responses" {
							tools = append(tools, map[string]any{"type": "function", "name": name, "parameters": map[string]any{"type": "object"}, "strict": false})
						} else {
							tools = append(tools, map[string]any{"name": name, "input_schema": map[string]any{"type": "object"}})
						}
					}
					body["tools"] = tools
					if path == "/v1/responses" {
						body["store"] = false
						body["input"] = "hi"
					} else {
						body["max_tokens"] = 100
						body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
					}
					encoded, _ := json.Marshal(body)
					req := httptest.NewRequest("POST", path, strings.NewReader(string(encoded)))
					req.Header.Set("Anthropic-Version", "2023-06-01")
					logs := reqlog.New(reqlog.Config{})
					h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999}, &auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999}), Upstream: up, RequestLog: logs})
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					success := len(names) == 1
					if calls != 1 {
						t.Fatal("replayed", calls)
					}
					if success {
						if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"lookup"`) {
							t.Fatal(rec.Code, rec.Body)
						}
					} else {
						if !stream && rec.Code != 502 {
							t.Fatal(rec.Code, rec.Body)
						}
						for _, marker := range []string{"response.completed", "message_stop", "response.function_call_arguments.delta", `"type":"tool_use"`} {
							if strings.Contains(rec.Body.String(), marker) {
								t.Fatal("false tool success", rec.Body)
							}
						}
					}
					entries := logs.Snapshot().Recent
					if len(entries) != 1 || entries[0].OK != success || entries[0].Attempts != 1 {
						t.Fatal(entries)
					}
				})
			}
		}
	}
}
