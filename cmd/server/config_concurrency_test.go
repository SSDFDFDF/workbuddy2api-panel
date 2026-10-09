// config_concurrency_test.go 配置保存路径的并发安全（事务互斥）。
//
// 告警去重表的并发安全测试已随 config.LogWarnings 迁到
// internal/config/config_test.go 的 TestConcurrentWarnings。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"workbuddy_manager/internal/config"
	"workbuddy_manager/internal/config/runtime"
	"workbuddy_manager/internal/pool"
	"workbuddy_manager/internal/scheduler"
	"workbuddy_manager/internal/upstream"
)

// TestConcurrentConfigSaveSerialized 守护「保存配置」的事务互斥：
// 多个面板标签页/脚本重试会并发 POST 配置，此前共用同一 .tmp 且无互斥，
// 实测出现 rename ENOENT 与后写覆盖先写。修复后 32 个并发保存必须全部成功，
// 且磁盘上留下的是完整可解析的配置。
func TestConcurrentConfigSaveSerialized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"api_key":"init"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	live := runtime.New(runtime.Snapshot{})
	p := pool.New("")
	up := &upstream.Client{}
	sch := scheduler.New(scheduler.Config{})

	const n = 32
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, err := json.Marshal(map[string]any{"api_key": fmt.Sprintf("key-%02d", i)})
			if err != nil {
				errCh <- err
				return
			}
			if _, err := saveConfig(payload, path, live, p, up, sch); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent save must not fail: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("final config must stay parseable: %v", err)
	}
	if cfg.APIKey == "" || cfg.APIKey == "init" {
		t.Fatalf("one submitted api_key must win, got %q", cfg.APIKey)
	}
}
