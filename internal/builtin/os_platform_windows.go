//go:build windows

package builtin

// os 平台相关实现（Windows）。
// 依赖 kernel32.dll 系统调用；内存/uptime/优先级经 Win32 API。

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
	procGetPriorityClass     = kernel32.NewProc("GetPriorityClass")
	procSetPriorityClass     = kernel32.NewProc("SetPriorityClass")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
	procGetConsoleMode       = kernel32.NewProc("GetConsoleMode")
	procGetStdHandle         = kernel32.NewProc("GetStdHandle")
	ntdll                    = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetVersion        = ntdll.NewProc("RtlGetVersion")
)

// osDevNull Windows 空设备路径。
func osDevNull() string { return `\\.\nul` }

// isTTYFdPlatform Windows：GetConsoleMode 判定真实控制台
// （重定向的管道/NUL 设备返回 false，与 Node uv_is_tty 一致）。
func isTTYFdPlatform(fd int) bool {
	var h uintptr
	switch fd {
	case 0:
		h = stdHandle(STD_INPUT_HANDLE)
	case 1:
		h = stdHandle(STD_OUTPUT_HANDLE)
	case 2:
		h = stdHandle(STD_ERROR_HANDLE)
	default:
		return false
	}
	if h == 0 || h == ^uintptr(0) {
		return false
	}
	var mode uint32
	r1, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode)))
	return r1 != 0
}

// stdHandle 取标准句柄（GetStdHandle）。
func stdHandle(kind int) uintptr {
	h, _, _ := procGetStdHandle.Call(uintptr(kind))
	return h
}

const (
	STD_INPUT_HANDLE  = -10
	STD_OUTPUT_HANDLE = -11
	STD_ERROR_HANDLE  = -12
)

// posixFsExtraConstants Windows 无额外 fs 常量。
func posixFsExtraConstants() map[string]int { return nil }

// osShell Windows 恒空（Node 实测 shell 为 null）。
func osShell() string { return "" }

// osUserIDs Windows 恒 -1（Node 实测）。
func osUserIDs() (int, int) { return -1, -1 }

// releaseInfo Windows NT 主版本号（如 10.0.26200）。
func releaseInfo() string {
	major, minor, build, _ := osVersionParts()
	if major == 0 {
		return ""
	}
	return itoa(major) + "." + itoa(minor) + "." + itoa(build)
}

// osVersionInfo Windows 产品名（如 "Windows 11 Home China"）。
func osVersionInfo() string {
	return osProductName()
}

