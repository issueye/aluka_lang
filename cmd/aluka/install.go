// Package main — aluka install / add / remove（Phase 5 WBS 5.8/5.9）。
//
//   aluka install [pkg]    解析 package.json 安装全部依赖；给定 pkg 时先 add 再装
//   aluka add <pkg>        解析最新版本写入 package.json 并安装
//   aluka remove <pkg>     从 package.json 移除并重新安装
//   aluka update           同 install（重新解析）
//
// registry 地址经环境变量 ALUKA_REGISTRY 覆盖（默认 https://registry.npmjs.org）。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/pkgmanager/installer"
	"github.com/aluka-lang/aluka/internal/pkgmanager/lockfile"
	"github.com/aluka-lang/aluka/internal/pkgmanager/registry"
	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
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

// runInstall 解析并安装 package.json 的依赖。
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
	if len(deps) == 0 {
		fmt.Println("No dependencies found in package.json")
		return nil
	}
	client := registry.New(registryFromEnv())
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
	return nil
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
		client := registry.New(registryFromEnv())
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

// registryFromEnv 读取 ALUKA_REGISTRY（可为空，用默认）。
func registryFromEnv() string {
	return os.Getenv("ALUKA_REGISTRY")
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
