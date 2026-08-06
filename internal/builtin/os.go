package builtin

// node:os 内置模块——提供操作系统信息与工具。
// 基于 Go os/runtime 标准库；平台相关数值经 os_platform_*.go 提取。

import (
	"net"
	"os"
	"runtime"

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
	_ = m.Set("version", engine.NewFunction("version", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(osVersionInfo()), nil
	}))
	_ = m.Set("machine", engine.NewFunction("machine", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(osMachine()), nil
	}))
	_ = m.Set("hostname", engine.NewFunction("hostname", func(args []engine.Value) (engine.Value, error) {
		hn, err := os.Hostname()
		if err != nil {
			return engine.Str(""), nil
		}
		return engine.Str(hn), nil
	}))
	_ = m.Set("availableParallelism", engine.NewFunction("availableParallelism", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(runtime.NumCPU()), nil
	}))

	// EOL / devNull：平台属性（字符串）。
	eol := "\n"
	if runtime.GOOS == "windows" {
		eol = "\r\n"
	}
	_ = m.Set("EOL", engine.Str(eol))
	_ = m.Set("devNull", engine.Str(osDevNull()))

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
		return engine.Number(float64(osTotalMem())), nil
	}))

	_ = m.Set("freemem", engine.NewFunction("freemem", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(osFreeMem())), nil
	}))

	_ = m.Set("uptime", engine.NewFunction("uptime", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(osUptime()), nil
	}))

	_ = m.Set("loadavg", engine.NewFunction("loadavg", func(args []engine.Value) (engine.Value, error) {
		la := osLoadavg()
		return engine.NewArray([]engine.Value{
			engine.Number(la[0]), engine.Number(la[1]), engine.Number(la[2]),
		}), nil
	}))

	_ = m.Set("getPriority", engine.NewFunction("getPriority", func(args []engine.Value) (engine.Value, error) {
		pid := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				pid = n
			}
		}
		p, err := osGetPriority(pid)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(p), nil
	}))

	_ = m.Set("setPriority", engine.NewFunction("setPriority", func(args []engine.Value) (engine.Value, error) {
		pid := 0
		priority := 0
		if len(args) == 1 {
			if n, ok := args[0].Int(); ok {
				priority = n
			}
		} else if len(args) >= 2 {
			if n, ok := args[0].Int(); ok {
				pid = n
			}
			if n, ok := args[1].Int(); ok {
				priority = n
			}
		}
		if err := osSetPriority(pid, priority); err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), nil
	}))

	_ = m.Set("endianness", engine.NewFunction("endianness", func(args []engine.Value) (engine.Value, error) {
		return engine.Str("LE"), nil
	}))

	// constants（signals/priority/errno/dlopen/UV）。
	_ = m.Set("constants", osConstantsObject())

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

func osUserInfo() engine.Value {
	info := engine.NewObject()
	if dir, err := os.UserHomeDir(); err == nil {
		_ = info.Set("homedir", engine.Str(dir))
	} else {
		_ = info.Set("homedir", engine.Str(""))
	}
	_ = info.Set("username", engine.Str(currentUser()))
	if s := osShell(); s != "" {
		_ = info.Set("shell", engine.Str(s))
	} else {
		_ = info.Set("shell", engine.Null())
	}
	uid, gid := osUserIDs()
	_ = info.Set("uid", engine.Number(float64(uid)))
	_ = info.Set("gid", engine.Number(float64(gid)))
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
		times := engine.NewObject()
		for _, k := range []string{"user", "nice", "sys", "idle", "irq"} {
			_ = times.Set(k, engine.Number(0))
		}
		_ = cpu.Set("times", times)
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
		// 过滤未启用的适配器（Node/libuv 只暴露 IFF_UP|IFF_RUNNING）。
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var addrVals []engine.Value
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			addrObj := engine.NewObject()
			ipStr := ip.String()
			family := "IPv4"
			if ip.To4() == nil {
				family = "IPv6"
			}
			_ = addrObj.Set("address", engine.Str(ipStr))
			_ = addrObj.Set("netmask", engine.Str(ipMaskString(ipnet.Mask, family)))
			_ = addrObj.Set("family", engine.Str(family))
			_ = addrObj.Set("mac", engine.Str(iface.HardwareAddr.String()))
			_ = addrObj.Set("internal", engine.Boolean(iface.Flags&net.FlagLoopback != 0))
			_ = addrObj.Set("cidr", engine.Str(ipnet.String()))
			if family == "IPv6" && ip.IsLinkLocalUnicast() {
				_ = addrObj.Set("scopeid", engine.IntValue(iface.Index))
			} else if family == "IPv6" {
				_ = addrObj.Set("scopeid", engine.IntValue(0))
			}
			addrVals = append(addrVals, addrObj)
		}
		if len(addrVals) > 0 {
			_ = result.Set(iface.Name, engine.NewArray(addrVals))
		}
	}
	return result
}

// ipMaskString 把 net.IPMask 格式化为 Node 风格 netmask：
// IPv4 点分十进制；IPv6 压缩十六进制（如 ffff:ffff:ffff:ffff::）。
func ipMaskString(m net.IPMask, family string) string {
	if family == "IPv4" && len(m) == 4 {
		return net.IPv4(m[0], m[1], m[2], m[3]).String()
	}
	// IPv6：补齐 16 字节后经 net.IP.String 压缩。
	if len(m) < 16 {
		v6 := make(net.IPMask, 16)
		copy(v6, m)
		m = v6
	}
	return net.IP(m).String()
}
