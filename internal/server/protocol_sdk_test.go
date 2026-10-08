package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// sdkImageDataURL 与 Python 端的 8 字节 PNG 头一致。
const sdkImageDataURL = "data:image/png;base64,iVBORw0KGgo="

// sdkImageRequest 校验官方 SDK 发出的图片请求在出站 chat 报文里的形态：
// Responses input_image / Messages image block 都必须变成上游唯一接受的
// image_url 对象形态，且文本/图片 part 顺序保持。
func sdkImageRequest(obj map[string]any) error {
	msgs, _ := obj["messages"].([]any)
	if len(msgs) < 1 {
		return fmt.Errorf("image request lost messages")
	}
	parts, ok := msgs[0].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 2 {
		return fmt.Errorf("image parts lost: %#v", msgs[0])
	}
	imageURL, ok := parts[0].(map[string]any)["image_url"].(map[string]any)
	if !ok || imageURL["url"] != sdkImageDataURL {
		return fmt.Errorf("image not normalized to object form: %#v", parts[0])
	}
	if text, _ := parts[1].(map[string]any)["text"].(string); text != "看图" {
		return fmt.Errorf("text part order changed: %#v", parts)
	}
	return nil
}

// sdkToolImageRequest 校验工具结果图片的默认抬升：tool 消息只留文本，图片出现在
// 紧随整批工具结果之后的 user 消息里（而不是 tool 多模态形态）。
func sdkToolImageRequest(obj map[string]any) error {
	msgs, _ := obj["messages"].([]any)
	if len(msgs) != 3 {
		return fmt.Errorf("hoisted tool image layout: %#v", msgs)
	}
	tool, _ := msgs[1].(map[string]any)
	if tool["role"] != "tool" {
		return fmt.Errorf("tool message lost")
	}
	parts, _ := tool["content"].([]any)
	if len(parts) != 1 || parts[0].(map[string]any)["text"] != "captured" {
		return fmt.Errorf("tool content must keep text only: %#v", tool["content"])
	}
	hoisted, _ := msgs[2].(map[string]any)
	if hoisted["role"] != "user" {
		return fmt.Errorf("hoisted images must be a user message: %#v", hoisted)
	}
	attached, _ := hoisted["content"].([]any)
	if len(attached) != 3 {
		return fmt.Errorf("hoisted parts: %#v", attached)
	}
	if header, _ := attached[0].(map[string]any)["text"].(string); !strings.HasPrefix(header, "Attached image(s) from tool result") {
		return fmt.Errorf("hoist header lost: %#v", attached[0])
	}
	imageURL, ok := attached[2].(map[string]any)["image_url"].(map[string]any)
	if !ok || imageURL["url"] != sdkImageDataURL {
		return fmt.Errorf("hoisted image lost: %#v", attached[2])
	}
	return nil
}

func sdkNamespaceNames(model string) []string {
	if strings.Contains(model, "long") {
		return []string{"nsh_a5a9H0ShJNLfYwTpuH8hgP5pqmDgXwoD0LZ95bRz2Uw", "nsh_xEZT841iGZKhpanpwRrIyzy4VhHHFvd7GKvmYwJaDgg"}
	}
	return []string{"ns_3_crm_lookup", "ns_2_fs_lookup"}
}

func sdkNamespaceRequest(obj map[string]any, results int) error {
	model := obj["model"].(string)
	retired := strings.HasSuffix(model, "retired")
	names := sdkNamespaceNames(model)
	tools, _ := obj["tools"].([]any)
	if retired {
		if _, exists := obj["tools"]; exists || results != 2 || obj["tool_choice"] != "none" {
			return fmt.Errorf("retired namespace unexpectedly authorized")
		}
	} else if len(tools) != 2 {
		return fmt.Errorf("namespace tool declarations lost")
	}
	for i, v := range tools {
		tool := v.(map[string]any)
		fn := tool["function"].(map[string]any)
		if tool["type"] != "function" || fn["name"] != names[i] || fn["description"] != "keep child description" {
			return fmt.Errorf("incorrect namespace wire declaration")
		}
	}
	if results > 0 {
		seen := 0
		for _, v := range obj["messages"].([]any) {
			m := v.(map[string]any)
			if calls, ok := m["tool_calls"].([]any); ok {
				if len(calls) != 2 {
					return fmt.Errorf("namespace tool history was split or lost")
				}
				for i, name := range names {
					fn := calls[i].(map[string]any)["function"].(map[string]any)
					if fn["name"] != name {
						return fmt.Errorf("namespace history identity changed")
					}
				}
			}
			if m["role"] == "tool" {
				parts, ok := m["content"].([]any)
				if retired {
					if !ok || len(parts) != 0 {
						return fmt.Errorf("empty tool result not retained as []")
					}
				} else if !ok || len(parts) != 2 || parts[0].(map[string]any)["text"] != " A\n" || parts[1].(map[string]any)["text"] != "B " {
					return fmt.Errorf("tool output text block order/whitespace lost")
				}
				seen++
			}
		}
		if seen != 2 {
			return fmt.Errorf("missing namespace tool results")
		}
	}
	return nil
}

