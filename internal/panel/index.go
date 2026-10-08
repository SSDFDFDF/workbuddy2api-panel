// index.go 面板静态资源与安全响应头。
//
// 前端资源经 go:embed 打进二进制（随服务部署，无外部构建步骤）：
//   - index.html  页面骨架（唯一直接下发的 HTML）
//   - app.css     全部样式（独立文件而非内联 <style>，便于评审与浏览器缓存）
//   - js/*.js     前端逻辑分片（文件名数字前缀 = 加载顺序）
//
// js/ 分片在 init 时按文件名字典序拼成 app.js 一次性下发，而不是用多个
// <script> 分别加载：拼接后的程序与"单一 app.js"语义完全一致（函数提升、
// 顶层 const/let 共享同一个全局词法环境），拆分只是源码组织手段，不引入
// 脚本间加载顺序 / TDZ 这类新的故障面。
//
// 安全头对"面板页面与全部 /panel/api/* 响应"统一生效：CSP 限制脚本只能来自本服务，
// 禁止被 iframe 嵌套（防点击劫持），禁 MIME 嗅探，并声明不泄露 Referer 出去。
package panel

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

//go:embed app.css
var appCSS []byte

//go:embed js/*.js
var panelJSParts embed.FS

// panelJS 服务端最终下发的 app.js：全部分片按文件名字典序拼接。
//
// 分片之间插入 `/* ==== js/<名字> ==== */` 横幅，便于在浏览器报错堆栈里定位
// 到源码分片；首片之前不插任何东西，因此 'use strict' 仍是整条程序的第一条
// 语句（指令序言允许前置注释，但不如直接保持原样稳妥）。
//
// 分片顺序即执行顺序，也是顶层 const/let 进入词法环境的顺序——必须与拆分前
// 的单文件顺序一致；新增分片按数字前缀插到正确位置（frontend_test.go 有守护）。
var panelJS = buildPanelJS()

func buildPanelJS() []byte {
	names, err := fs.Glob(panelJSParts, "js/*.js")
	if err != nil {
		panic("panel: 读取前端分片清单失败: " + err.Error())
	}
	if len(names) == 0 {
		panic("panel: 未找到前端分片 js/*.js，前端将无法下发")
	}
	var buf bytes.Buffer
	for i, name := range names {
		src, err := panelJSParts.ReadFile(name)
		if err != nil {
			panic("panel: 读取前端分片 " + name + " 失败: " + err.Error())
		}
		if i > 0 {
			fmt.Fprintf(&buf, "/* ==== %s ==== */\n", name)
		}
		buf.Write(src)
		if len(src) > 0 && src[len(src)-1] != '\n' {
			buf.WriteByte('\n') // 分片缺行尾换行时补一个，避免与下一片粘行
		}
	}
	return buf.Bytes()
}

// csp 内容安全策略（严格版，无需 unsafe-inline 脚本）：
//   - default-src 'none'        默认全禁，逐个开口
//   - script-src 'self'         只跑同源脚本（app.js，由 js/*.js 分片拼成）；
//     页面无内联事件处理器/内联脚本
//   - style-src 'self' 'unsafe-inline'
//     样式表本身是同源外链（app.css）；'unsafe-inline' 是设计取舍：页面有少量
//     style="..." 属性（进度条宽度、表格列宽），允许内联样式不会导致脚本执行；
//     仍禁止外部样式域与 @import 外链。
//   - connect-src 'self'        前端 fetch 只能打本服务
//   - img-src 'self' data:      图标/内联图
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

// index 输出面板页面（静态无秘密；数据接口 /panel/api/* 才走鉴权）。
func (p *Panel) index(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

// appScript 输出前端逻辑（同源脚本，供 CSP script-src 'self' 加载）。
func (p *Panel) appScript(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(panelJS)
}

// appStyles 输出面板样式表（同源外链，供 CSP style-src 'self' 加载）。
func (p *Panel) appStyles(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(appCSS)
}
