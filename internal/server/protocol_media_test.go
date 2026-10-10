package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/media"
	"workbuddy_manager/internal/upstream"
)

// 8 字节合法 PNG 头。
const mediaTinyImage = "data:image/png;base64,iVBORw0KGgo="

// mediaCaptureUpstream 记录出站 chat body 并返回正常 SSE。
func mediaCaptureUpstream(sent *map[string]any, calls *int) *upstream.Client {
	return &upstream.Client{ChatBaseCN: "https://media-test.invalid", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			return nil, err
		}
		*sent = obj
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sseOK))}, nil
	})}}
}

// TestMediaImagesReachUpstream 验证三个入口的用户图片都以上游唯一接受的
// 对象形态（{"url":"data:..."}）出站。
func TestMediaImagesReachUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		check            func(t *testing.T, sent map[string]any)
	}{
		{
			name: "chat-string-form",
			path: "/v1/chat/completions",
			body: `{"model":"cn:glm-5.2","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"看图"},{"type":"image_url","image_url":"` + mediaTinyImage + `"}]}]}`,
			check: func(t *testing.T, sent map[string]any) {
				content := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)
				if len(content) != 2 || content[0].(map[string]any)["text"] != "看图" {
					t.Fatalf("content=%#v", content)
				}
				imageURL, ok := content[1].(map[string]any)["image_url"].(map[string]any)
				if !ok || imageURL["url"] != mediaTinyImage || len(imageURL) != 1 {
					t.Fatalf("image_url=%#v", content[1])
				}
			},
		},
		{
			name: "responses-input-image",
			path: "/v1/responses",
			body: `{"model":"cn:glm-5.2","store":false,"stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + mediaTinyImage + `","detail":"auto"},{"type":"input_text","text":"看图"}]}]}`,
			check: func(t *testing.T, sent map[string]any) {
				content := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)
				if len(content) != 2 || content[1].(map[string]any)["text"] != "看图" {
					t.Fatalf("order/content changed: %#v", content)
				}
				imageURL, ok := content[0].(map[string]any)["image_url"].(map[string]any)
				if !ok || imageURL["url"] != mediaTinyImage || imageURL["detail"] != "auto" {
					t.Fatalf("image_url=%#v", content[0])
				}
			},
		},
		{
			name: "messages-image-block",
			path: "/v1/messages",
			body: `{"model":"cn:glm-5.2","max_tokens":16,"stream":true,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"text","text":"看图"}]}]}`,
			check: func(t *testing.T, sent map[string]any) {
				content := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)
				if len(content) != 2 || content[1].(map[string]any)["text"] != "看图" {
					t.Fatalf("order/content changed: %#v", content)
				}
				imageURL, ok := content[0].(map[string]any)["image_url"].(map[string]any)
				if !ok || imageURL["url"] != mediaTinyImage {
					t.Fatalf("image_url=%#v", content[0])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			calls := 0
			h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Anthropic-Version", "2023-06-01")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
			}
			tc.check(t, sent)
		})
	}
}

