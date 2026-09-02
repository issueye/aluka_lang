// 跨领域共享 helper（分包基座）。

package globals

import (
	"runtime"
)

// platformName 返回 Node.js 风格的平台名。
func platformName() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "windows":
		return "win32"
	case "freebsd":
		return "freebsd"
	default:
		return runtime.GOOS
	}
}

// archName 返回 Node.js 风格的架构名。
func archName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH
	}
}
