//go:build !windows

package builtin

// fs 平台相关实现（POSIX：Linux/macOS）：
//   - Stats 时间字段提取（syscall.Stat_t）
//   - statfs（syscall.Statfs）

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// statSysTimes 从 os.FileInfo 提取四类时间（毫秒浮点）。
// POSIX：atime/mtime/ctime 来自 Stat_t；birthtime Linux 用 ctime 近似，
// darwin 用 Birthtimespec。
func statSysTimes(info fs.FileInfo) (atimeMs, mtimeMs, ctimeMs, birthtimeMs float64) {
	mtimeMs = float64(info.ModTime().UnixMilli())
	atimeMs = mtimeMs
	ctimeMs = mtimeMs
	birthtimeMs = mtimeMs
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		atimeMs = timespecToMs(st.Atim)
		mtimeMs = timespecToMs(st.Mtim)
		ctimeMs = timespecToMs(st.Ctim)
		birthtimeMs = ctimeMs
	}
	return
}

// statSysNumbers 提取 Stats 数值字段（来自 syscall.Stat_t）。
func statSysNumbers(info fs.FileInfo) (nlink, uid, gid, ino, dev, rdev, blksize, blocks int64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		nlink = int64(st.Nlink)
		uid = int64(st.Uid)
		gid = int64(st.Gid)
		ino = int64(st.Ino)
		dev = int64(st.Dev)
		rdev = int64(st.Rdev)
		blksize = int64(st.Blksize)
		blocks = int64(st.Blocks)
	}
	return
}

// timespecToMs syscall.Timespec → 毫秒（含小数）。
func timespecToMs(ts syscall.Timespec) float64 {
	return float64(ts.Sec)*1000 + float64(ts.Nsec)/1e6
}

// lutimesImpl 不跟随符号链接地设置时间（近似：os.Chtimes 跟随链接；
// 符号链接场景有细微差异，记录为 knownDifference）。
func lutimesImpl(p string, atime, mtime time.Time) error {
	return os.Chtimes(p, atime, mtime)
}

// lchmodImpl 设置链接自身权限（近似：os.Chmod）。
func lchmodImpl(p string, mode os.FileMode) error {
	return os.Chmod(p, mode)
}

// lchownImpl 不跟随链接地 chown。
func lchownImpl(p string, uid, gid int) error {
	return os.Lchown(p, uid, gid)
}

// statfsFor 统计路径所在文件系统。
// 类型映射：与 Node statfs.type 语义一致（MAGIC 常量），这里简化返回 0
// （通用/未知）；主要字段 blocks/bfree/bavail/files/ffree 取自 Statfs。
func statfsFor(path string) (typ, bsize, blocks, bfree, bavail, files, ffree int64) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return
	}
	bsize = int64(s.Bsize)
	if bsize == 0 {
		bsize = 4096
	}
	blocks = int64(s.Blocks)
	bfree = int64(s.Bfree)
	bavail = int64(s.Bavail)
	files = int64(s.Files)
	ffree = int64(s.Ffree)
	return
}
