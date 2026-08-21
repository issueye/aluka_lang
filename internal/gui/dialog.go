package gui

import (
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// NormalizeDialogOptions 把 Electron 风格 properties 与缺省 Type 归一化。
func NormalizeDialogOptions(opts DialogOptions) DialogOptions {
	for _, p := range opts.Properties {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "opendirectory":
			opts.Directory = true
		case "multiselections":
			opts.Multiple = true
		case "openfile":
			if opts.Type == "" {
				opts.Type = "openFile"
			}
		}
	}
	switch strings.ToLower(opts.Type) {
	case "openfile", "open":
		opts.Type = "openFile"
	case "savefile", "save":
		opts.Type = "saveFile"
	case "message", "info", "warning", "error", "question", "":
		if opts.Type == "" && (opts.Directory || opts.Multiple || len(opts.Filters) > 0) {
			opts.Type = "openFile"
		}
	}
	return opts
}

// win32FilterString 生成 GetOpenFileName 用的双 NUL 结尾过滤器。
func win32FilterString(filters []FileFilter) string {
	var b strings.Builder
	for _, f := range filters {
		if f.Name == "" && len(f.Extensions) == 0 {
			continue
		}
		name := f.Name
		if name == "" {
			name = strings.Join(f.Extensions, ",")
		}
		exts := make([]string, 0, len(f.Extensions))
		for _, e := range f.Extensions {
			e = strings.TrimSpace(strings.TrimPrefix(e, "*."))
			e = strings.TrimPrefix(e, ".")
			if e == "" {
				continue
			}
			exts = append(exts, e)
		}
		if len(exts) == 0 {
			continue
		}
		b.WriteString(name)
		b.WriteByte(0)
		b.WriteString("*.")
		b.WriteString(strings.Join(exts, ";*."))
		b.WriteByte(0)
	}
	b.WriteString("All Files (*.*)")
	b.WriteByte(0)
	b.WriteString("*.*")
	b.WriteByte(0)
	b.WriteByte(0)
	return b.String()
}

// parseNULSeparatedPaths 解析 OFN_ALLOWMULTISELECT 缓冲（目录 + 文件名，双 NUL 结束）。
func parseNULSeparatedPaths(buf []uint16) []string {
	n := 0
	for n+1 < len(buf) {
		if buf[n] == 0 && buf[n+1] == 0 {
			break
		}
		n++
	}
	if n == 0 && (len(buf) == 0 || buf[0] == 0) {
		return nil
	}
	raw := utf16.Decode(buf[:n])
	parts := strings.Split(string(raw), "\x00")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return out
	}
	dir := out[0]
	files := make([]string, 0, len(out)-1)
	for _, name := range out[1:] {
		files = append(files, filepath.Join(dir, name))
	}
	return files
}

// messageBoxStyle 将 type/buttons 映射为 Win32 MessageBox 标志与返回值下标。
func messageBoxStyle(opts DialogOptions) (flags uint32, mapRet func(int) int) {
	var icon uint32 = 0x00000040 // MB_ICONINFORMATION
	switch strings.ToLower(opts.Type) {
	case "warning":
		icon = 0x00000030 // MB_ICONWARNING
	case "error":
		icon = 0x00000010 // MB_ICONERROR
	case "question":
		icon = 0x00000020 // MB_ICONQUESTION
	}

	btns := opts.Buttons
	if len(btns) == 0 {
		return icon, func(r int) int {
			if r == 0 {
				return -1
			}
			return 0
		}
	}

	norm := make([]string, len(btns))
	for i, s := range btns {
		norm[i] = strings.ToLower(strings.TrimSpace(s))
	}
	join := strings.Join(norm, "/")

	const (
		mbOK          = 0x00000000
		mbOKCancel    = 0x00000001
		mbYesNoCancel = 0x00000003
		mbYesNo       = 0x00000004
		idOK          = 1
		idCancel      = 2
		idYes         = 6
		idNo          = 7
	)

	indexOf := func(id int) int {
		for i, s := range norm {
			switch id {
			case idOK:
				if s == "ok" || s == "确定" {
					return i
				}
			case idCancel:
				if s == "cancel" || s == "取消" {
					return i
				}
			case idYes:
				if s == "yes" || s == "是" {
					return i
				}
			case idNo:
				if s == "no" || s == "否" {
					return i
				}
			}
		}
		return opts.DefaultID
	}

	switch join {
	case "ok/cancel", "确定/取消":
		return icon | mbOKCancel, indexOf
	case "yes/no", "是/否":
		return icon | mbYesNo, indexOf
	case "yes/no/cancel", "是/否/取消":
		return icon | mbYesNoCancel, indexOf
	default:
		return icon | mbOK, func(r int) int {
			if r == 0 {
				return opts.CancelID
			}
			return 0
		}
	}
}
