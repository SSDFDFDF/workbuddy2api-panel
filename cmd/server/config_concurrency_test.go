package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/livecfg"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
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
	live := livecfg.New(livecfg.Snapshot{})
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
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("final config must stay parseable: %v", err)
	}
	if cfg.APIKey == "" || cfg.APIKey == "init" {
		t.Fatalf("one submitted api_key must win, got %q", cfg.APIKey)
	}
}

// TestConcurrentConfigWarnings 守护告警去重表的并发安全：GET/POST 配置
// 会并发调用 logConfigWarnings，普通 map 无锁读写会直接触发 concurrent map writes。
func TestConcurrentConfigWarnings(t *testing.T) {
	printedConfigWarningsMu.Lock()
	printedConfigWarnings = map[string]bool{}
	printedConfigWarningsMu.Unlock()

	cfg := &Config{Warnings: []string{"concurrent-warning-regression"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				logConfigWarnings(cfg)
			}
		}()
	}
	close(start)
	wg.Wait()
}
