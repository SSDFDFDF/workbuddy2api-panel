// config.go 面板配置页接口：读取当前配置、校验并保存（热生效 + 重启项标注）。
//
// 分工：cmd/server 持有 Config 类型与校验逻辑（Load/normalize），此处只做
// HTTP 编排——GET 回显、POST 透传给注入的 SaveConfig 闭包（由 main 完成
// "校验 → 落盘 → 热应用 → 返回需重启字段列表"）。
package panel

import (
	"io"
	"log"
	"net/http"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// getConfig 返回当前配置文件内容与路径（前端按 schema 渲染表单）。
func (p *Panel) getConfig(w http.ResponseWriter, r *http.Request) {
	if p.cfg.LoadConfig == nil {
		writeErr(w, http.StatusNotImplemented, "config api not available")
		return
	}
	cfg, err := p.cfg.LoadConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	vInfo := upstream.GetLatestVersionInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"path":         p.cfg.ConfigPath,
		"config":       cfg,
		"version_info": vInfo,
	})
}

// saveConfig 保存配置：body 直接是配置 JSON（前端按 schema 组装完整对象）。
// SaveConfig 闭包内部完成校验+落盘+热应用；校验失败返回 400 且不写盘。
func (p *Panel) saveConfig(w http.ResponseWriter, r *http.Request) {
	if p.cfg.SaveConfig == nil {
		writeErr(w, http.StatusNotImplemented, "config api not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	restartRequired, err := p.cfg.SaveConfig(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if restartRequired == nil {
		restartRequired = []string{}
	}
	log.Printf("panel: 配置已保存（热生效完成；需重启字段 %d 个）", len(restartRequired))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
	})
}

// previewPrompt 解析草稿的 prompt 段并返回各域生效规则（不落盘）。
//
// 面板的"立即预览"按钮走这里：用户在表单里改了 mode/preset/file/text 之后，
// 保存前就能看到 cn/global 两域实际会发出去的正文（含提交截断标记），
// 避免"保存了但不知道发了什么"。
func (p *Panel) previewPrompt(w http.ResponseWriter, r *http.Request) {
	if p.cfg.PreviewPrompt == nil {
		writeErr(w, http.StatusNotImplemented, "prompt preview api not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	out, err := p.cfg.PreviewPrompt(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
