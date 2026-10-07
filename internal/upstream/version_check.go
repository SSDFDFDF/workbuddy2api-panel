// Package upstream 提供官方客户端最新版本探测与状态获取。
// 仅用于启动时自检并在管理面板配置页展示对比提示，绝不自动篡改用户配置。
package upstream

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ClientVersionInfo 官方客户端最新发版信息（供管理面板配置页展示提示）。
type ClientVersionInfo struct {
	LatestCN          string    `json:"latest_cn"`           // 国内最新语义版本（如 "5.7.6"）
	LatestCNBuild     string    `json:"latest_cn_build"`     // 国内最新完整构建号（如 "5.7.6.40409493"）
	LatestGlobal      string    `json:"latest_global"`       // 国际最新语义版本（如 "5.6.2"）
	LatestGlobalBuild string    `json:"latest_global_build"` // 国际最新完整构建号（如 "5.6.2.39458645"）
	CheckedAt         time.Time `json:"checked_at"`          // 最近一次检测时间
	Checked           bool      `json:"checked"`             // 是否已成功完成一次检测
}

var globalVersionInfo atomic.Pointer[ClientVersionInfo]

func init() {
	// 初始化内置默认值，确保未探测前不返回全空
	initInfo := &ClientVersionInfo{
		LatestCN:          defaultClientVersion,
		LatestCNBuild:     "5.7.6.40409493",
		LatestGlobal:      "5.6.2",
		LatestGlobalBuild: "5.6.2.39458645",
		Checked:           false,
	}
	globalVersionInfo.Store(initInfo)
}

// GetLatestVersionInfo 获取当前缓存的官方客户端最新版本信息。
func GetLatestVersionInfo() ClientVersionInfo {
	if p := globalVersionInfo.Load(); p != nil {
		return *p
	}
	return ClientVersionInfo{
		LatestCN:     defaultClientVersion,
		LatestGlobal: "5.6.2",
	}
}

// feedResponse 官方更新接口返回结构
type feedResponse struct {
	Version        string `json:"version"`
	ProductVersion string `json:"productVersion"`
	URL            string `json:"url"`
	Timestamp      int64  `json:"timestamp"`
}

func fetchFeedVersion(ctx context.Context, client *http.Client, endpoint string) (semver string, fullBuild string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", err
	}
	var feed feedResponse
	if err := json.Unmarshal(body, &feed); err != nil {
		return "", "", err
	}

	build := feed.ProductVersion
	if build == "" {
		build = feed.Version
	}
	if build == "" {
		return "", "", nil
	}

	// 提取语义化版本（前 3 段，如 5.7.6.40409493 -> 5.7.6）
	parts := strings.Split(build, ".")
	if len(parts) >= 3 {
		semver = strings.Join(parts[:3], ".")
	} else {
		semver = build
	}
	return semver, build, nil
}

// CheckLatestVersion 执行一次版本检测（带超时）。
func CheckLatestVersion(ctx context.Context, timeout time.Duration) ClientVersionInfo {
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	cCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpClient := &http.Client{Timeout: timeout}

	const cnFeedURL = "https://www.workbuddy.cn/v2/update?platform=workbuddy-win32-x64-user&version=5.0.0"
	const globalFeedURL = "https://www.workbuddy.ai/v2/update?platform=workbuddy-win32-x64-user&version=5.0.0"

	latestCN, buildCN, errCN := fetchFeedVersion(cCtx, httpClient, cnFeedURL)
	latestGlobal, buildGlobal, errGlobal := fetchFeedVersion(cCtx, httpClient, globalFeedURL)

	prev := GetLatestVersionInfo()
	next := &ClientVersionInfo{
		LatestCN:          prev.LatestCN,
		LatestCNBuild:     prev.LatestCNBuild,
		LatestGlobal:      prev.LatestGlobal,
		LatestGlobalBuild: prev.LatestGlobalBuild,
		CheckedAt:         time.Now(),
		Checked:           true,
	}

	if errCN == nil && latestCN != "" {
		next.LatestCN = latestCN
		next.LatestCNBuild = buildCN
	}
	if errGlobal == nil && latestGlobal != "" {
		next.LatestGlobal = latestGlobal
		next.LatestGlobalBuild = buildGlobal
	}

	globalVersionInfo.Store(next)
	return *next
}

// StartVersionCheckAsync 启动后台异步探测，启动时执行一次，不阻塞主服务。
func StartVersionCheckAsync() {
	go func() {
		// 稍微延迟 500ms 避免与主服务启动并发争抢 CPU / 网络
		time.Sleep(500 * time.Millisecond)
		info := CheckLatestVersion(context.Background(), 4*time.Second)
		if info.Checked {
			log.Printf("[Upstream] 客户端最新发版检测完成: 国内=%s(%s), 海外=%s(%s)",
				info.LatestCN, info.LatestCNBuild, info.LatestGlobal, info.LatestGlobalBuild)
		}
	}()
}
