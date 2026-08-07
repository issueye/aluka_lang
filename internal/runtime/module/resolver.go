// Package module implements the module system: Node.js-style module resolution
// (1C.11), CommonJS loader (1C.10), and ESM loader (1C.9).
//
// Resolution algorithm follows Node.js conventions:
//   - Relative paths (./, ../) resolve against the parent module's directory.
//   - Bare specifiers resolve via node_modules lookup.
//   - Extensions tried: .js, .mjs, .cjs, .json (and .ts in TS phase).
//   - Directory resolution: package.json "main" field, then index.*.
package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolver resolves module specifiers to absolute file paths.
type Resolver struct {
	// Extensions lists the file extensions tried during resolution (in order).
	Extensions []string

	// IndexNames lists the index file names tried for directory resolution.
	IndexNames []string

	// tsconfigCache 缓存已解析的 tsconfig.json（路径别名支持，1C.12/1C.13）。
	tsconfigCache *tsconfigCache
}

// NewResolver creates a Resolver with Node.js defaults.
// 扩展名补全顺序遵循需求文档 3.3.2（含 TS 扩展名）。
func NewResolver() *Resolver {
	return &Resolver{
		Extensions:    []string{".ts", ".mts", ".cts", ".js", ".mjs", ".cjs", ".json"},
		IndexNames:    []string{"index.ts", "index.mts", "index.cts", "index.js", "index.mjs", "index.cjs", "index.json"},
		tsconfigCache: newTsconfigCache(),
	}
}

// Resolve resolves a module specifier to an absolute file path.
// parentPath is the path of the module that initiated the resolution (for
// relative paths and node_modules lookup); it may be empty for the entry point.
//
// 以 require 语境解析（CJS require / require.resolve）——exports 条件集合
// 不含 "import"，与 Node 的 require() 语义一致。
func (r *Resolver) Resolve(specifier, parentPath string) (string, error) {
	return r.resolve(specifier, parentPath, requireConditions)
}

// ResolveImport 以 import 语境解析（ESM 静态导入 / 动态 import() /
// import.meta.resolve）——exports 条件集合含 "import" 不含 "require"，
// 与 Node 的 import 语义一致。
func (r *Resolver) ResolveImport(specifier, parentPath string) (string, error) {
	return r.resolve(specifier, parentPath, importConditions)
}

func (r *Resolver) resolve(specifier, parentPath string, conditions []string) (string, error) {
	// Absolute path (e.g. /foo/bar.js or C:\foo\bar.js)
	if filepath.IsAbs(specifier) {
		return r.resolveFileOrDir(specifier)
	}

	// Relative path: ./ or ../
	if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") {
		base := filepath.Dir(parentPath)
		full := filepath.Join(base, filepath.FromSlash(specifier))
		return r.resolveFileOrDir(full)
	}

	// Bare specifier: 先尝试 tsconfig paths 别名（1C.13），再回退 node_modules。
	if candidates := r.resolvePaths(specifier, filepath.Dir(parentPath)); len(candidates) > 0 {
		for _, cand := range candidates {
			if resolved, err := r.resolveFileOrDir(cand); err == nil {
				return resolved, nil
			}
		}
	}

	// 包内 imports 映射（#subpath）：从父模块目录向上找最近的 package.json，
	// 解析其 "imports" 字段（支持 node/import/require/default 条件与通配）。
	if strings.HasPrefix(specifier, "#") {
		if resolved, ok := r.resolvePackageImports(specifier, parentPath, conditions); ok {
			return resolved, nil
		}
	}

	// Bare specifier: walk up node_modules
	return r.resolveBare(specifier, parentPath, conditions)
}

