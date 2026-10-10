// modelcheck_test.go 模型目录门禁（modelcheck.go）的端到端与单元回归。
//
// 三条契约：
//  1. 目录可用 + 模型不存在 → 选号之前 404，**一个上游请求都不发**，
//     且不给任何账号写模型级负缓存（「不存在模型触发模型锁池」的根治点）；
//  2. 目录为空（未刷新过）→ fail-open，维持既有行为（上游 11102 兜底）；
//  3. 目录命中 → 重排候选域（global-only 模型先打 global，不拿 CN 号白撞 11102）
//     并纠正大小写。
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/upstream"
)

// gatedHandler 构造「CN 账号 + 目录快照」的 handler，返回上游调用计数。
func gatedHandler(t *testing.T, catalog []upstream.ModelInfo, behavior func(authz string) (int, string, bool)) (*Handler, *atomCount) {
	t.Helper()
	var calls atomCount
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls.add(1)
		return behavior(authz)
	})
	if len(catalog) > 0 {
		up.SetCatalogForTest("cn", catalog)
	}
	h := NewHandler(Config{
		Pool:     testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream: up,
	})
	return h, &calls
}

type atomCount struct{ n atomic.Int32 }

func (c *atomCount) add(d int32) { c.n.Add(d) }
func (c *atomCount) get() int32  { return c.n.Load() }

