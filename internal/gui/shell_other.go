//go:build !windows && !darwin

// 系统级文件/文件夹操作（Linux 等实装）：基于 xdg-open。
// ShowItemInFolder 无统一标准协议，退化为打开文件所在父目录。
package gui

import (
	"os/exec"
	"path/filepath"
)

// OpenPath 用系统默认程序打开文件或文件夹（文件夹由文件管理器打开）。
func OpenPath(path string) error {
	return exec.Command("xdg-open", path).Run()
}

// ShowItemInFolder 没有标准 API，退化为在文件管理器中打开父目录。
func ShowItemInFolder(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Run()
}

// OpenExternal 用系统默认应用打开一个外部 URL（浏览器 / mailto 等协议）。
func OpenExternal(url string) error {
	return exec.Command("xdg-open", url).Run()
}
