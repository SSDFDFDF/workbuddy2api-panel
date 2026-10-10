// warnings.go 配置告警的去重打印。
//
// 告警来源：文件里不认识的键（keyshape.go）、被剔除的非法 realm 与注入槽位
// 缺失（prompt.go / load.go）。只告警不阻断启动，但必须可见，否则用户改了键名
// 会以为已经生效。
package config

import (
	"log"
	"sync"
)

// printedWarnings 记录已打印过的告警，避免每次 Load 重复刷屏。
//
// 读侧不止启动路径：面板 GET /panel/api/config 走 Load 闭包，POST 走保存路径，
// 两者可能并发调用 LogWarnings——普通 map 无锁读写既是数据竞争，也可能直接
// 触发 fatal error: concurrent map writes。
var (
	printedWarningsMu sync.Mutex
	printedWarnings   = map[string]bool{}
)

// LogWarnings 把配置告警打到标准日志（面板日志页同源），同一条只打一次。
func LogWarnings(c *Config) {
	logOnce(c, c.Warnings, "WARN: [config] ")
}

// logOnce 打同一条内容只打印一次（整个进程内去重）。
//
// 读侧不止启动路径：面板 GET /panel/api/config 走 Load 闭包，POST 走保存路径，
// 两者可能并发调用——普通 map 无锁读写既是数据竞争，也可能直接触发
// fatal error: concurrent map writes。
func logOnce(c *Config, msgs []string, prefix string) {
	if c == nil {
		return
	}
	printedWarningsMu.Lock()
	defer printedWarningsMu.Unlock()
	for _, m := range msgs {
		key := prefix + m
		if printedWarnings[key] {
			continue
		}
		printedWarnings[key] = true
		log.Printf("%s%s", prefix, m)
	}
}
