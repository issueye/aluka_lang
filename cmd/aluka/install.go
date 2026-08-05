// Package main — aluka install / add / remove（Phase 5 WBS 5.8/5.9）。
//
//	aluka install [pkg]    解析 package.json 安装全部依赖；给定 pkg 时先 add 再装
//	aluka add <pkg>        解析最新版本写入 package.json 并安装
//	aluka remove <pkg>     从 package.json 移除并重新安装
//	aluka update           同 install（重新解析）
//
// registry 与鉴权配置优先级：环境变量 ALUKA_REGISTRY > 项目 .npmrc >
// 用户 ~/.npmrc > 默认 https://registry.npmjs.org。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/pkgmanager/config"
	"github.com/aluka-lang/aluka/internal/pkgmanager/installer"
	"github.com/aluka-lang/aluka/internal/pkgmanager/lockfile"
	"github.com/aluka-lang/aluka/internal/pkgmanager/registry"
	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
	"github.com/aluka-lang/aluka/internal/pkgmanager/workspace"
)

// cmdPkg 分发 install/add/remove/update。
func cmdPkg(first string, args []string) {
	dir, err := os.Getwd()
	if err != nil {
		fatalErr("aluka: " + err.Error())
	}
	pkgPath := filepath.Join(dir, "package.json")
	switch first {
	case "install", "update":
		if len(args) >= 1 {
			// aluka install <pkg> → add 语义。
			if err := addPackage(dir, pkgPath, args[0]); err != nil {
				fatalErr("aluka install: " + err.Error())
			}
			return
		}
		if err := runInstall(dir, pkgPath); err != nil {
			fatalErr("aluka install: " + err.Error())
		}
	case "add":
		if len(args) < 1 {
			fatalErr("aluka add: missing package name")
		}
		if err := addPackage(dir, pkgPath, args[0]); err != nil {
			fatalErr("aluka add: " + err.Error())
		}
	case "remove":
		if len(args) < 1 {
			fatalErr("aluka remove: missing package name")
		}
		if err := removePackage(dir, pkgPath, args[0]); err != nil {
			fatalErr("aluka remove: " + err.Error())
		}
	}
}

// runInstall 解析并安装 package.json 的依赖（含 workspace 支持）。
func runInstall(dir, pkgPath string) error {
	pj, err := readPkgJSON(pkgPath)
	if err != nil {
		return err
	}
	var deps []resolver.Dep
	for n, r := range depsMap(pj, "dependencies") {
		deps = append(deps, resolver.Dep{Name: n, Range: r})
	}
	for n, r := range depsMap(pj, "devDependencies") {
		deps = append(deps, resolver.Dep{Name: n, Range: r})
	}

	// --- workspace 支持：聚合各包依赖，本地包名不解析 registry ---
	local := map[string]string{} // 本地包名 → 目录
	wpkgs, err := workspace.Discover(dir)
	if err != nil {
		return err
	}
	if len(wpkgs) > 0 {
		fmt.Printf("found %d workspace packages\n", len(wpkgs))
		for _, wp := range wpkgs {
			local[wp.Name] = wp.Dir
		}
		for _, wp := range wpkgs {
			for _, d := range wp.Dependencies {
				if _, isLocal := local[d.Name]; !isLocal {
					deps = append(deps, d)
				}
			}
		}
	}
	if len(wpkgs) > 0 {
		filtered := deps[:0]
		for _, d := range deps {
			if _, isLocal := local[d.Name]; !isLocal {
				filtered = append(filtered, d)
			}
		}
		deps = filtered
	}

	if len(deps) > 0 {
		client := newRegistryClient(dir)
		res := resolver.New(client)
		resolution, err := res.Resolve(deps)
		if err != nil {
			return err
		}
		inst := installer.New(dir, client)
		inst.OnInstall = func(name, version string) {
			fmt.Printf("installed %s@%s\n", name, version)
		}
		if err := inst.Install(resolution); err != nil {
			return err
		}
		if err := lockfile.Write(filepath.Join(dir, "aluka.lock"), resolution); err != nil {
			return err
		}
		fmt.Printf("installed %d packages\n", len(resolution.Pkgs))
	} else if len(wpkgs) == 0 {
		fmt.Println("No dependencies found in package.json")
		return nil
	} else if err := lockfile.Write(filepath.Join(dir, "aluka.lock"), &resolver.Resolution{}); err != nil {
		// workspace-only 场景：无外部依赖，仍生成 lockfile 占位。
		return err
	}

	// 链接本地 workspace 包到根 node_modules。
	for name, wpDir := range local {
		if err := linkLocalPackage(dir, name, wpDir); err != nil {
			return err
		}
	}
	return nil
}

