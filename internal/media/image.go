// image.go 图片像素级处理：格式转码 + 等比压缩。
//
// 对齐官方客户端 ImageUtils.prepareImageDataUrl 的三档行为（逆向记录见
// docs/WORKBUDDY_MULTIMODAL_ATTACHMENT_PROTOCOL.md §3.2）：
//   - 原生格式（jpeg/png/webp）直接透传，不重编码；
//   - 上游不支持的格式（gif/bmp/tiff）转 PNG（无损，与官方 jimp→PNG 同语义）；
//   - 超过档位边长/体积的图片等比缩放 + JPEG 质量阶梯（有损，官方同样用 JPEG）。
//
// 两条纪律：
//  1. **默认全关**：只有显式配置 `media.image_transcode` / `media.image_max_dimension`
//     才会解码像素；关闭时热路径仍只有 base64 形状/大小校验（与加本文件前一致）。
//  2. **不静默降级**：转码/压缩失败、超出解码预算、PNG 转码后仍超上游硬限制时，
//     明确 400 并给出可操作提示，不偷偷丢图、不换成占位文本。
//
// 与官方客户端的已知差异：Go 没有 webp 编码器，webp 只能解码后重编码为 PNG/JPEG；
// GIF 只保留首帧（动画信息丢失，官方转 PNG 同样如此）。
package media

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"

	// 注册 bmp/tiff/webp 解码器（image.DecodeConfig / image.Decode 依赖）。
	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const (
	// MaxImageSide 单边像素上限（官方 prepareImageDataUrl：32768px）。
	//
	// 与 MaxImagePixels / MaxDecodedPixels 一样，**只在像素策略开启时校验**：判断尺寸
	// 至少要解图片头，而“策略全关 = 不解码像素”是默认路径的承诺（热路径 0 分配）。
	MaxImageSide = 32768
	// MaxImagePixels 单图像素总数上限（官方：40MP）。
	MaxImagePixels = 40 << 20
	// MaxDecodedPixels 单次规范化（Normalize / ApplyToolPolicy 各一次）累计解码
	// 像素预算：解码后的 RGBA 约 4 字节/像素，预算把并发请求的内存放大封顶。
	MaxDecodedPixels = 40 << 20
)

// compressTier 压缩档位（对齐官方 CODEBUDDY_CODE_IMAGE_COMPRESSION_MAX_DIMENSION）：
// 边长上限 + base64 长度预算 + JPEG 质量阶梯（从高到低逐档尝试）。
type compressTier struct {
	dimension int
	budget    int
	qualities []int
}

var compressTiers = map[int]compressTier{
	1080: {dimension: 1080, budget: 512000, qualities: []int{85, 70, 50, 30}},
	2000: {dimension: 2000, budget: 5 << 20, qualities: []int{80, 60, 40, 20}},
}

// ValidateImageMaxDimension 校验压缩档位取值：0 = 关闭，1080 / 2000 为内置两档。
func ValidateImageMaxDimension(v int) error {
	if v == 0 {
		return nil
	}
	if _, ok := compressTiers[v]; !ok {
		return fmt.Errorf("media.image_max_dimension: 0 (off), 1080 or 2000 required, got %d", v)
	}
	return nil
}

// normalizer 单次请求的图片规范化器：持有策略快照与解码像素预算。
type normalizer struct {
	policy        ImagePolicy
	decodedPixels int64
}

func newNormalizer() *normalizer { return &normalizer{policy: CurrentImagePolicy()} }

