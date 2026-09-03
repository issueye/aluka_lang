// Package installer 实现 node_modules 布局与并发下载解压（Phase 5 WBS 5.4/5.5）。
//
// 布局（P0 简化 hoisting）：所有解析包扁平放入 node_modules/<name>；
// scoped 包写入 node_modules/@scope/<name>。tarball 用 Go archive/tar +
// compress/gzip 解压，剥离 npm 的 package/ 顶层前缀，并做路径穿越防护。
package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/pkgmanager/registry"
	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
)

// Installer 负责把解析结果安装到 node_modules。
type Installer struct {
	RootDir     string
	Client      *registry.Client
	Concurrency int
	// OnInstall 每装好一个包回调（可空，用于进度输出）。
	OnInstall func(name, version string)
}

// New 创建安装器，默认并发 8。
func New(rootDir string, client *registry.Client) *Installer {
	return &Installer{RootDir: rootDir, Client: client, Concurrency: 8}
}

// Install 安装全部包并返回首个错误。
func (inst *Installer) Install(res *resolver.Resolution) error {
	nodeModules := filepath.Join(inst.RootDir, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		return err
	}
	sem := make(chan struct{}, inst.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, pkg := range res.PkgOrder() {
		wg.Add(1)
		sem <- struct{}{}
		go func(p *resolver.Resolved) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := inst.installPkg(nodeModules, p); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("install %s@%s: %w", p.Name, p.Version, err)
				}
				mu.Unlock()
				return
			}
			if inst.OnInstall != nil {
				inst.OnInstall(p.Name, p.Version)
			}
		}(pkg)
	}
	wg.Wait()
	return firstErr
}

// installPkg 下载并解压单个包。
func (inst *Installer) installPkg(nodeModules string, p *resolver.Resolved) error {
	data, err := inst.Client.DownloadTarball(p.Tarball)
	if err != nil {
		return err
	}
	target := filepath.Join(nodeModules, filepath.FromSlash(p.Name))
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	// 清空目标目录（幂等重装）。
	if err := clearDir(target); err != nil {
		return err
	}
	return untar(data, target)
}

// clearDir 清空目录内容（保留目录本身）。
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// untar 解压 tar.gz 字节到 target（剥离 package/ 顶层前缀，防路径穿越）。
func untar(data []byte, target string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	cleanTarget := filepath.Clean(target)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		name := stripTopLevel(hdr.Name)
		if name == "" {
			continue
		}
		full := filepath.Clean(filepath.Join(target, filepath.FromSlash(name)))
		if full != cleanTarget && !strings.HasPrefix(full, cleanTarget+string(filepath.Separator)) {
			continue // 路径穿越防护。
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(full, 0o755)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			if err := writeFile(full, tr, hdr.Size); err != nil {
				return err
			}
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.Symlink(hdr.Linkname, full) // 失败容错（Windows 权限场景）。
		}
	}
	return nil
}

// writeFile 写常规文件（限制大小防 zip bomb：单文件 256MB）。
func writeFile(path string, r io.Reader, size int64) error {
	if size > 256<<20 {
		return fmt.Errorf("file too large: %s (%d bytes)", path, size)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.CopyN(f, r, size); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// stripTopLevel 剥离 tarball 顶层目录（npm 的 package/）。
// 返回 "" 表示该条目本身就是顶层目录/文件（跳过）。
func stripTopLevel(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return ""
}
