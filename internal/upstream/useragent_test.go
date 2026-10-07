package upstream

import (
	"net/http"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func TestV2RealmIdentity(t *testing.T) {
	for _, realm := range []string{"cn", "global"} {
		a := &auth.Auth{UID: realm}
		_, _ = auth.BackfillRealmFor(a, realm)
		c := New()
		want := "WorkBuddy/5.7.6 WorkBuddy/5.7.6 CLI/2.156.0"
		ver := "5.7.6"
		if realm == "global" {
			want = "WorkBuddy/5.6.2 WorkBuddy AI/5.6.2 CLI/2.147.0"
			ver = "5.6.2"
		}
		for _, host := range []string{"https://www.workbuddy.cn", "https://www.workbuddy.ai", "https://proxy.invalid"} {
			r, _ := http.NewRequest("POST", host, nil)
			c.ChatHeaders(r, a, "", ChatMeta{})
			if r.UserAgent() != want || r.Header.Get("X-IDE-Version") != ver {
				t.Fatal(r.Header)
			}
			if r.Header.Get("X-Refresh-Token") != "" {
				t.Fatal("refresh leaked")
			}
		}
		r, _ := http.NewRequest("GET", "https://billing.invalid", nil)
		c.BillingHeaders(r, a)
		if r.UserAgent() != want {
			t.Fatal(r.Header)
		}
	}
}
func TestV2PurposeOverride(t *testing.T) {
	c := New()
	c.Profiles = map[string]IdentityProfile{"global": {ClientVersion: "6.0.0", CLIVersion: "3.0.0", UserAgents: map[string]string{"chat": "explicit/1"}}}
	a := &auth.Auth{}
	_, _ = auth.BackfillRealmFor(a, "global")
	if c.purposeUA(a, "chat") != "explicit/1" || !strings.Contains(c.purposeUA(a, "refresh"), "6.0.0") || strings.Contains(c.purposeUA(nil, "chat"), "6.0.0") {
		t.Fatal("override leaked across realm/purpose")
	}
}
func TestV2RefreshSnapshotRealm(t *testing.T) {
	a := &auth.Auth{UID: "u", AccessToken: "old", RefreshToken: "r"}
	_, _ = auth.BackfillRealmFor(a, "global")
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "global.invalid" || !strings.Contains(r.UserAgent(), "WorkBuddy AI/5.6.2") || r.Header.Get("Origin") != "https://www.workbuddy.ai" {
			t.Fatal(r.URL, r.Header)
		}
		return jsonResp(200, `{"code":0,"data":{"accessToken":"new","refreshToken":"new-r","expiresIn":3600}}`), nil
	})
	c.GlobalEnabled = true
	c.ChatBaseGlobal = "https://global.invalid"
	if err := c.RefreshToken(a); err != nil {
		t.Fatal(err)
	}
}
func TestV2ConversationHeaders(t *testing.T) {
	c := New()
	r, _ := http.NewRequest("POST", "https://x.invalid", nil)
	a := &auth.Auth{UID: "u"}
	c.ChatHeaders(r, a, "", ChatMeta{ConversationID: "conv", ConversationRequestID: "turn", RootRequestID: "root", ParentConversationID: "parent", AgentType: "subagent", Traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"})
	for k, v := range map[string]string{"X-Conversation-ID": "conv", "X-Conversation-Request-ID": "turn", "X-Root-Request-ID": "root", "X-Parent-Conversation-ID": "parent", "X-Agent-Type": "subagent", "X-B3-TraceId": "0123456789abcdef0123456789abcdef"} {
		if r.Header.Get(k) != v {
			t.Fatal(k, r.Header)
		}
	}
}
