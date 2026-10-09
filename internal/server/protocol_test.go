package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/reqlog"
	"workbuddy_manager/internal/upstream"
)

func protocolRequest(path string, stream bool) *http.Request {
	body := fmt.Sprintf(`{"model":"cn:glm-5.2","store":false,"stream":%t,"input":"hi"}`, stream)
	if path == "/v1/messages" {
		body = fmt.Sprintf(`{"model":"cn:glm-5.2","max_tokens":100,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Anthropic-Version", "2023-06-01")
	r.Header.Set("Authorization", "Bearer secret")
	return r
}

func TestProtocolEndpointsAndAccounting(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprint(path, stream), func(t *testing.T) {
				var calls atomic.Int32
				up := &upstream.Client{ChatBaseCN: "https://fake.example", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					raw, _ := io.ReadAll(r.Body)
					var obj map[string]any
					if err := json.Unmarshal(raw, &obj); err != nil {
						t.Fatal(err)
					}
					if obj["model"] != "glm-5.2" || obj["stream"] != true || obj["messages"] == nil {
						t.Fatalf("wrong upstream request %s", raw)
					}
					for _, k := range []string{"input", "instructions", "store", "max_output_tokens", "system"} {
						if _, exists := obj[k]; exists {
							t.Fatalf("foreign field %s in %s", k, raw)
						}
					}
					if r.Header.Get("Authorization") != "Bearer at1" || r.Header.Get("X-Api-Key") != "" {
						t.Fatal("downstream credential leaked")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sseOK))}, nil
				})}}
				logs := reqlog.New(reqlog.Config{})
				p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
				h := NewHandler(Config{Pool: p, Upstream: up, APIKey: "secret", RequestLog: logs})
				r := protocolRequest(path, stream)
				if path == "/v1/messages" {
					r.Header.Del("Authorization")
					r.Header.Set("X-Api-Key", "secret")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				if rec.Code != 200 {
					t.Fatalf("%d %s", rec.Code, rec.Body)
				}
				if !strings.Contains(rec.Body.String(), "你好") || !strings.Contains(rec.Body.String(), "cn:glm-5.2") {
					t.Fatal(rec.Body.String())
				}
				if stream && !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatal(rec.Header())
				}
				if strings.Contains(rec.Body.String(), "[DONE]") || strings.Contains(rec.Body.String(), "chat.completion") {
					t.Fatal("native response leaked")
				}
				if calls.Load() != 1 {
					t.Fatal(calls.Load())
				}
				s := logs.Snapshot()
				if len(s.Recent) != 1 || s.Recent[0].Path != path || !s.Recent[0].OK || s.Recent[0].Attempts != 1 || s.Recent[0].TotalTokens != 2 {
					t.Fatalf("wrong accounting: %+v", s.Recent)
				}
			})
		}
	}
}

func TestMessagesAuthAndValidation(t *testing.T) {
	cases := []struct {
		name, key, bearer, version, beta string
		code                             int
	}{
		{"x-key", "secret", "", "2023-06-01", "", 200},
		{"bearer", "", "Bearer secret", "2023-06-01", "", 200},
		{"both", "secret", "Bearer secret", "2023-06-01", "", 200},
		{"conflict", "wrong", "Bearer secret", "2023-06-01", "", 401},
		{"conflict2", "secret", "Bearer wrong", "2023-06-01", "", 401},
		{"missing", "", "", "2023-06-01", "", 401},
		{"bad-version", "secret", "", "future", "", 400},
		{"missing-version", "secret", "", "", "", 400},
		{"beta", "secret", "", "2023-06-01", "tools-beta", 400},
		{"duplicate-key", "secret", "", "2023-06-01", "", 401},
		{"duplicate-bearer", "", "Bearer secret", "2023-06-01", "", 401},
		{"duplicate-version", "secret", "", "2023-06-01", "", 400},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var called int
			up := newFakeUpstream(t, func(string) (int, string, bool) { called++; return 200, sseOK, true })
			h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "u", AccessToken: "a", ExpiresAt: 9999999999}), Upstream: up, APIKey: "secret"})
			r := protocolRequest("/v1/messages", false)
			r.Header.Del("Authorization")
			r.Header.Del("Anthropic-Version")
			if tt.key != "" {
				r.Header.Set("X-Api-Key", tt.key)
			}
			if tt.bearer != "" {
				r.Header.Set("Authorization", tt.bearer)
			}
			if tt.version != "" {
				r.Header.Set("Anthropic-Version", tt.version)
			}
			if tt.beta != "" {
				r.Header.Set("Anthropic-Beta", tt.beta)
			}
			switch tt.name {
			case "duplicate-key":
				r.Header.Add("X-Api-Key", "secret")
			case "duplicate-bearer":
				r.Header.Add("Authorization", "Bearer secret")
			case "duplicate-version":
				r.Header.Add("Anthropic-Version", "2023-06-01")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tt.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if tt.code != 200 && (called != 0 || !strings.Contains(rec.Body.String(), `"type":"error"`)) {
				t.Fatalf("invalid auth error: %s calls %d", rec.Body, called)
			}
		})
	}
}

func TestProtocolNoReplayOnTruncation(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			var calls int
			up := newFakeUpstream(t, func(string) (int, string, bool) {
				calls++
				return 200, `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n", true
			})
			logs := reqlog.New(reqlog.Config{})
			h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "u1", AccessToken: "a1", ExpiresAt: 9999999999}, &auth.Auth{UID: "u2", AccessToken: "a2", ExpiresAt: 9999999999}), Upstream: up, RequestLog: logs})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, protocolRequest(path, stream))
			if calls != 1 {
				t.Fatalf("replayed %d calls", calls)
			}
			if !stream && rec.Code != 502 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if stream && !strings.Contains(rec.Body.String(), "error") {
				t.Fatal(rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "event: response.completed") || strings.Contains(rec.Body.String(), "event: message_stop") {
				t.Fatal("false successful terminal")
			}
			s := logs.Snapshot()
			if len(s.Recent) != 1 || s.Recent[0].OK || s.Recent[0].Attempts != 1 {
				t.Fatal(s.Recent)
			}
		}
	}
}

func TestUnsupportedProtocolInputNeverReachesUpstream(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) { t.Fatal("invalid request reached upstream"); return 200, sseOK, true })
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "u", AccessToken: "a", ExpiresAt: 9999999999}), Upstream: up})
	for path, body := range map[string]string{
		"/v1/responses": `{"model":"x","input":"hi","store":false,"previous_response_id":"other-user-response"}`,
		"/v1/messages":  `{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":100}}`,
	} {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Anthropic-Version", "2023-06-01")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 400 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
}
