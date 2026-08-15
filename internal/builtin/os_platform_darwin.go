//go:build darwin

package builtin

// os 平台相关实现（macOS）：darwin 的 syscall 包无 Utsname/Uname，
// uname 等价信息经 sysctl 读取。

import "syscall"

// releaseInfo 内核发行版本（sysctl kern.osrelease ≈ uname -r）。
func releaseInfo() string {
	v, err := syscall.Sysctl("kern.osrelease")
	if err != nil {
		return ""
	}
	return v
}

// osVersionInfo 系统版本描述（sysctl kern.version ≈ uname -v）。
func osVersionInfo() string {
	v, err := syscall.Sysctl("kern.version")
	if err != nil {
		return ""
	}
	return v
}

// osMachine 机器类型（sysctl hw.machine ≈ uname -m）。
func osMachine() string {
	v, err := syscall.Sysctl("hw.machine")
	if err != nil {
		return ""
	}
	return v
}
