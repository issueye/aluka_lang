//go:build !windows && !darwin

package builtin

// os 平台相关实现（Linux/BSD）：uname（syscall.Utsname/Uname）。
// darwin 无对应系统调用封装，见 os_platform_darwin.go（sysctl 实现）。

import "syscall"

// utsnameStr 把 uname 定长字节数组转字符串。
func utsnameStr(field []int8) string {
	chars := make([]byte, 0, len(field))
	for _, c := range field {
		if c == 0 {
			break
		}
		chars = append(chars, byte(c))
	}
	return string(chars)
}

// releaseInfo 内核发行版本（uname -r）。
func releaseInfo() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	return utsnameStr(u.Release[:])
}

// osVersionInfo 系统版本描述（uname -v）。
func osVersionInfo() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	return utsnameStr(u.Version[:])
}

// osMachine 机器类型（uname -m）。
func osMachine() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	return utsnameStr(u.Machine[:])
}
