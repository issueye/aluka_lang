package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 本文件实现 tsconfig.json 读取（1C.12）与路径别名解析（1C.13）。
//
// tsconfig.json 的 compilerOptions 中与模块解析相关的字段：
//   - baseUrl: 非相对模块的基准解析目录（绝对或相对 tsconfig 所在目录）
//   - paths: 路径别名映射，如 { "@/*": ["src/*"] }
//
// paths 的匹配规则（TypeScript 行为）：
//   - key 中的 "*" 是通配符，匹配任意片段；value 中的 "*" 替换为匹配到的片段
//   - 例：paths {"@/*": ["src/*"]}，specifier "@/utils/helper" → "src/utils/helper"
//   - 一个 key 可映射多个 target，按顺序尝试
//   - 无 "*" 的 key 做精确匹配
//   - 解析出的 target 路径再以 baseUrl（或 tsconfig 目录）为基准解析为绝对路径

// tsconfig 缓存：按 tsconfig.json 所在目录缓存解析结果。
type tsconfigCacheEntry struct {
	baseURL  string                 // 绝对路径的 baseUrl（"" 表示未设置）
	paths    map[string][]string    // 原始 paths 映射
	rootDir  string                 // tsconfig.json 所在目录（绝对路径）
	hasPaths bool
}

// tsconfigCache 缓存已解析的 tsconfig，避免重复读文件。
// 缓存键为 tsconfig.json 所在目录的绝对路径。
type tsconfigCache struct {
	mu    sync.Mutex
	store map[string]*tsconfigCacheEntry
}

func newTsconfigCache() *tsconfigCache {
	return &tsconfigCache{store: make(map[string]*tsconfigCacheEntry)}
}

// findAndParse 沿 parentDir 向上查找 tsconfig.json，解析并缓存。
// 返回解析结果与 tsconfig 所在目录（找不到返回 nil）。
func (c *tsconfigCache) findAndParse(parentDir string) *tsconfigCacheEntry {
	absDir, err := filepath.Abs(parentDir)
	if err != nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 沿目录树向上查找，命中缓存或文件即止。
	searchDir := absDir
	for {
		if entry, ok := c.store[searchDir]; ok {
			return entry
		}
		tsconfigPath := filepath.Join(searchDir, "tsconfig.json")
		if info, err := os.Stat(tsconfigPath); err == nil && !info.IsDir() {
			entry := parseTsconfig(searchDir, tsconfigPath)
			c.store[searchDir] = entry
			return entry
		}
		// jsconfig.json 作为回退（VS Code 项目常用）
		jsconfigPath := filepath.Join(searchDir, "jsconfig.json")
		if info, err := os.Stat(jsconfigPath); err == nil && !info.IsDir() {
			entry := parseTsconfig(searchDir, jsconfigPath)
			c.store[searchDir] = entry
			return entry
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break // 到达根目录
		}
		searchDir = parent
	}
	return nil
}