// Opt-in: WB2A_SDK_PYTHON must point to a Python with openai/anthropic installed.
// All HTTP stays on loopback or the fake RoundTripper. No WorkBuddy credentials,
// package installation, model call or network dependency is needed by go test.
func TestProtocolSDKSmoke(t *testing.T) {
	python := os.Getenv("WB2A_SDK_PYTHON")
	if python == "" {
		t.Skip("set WB2A_SDK_PYTHON to run official SDK smoke tests")
	}
	usage := `data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3,"cache_read_input_tokens":0,"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":2}}}` + "\n\ndata: [DONE]\n\n"
	up := &upstream.Client{ChatBaseCN: "https://sdk-test.invalid", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		obj, err := jsondoc.Object(raw)
		if err != nil {
			return nil, err
		}
		if obj["stream"] != true || r.Header.Get("Authorization") != "Bearer fake-upstream-token" {
			return nil, fmt.Errorf("unexpected fake upstream request")
		}
		for _, k := range []string{"input", "store", "system", "max_output_tokens"} {
			if _, ok := obj[k]; ok {
				return nil, fmt.Errorf("foreign upstream field %s", k)
			}
		}
		model, _ := obj["model"].(string)
		text, finish := "你好", "stop"
		var results int
		for _, v := range obj["messages"].([]any) {
			m := v.(map[string]any)
			if m["role"] == "tool" {
				results++
				if model == "tools-empty" {
					parts, ok := m["content"].([]any)
					if !ok || len(parts) != 0 {
						return nil, fmt.Errorf("SDK empty result changed on wire")
					}
				}
			}
			if m["role"] == "assistant" && m["tool_calls"] != nil {
				calls := m["tool_calls"].([]any)
				fn := calls[0].(map[string]any)["function"].(map[string]any)
				args, err := jsondoc.Object([]byte(fn["arguments"].(string)))
				if err != nil || args["n"] != json.Number("9007199254740993") {
					return nil, fmt.Errorf("tool history lost numeric precision")
				}
			}
		}
		if strings.HasPrefix(model, "namespace") {
			if err := sdkNamespaceRequest(obj, results); err != nil {
				return nil, err
			}
		}
		switch model {
		case "images":
			if err := sdkImageRequest(obj); err != nil {
				return nil, err
			}
		case "images-tool":
			if err := sdkToolImageRequest(obj); err != nil {
				return nil, err
			}
			text = "工具完成"
		default:
			if results > 0 {
				if results != 2 {
					return nil, fmt.Errorf("missing parallel tool result")
				}
				text = "工具完成"
			}
		}
		if model == "length" {
			finish = "length"
		}
		body := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":%q}]}\n\n", text, finish)
		if strings.HasPrefix(model, "tools") && results == 0 {
			first, second := "look", "up"
			if model == "tools-repeated" {
				first, second = "lookup", "lookup"
			}
			if model == "tools-cumulative" {
				first, second = "look", "lookup"
			}
			body = `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"","arguments":"{"}},{"index":1,"id":"b","type":"function","function":{"name":"lookup","arguments":"{\"n\":2}"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":"","function":{"name":"` + first + `","arguments":"\"n\":"}},{"index":1,"function":{"name":"lookup"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"` + second + `","arguments":"9007199254740993}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"
		}
		if strings.HasPrefix(model, "namespace") && results == 0 {
			finish := "tool_calls"
			args := `{"n":9007199254740993}`
			if model == "namespace-cutoff" {
				finish, args = "length", `{"n":`
			}
			names := sdkNamespaceNames(model)
			body = fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":%q,"arguments":""}},{"index":1,"id":"b","type":"function","function":{"name":%q,"arguments":"{\"n\":2}"}}]}}]}`, names[0], names[1]) + "\n\n" +
				fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":%q,"arguments":%q}}]},"finish_reason":%q}]}`, names[0], args, finish) + "\n\n"
		}
		body += usage
		if model == "partial-tool" || model == "filtered-tool" || model == "bad-tool" {
			finish, args := "length", `{"n":`
			if model == "filtered-tool" {
				finish = "content_filter"
			}
			if model == "bad-tool" {
				args = `{"n":]`
			}
			body = fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"checking\"}}]}\n\n"+
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"cut\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":%q}}]},\"finish_reason\":%q}]}\n\n", args, finish) + usage
		}
		if model == "truncated" {
			body = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "sdk-test", AccessToken: "fake-upstream-token", ExpiresAt: 9999999999}), Upstream: up, APIKey: "sdk-test-key"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "../../scripts/protocol_sdk_smoke.py", srv.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("SDK smoke: %v\n%s", err, output)
	}
	t.Log(string(output))
}