// resolvePackageImports 解析 package.json 的 "imports" 映射（如 chalk 的
// "#ansi-styles"）。支持条件对象（node/import/require/default）与通配
// 子路径（"#util/*" → "./lib/*.js"，* 替换为匹配段——Node 语义）。
func (r *Resolver) resolvePackageImports(specifier, parentPath string, conditions []string) (string, bool) {
	dir := filepath.Dir(parentPath)
	for {
		pkgPath := filepath.Join(dir, "package.json")
		data, err := os.ReadFile(pkgPath)
		if err == nil {
			var pkg struct {
				Imports map[string]json.RawMessage `json:"imports"`
			}
			if json.Unmarshal(data, &pkg) == nil {
				if raw, star, matched := matchPackageImport(pkg.Imports, specifier); matched {
					target := conditionalExportTarget(raw, conditions)
					if target == "" {
						return "", false
					}
					// 通配替换：目标中的 * → 匹配段。
					if star != "" && strings.Contains(target, "*") {
						target = strings.ReplaceAll(target, "*", star)
					}
					full := filepath.Join(dir, filepath.FromSlash(target))
					if resolved, err := r.resolveFileOrDir(full); err == nil {
						return resolved, true
					}
				}
			}
			return "", false // 已到最近 package.json：不再向上
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// matchPackageImport 从 imports 映射匹配 specifier：精确键优先，其次最长
// 通配键。返回（目标 raw、通配匹配段、是否命中）。
func matchPackageImport(imports map[string]json.RawMessage, specifier string) (json.RawMessage, string, bool) {
	if raw, ok := imports[specifier]; ok {
		return raw, "", true
	}
	bestKey := ""
	var bestRaw json.RawMessage
	bestStar := ""
	for key, raw := range imports {
		star := strings.IndexByte(key, '*')
		if star < 0 {
			continue
		}
		prefix, suffix := key[:star], key[star+1:]
		if strings.HasPrefix(specifier, prefix) && strings.HasSuffix(specifier, suffix) && len(key) > len(bestKey) {
			bestKey = key
			bestRaw = raw
			bestStar = specifier[len(prefix) : len(specifier)-len(suffix)]
		}
	}
	if bestKey == "" {
		return nil, "", false
	}
	return bestRaw, bestStar, true
}

// resolveFileOrDir tries the path as a file, then as a directory.
func (r *Resolver) resolveFileOrDir(path string) (string, error) {
	// Try as a file (exact path or with extensions)
	if resolved, ok := r.tryFile(path); ok {
		return resolved, nil
	}

	// Try as a directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return r.resolveDir(path)
	}

	return "", fmt.Errorf("module: cannot resolve %q", path)
}

// tryFile tries the exact path, then with each extension appended.
func (r *Resolver) tryFile(path string) (string, bool) {
	// Exact path
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, true
	}
	// TypeScript packages commonly preserve `.js` specifiers in source while
	// publishing/execing the corresponding `.ts` file directly.
	ext := strings.ToLower(filepath.Ext(path))
	var tsExts []string
	switch ext {
	case ".js":
		tsExts = []string{".ts", ".tsx"}
	case ".mjs":
		tsExts = []string{".mts", ".ts"}
	case ".cjs":
		tsExts = []string{".cts", ".ts"}
	}
	if len(tsExts) > 0 {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		for _, tsExt := range tsExts {
			candidate := base + tsExt
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
		}
	}
	// With extensions
	for _, ext := range r.Extensions {
		full := path + ext
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			return full, true
		}
	}
	return "", false
}

// resolveDir resolves a directory by reading package.json, then trying index files.
func (r *Resolver) resolveDir(dir string) (string, error) {
	// Read package.json for "main" field
	if main, ok := r.readPackageMain(dir); ok {
		candidate := filepath.Join(dir, filepath.FromSlash(main))
		if resolved, ok := r.tryFile(candidate); ok {
			return resolved, nil
		}
		// "main" might point to a sub-directory (e.g. "dist/")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if resolved, err := r.resolveDir(candidate); err == nil {
				return resolved, nil
			}
		}
	}

	// Try index files
	for _, idx := range r.IndexNames {
		candidate := filepath.Join(dir, idx)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("module: cannot resolve directory %q", dir)
}

