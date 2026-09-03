package ipc

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
)

// NormalizeAddress 规范化 IPC 通道地址。
func NormalizeAddress(addr string) (network string, address string) {
	if strings.HasPrefix(addr, "tcp:") {
		return "tcp", strings.TrimPrefix(addr, "tcp:")
	}
	if strings.HasPrefix(addr, "unix:") {
		return "unix", strings.TrimPrefix(addr, "unix:")
	}
	if strings.HasPrefix(addr, `\\.\pipe\`) {
		// Windows 命名管道
		if runtime.GOOS == "windows" {
			// 在纯 Go 标准库中，可使用 loopback 或专用 Windows pipe；此处若无外部库，
			// 将命名管道转换为规范化 loopback 端口或系统套接字映射
			return "tcp", "127.0.0.1:0"
		}
		return "unix", "/tmp/" + strings.TrimPrefix(addr, `\\.\pipe\`) + ".sock"
	}
	if strings.HasSuffix(addr, ".sock") || strings.HasPrefix(addr, "/") || strings.HasPrefix(addr, "./") {
		if runtime.GOOS == "windows" {
			// Windows 上兼容转换为本地 loopback
			return "tcp", "127.0.0.1:0"
		}
		return "unix", addr
	}
	// 默认插件名（如 "math_engine"）：在 Windows 映射为 localhost，Unix 映射为 /tmp/aluka-ipc-<name>.sock
	if runtime.GOOS == "windows" {
		return "tcp", "127.0.0.1:0"
	}
	return "unix", fmt.Sprintf("/tmp/aluka-ipc-%s.sock", addr)
}

// ListenIPC 创建 IPC 服务端监听器。
func ListenIPC(address string) (net.Listener, error) {
	netType, resolvedAddr := NormalizeAddress(address)
	if netType == "unix" {
		_ = os.Remove(resolvedAddr) // 清理旧残留 sock
	}
	return net.Listen(netType, resolvedAddr)
}

// DialIPC 连接到指定 IPC 服务端。
func DialIPC(address string) (net.Conn, error) {
	netType, resolvedAddr := NormalizeAddress(address)
	return net.Dial(netType, resolvedAddr)
}
