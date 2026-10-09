// index.go 面板静态资源与安全响应头。
//
// 前端已迁移到 React + Vite 工程（仓库根 frontend/），构建产物固定输出到
// internal/panel/web/（index.html + app.js + app.css 三件套），由 web_embed.go
// 嵌入二进制。本文件只保留：
//   - CSP 等安全响应头（面板页面与全部 /panel/api/* 统一生效）；
//   - 静态资源 handler（页面 / app.js / app.css）。
//
// 安全头对"面板页面与全部 /panel/api/* 响应"统一生效：CSP 限制脚本只能来自本服务，
// 禁止被 iframe 嵌套（防点击劫持），禁 MIME 嗅探，并声明不泄露 Referer 出去。
package panel

import (
	"net/http"
)

// csp 内容安全策略（严格版，无需 unsafe-inline 脚本）：
//   - default-src 'none'        默认全禁，逐个开口
//   - script-src 'self'         只跑同源脚本（app.js，Vite 产物 ESM）
//   - style-src 'self' 'unsafe-inline'
//     样式表是同源外链（app.css）；'unsafe-inline' 是设计取舍：React 内联
//     style 属性（进度条宽度、主题色等）允许内联样式不会导致脚本执行；
//     仍禁止外部样式域与 @import 外链。
//   - connect-src 'self'        前端 fetch 只能打本服务
//   - img-src 'self' data:      图标/内联图（QR 码走内联 SVG，不需要）
//   - form-action 'none'        页面无表单提交目标（配置页是 JS 提交）
//   - frame-ancestors 'none'    禁止被任何站点 iframe 嵌套（点击劫持）
//   - base-uri 'none'          禁止注入 <base> 改写相对路径
const csp = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"connect-src 'self'; img-src 'self' data:; form-action 'none'; " +
	"frame-ancestors 'none'; base-uri 'none'"

// setSecurityHeaders 写入面板统一安全响应头（页面与 API 都要，API 也含 JSON 数据）。
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff") // 禁 MIME 嗅探
	w.Header().Set("X-Frame-Options", "DENY")           // 老浏览器兜底（CSP frame-ancestors 的等价项）
	w.Header().Set("Referrer-Policy", "no-referrer")    // 不外泄面板地址给外部站点
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

// index 输出面板页面（React 构建产物；静态无秘密，数据接口 /panel/api/* 才走鉴权）。
func (p *Panel) index(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(webIndexBytes())
}

// appScript 输出前端逻辑（Vite 构建的 ESM 产物，CSP script-src 'self' 加载）。
func (p *Panel) appScript(w http.ResponseWriter, r *http.Request) {
	b, mime, ok := webAsset("app.js")
	if !ok {
		http.NotFound(w, r)
		return
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// appStyles 输出面板样式表（Tailwind 构建产物）。
func (p *Panel) appStyles(w http.ResponseWriter, r *http.Request) {
	b, mime, ok := webAsset("app.css")
	if !ok {
		http.NotFound(w, r)
		return
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
