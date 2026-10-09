package upstream

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"workbuddy_manager/internal/auth"
)

// IdentityProfile is a realm-scoped identity. Purpose overrides never change realm.
type IdentityProfile struct {
	ClientVersion   string            `json:"client_version"`
	CLIVersion      string            `json:"cli_version"`
	ProductName     string            `json:"product_name"`
	ApplicationName string            `json:"application_name"`
	Origin          string            `json:"origin"`
	Language        string            `json:"language"`
	UserAgents      map[string]string `json:"user_agents,omitempty"`
}

func DefaultIdentity(realm string) IdentityProfile {
	if realm == "global" {
		return IdentityProfile{ClientVersion: "5.6.2", CLIVersion: "2.147.0", ProductName: "WorkBuddy AI", ApplicationName: "workbuddy-ai", Origin: originRefererGlobal, Language: "en-US"}
	}
	return IdentityProfile{ClientVersion: defaultClientVersion, CLIVersion: defaultCliVersion, ProductName: "WorkBuddy", ApplicationName: "WorkBuddy", Origin: originRefererCN, Language: "zh-CN"}
}
func (c *Client) identity(a *auth.Auth) IdentityProfile {
	realm := "cn"
	if a != nil {
		realm = a.Realm()
	}
	p := DefaultIdentity(realm)
	if c != nil {
		if v, ok := c.optsNow().Profiles[realm]; ok {
			if v.ClientVersion != "" {
				p.ClientVersion = v.ClientVersion
			}
			if v.CLIVersion != "" {
				p.CLIVersion = v.CLIVersion
			}
			if v.ProductName != "" {
				p.ProductName = v.ProductName
			}
			if v.ApplicationName != "" {
				p.ApplicationName = v.ApplicationName
			}
			if v.Origin != "" {
				p.Origin = v.Origin
			}
			if v.Language != "" {
				p.Language = v.Language
			}
			p.UserAgents = v.UserAgents
		}
	}
	return p
}
func (c *Client) purposeUA(a *auth.Auth, purpose string) string {
	p := c.identity(a)
	if v := p.UserAgents[purpose]; v != "" {
		return v
	}
	switch purpose {
	case "banner":
		return "WorkBuddy/" + p.ClientVersion
	case "desktop":
		return p.ApplicationName + "/" + p.ClientVersion + " " + p.ApplicationName + "/" + p.ClientVersion + " CLI/" + p.CLIVersion
	default:
		return "WorkBuddy/" + p.ClientVersion + " " + p.ProductName + "/" + p.ClientVersion + " CLI/" + p.CLIVersion
	}
}
func ValidateIdentity(p IdentityProfile) error {
	for _, v := range []string{p.ClientVersion, p.CLIVersion, p.ProductName, p.ApplicationName, p.Origin, p.Language} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid profile header value")
		}
	}
	if p.Origin != "" {
		u, e := url.Parse(p.Origin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("profile origin must be an HTTPS origin")
		}
	}
	for k, v := range p.UserAgents {
		switch k {
		case "chat", "refresh", "catalog", "billing", "banner", "desktop":
		default:
			return fmt.Errorf("unknown UA purpose %q", k)
		}
		if strings.ContainsAny(v, "\r\n") || len(v) > 1024 {
			return fmt.Errorf("invalid user agent")
		}
	}
	return nil
}

// noRedirectClient prevents credential-bearing requests from following Location.
// A value copy leaves caller-owned client configuration immutable.
func noRedirectClient(c *http.Client) *http.Client {
	v := *c
	v.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &v
}

type refreshCall struct {
	done chan struct{}
	err  error
}
type refreshGroup struct {
	mu    sync.Mutex
	calls map[string]*refreshCall
}

func (g *refreshGroup) Do(key string, fn func() error) error {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*refreshCall{}
	}
	if call := g.calls[key]; call != nil {
		g.mu.Unlock()
		<-call.done
		return call.err
	}
	call := &refreshCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.calls, key); close(call.done); g.mu.Unlock() }()
	call.err = fn()
	return call.err
}
