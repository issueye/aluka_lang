//go:build !windows

package builtin

// os 平台相关实现（POSIX：Linux/macOS 等）。
// 内存/负载/uptime 通过 /proc（Linux）读取；其他平台返回 0。

import (
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// osDevNull POSIX 空设备路径。
func osDevNull() string { return "/dev/null" }

// isTTYFdPlatform POSIX：fd 指向字符设备（终端）。
func isTTYFdPlatform(fd int) bool {
	var f *os.File
	switch fd {
	case 0:
		f = os.Stdin
	case 1:
		f = os.Stdout
	case 2:
		f = os.Stderr
	default:
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// posixFsExtraConstants POSIX 平台额外 fs 常量（Windows 无）。
func posixFsExtraConstants() map[string]int {
	return map[string]int{
		"O_SYNC":      0o101000,
		"O_DSYNC":     0o100000,
		"O_RSYNC":     0o101000,
		"O_DIRECTORY": 0o200000,
		"O_NOFOLLOW":  0o400000,
		"S_IFBLK":     0o060000,
		"S_IFSOCK":    0o140000,
	}
}

// osShell 登录 shell（优先 $SHELL）。
func osShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return ""
}

// osUserIDs 返回 uid/gid（POSIX 有真实值；失败时 -1）。
func osUserIDs() (int, int) {
	if u, err := user.Current(); err == nil {
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			if gid, err := strconv.Atoi(u.Gid); err == nil {
				return uid, gid
			}
		}
	}
	return -1, -1
}

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
	if runtime.GOOS == "darwin" {
		// darwin 的 Utsname.Machine 字段存在，走通用路径。
	}
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	return utsnameStr(u.Machine[:])
}

// osTotalMem 总内存（字节；非 Linux 返回 0）。
func osTotalMem() int64 {
	line := readProcFileLine("/proc/meminfo", "MemTotal:")
	if line == "" {
		return 0
	}
	return meminfoKB(line) * 1024
}

// osFreeMem 可用内存（字节；非 Linux 返回 0）。
func osFreeMem() int64 {
	line := readProcFileLine("/proc/meminfo", "MemAvailable:")
	if line == "" {
		line = readProcFileLine("/proc/meminfo", "MemFree:")
	}
	if line == "" {
		return 0
	}
	return meminfoKB(line) * 1024
}

// meminfoKB 从 "/proc/meminfo" 行提取 kB 数值。
func meminfoKB(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func readProcFileLine(path, prefix string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// osUptime 系统启动时长（秒；非 Linux 返回 0）。
func osUptime() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return f
}

// osLoadavg 1/5/15 分钟平均负载（非 Linux 返回 [0,0,0]）。
func osLoadavg() [3]float64 {
	var out [3]float64
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return out
	}
	fields := strings.Fields(string(data))
	for i := 0; i < 3 && i < len(fields); i++ {
		if f, err := strconv.ParseFloat(fields[i], 64); err == nil {
			out[i] = f
		}
	}
	return out
}

// osGetPriority 返回进程优先级（Node PRIORITY_* 数值 ≈ nice 值）。
func osGetPriority(pid int) (int, error) {
	if pid == 0 {
		pid = os.Getpid()
	}
	return syscall.Getpriority(syscall.PRIO_PROCESS, pid)
}

// osSetPriority 设置进程优先级（Node PRIORITY_* 数值 → nice 值）。
func osSetPriority(pid int, priority int) error {
	if pid == 0 {
		pid = os.Getpid()
	}
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, priority)
}
