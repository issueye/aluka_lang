//go:build !windows && !darwin

package nodefs

// fs 平台相关实现（Linux/BSD）：syscall.Stat_t 时间字段名为
// Atim/Mtim/Ctim；darwin 为 Atimespec 系列，见 fs_platform_darwin.go。

import (
	"io/fs"
	"syscall"
)

// statSysTimes 从 os.FileInfo 提取四类时间（毫秒浮点）。
// Linux：atime/mtime/ctime 来自 Stat_t；birthtime 用 ctime 近似。
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