// parseTsconfig 读取并解析单个 tsconfig.json/jsconfig.json 文件。
func parseTsconfig(dir, path string) *tsconfigCacheEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return &tsconfigCacheEntry{rootDir: dir}
	}
	// tsconfig.json 允许包含注释（jsonc），这里做容错：去掉 // 与 /* */ 注释。
	cleaned := stripJSONC(data)
	var raw struct {
		CompilerOptions struct {
			BaseURL string            `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(cleaned, &raw); err != nil {
		return &tsconfigCacheEntry{rootDir: dir}
	}

	entry := &tsconfigCacheEntry{
		rootDir:  dir,
		paths:    raw.CompilerOptions.Paths,
		hasPaths: len(raw.CompilerOptions.Paths) > 0,
	}

	// baseUrl：相对路径以 tsconfig 目录为基准解析。
	if raw.CompilerOptions.BaseURL != "" {
		if filepath.IsAbs(raw.CompilerOptions.BaseURL) {
			entry.baseURL = raw.CompilerOptions.BaseURL
		} else {
			entry.baseURL = filepath.Join(dir, filepath.FromSlash(raw.CompilerOptions.BaseURL))
		}
	}

	return entry
}

// stripJSONC 移除 JSON 中的 // 行注释与 /* */ 块注释，使其可被标准 json 解析。
// 这是 tsconfig.json 的常见写法（jsonc 格式）。实现保守：不在字符串字面量内的注释才移除。
func stripJSONC(data []byte) []byte {
	var out strings.Builder
	out.Grow(len(data))
	inStr := false
	strQuote := byte(0)
	i := 0
	for i < len(data) {
		ch := data[i]
		// 处理字符串字面量内部（含转义）。
		if inStr {
			out.WriteByte(ch)
			if ch == '\\' && i+1 < len(data) {
				out.WriteByte(data[i+1])
				i += 2
				continue
			}
			if ch == strQuote {
				inStr = false
			}
			i++
			continue
		}
		switch {
		case ch == '"' :
			inStr = true
			strQuote = ch
			out.WriteByte(ch)
			i++
		case ch == '/' && i+1 < len(data) && data[i+1] == '/':
			// 行注释：跳到行尾
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(data) && data[i+1] == '*':
			// 块注释：跳到 */
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i += 2
			if i > len(data) {
				i = len(data)
			}
		default:
			out.WriteByte(ch)
			i++
		}
	}
	return []byte(out.String())
}

// resolvePaths 按 tsconfig paths 规则将 bare specifier 映射为候选目标路径。
// 返回的候选路径均为绝对路径（以 baseUrl 或 tsconfig 目录为基准）。
// 若无 tsconfig 或无匹配的 paths，返回 nil。
func (r *Resolver) resolvePaths(specifier, parentDir string) []string {
	entry := r.tsconfigCache.findAndParse(parentDir)
	if entry == nil {
		return nil
	}

	var candidates []string
	// 基准目录：baseUrl 优先，否则用 tsconfig 所在目录。
	base := entry.baseURL
	if base == "" {
		base = entry.rootDir
	}

	// 无 paths 配置时：若设置了 baseUrl，bare specifier 相对 baseUrl 解析。
	if !entry.hasPaths {
		if entry.baseURL != "" {
			candidates = append(candidates, filepath.Join(entry.baseURL, filepath.FromSlash(specifier)))
		}
		return candidates
	}

	// TypeScript 规则：最长匹配 key 优先。先收集所有匹配，按 key 长度降序处理。
	type match struct {
		prefix string
		star   bool
		targets []string
	}
	var matched []match
	for key, targets := range entry.paths {
		if starIdx := strings.Index(key, "*"); starIdx >= 0 {
			// 通配符 key：如 "@/*"，prefix 为 "/*@/"
			prefix := key[:starIdx]
			suffix := key[starIdx+1:]
			if strings.HasPrefix(specifier, prefix) && strings.HasSuffix(specifier, suffix) &&
				len(specifier) >= len(prefix)+len(suffix) {
				matched = append(matched, match{prefix: key, star: true, targets: targets})
			}
		} else if specifier == key {
			// 精确匹配
			matched = append(matched, match{prefix: key, star: false, targets: targets})
		}
	}
	if len(matched) == 0 {
		// 未匹配 paths：若设置了 baseUrl，bare specifier 仍可相对 baseUrl 解析。
		if entry.baseURL != "" {
			cand := filepath.Join(entry.baseURL, filepath.FromSlash(specifier))
			candidates = append(candidates, cand)
		}
		return candidates
	}

	// 按 key 长度降序（最长匹配优先）。
	for _, m := range matched {
		starIdx := strings.Index(m.prefix, "*")
		for _, target := range m.targets {
			var resolved string
			if m.star {
				// 提取通配符匹配的片段。
				prefix := m.prefix[:starIdx]
				suffix := m.prefix[starIdx+1:]
				wildcard := specifier[len(prefix) : len(specifier)-len(suffix)]
				// target 中的 "*" 替换为匹配片段。
				resolved = strings.ReplaceAll(target, "*", wildcard)
			} else {
				resolved = target
			}
			// target 相对 baseUrl 解析（若已是绝对路径则直接用）。
			if filepath.IsAbs(resolved) {
				candidates = append(candidates, resolved)
			} else {
				candidates = append(candidates, filepath.Join(base, filepath.FromSlash(resolved)))
			}
		}
	}
	return candidates
}
