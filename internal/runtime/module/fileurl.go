package module

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// NormalizeModulePath 将 file:// URL 或本机路径规范为本机路径。
// 非 file URL 原样返回（相对路径留给调用方 Abs / Join）。
func NormalizeModulePath(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return input
	}
	if isFileURL(trimmed) {
		return FileURLToPath(trimmed)
	}
	return input
}

func isFileURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "file:")
}

// FileURLToPath 将 file:// URL 转成本机路径（对齐 Node fileURLToPath / Windows 盘符语义）。
func FileURLToPath(input string) string {
	u, err := url.Parse(input)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		return input
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		// UNC：file://server/share → \\server\share
		p := filepath.FromSlash(u.Path)
		if runtime.GOOS == "windows" {
			return `\\` + u.Host + p
		}
		return "//" + u.Host + u.Path
	}
	p := u.Path // 已解码
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(p, "/") {
			return filepath.FromSlash(p)
		}
		// /C:/x → C:/x
		if len(p) >= 3 && p[2] == ':' {
			p = p[1:]
		} else if len(p) >= 3 && p[2] == '|' {
			p = string(p[1]) + ":" + p[3:]
		}
	}
	return filepath.FromSlash(p)
}

// PathToFileURLString 将绝对路径转为 file:// URL（Windows 驱动器盘符带斜杠）。
// 经 url.URL.String() 做百分号转义（# → %23、空格 → %20、? → %3F、
// 非 ASCII → UTF-8 百分号编码），对齐 Node pathToFileURL；不转义时含 #
// 的路径会被 FileURLToPath 的 url.Parse 当成 fragment 截断。
func PathToFileURLString(abs string) string {
	slash := filepath.ToSlash(abs)
	if len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	return u.String()
}