// readPackageMain reads the "main" field from package.json in dir.
func (r *Resolver) readPackageMain(dir string) (string, bool) {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return "", false
	}
	var pkg struct {
		Main   string `json:"main"`
		Module string `json:"module"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", false
	}
	// Prefer "main"; fall back to "module" (ESM entry point)
	if pkg.Main != "" {
		return pkg.Main, true
	}
	if pkg.Module != "" {
		return pkg.Module, true
	}
	return "", false
}

// readPackageType reads the "type" field from package.json in dir.
// Returns "module" for ESM, "commonjs" for CJS (default).
func (r *Resolver) readPackageType(dir string) string {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return "commonjs"
	}
	var pkg struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "commonjs"
	}
	if pkg.Type == "module" {
		return "module"
	}
	return "commonjs"
}

// resolveBare resolves a bare specifier (e.g. "lodash") by walking up
// the directory tree looking for node_modules/<specifier>.
func (r *Resolver) resolveBare(specifier, parentPath string, conditions []string) (string, error) {
	pkgName, subPath := splitPackageSpecifier(specifier)
	if pkgName == "" {
		return "", fmt.Errorf("module: invalid package specifier %q", specifier)
	}

	// Walk up from parentPath's directory looking for node_modules
	searchDir := filepath.Dir(parentPath)
	if parentPath == "" {
		searchDir, _ = os.Getwd()
	}

	for {
		candidate := filepath.Join(searchDir, "node_modules", filepath.FromSlash(pkgName))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			// Found the package
			if resolved, hasExports, err := r.resolvePackageExports(candidate, subPath, conditions); hasExports {
				if err != nil {
					return "", fmt.Errorf("module: package %q: %w", pkgName, err)
				}
				return resolved, nil
			}
			if subPath != "" {
				target := filepath.Join(candidate, filepath.FromSlash(subPath))
				return r.resolveFileOrDir(target)
			}
			return r.resolveDir(candidate)
		}

		// Go up one directory
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break // reached root
		}
		searchDir = parent
	}

	return "", fmt.Errorf("module: cannot find package %q in node_modules", specifier)
}

// splitPackageSpecifier separates a bare package name from its subpath,
// including scoped names such as @scope/pkg/feature.
func splitPackageSpecifier(specifier string) (pkgName, subPath string) {
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") {
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", ""
		}
		pkgName = parts[0] + "/" + parts[1]
		if len(parts) > 2 {
			subPath = strings.Join(parts[2:], "/")
		}
		return pkgName, subPath
	}
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	pkgName = parts[0]
	if len(parts) > 1 {
		subPath = strings.Join(parts[1:], "/")
	}
	return pkgName, subPath
}

// resolvePackageExports resolves package.json exports for the requested
// package subpath. hasExports distinguishes an absent exports field (where
// legacy main/index fallback applies) from an unexported subpath.
func (r *Resolver) resolvePackageExports(pkgDir, subPath string, conditions []string) (resolved string, hasExports bool, err error) {
	data, readErr := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if readErr != nil {
		return "", false, nil
	}
	var pkg struct {
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(data, &pkg) != nil || len(pkg.Exports) == 0 || string(pkg.Exports) == "null" {
		return "", false, nil
	}

	requestKey := "."
	if subPath != "" {
		requestKey = "./" + filepath.ToSlash(subPath)
	}
	rawTarget, ok := matchPackageExport(pkg.Exports, requestKey, conditions)
	if !ok {
		return "", true, fmt.Errorf("subpath %q is not exported", requestKey)
	}
	target := conditionalExportTarget(rawTarget, conditions)
	if target == "" || !strings.HasPrefix(target, "./") {
		return "", true, fmt.Errorf("subpath %q has no supported target", requestKey)
	}
	full := filepath.Join(pkgDir, filepath.FromSlash(strings.TrimPrefix(target, "./")))
	resolved, resolveErr := r.resolveFileOrDir(full)
	if resolveErr != nil {
		return "", true, resolveErr
	}
	return resolved, true, nil
}

func matchPackageExport(exports json.RawMessage, requestKey string, conditions []string) (json.RawMessage, bool) {
	var direct string
	if json.Unmarshal(exports, &direct) == nil {
		if requestKey == "." {
			return exports, true
		}
		return nil, false
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(exports, &obj) != nil {
		return nil, false
	}
	// An object without subpath keys is the conditional target for root.
	hasSubpathKeys := false
	for key := range obj {
		if strings.HasPrefix(key, ".") {
			hasSubpathKeys = true
			break
		}
	}
	if !hasSubpathKeys {
		if requestKey == "." {
			return exports, true
		}
		return nil, false
	}
	if raw, ok := obj[requestKey]; ok {
		return raw, true
	}

	bestKey := ""
	bestMatch := ""
	for key := range obj {
		star := strings.IndexByte(key, '*')
		if star < 0 {
			continue
		}
		prefix, suffix := key[:star], key[star+1:]
		if strings.HasPrefix(requestKey, prefix) && strings.HasSuffix(requestKey, suffix) && len(key) > len(bestKey) {
			bestKey = key
			bestMatch = requestKey[len(prefix) : len(requestKey)-len(suffix)]
		}
	}
	if bestKey == "" {
		return nil, false
	}
	raw := obj[bestKey]
	target := conditionalExportTarget(raw, conditions)
	if target == "" {
		return nil, false
	}
	replaced, _ := json.Marshal(strings.ReplaceAll(target, "*", bestMatch))
	return replaced, true
}

// requireConditions / importConditions 是 exports 条件解析的候选条件集合，
// 按加载语境区分（Node 语义：条件集合由解析语境决定）。
//
//   - require 语境（CJS require）：["require", "node", "default"]——不含
//     "import"。若把 "import" 列为候选，require 一个带 exports 条件的包
//     （如 is-promise 的 {"import": "./index.mjs", "require": "./index.js"}）
//     会错误匹配 import 条件加载 ESM 入口，返回 {default: fn} 命名空间对象，
//     导致 `require(...) is not a function`。
//   - import 语境（ESM 静态导入/动态 import/import.meta.resolve）：
//     ["import", "node", "default"]。
var (
	requireConditions = []string{"require", "node", "default"}
	importConditions  = []string{"import", "node", "default"}
)

func conditionalExportTarget(raw json.RawMessage, conditions []string) string {
	var target string
	if json.Unmarshal(raw, &target) == nil {
		return target
	}
	var alternatives []json.RawMessage
	if json.Unmarshal(raw, &alternatives) == nil {
		for _, alternative := range alternatives {
			if target := conditionalExportTarget(alternative, conditions); target != "" {
				return target
			}
		}
		return ""
	}
	var cond map[string]json.RawMessage
	if json.Unmarshal(raw, &cond) == nil {
		for _, condition := range conditions {
			if value, ok := cond[condition]; ok {
				if target := conditionalExportTarget(value, conditions); target != "" {
					return target
				}
			}
		}
	}
	return ""
}

// ModuleType determines the module type for a file path.
// Returns "module" for ESM (.mjs/.ts/.mts or .js with type:module in package.json)
// and "commonjs" for CJS (.cjs/.cts or .js with type:commonjs/default).
// TS 文件按 ESM 处理（loadESM 会做类型剥离转译）。
func (r *Resolver) ModuleType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".mjs", ".ts", ".mts":
		return "module"
	case ".cjs", ".cts":
		return "commonjs"
	case ".json":
		return "json"
	default:
		// .js — check package.json "type" field
		dir := filepath.Dir(path)
		return r.readPackageType(dir)
	}
}
