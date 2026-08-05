// Package workspace 实现 npm workspace（monorepo）发现（Phase 5 WBS 5.11）。
//
// 根 package.json 的 workspaces 字段（数组或 {packages: []}）声明 glob 模式，
// 如 "packages/*" / "packages/**"。Discover 展开 glob、为每个匹配目录读取其
// package.json，返回本地包（名称/版本/依赖）。本地包名在依赖解析时优先于
// registry（不下载，直接链接进 node_modules）。
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
)

// Package 描述一个 workspace 包。
type Package struct {
	Name         string
	Version      string
	Dir          string // 绝对路径
	Dependencies []resolver.Dep
}

// Globs 从根 package.json 提取 workspace 模式列表。
func Globs(pj map[string]any) []string {
	raw, ok := pj["workspaces"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		if pkgs, ok := v["packages"].([]any); ok {
			var out []string
			for _, e := range pkgs {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// Discover 发现根目录下的全部 workspace 包。
func Discover(rootDir string) ([]*Package, error) {
	pj, err := readPkgJSON(filepath.Join(rootDir, "package.json"))
	if err != nil {
		return nil, err
	}
	globs := Globs(pj)
	if len(globs) == 0 {
		return nil, nil
	}
	dirs := expandGlobs(rootDir, globs)
	var pkgs []*Package
	for _, dir := range dirs {
		wp, err := readWorkspacePackage(dir)
		if err != nil {
			return nil, err
		}
		if wp == nil {
			continue
		}
		pkgs = append(pkgs, wp)
	}
	return pkgs, nil
}

// readWorkspacePackage 读取单个 workspace 包的 package.json。
func readWorkspacePackage(dir string) (*Package, error) {
	pj, err := readPkgJSON(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	name, _ := pj["name"].(string)
	if name == "" {
		return nil, nil // 无 name 的目录不是 workspace 包
	}
	wp := &Package{Name: name, Dir: dir}
	if v, ok := pj["version"].(string); ok {
		wp.Version = v
	}
	for n, r := range depsOf(pj, "dependencies") {
		wp.Dependencies = append(wp.Dependencies, resolver.Dep{Name: n, Range: r})
	}
	for n, r := range depsOf(pj, "devDependencies") {
		wp.Dependencies = append(wp.Dependencies, resolver.Dep{Name: n, Range: r})
	}
	return wp, nil
}

// expandGlobs 展开 workspace glob 模式（支持 ** / * / ? 与 ! 排除）。
func expandGlobs(rootDir string, globs []string) []string {
	var includes, excludes []string
	for _, g := range globs {
		if strings.HasPrefix(g, "!") {
			excludes = append(excludes, strings.TrimPrefix(g, "!"))
		} else {
			includes = append(includes, g)
		}
	}
	var dirs []string
	seen := map[string]bool{}
	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == rootDir {
			return nil
		}
		rel := filepath.ToSlash(mustRel(rootDir, path))
		if isIgnoredDir(rel) {
			return filepath.SkipDir
		}
		excluded := false
		for _, ex := range excludes {
			if matchGlob(ex, rel) {
				excluded = true
				break
			}
		}
		if excluded || seen[path] {
			return nil
		}
		for _, inc := range includes {
			if matchGlob(inc, rel) {
				seen[path] = true
				dirs = append(dirs, path)
				break
			}
		}
		return nil
	})
	return dirs
}

// matchGlob 将 glob 模式与相对路径匹配（支持 ** 跨目录）。
func matchGlob(pattern, rel string) bool {
	pat := strings.Split(filepath.ToSlash(strings.TrimSuffix(pattern, "/")), "/")
	path := strings.Split(filepath.ToSlash(rel), "/")
	return matchSegments(pat, path)
}

// matchSegments 递归匹配路径段。
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, _ := filepath.Match(pat[0], path[0])
	if !ok {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// isIgnoredDir 判断目录是否应跳过（node_modules / 隐藏目录）。
func isIgnoredDir(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg == "node_modules" || seg == ".git" || seg == ".aluka" || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
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

// depsOf 从通用 map 提取依赖对象。
func depsOf(m map[string]any, key string) map[string]string {
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
