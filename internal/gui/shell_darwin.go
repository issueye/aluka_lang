//go:build darwin

// 系统级文件/文件夹操作（macOS 实装）：基于 `open`（-R 在 Finder 中定位）。
package gui

import "os/exec"

// OpenPath 用系统默认程序打开文件或文件夹（文件夹由 Finder 打开）。
func OpenPath(path string) error {
	return exec.Command("open", path).Run()
}

// ShowItemInFolder 在 Finder 中显示并选中该文件/文件夹（open -R）。
func ShowItemInFolder(path string) error {
	return exec.Command("open", "-R", path).Run()
}
