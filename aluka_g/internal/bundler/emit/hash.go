package emit

import (
	"fmt"
	"hash/fnv"
	"path"
	"strings"
	"unicode"
)

// HashedAssetPath 生成 Vite 风格产物路径：assets/<base>-<hash><ext>。
func HashedAssetPath(base, hash, ext string) string {
	return HashedAssetPathIn("assets", base, hash, ext)
}

// AssetDir 规范化产物子目录（空则 assets）。
func AssetDir(dir string) string {
	dir = strings.Trim(strings.ReplaceAll(dir, "\\", "/"), "/")
	if dir == "" {
		return "assets"
	}
	return dir
}

// HashedAssetPathIn 使用指定子目录生成 hashed 路径。
func HashedAssetPathIn(dir, base, hash, ext string) string {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return AssetDir(dir) + "/" + sanitizeAssetBase(base) + "-" + hash + ext
}

// ContentHash 返回 8 位 hex（fnv-1a 低 32 位），足够做缓存破坏。
func ContentHash(parts ...string) string {
	h := fnv.New32a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%08x", h.Sum32())
}

func sanitizeAssetBase(base string) string {
	base = strings.TrimSpace(base)
	base = strings.ReplaceAll(base, "\\", "/")
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "chunk"
	}
	return out
}

func relativeModuleSpecifier(fromFile, toFile string) string {
	fromDir := path.Dir(strings.ReplaceAll(fromFile, "\\", "/"))
	rel := relSlash(fromDir, strings.ReplaceAll(toFile, "\\", "/"))
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}

func relSlash(fromDir, toFile string) string {
	fromDir = path.Clean(fromDir)
	toFile = path.Clean(toFile)
	if fromDir == "." {
		if !strings.HasPrefix(toFile, ".") {
			return "./" + toFile
		}
		return toFile
	}
	fromParts := strings.Split(fromDir, "/")
	toParts := strings.Split(toFile, "/")
	i := 0
	for i < len(fromParts) && i < len(toParts)-1 && fromParts[i] == toParts[i] {
		i++
	}
	var b strings.Builder
	for j := i; j < len(fromParts); j++ {
		if fromParts[j] == "." || fromParts[j] == "" {
			continue
		}
		b.WriteString("../")
	}
	for j := i; j < len(toParts); j++ {
		if j > i {
			b.WriteByte('/')
		}
		b.WriteString(toParts[j])
	}
	rel := b.String()
	if rel == "" {
		rel = path.Base(toFile)
	}
	return rel
}
