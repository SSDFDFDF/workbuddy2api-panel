package media

import (
	"encoding/json"
	"strings"
	"testing"
)

// 8 字节的合法 PNG 头：base64("iVBORw0KGgo=")。
const tinyPNG = "data:image/png;base64,iVBORw0KGgo="

func TestNormalizeStringForm(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "rules"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "看图"},
			map[string]any{"type": "image_url", "image_url": tinyPNG},
		}},
	}}
	n, err := Normalize(obj)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	part := obj["messages"].([]any)[1].(map[string]any)["content"].([]any)[1].(map[string]any)
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok || imageURL["url"] != tinyPNG {
		t.Fatalf("string form not normalized: %#v", part)
	}
	if len(imageURL) != 1 {
		t.Fatalf("normalization invented fields: %#v", imageURL)
	}
}

func TestNormalizePreservesCanonicalObject(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{
				"url": tinyPNG, "detail": "high", "mime_type": "image/png",
			}},
		}},
	}}
	if n, err := Normalize(obj); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	imageURL := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)
	if imageURL["detail"] != "high" || imageURL["mime_type"] != "image/png" || imageURL["url"] != tinyPNG {
		t.Fatalf("object form changed: %#v", imageURL)
	}
}

func TestNormalizeRejections(t *testing.T) {
	for _, tc := range []struct {
		name, imageURL string
		want           string
	}{
		{"external-url", `"https://cdn.example.test/x.png"`, "external URLs"},
		{"non-image-data-url", `"data:text/plain;base64,aGk="`, "image/*"},
		{"no-base64", `"data:image/png,abc"`, "base64-encoded"},
		{"invalid-base64", `"data:image/png;base64,!!!not-base64!!!"`, "invalid base64"},
		{"empty-payload", `"data:image/png;base64,"`, "empty"},
		{"padded-middle", `"data:image/png;base64,iVBORw=0KGgo="`, "invalid base64"},
		{"bad-padding", `"data:image/png;base64,QQ="`, "invalid base64"},
		{"empty-string", `""`, "non-empty"},
		{"url-type", `5`, "object (or string form)"},
		{"object-no-url", `{"detail":"auto"}`, "non-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "image_url", "image_url": nil},
				}},
			}}
			// 用原始 JSON 值注入形态，避免类型断言把测试写死。
			part := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
			part["image_url"] = rawJSONValue(t, tc.imageURL)
			if _, err := Normalize(obj); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeRejectsUnsupportedMediaParts(t *testing.T) {
	for _, typ := range []string{"input_audio", "audio_url", "video_url", "file_url", "input_file"} {
		obj := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": typ, "data": "x"},
			}},
		}}
		_, err := Normalize(obj)
		if err == nil || !strings.Contains(err.Error(), typ) {
			t.Fatalf("%s: err=%v", typ, err)
		}
	}
}

func TestNormalizeCountLimit(t *testing.T) {
	parts := make([]any, 0, MaxImagesPerRequest+1)
	for i := 0; i <= MaxImagesPerRequest; i++ {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": tinyPNG})
	}
	obj := map[string]any{"messages": []any{map[string]any{"role": "user", "content": parts}}}
	if _, err := Normalize(obj); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeTotalBytesLimit(t *testing.T) {
	// 单图略小于上限（5033166 字节），5 张累计 25165830 > 24 MiB，触发总量限制。
	payload := strings.Repeat("A", 6710888)
	url := "data:image/png;base64," + payload
	parts := make([]any, 0, 5)
	for i := 0; i < 5; i++ {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": url})
	}
	obj := map[string]any{"messages": []any{map[string]any{"role": "user", "content": parts}}}
	if _, err := Normalize(obj); err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("err=%v", err)
	}
}

func TestDataURLSizeLimits(t *testing.T) {
	if _, err := DataURL(tinyPNG, "p"); err != nil {
		t.Fatal(err)
	}
	// 解码后恰好 5 MiB（5242880 字节）仍在限内。
	exact := strings.Repeat("A", 6990507) // (6990507+1)/4*3 - 1 = 5242880
	if size, err := DataURL("data:image/jpeg;base64,"+exact+"=", "p"); err != nil || size != MaxImageBytes {
		t.Fatalf("size=%d err=%v", size, err)
	}
	// 多 1 字节即拒绝。
	over := strings.Repeat("A", 6990508)
	if _, err := DataURL("data:image/jpeg;base64,"+over, "p"); err == nil || !strings.Contains(err.Error(), "exceeding") {
		t.Fatalf("err=%v", err)
	}
}

func TestDataURLToleratesWhitespaceAndUnpadded(t *testing.T) {
	if size, err := DataURL("data:image/png;base64,iVBO\nRw0K\r\nGgo=", "p"); err != nil || size != 8 {
		t.Fatalf("size=%d err=%v", size, err)
	}
	if size, err := DataURL("data:image/png;base64,iVBORw0KGgo", "p"); err != nil || size != 8 {
		t.Fatalf("unpadded size=%d err=%v", size, err)
	}
}

// 纯文本请求是热路径（每次 Parse 都会扫一遍消息）：图片校验不能给无媒体请求
// 引入分配。命中媒体 part 才构造错误路径字符串。
func TestNormalizeTextPathAllocations(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "rules"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "text", "text": "world"},
		}},
		map[string]any{"role": "tool", "tool_call_id": "a", "content": "result"},
	}}
	allocs := testing.AllocsPerRun(200, func() {
		if n, err := Normalize(obj); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})
	if allocs != 0 && !raceEnabled {
		t.Fatalf("text-only Normalize allocates %.1f objects per run", allocs)
	}
}

func TestHasImages(t *testing.T) {
	withImage := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "rules"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "image_url", "image_url": tinyPNG},
		}},
	}}
	if !HasImages(withImage) {
		t.Fatal("image part not detected")
	}
	for _, obj := range []map[string]any{
		{"messages": []any{map[string]any{"role": "user", "content": "text"}}},
		{"messages": []any{}},
		{},
	} {
		if HasImages(obj) {
			t.Fatalf("false positive: %#v", obj)
		}
	}
}

func TestPart(t *testing.T) {
	part := Part(tinyPNG, "")
	if part["type"] != "image_url" {
		t.Fatal(part)
	}
	if imageURL := part["image_url"].(map[string]any); imageURL["url"] != tinyPNG || len(imageURL) != 1 {
		t.Fatal(imageURL)
	}
	part = Part(tinyPNG, "low")
	if part["image_url"].(map[string]any)["detail"] != "low" {
		t.Fatal(part)
	}
}

// rawJSONValue 把测试用 JSON 片段解成 any（保留字符串/对象/数字形态）。
func rawJSONValue(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
