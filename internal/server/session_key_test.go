package server

import (
	"encoding/json"
	"testing"

	"workbuddy_manager/internal/media"
	"workbuddy_manager/internal/protocol"
	"workbuddy_manager/internal/session"
)

func TestBodySessionKeyPreservesOriginalProtocol(t *testing.T) {
	old := media.CurrentToolPolicy()
	if err := media.SetToolPolicy(media.ToolPolicyHoist); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.SetToolPolicy(old) })
	cases := []struct {
		name    string
		kind    protocol.Kind
		raw     string
		mutated bool
	}{
		{"native-derived", protocol.Chat, `{"model":"glm-5.2","messages":[{"role":"system","content":"system"},{"role":"user","content":[{"type":"text","text":" hi "},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`, false},
		{"native-explicit", protocol.Chat, `{"model":"glm-5.2","session_id":"session","prompt_cache_key":"cache","messages":[{"role":"user","content":"hi"}]}`, false},
		{"native-suppressed", protocol.Chat, `{"model":"glm-5.2","metadata":{"user_id":"user"},"messages":[{"role":"user","content":"hi"}]}`, false},
		{"native-hoist", protocol.Chat, `{"model":"glm-5.2","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`, true},
		{"responses-no-derived-chat", protocol.Responses, `{"model":"glm-5.2","input":"hi"}`, false},
		{"responses-cache", protocol.Responses, `{"model":"glm-5.2","input":"hi","prompt_cache_key":"cache"}`, false},
		{"anthropic-metadata", protocol.Anthropic, `{"model":"glm-5.2","max_tokens":100,"metadata":{"user_id":"{\"session_id\":\"claude-session\"}"},"messages":[{"role":"user","content":"hi"}]}`, false},
		{"anthropic-original-system", protocol.Anthropic, `{"model":"glm-5.2","max_tokens":100,"system":"system outside messages","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			r, err := protocol.Decode(tc.kind, raw)
			if err != nil {
				t.Fatal(err)
			}
			if r.Mutated != tc.mutated {
				t.Fatalf("mutated=%v want %v", r.Mutated, tc.mutated)
			}
			before, _ := json.Marshal(r)
			want := session.BodyKey(raw, r.Chat.PromptCacheKey)
			if got := bodySessionKey(r, raw); got != want {
				t.Fatalf("key=%q want original key %q", got, want)
			}
			after, _ := json.Marshal(r)
			if string(before) != string(after) {
				t.Fatal("key extraction mutated request")
			}
			if tc.name == "responses-no-derived-chat" && want != "" {
				t.Fatal("Responses input must not become Chat-derived key")
			}
			if tc.name == "native-hoist" && want != "" {
				t.Fatal("inserted user must not become session material")
			}
		})
	}
}
