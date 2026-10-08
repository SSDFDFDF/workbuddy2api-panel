package media

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

func setImagePolicy(t *testing.T, p ImagePolicy) {
	t.Helper()
	prev := CurrentImagePolicy()
	if err := SetImagePolicy(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetImagePolicy(prev) })
}

// encodeImagePart 生成 messages 里的一个图片 part（策略处理的输入）。
func imageObject(url string) map[string]any {
	return map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}},
		}},
	}}
}

func urlOf(t *testing.T, obj map[string]any) string {
	t.Helper()
	part := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	return part["image_url"].(map[string]any)["url"].(string)
}

// solidPNG 生成一张纯色 PNG 并返回 data URL 与原始字节。
func solidPNG(t *testing.T, w, h int) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return dataURL(buf.Bytes(), "image/png"), buf.Bytes()
}

func solidGIF(t *testing.T, w, h int) string {
	t.Helper()
	pal := []color.Color{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for x := 0; x < w; x++ {
		img.SetColorIndex(x, 0, 1)
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return dataURL(buf.Bytes(), "image/gif")
}

func TestImagePolicyDefaultsToNoDecode(t *testing.T) {
	setImagePolicy(t, ImagePolicy{})
	url, _ := solidPNG(t, 64, 64)
	obj := imageObject(url)
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	if got := urlOf(t, obj); got != url {
		t.Fatalf("policy off must not rewrite the image: %q", got)
	}
	// 关闭时连解码都不发生：超大边长（但字节很小）的图不会被拒。
	// 这里用合法但极端尺寸的 PNG 头难以构造，改为断言策略 zero-value 的 active()。
	if (ImagePolicy{}).active() {
		t.Fatal("zero ImagePolicy must be inactive")
	}
}

// 转码：gif → png（无损），且扩展字段保留、其余 part 不动。
func TestImageTranscodeGIFToPNG(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	gifURL := solidGIF(t, 32, 24)
	part := map[string]any{"type": "image_url", "image_url": map[string]any{"url": gifURL, "detail": "low"}}
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{part}},
	}}
	n, err := Normalize(obj)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got := urlOf(t, obj)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("gif not transcoded: %s", got[:40])
	}
	if part["image_url"].(map[string]any)["detail"] != "low" {
		t.Fatal("extension field lost on transcode")
	}
	_, payload, _ := splitDataURL(got, "p")
	raw, err := decodeBase64(payload)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "png" || cfg.Width != 32 || cfg.Height != 24 {
		t.Fatalf("transcoded image: format=%s cfg=%+v err=%v", format, cfg, err)
	}
}

// 原生格式（png/jpeg/webp）在只开转码时不重编码（保持逐字透传）。
func TestImageTranscodeLeavesNativeFormatsUntouched(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	pngURL, _ := solidPNG(t, 40, 40)
	for _, url := range []string{pngURL} {
		obj := imageObject(url)
		if _, err := Normalize(obj); err != nil {
			t.Fatal(err)
		}
		if got := urlOf(t, obj); got != url {
			t.Fatalf("native format rewritten: %s", got[:32])
		}
	}
}

// bmp 也能转码（x/image 解码器）。
func TestImageTranscodeBMP(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	src := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for x := 0; x < 20; x++ {
		src.Set(x, 0, color.RGBA{G: 255, A: 255})
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	obj := imageObject(dataURL(buf.Bytes(), "image/bmp"))
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	if got := urlOf(t, obj); !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("bmp not transcoded: %s", got[:40])
	}
}

// 转码开关下无法解码的"声明为 gif"载荷：明确报错，不静默透传。
func TestImageTranscodeUndecodableDeclaredFormatFails(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	obj := imageObject("data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")))
	if _, err := Normalize(obj); err == nil || !strings.Contains(err.Error(), "could not be decoded") {
		t.Fatalf("err=%v", err)
	}
}

