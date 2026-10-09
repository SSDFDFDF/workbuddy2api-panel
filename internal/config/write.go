// write.go 配置文件落盘原语：原子替换 + 只读/挂载错误的可操作提示 + 快照备份。
//
// 为什么独立成文件：这些语义（tmp+rename、Docker 单文件 bind mount 的 EBUSY
// 回落、只读挂载的成因提示、0600）此前只存在于 cmd/server 的面板保存路径里；
// 版本迁移在**读文件**时也要回写一次，两处必须完全一致，否则会出现
// "面板保存能写、迁移回写写不了"这种只在特定部署形态暴露的差异。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// WriteFileAtomic 原子替换写文件：tmp + 写 + rename（同目录，保证 rename 原子）。
//
// 唯一的例外是 Docker 单文件 bind mount：挂载目标不能被 rename 覆盖
// （Linux 返回 EBUSY / "device or resource busy"），此时回落"原地截断改写"，
// 并靠 fsync 保证内容落盘。原地改写期间若失败，完整新内容保留在 tmp 文件里，
// 供用户手工恢复（返回的 error 会写明 tmp 路径）。
//
// 权限固定 0600：配置文件含 api_key / 代理凭证 / device token。
func WriteFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return classifyWriteErr(path, fmt.Errorf("写入临时文件 %s: %w", tmp, err))
	}
	if err := os.Rename(tmp, path); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EBUSY) {
		_ = os.Remove(tmp)
		return classifyWriteErr(path, fmt.Errorf("替换 %s: %w", path, err))
	}

	// bind mount 回落：原地截断改写。
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if openErr != nil {
		_ = os.Remove(tmp)
		return classifyWriteErr(path, fmt.Errorf("替换 %s（bind mount 原地改写也无法打开）: %w", path, openErr))
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("替换 %s（bind mount 原地改写失败，完整新内容保留在 %s，可手工恢复）: %w", path, tmp, writeErr)
	}
	_ = os.Remove(tmp)
	if closeErr != nil {
		return classifyWriteErr(path, fmt.Errorf("替换 %s（bind mount 原地改写）: %w", path, closeErr))
	}
	return nil
}

// classifyWriteErr 把"写不进去"翻成可操作的成因提示。
//
// 这三类错误在容器部署里最常见，原始 errno 文案对用户没有任何指导意义：
//   - EROFS：配置文件以只读方式挂载（docker run 的 :ro / compose 的 read_only）；
//   - EACCES/EPERM：容器用户 uid 与挂载文件属主不一致（PUID/PGID 或 chown）；
//   - 其他：原样返回。
func classifyWriteErr(path string, err error) error {
	switch {
	case errors.Is(err, syscall.EROFS):
		return fmt.Errorf("%s 所在文件系统只读（配置以只读方式挂载：docker run 的 `:ro` 或 compose 的 read_only）: %w", path, err)
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%s 无写权限（容器用户 uid 与挂载文件属主不一致：`chown 10001:10001 %s` 或设 PUID/PGID；也可能是以非属主身份裸跑）: %w", path, filepath.Base(path), err)
	default:
		return err
	}
}

// SnapshotFile 把 src 的当前内容快照到 dst（0600）。
//
// 两条调用约定：
//   - dst 已存在则不覆盖（快照是"回到迁移前/首次改动前"的那一份，不能被后来的
//     状态覆盖，否则兜底就失效了）；
//   - src 不存在返回 nil（没有可快照的内容，不是错误）。
func SnapshotFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(dst, raw, 0o600)
}

// BackupPath 通用兜底备份名（config.json.bak）。
func BackupPath(path string) string { return path + ".bak" }

// VersionSnapshotPath 迁移前快照名（config.json.v<from>）：文件从哪个版本迁上来，
// 快照就叫哪个名字，语义自解释，便于用户直接 `cp` 回退。
func VersionSnapshotPath(path string, from int) string {
	return fmt.Sprintf("%s.v%d", path, from)
}

// SeedBackup 首次成功加载时顺手种一份 .bak（已存在则不动）。
//
// 为什么需要：面板保存路径只保留"上一版"，用户**第一次**改坏配置时磁盘上还没有
// 任何备份，兜底等于不存在。启动成功时种一份，把可用回滚点提前到第一次改动之前。
func SeedBackup(path string) error { return SnapshotFile(path, BackupPath(path)) }