// TestMediaRejectedBeforeAccountSelection 明确报错路径：非法媒体请求在选号前
// 就被拒绝（不消耗账号配额、不打上游）。
func TestMediaRejectedBeforeAccountSelection(t *testing.T) {
	oversize := "data:image/png;base64," + strings.Repeat("A", 7000000)
	for _, tc := range []struct {
		name, path, body, want string
		policy                 media.ToolPolicy
	}{
		{
			name: "chat-external-url",
			path: "/v1/chat/completions",
			body: `{"model":"cn:glm-5.2","stream":true,"messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.test/x.png"}]}]}`,
			want: "external URLs",
		},
		{
			name: "responses-oversize",
			path: "/v1/responses",
			body: `{"model":"cn:glm-5.2","store":false,"stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + oversize + `"}]}]}`,
			want: "exceeding",
		},
		{
			name:   "messages-tool-result-image",
			path:   "/v1/messages",
			body:   `{"model":"cn:glm-5.2","max_tokens":16,"stream":true,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"shot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}]}`,
			want:   "tool result images are not forwarded",
			policy: media.ToolPolicyReject,
		},
		{
			name: "responses-tool-document",
			path: "/v1/responses",
			body: `{"model":"cn:glm-5.2","store":false,"stream":true,"input":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":[{"type":"input_file","file_id":"file-1"}]}]}`,
			want: "no file part",
		},
		{
			name: "chat-unsupported-media-part",
			path: "/v1/chat/completions",
			body: `{"model":"cn:glm-5.2","stream":true,"messages":[{"role":"user","content":[{"type":"video_url","video_url":{"url":"data:video/mp4;base64,aGk="}}]}]}`,
			want: "video_url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.policy != "" {
				prev := media.CurrentToolPolicy()
				if err := media.SetToolPolicy(tc.policy); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = media.SetToolPolicy(prev) })
			}
			var sent map[string]any
			calls := 0
			h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Anthropic-Version", "2023-06-01")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body=%s want contains %q", rec.Body, tc.want)
			}
			if calls != 0 {
				t.Fatalf("rejected media request reached upstream: calls=%d", calls)
			}
		})
	}
}

// TestImageHintWithMixedContentMessages 回归：同一请求里 system 是字符串 content、
// user 是图片数组时，gateway_hint 仍须判定"请求确实带图"。旧 hasImagePart 按 body
// 反序列化会在字符串 content 上整体失败并漏判，11133 只给中性参数 hint。
func TestImageHintWithMixedContentMessages(t *testing.T) {
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		return 400, `{"code":11133,"msg":"model does not support image"}`, false
	})
	// 目录快照里 glm-5.2 未声明 supports_images（SupportsImages=false）：
	// gateway_hint 的「模型不支持图片」判定需要目录命中（hintContext 只读快照）。
	up.SetCatalogForTest("cn", []upstream.ModelInfo{{ID: "glm-5.2"}})
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: up})
	body := `{"model":"glm-5.2","stream":true,"messages":[{"role":"system","content":"rules"},{"role":"user","content":[{"type":"image_url","image_url":"` + mediaTinyImage + `"}]}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not support images") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

// TestToolImageHoistOnTheWire 桥接入口的工具结果图片默认抬升：出站 body 里 tool
// 消息只留文本/占位，图片以工具批次之后的 user 消息出现（上游不会因 tool 多模态
// 形态拒绝的写法）。
func TestToolImageHoistOnTheWire(t *testing.T) {
	body := `{"model":"cn:glm-5.2","store":false,"stream":true,"input":[` +
		`{"type":"function_call","call_id":"call_1","name":"shot","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"captured"},{"type":"input_image","image_url":"` + mediaTinyImage + `"}]}]}`
	var sent map[string]any
	calls := 0
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
	}
	msgs := sent["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%#v", msgs)
	}
	tool := msgs[1].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool=%#v", tool)
	}
	parts, _ := tool["content"].([]any)
	if len(parts) != 1 || parts[0].(map[string]any)["text"] != "captured" {
		t.Fatalf("tool content must keep text only: %#v", tool["content"])
	}
	user := msgs[2].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("hoisted message must be user: %#v", user)
	}
	hoisted, _ := user["content"].([]any)
	if len(hoisted) != 3 || hoisted[0].(map[string]any)["text"] != "Attached image(s) from tool result:" {
		t.Fatalf("hoisted content=%#v", hoisted)
	}
	imageURL, ok := hoisted[2].(map[string]any)["image_url"].(map[string]any)
	if !ok || imageURL["url"] != mediaTinyImage {
		t.Fatalf("hoisted image=%#v", hoisted[2])
	}
}

// TestChatToolImageStaysInlineByDefault 原生 Chat 默认透传：tool 消息里的图片
// 原样出站，网关不改写既有转发行为。
func TestChatToolImageStaysInlineByDefault(t *testing.T) {
	body := `{"model":"cn:glm-5.2","stream":true,"messages":[` +
		`{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"shot","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1","content":[{"type":"image_url","image_url":{"url":"` + mediaTinyImage + `"}}]}]}`
	var sent map[string]any
	calls := 0
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
	}
	msgs := sent["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages=%#v", msgs)
	}
	parts, ok := msgs[1].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("tool content=%#v", msgs[1])
	}
	imageURL, ok := parts[0].(map[string]any)["image_url"].(map[string]any)
	if !ok || imageURL["url"] != mediaTinyImage {
		t.Fatalf("inline image lost: %#v", parts[0])
	}
}