// image 校验一个内联图片 data URL，并按策略返回（可能被转码/压缩后的）URL 与其
// base64 解码后的字节数。策略关闭时只做形状/大小校验，返回原 URL（零解码、零重编码）。
// 字节数是调用方需要的唯一附带信息：这里顺手算出，避免调用方为同一 URL 再解析一遍。
func (n *normalizer) image(url, path string) (string, int, error) {
	mime, payload, err := splitDataURL(url, path)
	if err != nil {
		return "", 0, err
	}
	size, err := base64Size(payload)
	if err != nil {
		return "", 0, invalid(path, "invalid base64 image payload")
	}
	if !n.policy.active() {
		return url, size, nil
	}
	raw, err := decodeBase64(payload)
	if err != nil {
		return "", 0, invalid(path, "invalid base64 image payload")
	}
	cfg, format, cfgErr := image.DecodeConfig(bytes.NewReader(raw))
	if cfgErr != nil {
		// 解不出头部：只有「声明的类型本身就需要转码」才报错（用户开了转码开关，
		// 期待网关处理这类图）；其余未知格式原样交给上游判定（可能是上游新支持的
		// 格式，网关不冒充能力边界）。
		if mimeNeedsTranscode(mime) {
			return "", 0, invalid(path, "image could not be decoded for transcoding; re-encode it client-side")
		}
		return url, size, nil
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, invalid(path, "image dimensions are invalid")
	}
	if cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return "", 0, invalid(path, fmt.Sprintf("image is %dx%d, exceeding the %dpx per-side upstream limit", cfg.Width, cfg.Height, MaxImageSide))
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > MaxImagePixels {
		return "", 0, invalid(path, fmt.Sprintf("image is %dx%d (%.1fMP), exceeding the %.0fMP upstream limit", cfg.Width, cfg.Height, float64(pixels)/1e6, float64(MaxImagePixels)/1e6))
	}

	needConvert := mimeNeedsTranscode(mime) || formatNeedsTranscode(format)
	tier, compress := n.policy.tier()
	needCompress := compress && (maxInt(cfg.Width, cfg.Height) > tier.dimension || len(payload) > tier.budget)
	if !needConvert && !needCompress {
		return url, size, nil
	}
	if n.decodedPixels+pixels > MaxDecodedPixels {
		return "", 0, invalid(path, "images in one request exceed the decode budget; send fewer or smaller images (or raise media.image_max_dimension handling client-side)")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", 0, invalid(path, "image could not be decoded: "+err.Error())
	}
	n.decodedPixels += pixels
	processed, encoded, err := n.transform(img, cfg.Width, cfg.Height, tier, compress, path)
	if err != nil {
		return "", 0, err
	}
	// 变换后的字节数是调用方做总量限制的权威值（transform 直接给出，不必再解码）。
	return processed, encoded, nil
}

// transform 把已解码图片编码为上游可接受的 data URL，并返回编码后的原始字节数。
//
//   - 只转码（未开压缩）：PNG 无损（Go 无 webp 编码器）；仍超上游 5 MiB 硬限制 →
//     明确报错并提示开启压缩档。
//   - 开启压缩：等比缩放到档位边长后走 JPEG 质量阶梯；最低档仍超档位预算但 ≤5 MiB 硬
//     限制时采用最低档（预算只是官方客户端的自选目标，5 MiB 才是上游硬限制）。
func (n *normalizer) transform(img image.Image, width, height int, tier compressTier, compress bool, path string) (string, int, error) {
	if !compress {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return "", 0, invalid(path, "image could not be re-encoded as PNG: "+err.Error())
		}
		if base64Len(buf.Len()) > MaxImageBytes {
			return "", 0, invalid(path, "transcoded PNG still exceeds the upstream 5 MiB limit; downscale it client-side or set media.image_max_dimension")
		}
		return dataURL(buf.Bytes(), "image/png"), buf.Len(), nil
	}
	target := img
	if maxInt(width, height) > tier.dimension {
		target = scaleToFit(img, tier.dimension)
	}
	var best []byte
	for _, quality := range tier.qualities {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, flattenOnWhite(target), &jpeg.Options{Quality: quality}); err != nil {
			continue
		}
		best = buf.Bytes()
		if base64Len(len(best)) <= tier.budget {
			break
		}
	}
	if best == nil {
		return "", 0, invalid(path, "image could not be re-encoded as JPEG")
	}
	if base64Len(len(best)) > MaxImageBytes {
		return "", 0, invalid(path, "compressed image still exceeds the upstream 5 MiB limit; downscale it client-side")
	}
	return dataURL(best, "image/jpeg"), len(best), nil
}

