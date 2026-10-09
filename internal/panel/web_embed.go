// web_embed.go 前端构建产物嵌入：internal/panel/web/（React + Vite 构建，源码在
// 仓库根 frontend/，npm run build 输出到这里）。
//
// 产物为固定三件套 index.html + app.js + app.css（vite.config.ts 固定文件名，
// 不带哈希），由路由按 /panel/ 前缀分发：
//   - GET /panel/{$}        → index.html
//   - GET /panel/app.js     → app.js（ESM，CSP script-src 'self' 命中）
//   - GET /panel/app.css    → app.css
//
// 构建链：make build / Dockerfile 先跑 npm run build 再编 Go；产物目录随源码
// 提交，无 node 的环境也能直接编译（go:embed 只要目录里有文件即可）。
package panel

import (
	"embed"
	"io/fs"
	"net/http"
)

// webFS 前端产物（frontend/ 构建输出）。README.md 由构建时生成（frontend/README.md
// 复制件），嵌入以保持目录非空可编译；实际分发只认 index.html/app.js/app.css。
//
//go:embed web
var webFS embed.FS

// webIndex 面板页面（构建产物 index.html）。
func webIndexBytes() []byte {
	b, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		panic("panel: 前端产物 web/index.html 缺失（在 frontend/ 目录跑 npm run build）: " + err.Error())
	}
	return b
}

// webAsset 读取构建产物里的静态资源（app.js / app.css）。
func webAsset(name string) ([]byte, string, bool) {
	b, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil {
		return nil, "", false
	}
	mime := "application/octet-stream"
	switch {
	case name == "app.js":
		mime = "text/javascript; charset=utf-8"
	case name == "app.css":
		mime = "text/css; charset=utf-8"
	}
	return b, mime, true
}

// newIndexHandler 新版面板页面 handler（替代旧 index.go 的内联 HTML）。
func newIndexHandler() http.HandlerFunc {
	html := webIndexBytes()
	return func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(html)
	}
}
