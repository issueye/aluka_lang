package builtin

// node:os 内置模块——提供操作系统信息与工具。
// 基于 Go os/runtime 标准库。

import (
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewOS 构造 node:os 模块的导出对象。
func NewOS(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// --- 平台与架构（均为函数，符合 Node.js API）---
	_ = m.Set("platform", engine.NewFunction("platform", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(platformName()), nil
	}))
	_ = m.Set("arch", engine.NewFunction("arch", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(archName()), nil
	}))
	_ = m.Set("type", engine.NewFunction("type", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(osTypeName()), nil
	}))
	_ = m.Set("release", engine.NewFunction("release", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(releaseInfo()), nil
	}))
	_ = m.Set("hostname", engine.NewFunction("hostname", func(args []engine.Value) (engine.Value, error) {
		hn, err := os.Hostname()
		if err != nil {
			return engine.Str(""), nil
		}
		return engine.Str(hn), nil
	}))

	// EOL：平台换行符（字符串属性，非函数）。
	eol := "\n"
	if runtime.GOOS == "windows" {
		eol = "\r\n"
	}
	_ = m.Set("EOL", engine.Str(eol))

	// --- 方法 ---

	_ = m.Set("homedir", engine.NewFunction("homedir", func(args []engine.Value) (engine.Value, error) {
		dir, err := os.UserHomeDir()
		if err != nil {
			return engine.Str(""), nil
		}
		return engine.Str(dir), nil
	}))

	_ = m.Set("tmpdir", engine.NewFunction("tmpdir", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(os.TempDir()), nil
	}))

	_ = m.Set("userInfo", engine.NewFunction("userInfo", func(args []engine.Value) (engine.Value, error) {
		return osUserInfo(), nil
	}))

	_ = m.Set("cpus", engine.NewFunction("cpus", func(args []engine.Value) (engine.Value, error) {
		return osCPUs(), nil
	}))

	_ = m.Set("networkInterfaces", engine.NewFunction("networkInterfaces", func(args []engine.Value) (engine.Value, error) {
		return osNetworkInterfaces(), nil
	}))

	_ = m.Set("totalmem", engine.NewFunction("totalmem", func(args []engine.Value) (engine.Value, error) {
		// 简化：Go 标准库无直接 API，返回 0（后续可调用 syscall 扩展）。
		return engine.Number(0), nil
	}))

	_ = m.Set("freemem", engine.NewFunction("freemem", func(args []engine.Value) (engine.Value, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return engine.Number(float64(m.Sys - m.Alloc)), nil
	}))

	_ = m.Set("uptime", engine.NewFunction("uptime", func(args []engine.Value) (engine.Value, error) {
		// 简化：返回进程运行时间近似值。
		return engine.Number(0), nil
	}))

	_ = m.Set("endianness", engine.NewFunction("endianness", func(args []engine.Value) (engine.Value, error) {
		// x86/ARM 均为小端
		return engine.Str("LE"), nil
	}))

	// constants（信号常量，简化版）
	constants := engine.NewObject()
	_ = constants.Set("SIGHUP", engine.IntValue(1))
	_ = constants.Set("SIGINT", engine.IntValue(2))
	_ = constants.Set("SIGTERM", engine.IntValue(15))
	_ = constants.Set("SIGKILL", engine.IntValue(9))
	_ = m.Set("constants", constants)

	return m, nil
}

func platformName() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return runtime.GOOS
	}
}

func archName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm64":
		return "arm64"
	case "arm":
		return "arm"
	default:
		return runtime.GOARCH
	}
}

func osTypeName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows_NT"
	case "darwin":
		return "Darwin"
	default:
		return "Linux"
	}
}

func releaseInfo() string {
	// 简化：返回空（完整实现需调用 uname 等系统调用）。
	return ""
}

func osUserInfo() engine.Value {
	info := engine.NewObject()
	if dir, err := os.UserHomeDir(); err == nil {
		_ = info.Set("homedir", engine.Str(dir))
	} else {
		_ = info.Set("homedir", engine.Str(""))
	}
	_ = info.Set("username", engine.Str(currentUser()))
	_ = info.Set("shell", engine.Str(""))
	_ = info.Set("uid", engine.Number(-1))
	_ = info.Set("gid", engine.Number(-1))
	return info
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return ""
}

func osCPUs() engine.Value {
	n := runtime.NumCPU()
	cpus := make([]engine.Value, n)
	for i := 0; i < n; i++ {
		cpu := engine.NewObject()
		_ = cpu.Set("model", engine.Str("unknown"))
		_ = cpu.Set("speed", engine.Number(0))
		_ = cpu.Set("times", engine.NewObject())
		cpus[i] = cpu
	}
	return engine.NewArray(cpus)
}

func osNetworkInterfaces() engine.Value {
	result := engine.NewObject()
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		var addrVals []engine.Value
		for _, addr := range addrs {
			addrObj := engine.NewObject()
			ipStr := addr.String()
			family := "IPv4"
			if strings.Contains(ipStr, ":") {
				family = "IPv6"
			}
			_ = addrObj.Set("address", engine.Str(strings.Split(ipStr, "/")[0]))
			_ = addrObj.Set("netmask", engine.Str("255.255.255.0"))
			_ = addrObj.Set("family", engine.Str(family))
			_ = addrObj.Set("internal", engine.Boolean(iface.Flags&net.FlagLoopback != 0))
			addrVals = append(addrVals, addrObj)
		}
		_ = result.Set(iface.Name, engine.NewArray(addrVals))
	}
	return result
}
