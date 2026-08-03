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
}

// NewResolver creates a Resolver with Node.js defaults.
func NewResolver() *Resolver {
	return &Resolver{
		Extensions: []string{".js", ".mjs", ".cjs", ".json"},
		IndexNames: []string{"index.js", "index.mjs", "index.cjs", "index.json"},
	}
}

// Resolve resolves a module specifier to an absolute file path.
// parentPath is the path of the module that initiated the resolution (for
// relative paths and node_modules lookup); it may be empty for the entry point.
func (r *Resolver) Resolve(specifier, parentPath string) (string, error) {
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

	// Bare specifier: walk up node_modules
	return r.resolveBare(specifier, parentPath)
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
func (r *Resolver) resolveBare(specifier, parentPath string) (string, error) {
	// Split package name and subpath (e.g. "lodash/fp" → "lodash", "fp")
	parts := strings.SplitN(specifier, "/", 2)
	pkgName := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
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

// ModuleType determines the module type for a file path.
// Returns "module" for ESM (.mjs or .js with type:module in package.json)
// and "commonjs" for CJS (.cjs or .js with type:commonjs/default).
func (r *Resolver) ModuleType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".mjs":
		return "module"
	case ".cjs":
		return "commonjs"
	case ".json":
		return "json"
	default:
		// .js — check package.json "type" field
		dir := filepath.Dir(path)
		return r.readPackageType(dir)
	}
}
