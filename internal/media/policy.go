// policy.go 工具结果图片（多模态工具结果）与图片像素处理的进程级策略。
//
// 背景（逆向记录见 docs/WORKBUDDY_MULTIMODAL_ATTACHMENT_PROTOCOL.md §4.3/§4.4）：
// 工具结果里的图片只有两种出站形态——
//  1. **原样透传**：tool 消息 content 数组里带 image_url part。上游对 tool 多模态
//     形态**挑模型**（"item of 'content' should be a message of a certain modal"），
//     官方客户端靠删图重试自愈；
//  2. **抬升（hoist）**：把图片从 tool 消息抽出，在整批工具结果之后插入一条
//     `user` 图片消息（官方 custom-model-tool-media-hoist 插件同构）。这是唯一
//     已知的、上游不会因「多模态 tool 形态」拒绝的写法，但**改变了图片的归属
//     角色与上下文位置**，属显式兼容变换，不是无损编码。
//
// 因此策略显式可配，且两个入口的默认值不同：
//   - 原生 Chat 默认 passthrough：不改变既有转发行为（严格转发契约）；
//   - Responses / Messages 默认 hoist：桥接入口的服务对象是 agent 客户端
//     （截图/读图工具结果常见），拒绝会让能力不可用，透传则撞未验证形态。
//
// 配置 `media.tool_images` 可覆盖（auto/passthrough/hoist/reject），热生效。
package media

import (
	"fmt"
	"sync/atomic"
)

// ToolPolicy 工具结果图片策略。
type ToolPolicy string

const (
	// ToolPolicyAuto 未显式配置：按入口取默认（见 ToolPolicyFor）。
	ToolPolicyAuto ToolPolicy = "auto"
	// ToolPolicyPassthrough 原样转发 tool 消息里的图片 part（仍做形状/资源校验）。
	ToolPolicyPassthrough ToolPolicy = "passthrough"
	// ToolPolicyHoist 把图片抽出，改为工具批次之后的 user 图片消息。
	ToolPolicyHoist ToolPolicy = "hoist"
	// ToolPolicyReject 明确 400，不转发工具结果图片。
	ToolPolicyReject ToolPolicy = "reject"
)

var toolPolicy atomic.Value // 存放 ToolPolicy；零值 = auto

// ImagePolicy 图片像素处理策略（转码/压缩）。零值 = 全关（只做形状/大小校验，
// 不解码像素）；配置装载/热保存路径整体替换。
type ImagePolicy struct {
	// Transcode 把上游不支持的格式（gif/bmp/tiff）转码为 PNG（无损）。
	Transcode bool
	// MaxDimension 压缩档位边长（0 = 关闭；1080 / 2000 两档，见 compressTiers）。
	MaxDimension int
}

var imagePolicy atomic.Pointer[ImagePolicy]

// active 报告是否需要像素级处理（关闭时热路径零解码）。
func (p ImagePolicy) active() bool { return p.Transcode || p.MaxDimension != 0 }

// tier 返回生效的压缩档位与是否启用压缩。
func (p ImagePolicy) tier() (compressTier, bool) {
	if p.MaxDimension == 0 {
		return compressTier{}, false
	}
	tier, ok := compressTiers[p.MaxDimension]
	return tier, ok
}

// ValidateImagePolicy 校验图片策略取值（不改变当前生效值）。
func ValidateImagePolicy(p ImagePolicy) error { return ValidateImageMaxDimension(p.MaxDimension) }

// SetImagePolicy 设置进程级图片策略（非法值不改变当前生效值）。
func SetImagePolicy(p ImagePolicy) error {
	if err := ValidateImagePolicy(p); err != nil {
		return err
	}
	imagePolicy.Store(&p)
	return nil
}

// CurrentImagePolicy 返回当前生效的图片策略（未设置为零值 = 全关）。
func CurrentImagePolicy() ImagePolicy {
	if p := imagePolicy.Load(); p != nil {
		return *p
	}
	return ImagePolicy{}
}

// ValidateToolPolicy 校验策略取值（不改变当前生效值）：供配置装载/保存路径先校验，
// 再由热应用路径 SetToolPolicy 生效（校验失败时不能让进程级状态已被改写）。
// 空串合法，等价于 auto（配置文件省略该键的形态）。
func ValidateToolPolicy(p ToolPolicy) error {
	switch p {
	case "", ToolPolicyAuto, ToolPolicyPassthrough, ToolPolicyHoist, ToolPolicyReject:
		return nil
	}
	return fmt.Errorf("media.tool_images: auto, passthrough, hoist or reject required, got %q", string(p))
}

// SetToolPolicy 设置进程级策略（配置装载与热保存路径调用）。非法值返回错误，
// 不改变当前生效值；空串按 auto 处理。
func SetToolPolicy(p ToolPolicy) error {
	if err := ValidateToolPolicy(p); err != nil {
		return err
	}
	if p == "" {
		p = ToolPolicyAuto
	}
	toolPolicy.Store(p)
	return nil
}

// CurrentToolPolicy 返回当前显式配置的策略（未配置为 ToolPolicyAuto）。
func CurrentToolPolicy() ToolPolicy {
	if v, ok := toolPolicy.Load().(ToolPolicy); ok {
		return v
	}
	return ToolPolicyAuto
}

// ToolPolicyFor 解析某入口实际生效的策略。bridge=true 表示 Responses / Messages
// 桥接入口。
func ToolPolicyFor(bridge bool) ToolPolicy {
	if p := CurrentToolPolicy(); p != ToolPolicyAuto {
		return p
	}
	if bridge {
		return ToolPolicyHoist
	}
	return ToolPolicyPassthrough
}
