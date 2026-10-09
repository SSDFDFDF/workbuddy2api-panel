// lock.go 配置文件的读改写事务锁。
//
// 同一份配置文件上有两条"读 → 改 → 写"路径：
//   - 面板保存：读旧文件 → 合并提交 → 校验 → 落盘（cmd/server 的 saveConfig）；
//   - 版本迁移：读文件 → 迁移 → 回写（Load，见 migrate.go）。
//
// 两者必须共用同一把锁。否则存在这个交错（窗口很小但真实）：
//
//	Load 读到 v2 文件 ────► 迁移并回写（内容 = 迁移后的旧值）
//	save 读到 v2 文件 ─► 用户改了热字段 ─► 落盘（内容 = 新值）
//	                          ▲ Load 的回写若发生在这里，用户这次保存就**丢了**
//
// 这也是为什么锁放在配置域：它是配置文件的事务语义，不是某个调用方的私有状态。
package config

import "sync"

var fileTxMu sync.Mutex

// FileTx 在同一把进程级互斥锁里执行配置文件的读改写事务。
//
// 不可重入：事务内部不要再调用 FileTx（Load 已在自己的事务里包住迁移回写，
// 所以 writeMigrated 不再单独取锁）。
func FileTx(fn func() error) error {
	fileTxMu.Lock()
	defer fileTxMu.Unlock()
	return fn()
}
