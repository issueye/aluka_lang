//go:build darwin

package builtin

// fs 平台相关实现（macOS）：Stat_t 时间字段为 Atimespec/Mtimespec/
// Ctimespec/Birthtimespec（birthtime 为真实创建时间）。

import (
	"io/fs"
	"syscall"
)

// statSysTimes 从 os.FileInfo 提取四类时间（毫秒浮点）。
func statSysTimes(info fs.FileInfo) (atimeMs, mtimeMs, ctimeMs, birthtimeMs float64) {
	mtimeMs = float64(info.ModTime().UnixMilli())
	atimeMs = mtimeMs
	ctimeMs = mtimeMs
	birthtimeMs = mtimeMs
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		atimeMs = timespecToMs(st.Atimespec)
		mtimeMs = timespecToMs(st.Mtimespec)
		ctimeMs = timespecToMs(st.Ctimespec)
		birthtimeMs = timespecToMs(st.Birthtimespec)
	}
	return
}
