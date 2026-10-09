package panel

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 前端产物守护：React 工程构建到 internal/panel/web/（固定三件套），
// 嵌入产物必须与磁盘一致、可解析、且引用齐全。这里守护嵌入与分发两侧，
// 前端源码自身的类型/构建检查在 frontend/ 的 npm build（tsc + vite）完成。

// TestWebAssetsLayout 守护构建产物清单：web/ 下必须是 index.html + app.js +
// app.css 三件套（外加产物目录说明文件）。多出的文件意味着 vite 配置漂移
//（比如 hash 文件名、code splitting），会让 CSP 命中与 embed 清单失效。
func TestWebAssetsLayout(t *testing.T) {
	allowed := map[string]bool{
		"index.html": true,
		"app.js":     true,
		"app.css":    true,
		"README.md":  true,
	}
	entries, err := os.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("web/ 下不允许子目录：%s", e.Name())
		}
		if !allowed[e.Name()] {
			t.Fatalf("web/ 下出现未预期的产物 %q（约定固定三件套，检查 frontend/vite.config.ts）", e.Name())
		}
	}
	for _, need := range []string{"index.html", "app.js", "app.css"} {
		if _, err := os.Stat(filepath.Join("web", need)); err != nil {
			t.Fatalf("缺少构建产物 %s（在 frontend/ 目录跑 npm run build）: %v", need, err)
		}
	}
}

// TestWebIndexReferencesAssets 页面引用的资源必须都在产物里且同源相对路径。
func TestWebIndexReferencesAssets(t *testing.T) {
	html := string(webIndexBytes())
	if !strings.Contains(html, `src="./app.js"`) {
		t.Error("index.html 未以 ./app.js 引用脚本（CSP script-src 'self' 需要）")
	}
	if !strings.Contains(html, `href="./app.css"`) {
		t.Error("index.html 未以 ./app.css 引用样式")
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Error("index.html 引用了外部资源（CSP default-src 'none' 会被拦）")
	}
}

// TestWebJSIsParsable 嵌入的 app.js 必须能通过 JS 解析器语法校验
//（Vite 产物理论必过，防的是产物被手工改坏 / 半截写入）。
// 无 node 环境时跳过（不阻塞无 Node 的构建机）。
func TestWebJSIsParsable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping JS syntax check")
	}
	b, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.js")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--check", path)
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("嵌入的 app.js 语法校验失败：%v\n%s", err, out)
	}
}

// TestWebAssetsServed 静态分发：app.js/app.css 按正确 MIME 与安全头下发。
func TestWebAssetsServed(t *testing.T) {
	p := newTestPanel()
	for path, wantType := range map[string]string{
		"/panel/":       "text/html",
		"/panel/app.js": "text/javascript",
		"/panel/app.css": "text/css",
	} {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, wantType) {
			t.Errorf("%s: Content-Type=%q, want prefix %q", path, got, wantType)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: body empty", path)
		}
	}
}