// linkLocalPackage 将 workspace 包链接进 node_modules（symlink，失败回退拷贝）。
func linkLocalPackage(rootDir, name, wpDir string) error {
	target := filepath.Join(rootDir, "node_modules", filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.Symlink(wpDir, target); err == nil {
		return nil
	}
	return copyDir(wpDir, target)
}

// copyDir 递归拷贝目录（跳过符号链接）。
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode())
	})
}

// addPackage 解析 pkg 最新版本写入 package.json 并安装。
func addPackage(dir, pkgPath, spec string) error {
	name, rng := splitSpec(spec)
	if name == "" {
		return fmt.Errorf("invalid package spec %q", spec)
	}
	pj, err := readPkgJSON(pkgPath)
	if err != nil {
		return err
	}
	deps := depsMap(pj, "dependencies")
	if rng == "" {
		client := newRegistryClient(dir)
		md, err := client.GetMetadata(name)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", name, err)
		}
		latest := md.DistTags["latest"]
		if latest == "" {
			return fmt.Errorf("no latest dist-tag for %s", name)
		}
		rng = "^" + latest
	}
	deps[name] = rng
	setDeps(pj, "dependencies", deps)
	if err := writePkgJSON(pkgPath, pj); err != nil {
		return err
	}
	fmt.Printf("added %s@%s\n", name, rng)
	return runInstall(dir, pkgPath)
}

// removePackage 从 package.json 移除包并重新安装。
func removePackage(dir, pkgPath, name string) error {
	pj, err := readPkgJSON(pkgPath)
	if err != nil {
		return err
	}
	deps := depsMap(pj, "dependencies")
	delete(deps, name)
	setDeps(pj, "dependencies", deps)
	dev := depsMap(pj, "devDependencies")
	delete(dev, name)
	setDeps(pj, "devDependencies", dev)
	if err := writePkgJSON(pkgPath, pj); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", name)
	return runInstall(dir, pkgPath)
}

// splitSpec 拆分 "pkg" / "pkg@range" / "@scope/pkg" / "@scope/pkg@range"。
func splitSpec(spec string) (name, rng string) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "@") {
		if i := strings.Index(spec[1:], "@"); i >= 0 {
			return spec[:i+1], spec[i+2:]
		}
		return spec, ""
	}
	if i := strings.LastIndexByte(spec, '@'); i > 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

// newRegistryClient 构造 registry 客户端：ALUKA_REGISTRY env > .npmrc registry
// > 默认；鉴权 token 从 .npmrc 按 registry 主机匹配。
func newRegistryClient(dir string) *registry.Client {
	reg := os.Getenv("ALUKA_REGISTRY")
	token := ""
	if cfg, err := config.Load(dir); err == nil {
		if reg == "" {
			reg = cfg.Registry
		}
		token = cfg.TokenFor(reg)
	}
	c := registry.New(reg)
	c.Token = token
	return c
}

// readPkgJSON 读取 package.json（不存在返回空 map）。
func readPkgJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// writePkgJSON 写回 package.json（2 空格缩进）。
func writePkgJSON(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// depsMap 从通用 map 提取依赖对象。
func depsMap(m map[string]any, key string) map[string]string {
	out := map[string]string{}
	if raw, ok := m[key]; ok {
		if obj, ok := raw.(map[string]any); ok {
			for k, v := range obj {
				out[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return out
}

// setDeps 写回依赖对象。
func setDeps(m map[string]any, key string, deps map[string]string) {
	obj := make(map[string]any, len(deps))
	for k, v := range deps {
		obj[k] = v
	}
	m[key] = obj
}