// TestChatToolImageHoistConfigured 显式配置后，原生 Chat 也走抬升（并因此走重新
// 序列化路径，未知扩展字段仍保留）。
func TestChatToolImageHoistConfigured(t *testing.T) {
	prev := media.CurrentToolPolicy()
	if err := media.SetToolPolicy(media.ToolPolicyHoist); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.SetToolPolicy(prev) })
	body := `{"model":"cn:glm-5.2","stream":true,"messages":[` +
		`{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"shot","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1","content":[{"type":"image_url","image_url":{"url":"` + mediaTinyImage + `"}}],"custom_big":9007199254740993}]}`
	var sent map[string]any
	calls := 0
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
	}
	msgs := sent["messages"].([]any)
	if len(msgs) != 3 || msgs[2].(map[string]any)["role"] != "user" {
		t.Fatalf("messages=%#v", msgs)
	}
	if got := msgs[1].(map[string]any)["content"]; got != "(see attached image)" {
		t.Fatalf("placeholder=%#v", got)
	}
	if msgs[1].(map[string]any)["custom_big"] == nil {
		t.Fatal("unknown extension lost on re-serialization path")
	}
}

// TestImageTranscodeOnTheWire 开启格式转码后，GIF 图片在出站 body 里变成 PNG
// （原生 Chat 与桥接入口共用 forwarding.Parse 的归一/处理路径）。
func TestImageTranscodeOnTheWire(t *testing.T) {
	prev := media.CurrentImagePolicy()
	if err := media.SetImagePolicy(media.ImagePolicy{Transcode: true, MaxDimension: 1080}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.SetImagePolicy(prev) })

	var buf bytes.Buffer
	gifEncode(t, &buf, 1200, 40)
	gifURL := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	body := `{"model":"cn:glm-5.2","stream":true,"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":"` + gifURL + `"}]}]}`
	var sent map[string]any
	calls := 0
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
	}
	part := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	got := part["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(got, "data:image/jpeg;base64,") && !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("image not transcoded on the wire: %.60s", got)
	}
	if strings.HasPrefix(got, "data:image/gif") {
		t.Fatal("gif reached upstream")
	}
}

// TestImagePolicyOffKeepsBytesVerbatim 默认（策略全关）时图片字节逐字出站：
// 同一 GIF 不会被重编码。
func TestImagePolicyOffKeepsBytesVerbatim(t *testing.T) {
	prev := media.CurrentImagePolicy()
	if err := media.SetImagePolicy(media.ImagePolicy{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.SetImagePolicy(prev) })

	var buf bytes.Buffer
	gifEncode(t, &buf, 60, 30)
	gifURL := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	body := `{"model":"cn:glm-5.2","stream":true,"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":"` + gifURL + `"}]}]}`
	var sent map[string]any
	calls := 0
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "media", AccessToken: "t", ExpiresAt: 9999999999}), Upstream: mediaCaptureUpstream(&sent, &calls)})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", rec.Code, calls, rec.Body)
	}
	part := sent["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if got := part["image_url"].(map[string]any)["url"].(string); got != gifURL {
		t.Fatal("policy off must forward the original image bytes")
	}
}

// gifEncode 生成一张渐变 GIF（测试用最小合法输入）。
func gifEncode(t *testing.T, buf *bytes.Buffer, w, h int) {
	t.Helper()
	pal := make(color.Palette, 0, 256)
	for i := 0; i < 256; i++ {
		pal = append(pal, color.RGBA{R: uint8(i), G: uint8(255 - i), B: 128, A: 255})
	}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.SetColorIndex(x, y, uint8((x+y)%256))
		}
	}
	if err := gif.Encode(buf, img, nil); err != nil {
		t.Fatal(err)
	}
}
