// lock.go 配置文件的读改写事务锁。
//
// 读盘本身是无锁的（Load 只读、写入用 WriteFileAtomic 原子替换），需要串行化的
// 只有「读 → 改 → 写」整段：
//
//	save 读到配置 ──► 应用提交 ──► 落盘
//	save 读到配置 ──────────► 应用提交 ──► 落盘
//	              ▲ 两个保存交错时，后写的会覆盖先写的（丢更新）
//
// 所以面板保存（cmd/server 的 saveConfig）整段持锁；文件系统层面只把临时文件
// 换随机名解决不了丢更新。
//
// 这也是为什么锁放在配置域：它是配置文件的事务语义，不是某个调用方的私有状态。
package config

import "sync"

var fileTxMu sync.Mutex

// FileTx 在同一把进程级互斥锁里执行配置文件的读改写事务。
//
// 不可重入：事务体内部不要再调用 FileTx。
func FileTx(fn func() error) error {
	fileTxMu.Lock()
	defer fileTxMu.Unlock()
	return fn()
}
