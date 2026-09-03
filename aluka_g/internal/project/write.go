package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OutputDir 统一计算 web 产物输出目录（OutFile 时为其父目录）。
func OutputDir(opts Options) string {
	if opts.OutDir != "" {
		return opts.OutDir
	}
	if opts.OutFile != "" {
		return filepath.Dir(opts.OutFile)
	}
	return "."
}

// PrimaryName 返回 web 构建的主产物资产名：HTML/CSS 入口为同名文件，
// JS/TS/TSX 入口为去扩展名 + ".js"。
func PrimaryName(entry string) string {
	base := filepath.Base(entry)
	ext := strings.ToLower(filepath.Ext(entry))
	if ext == ".html" || ext == ".css" {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) + ".js"
}

// assetTarget 把 name 解析到 outDir 下。拒绝空、绝对路径、盘符，
// 以及相对 outDir 逃逸（Rel 以 ".." 开头）的路径。
func assetTarget(outDir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("asset name is empty")
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("asset name %q escapes outDir (absolute path)", name)
	}
	// Windows 盘符相对形式（如 C:foo）经 FromSlash 后仍可能被 IsAbs 漏掉。
	if vol := filepath.VolumeName(cleaned); vol != "" {
		return "", fmt.Errorf("asset name %q escapes outDir (volume path)", name)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset name %q escapes outDir", name)
	}
	if outDir == "" {
		outDir = "."
	}
	dest := filepath.Join(outDir, cleaned)
	rel, err := filepath.Rel(outDir, dest)
	if err != nil {
		return "", fmt.Errorf("asset name %q: %w", name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset name %q escapes outDir", name)
	}
	return dest, nil
}

// WriteAssets 写出产物；written 非 nil 时清理上一轮残留文件，然后调用 writeBundle。
func WriteAssets(entry string, assets map[string][]byte, opts Options, written map[string]bool) error {
	if err := writeAssets(entry, assets, opts); err != nil {
		return err
	}
	if written != nil {
		outDir := OutputDir(opts)
		current := map[string]bool{}
		for name := range assets {
			var target string
			if opts.OutFile != "" && name == PrimaryName(entry) {
				target = opts.OutFile
			} else {
				var err error
				target, err = assetTarget(outDir, name)
				if err != nil {
					return err
				}
			}
			current[target] = true
		}
		for target := range written {
			if !current[target] {
				_ = os.Remove(target)
			}
		}
		for target := range current {
			written[target] = true
		}
	}
	return opts.Host().WriteBundle(assetNames(assets))
}

func writeAssets(entry string, assets map[string][]byte, opts Options) error {
	outDir := opts.OutDir
	if outDir == "" && opts.OutFile != "" {
		outDir = filepath.Dir(opts.OutFile)
	}
	if outDir == "" {
		outDir = "."
	}
	if outDir != "." {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
	}
	for name, data := range assets {
		var target string
		if opts.OutFile != "" && name == PrimaryName(entry) {
			target = opts.OutFile
		} else {
			var err error
			target, err = assetTarget(outDir, name)
			if err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