// splitDataURL 解析并校验内联图片 data URL，返回声明的媒体类型与 base64 payload。
// 这是 DataURL 与像素处理共用的入口：外链、非 image 类型、缺 base64 标记、非法
// base64、空 payload、超 5 MiB 一律拒绝。
func splitDataURL(url, path string) (string, string, error) {
	rest, ok := strings.CutPrefix(url, "data:")
	if !ok {
		return "", "", invalid(path, "only inline data:image/*;base64 URLs are forwarded; external URLs have no verified upstream support and the gateway does not fetch them (inline the bytes first)")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return "", "", invalid(path, "malformed data URL: missing ','")
	}
	segs := strings.Split(meta, ";")
	mime := strings.ToLower(strings.TrimSpace(segs[0]))
	if !strings.HasPrefix(mime, "image/") || len(mime) == len("image/") {
		return "", "", invalid(path, "image/* media type required in the data URL")
	}
	encoded := false
	if len(segs) > 1 {
		encoded = strings.EqualFold(strings.TrimSpace(segs[len(segs)-1]), "base64")
	}
	if !encoded {
		return "", "", invalid(path, "base64-encoded data URL required (data:image/<type>;base64,<payload>)")
	}
	size, err := base64Size(payload)
	if err != nil {
		return "", "", invalid(path, "invalid base64 image payload")
	}
	if size == 0 {
		return "", "", invalid(path, "image payload is empty")
	}
	if size > MaxImageBytes {
		return "", "", invalid(path, fmt.Sprintf("image decodes to %d bytes, exceeding the %d-byte upstream limit; downscale or recompress it first", size, MaxImageBytes))
	}
	return mime, payload, nil
}

// mimeNeedsTranscode 报告声明的媒体类型是否需要转码（上游不支持的输入格式）。
// gif/bmp/tiff 是官方客户端 jimp→PNG 的三种转码输入。
func mimeNeedsTranscode(mime string) bool {
	switch mime {
	case "image/gif", "image/bmp", "image/x-ms-bmp", "image/tiff", "image/tif":
		return true
	}
	return false
}

// formatNeedsTranscode 报告实际解码格式是否需要转码（覆盖声明与内容不符的形态）。
func formatNeedsTranscode(format string) bool {
	switch format {
	case "gif", "bmp", "tiff":
		return true
	}
	return false
}

// decodeBase64 解码已通过 base64Size 校验的 payload（容忍折行/空白与无填充）。
func decodeBase64(payload string) ([]byte, error) {
	clean := make([]byte, 0, len(payload))
	for i := 0; i < len(payload); i++ {
		switch c := payload[i]; c {
		case '\n', '\r', ' ', '\t':
		default:
			clean = append(clean, c)
		}
	}
	if pad := len(clean) % 4; pad != 0 {
		clean = append(clean, bytes.Repeat([]byte{'='}, 4-pad)...)
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

// dataURL 拼回 data URL（base64 标准字母表，无折行）。
func dataURL(raw []byte, mime string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// base64Len 返回 n 字节编码为 base64 后的长度（含填充）。
func base64Len(n int) int { return (n + 2) / 3 * 4 }

// scaleToFit 等比缩放到最长边 = maxSide（不放大，只缩小）。
func scaleToFit(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return img
	}
	nw, nh := maxSide, maxSide
	if w >= h {
		nh = h * maxSide / w
	} else {
		nw = w * maxSide / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// flattenOnWhite 把带 alpha 的图片合到白底上（JPEG 不支持透明通道）。
func flattenOnWhite(img image.Image) image.Image {
	if opaque, ok := img.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		return img
	}
	b := img.Bounds()
	dst := image.NewRGBA(b)
	xdraw.Draw(dst, b, image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.Draw(dst, b, img, b.Min, xdraw.Over)
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
