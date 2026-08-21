//go:build windows

// 系统级文件/文件夹操作（Windows 实装）：
//   OpenPath            → ShellExecuteW("open")，文件夹交给资源管理器、文件用默认关联程序
//   ShowItemInFolder    → explorer /select,<path>，在资源管理器中定位并选中该项
package gui

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// OpenPath 用系统默认程序打开文件或文件夹（文件夹由资源管理器打开）。
func OpenPath(path string) error {
	return shellExecute("open", path, "", 1 /* SW_SHOWNORMAL */)
}

// ShowItemInFolder 在资源管理器中显示并选中该文件/文件夹。
// explorer 的 /select 参数必须整体作为一个独立参数传入，拆分会导致路径解析歧义。
func ShowItemInFolder(path string) error {
	return shellExecute("open", "explorer.exe", "/select,"+path, 1)
}

// shellExecute 封装 ShellExecuteW 调用；返回值 ≤32 表示失败（0 或 SE_ERR_*）。
func shellExecute(operation, path, params string, showCmd int) error {
	if path == "" {
		return errors.New("shell: empty path")
	}
	opPtr, err := syscall.UTF16PtrFromString(operation)
	if err != nil {
		return err
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var paramsPtr *uint16
	if params != "" {
		paramsPtr, err = syscall.UTF16PtrFromString(params)
		if err != nil {
			return err
		}
	}
	ret, _, _ := procShellExecuteW.Call(
		0, // hwnd
		uintptr(unsafe.Pointer(opPtr)),
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0, // lpDirectory
		uintptr(showCmd),
	)
	if uintptr(ret) > 32 /* SE_ERR_OK 以上为成功 */ {
		return nil
	}
	return errors.New("shell: failed to open path (ShellExecute error)")
}