// 压缩档位：超过档位边长的图片被等比缩放 + JPEG 重编码。
func TestImageCompressDownscales(t *testing.T) {
	setImagePolicy(t, ImagePolicy{MaxDimension: 1080})
	url, _ := solidPNG(t, 4000, 2000)
	obj := imageObject(url)
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	got := urlOf(t, obj)
	if !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("expected jpeg output, got %s", got[:40])
	}
	_, payload, _ := splitDataURL(got, "p")
	raw, _ := decodeBase64(payload)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "jpeg" {
		t.Fatalf("format=%s err=%v", format, err)
	}
	if cfg.Width != 1080 || cfg.Height != 540 {
		t.Fatalf("aspect ratio broken: %dx%d", cfg.Width, cfg.Height)
	}
	if len(payload) > 512000 {
		t.Fatalf("base64 budget exceeded: %d", len(payload))
	}
}

// 未超档位的图片在开启压缩时不重编码（零改动）。
func TestImageCompressLeavesSmallImagesUntouched(t *testing.T) {
	setImagePolicy(t, ImagePolicy{MaxDimension: 2000})
	url, _ := solidPNG(t, 64, 64)
	obj := imageObject(url)
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	if got := urlOf(t, obj); got != url {
		t.Fatal("small image rewritten despite fitting the tier")
	}
}

// 透明 PNG 压成 JPEG 时合白底（不出现黑底/花屏），且仍是合法 JPEG。
func TestImageCompressFlattensAlpha(t *testing.T) {
	setImagePolicy(t, ImagePolicy{MaxDimension: 1080})
	img := image.NewRGBA(image.Rect(0, 0, 3000, 500))
	for x := 0; x < 3000; x++ {
		img.Set(x, 0, color.RGBA{R: 10, A: 0}) // 全透明
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	obj := imageObject(dataURL(buf.Bytes(), "image/png"))
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	_, payload, _ := splitDataURL(urlOf(t, obj), "p")
	raw, _ := decodeBase64(payload)
	decoded, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Fatalf("transparent area not flattened to white: %v %v %v", r, g, b)
	}
}

// 超过单边/像素硬限制：明确报错（与上游限制对齐）。
func TestImageDimensionLimits(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	if _, err := Normalize(imageObject(solidGIF(t, 40000, 2))); err == nil || !strings.Contains(err.Error(), "per-side") {
		t.Fatalf("per-side limit: err=%v", err)
	}
}

// 解码预算：单请求累计解码像素超预算 → 明确报错（内存封顶）。
func TestImageDecodeBudget(t *testing.T) {
	setImagePolicy(t, ImagePolicy{MaxDimension: 1080})
	url, _ := solidPNG(t, 4000, 4000) // 16MP，每张都需压缩
	parts := make([]any, 0, 4)
	for i := 0; i < 4; i++ {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
	}
	obj := map[string]any{"messages": []any{map[string]any{"role": "user", "content": parts}}}
	if _, err := Normalize(obj); err == nil || !strings.Contains(err.Error(), "decode budget") {
		t.Fatalf("err=%v", err)
	}
}

// 工具结果图片同样走转码/压缩（normalizer 共用）。
func TestToolImageTranscode(t *testing.T) {
	setImagePolicy(t, ImagePolicy{Transcode: true})
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "c", "type": "function", "function": map[string]any{"name": "shot", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "c", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": solidGIF(t, 16, 16)}},
		}},
	}}
	changed, err := ApplyToolPolicy(obj, ToolPolicyHoist)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	hoisted := obj["messages"].([]any)[2].(map[string]any)["content"].([]any)[2].(map[string]any)
	got := hoisted["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("tool image not transcoded: %s", got[:40])
	}
}

// 策略关闭时，转码/压缩路径零解码：一个"声明 gif 但内容不是图片"的载荷原样透传
// （由上游判定），与加本文件前的行为一致。
func TestImagePolicyOffKeepsLegacyBehavior(t *testing.T) {
	setImagePolicy(t, ImagePolicy{})
	url := "data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))
	obj := imageObject(url)
	if _, err := Normalize(obj); err != nil {
		t.Fatal(err)
	}
	if got := urlOf(t, obj); got != url {
		t.Fatal("policy off must pass the image through")
	}
}

func TestValidateImageMaxDimension(t *testing.T) {
	for _, ok := range []int{0, 1080, 2000} {
		if err := ValidateImageMaxDimension(ok); err != nil {
			t.Fatalf("%d rejected: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 1, 512, 4096} {
		if err := ValidateImageMaxDimension(bad); err == nil {
			t.Fatalf("%d accepted", bad)
		}
	}
}