// osMachine Windows 机器类型（映射为 Node 风格：AMD64→x86_64 等）。
func osMachine() string {
	switch os.Getenv("PROCESSOR_ARCHITECTURE") {
	case "AMD64":
		return "x86_64"
	case "ARM64":
		return "arm64"
	case "x86":
		return "i686"
	default:
		return "x86_64"
	}
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// osTotalMem 总物理内存（字节）。
func osTotalMem() int64 {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r1 == 0 {
		return 0
	}
	return int64(st.TotalPhys)
}

// osFreeMem 可用物理内存（字节）。
func osFreeMem() int64 {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r1 == 0 {
		return 0
	}
	return int64(st.AvailPhys)
}

// osUptime 系统启动时长（秒）。
func osUptime() float64 {
	r1, _, _ := procGetTickCount64.Call()
	return float64(int64(r1)) / 1000.0
}

// osLoadavg Windows 恒 [0,0,0]（Node 实测）。
func osLoadavg() [3]float64 { return [3]float64{0, 0, 0} }

// nodePriorityToWinClass 把 Node PRIORITY_* 数值映射为 Windows 优先级类。
func nodePriorityToWinClass(priority int) uint32 {
	switch priority {
	case 19: // PRIORITY_LOW
		return 0x00000040 // IDLE_PRIORITY_CLASS
	case 10: // PRIORITY_BELOW_NORMAL
		return 0x00004000 // BELOW_NORMAL_PRIORITY_CLASS
	case -7: // PRIORITY_ABOVE_NORMAL
		return 0x00008000 // ABOVE_NORMAL_PRIORITY_CLASS
	case -14: // PRIORITY_HIGH
		return 0x00000080 // HIGH_PRIORITY_CLASS
	case -20: // PRIORITY_HIGHEST
		return 0x00000100 // REALTIME_PRIORITY_CLASS
	default: // PRIORITY_NORMAL
		return 0x00000020 // NORMAL_PRIORITY_CLASS
	}
}

// winClassToNodePriority Windows 优先级类 → Node PRIORITY_* 数值。
func winClassToNodePriority(class uint32) int {
	switch class {
	case 0x00000040:
		return 19
	case 0x00004000:
		return 10
	case 0x00008000:
		return -7
	case 0x00000080:
		return -14
	case 0x00000100:
		return -20
	default:
		return 0
	}
}

// osGetPriority 返回进程优先级（Node PRIORITY_* 数值）。
func osGetPriority(pid int) (int, error) {
	if pid == 0 || pid == os.Getpid() {
		handle, err := syscall.GetCurrentProcess()
		if err != nil {
			return 0, err
		}
		r1, _, e := procGetPriorityClass.Call(uintptr(handle))
		if r1 == 0 {
			return 0, e
		}
		return winClassToNodePriority(uint32(r1)), nil
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	handle, _, e := procOpenProcess.Call(PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return 0, e
	}
	defer procCloseHandle.Call(handle)
	r1, _, e2 := procGetPriorityClass.Call(handle)
	if r1 == 0 {
		return 0, e2
	}
	return winClassToNodePriority(uint32(r1)), nil
}

// osSetPriority 设置进程优先级。
func osSetPriority(pid int, priority int) error {
	if pid == 0 || pid == os.Getpid() {
		handle, err := syscall.GetCurrentProcess()
		if err != nil {
			return err
		}
		r1, _, e := procSetPriorityClass.Call(uintptr(handle), uintptr(nodePriorityToWinClass(priority)))
		if r1 == 0 {
			return e
		}
		return nil
	}
	const PROCESS_SET_INFORMATION = 0x0200
	handle, _, e := procOpenProcess.Call(PROCESS_SET_INFORMATION, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return e
	}
	defer procCloseHandle.Call(handle)
	r1, _, e2 := procSetPriorityClass.Call(handle, uintptr(nodePriorityToWinClass(priority)))
	if r1 == 0 {
		return e2
	}
	return nil
}

// itoa 简易整数转字符串（避免依赖 strconv 的分文件差异）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

type osVersionInfoExW struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformId        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

var versionOnce = false
var versionMajor, versionMinor, versionBuild uint32

// osVersionParts 返回 Windows 主/次/构建号。
func osVersionParts() (int, int, int, error) {
	if !versionOnce {
		var vi osVersionInfoExW
		vi.OSVersionInfoSize = uint32(unsafe.Sizeof(vi))
		if r1, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&vi))); r1 == 0 {
			versionMajor = vi.MajorVersion
			versionMinor = vi.MinorVersion
			versionBuild = vi.BuildNumber
		}
		versionOnce = true
	}
	return int(versionMajor), int(versionMinor), int(versionBuild), nil
}

// osProductName Windows 产品名（注册表 ProductName；构建号 ≥ 22000 时
// "Windows 10" 替换为 "Windows 11"——与 Node 一致，注册表值未随 11 更新）。
func osProductName() string {
	data, err := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
	if err != nil {
		return "Windows"
	}
	major, _, build, err := osVersionParts()
	if err == nil && major >= 10 && build >= 22000 && len(data) >= 9 && data[:9] == "Windows 1" {
		// "Windows 10 ..." → "Windows 11 ..."（Node 的映射）。
		if strings.HasPrefix(data, "Windows 10") {
			data = "Windows 11" + data[len("Windows 10"):]
		}
	}
	return data
}

func readRegistryString(subkey, value string) (string, error) {
	k, err := openRegKey(subkey)
	if err != nil {
		return "", err
	}
	defer closeRegKey(k)
	buf := make([]byte, 4096)
	var size uint32 = 4096
	var typ uint32
	r1, _, e := procRegQueryValueExW.Call(k,
		uintptr(unsafe.Pointer(regUTF16Ptr(value))),
		uintptr(0),
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)))
	if r1 != 0 {
		return "", e
	}
	// buf 内为 UTF-16LE 字符串。
	u16 := buf[:size]
	chars := make([]uint16, 0, len(u16)/2)
	for i := 0; i+1 < len(u16); i += 2 {
		c := uint16(u16[i]) | uint16(u16[i+1])<<8
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return syscall.UTF16ToString(chars), nil
}

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

func regUTF16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func openRegKey(subkey string) (uintptr, error) {
	const HKEY_LOCAL_MACHINE = 0x80000002
	const KEY_READ = 0x20019
	var h uintptr
	r1, _, e := procRegOpenKeyExW.Call(HKEY_LOCAL_MACHINE, uintptr(unsafe.Pointer(regUTF16Ptr(subkey))), 0, KEY_READ, uintptr(unsafe.Pointer(&h)))
	if r1 != 0 {
		return 0, e
	}
	return h, nil
}

func closeRegKey(h uintptr) {
	procRegCloseKey.Call(h)
}