// TestGateRejectsUnknownModelBeforeAnyUpstreamCall：目录里有模型 A、客户端请求 B
// → 404 model_not_found，零上游调用，零模型锁池。
func TestGateRejectsUnknownModelBeforeAnyUpstreamCall(t *testing.T) {
	h, calls := gatedHandler(t, []upstream.ModelInfo{{ID: "glm-5.2"}}, func(string) (int, string, bool) {
		return 200, sseOK, true
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"totally-made-up-model","messages":[{"role":"user","content":"hi"}]}`)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s (want 404 model_not_found)", rec.Code, rec.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("resp not json: %v", err)
	}
	e := resp["error"].(map[string]any)
	if e["code"] != "model_not_found" {
		t.Errorf("code=%v", e["code"])
	}
	if hint, _ := e["gateway_hint"].(string); !strings.Contains(hint, "/v1/models") {
		t.Errorf("hint should point at /v1/models: %v", e["gateway_hint"])
	}
	if got := calls.get(); got != 0 {
		t.Errorf("unknown model must be rejected before any upstream call, got %d", got)
	}
	// 根治点断言：不给账号写模型级负缓存（旧行为会写 6h 起的 11102 条目，
	// 于是「模型锁池」里长出一个永不自愈的条目）。
	up := h.cfg.Upstream
	if _, _, ready := up.LookupCatalog("cn", "totally-made-up-model"); !ready {
		t.Fatal("catalog should be ready")
	}
	st, _ := h.cfg.Pool.Status("u1")
	if len(st.RateLimitedModels) != 0 {
		t.Errorf("rejected model must not lock the pool: %+v", st.RateLimitedModels)
	}
}

// TestGateFailsOpenOnEmptyCatalog：目录为空（未刷新过）→ 放行，由上游 11102 兜底。
// 关键：不把「没刷新过」误判成「模型不存在」。
func TestGateFailsOpenOnEmptyCatalog(t *testing.T) {
	h, calls := gatedHandler(t, nil, func(string) (int, string, bool) {
		return 200, sseOK, true
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"any-model","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty catalog must fail open: code=%d body=%s", rec.Code, rec.Body)
	}
	if got := calls.get(); got != 1 {
		t.Errorf("fail-open should reach upstream once, got %d", got)
	}
}

// TestGateCaseInsensitiveCanonicalizes：客户端大小写写错 → 放行并用**目录里的规范 id**
// 出站（上游对大小写变体同样回 11102，在网关侧顺手纠正），而不是 404。
func TestGateCaseInsensitiveCanonicalizes(t *testing.T) {
	var outbound atomic.Value
	up := newFakeUpstream(t, func(authz string) (int, string, bool) { return 200, sseOK, true })
	// 用自定义 transport 捕获出站 body 里的 model 字段。
	up.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		outbound.Store(string(raw))
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseOK)),
		}, nil
	})}
	up.SetCatalogForTest("cn", []upstream.ModelInfo{{ID: "glm-5.2"}})
	h := NewHandler(Config{
		Pool:     testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}),
		Upstream: up,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"GLM-5.2","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("case variant should be accepted via catalog: code=%d body=%s", rec.Code, rec.Body)
	}
	body, _ := outbound.Load().(string)
	if !strings.Contains(body, `"model":"glm-5.2"`) {
		t.Errorf("outbound model should be canonicalized to the catalog value: %s", body)
	}
}

// TestCheckModelReordersCandidateRealms：候选域重排——命中域优先，未命中域保留在后。
// 这样 global-only 模型在 auto 策略下先打 global，不再拿 CN 号白撞一次 11102
// （每次白撞都会给该 CN 号写 6h 模型负缓存）。
func TestCheckModelReordersCandidateRealms(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, sseOK, true })
	up.SetCatalogForTest("cn", []upstream.ModelInfo{{ID: "glm-5.2"}})
	up.SetCatalogForTest("global", []upstream.ModelInfo{{ID: "gpt-5.4"}})
	h := NewHandler(Config{Pool: testPoolWith(), Upstream: up})

	realms, bare, rejected := h.checkModel([]string{"cn", "global"}, "gpt-5.4")
	if rejected {
		t.Fatal("global-only model must not be rejected when cn has no such model but global does")
	}
	if bare != "gpt-5.4" || len(realms) != 2 || realms[0] != "global" {
		t.Errorf("realms=%v bare=%s (want global first)", realms, bare)
	}
	// 命中域的目录值成为规范名（大小写纠正）。
	if _, canon, _ := h.checkModel([]string{"cn"}, "GLM-5.2"); canon != "glm-5.2" {
		t.Errorf("canonical should be catalog value, got %q", canon)
	}
	// 全部候选域目录可用且都没有 → 拒绝。
	if _, _, rejected := h.checkModel([]string{"cn", "global"}, "nope-model"); !rejected {
		t.Error("unknown model with both catalogs ready must be rejected")
	}
	// 有一个域目录为空 → fail-open。
	up.SetCatalogForTest("global", nil)
	if _, _, rejected := h.checkModel([]string{"cn", "global"}, "nope-model"); rejected {
		t.Error("empty catalog in a candidate realm must fail open")
	}
}

// TestGateRejectionLoggedAsNotFound：拒绝路径也要落一行请求日志（状态 404），
// 否则面板「运行日志」看不到被门禁拦下的请求。
func TestGateRejectionLoggedAsNotFound(t *testing.T) {
	withChatLog(t)
	h, _ := gatedHandler(t, []upstream.ModelInfo{{ID: "glm-5.2"}}, func(string) (int, string, bool) {
		return 200, sseOK, true
	})
	rec := httptest.NewRecorder()
	line := captureStdout(t, func() {
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"ghost-model","messages":[{"role":"user","content":"hi"}]}`)))
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rec.Code)
	}
	if !strings.Contains(line, "ghost-model") || !strings.Contains(line, "404") {
		t.Errorf("rejection should be logged as 404: %q", line)
	}
}

// TestCheckModelCanonicalFromPrimaryRealm：规范名取**首个命中域**（选号优先级最高）
// 的目录拼写——出站模型名必须与将被选号的域逐字一致，不能被后面域的拼写覆盖。
func TestCheckModelCanonicalFromPrimaryRealm(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, sseOK, true })
	up.SetCatalogForTest("cn", []upstream.ModelInfo{{ID: "GLM-5.2"}}) // CN 目录拼写
	up.SetCatalogForTest("global", []upstream.ModelInfo{{ID: "glm-5.2"}})
	h := NewHandler(Config{Pool: testPoolWith(), Upstream: up})

	realms, canon, rejected := h.checkModel([]string{"cn", "global"}, "glm-5.2")
	if rejected {
		t.Fatal("both realms serve the model, must not reject")
	}
	if realms[0] != "cn" || canon != "GLM-5.2" {
		t.Errorf("realms=%v canon=%q (want cn first + CN catalog spelling)", realms, canon)
	}
}
