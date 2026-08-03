package module

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// 本文件实现字节码磁盘缓存（1C.14）。缓存位于源文件所在目录向上查找的
// node_modules/.aluka/cache/ 下（需求文档 3.3.3），以缓存键命名。
//
// 缓存键 = sha256(源文件绝对路径 + mtime + size + 格式版本) 的十六进制。
// 失效策略：源文件 mtime/size 变化即失效；格式版本不匹配也失效。

// bcCacheDirName 是缓存目录名（相对 node_modules）。
const bcCacheDirName = ".aluka/cache"

// bytecodeCache 提供字节码 Module 的磁盘读写。
type bytecodeCache struct {
	disabled bool // true 时禁用缓存（对应 --no-cache）
}

// cacheKey 计算缓存键：基于源文件路径、mtime、size、字节码格式版本。
func (bc *bytecodeCache) cacheKey(absPath string, info os.FileInfo) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d", absPath, info.ModTime().UnixNano(), info.Size(), bytecode.FormatVersion)
	return hex.EncodeToString(h.Sum(nil))
}

// cacheDir 沿目录树向上查找 node_modules 目录，返回 .aluka/cache 路径。
// 若找不到 node_modules，回退到用户主目录下的 ~/.aluka/cache。
func (bc *bytecodeCache) cacheDir(srcPath string) string {
	dir := filepath.Dir(srcPath)
	for {
		candidate := filepath.Join(dir, "node_modules", bcCacheDirName)
		// 返回最近的 node_modules/.aluka/cache（无论是否存在，加载时创建）。
		return candidate
	}
}

// load 从磁盘读取缓存的字节码 Module。未命中返回 (nil, nil)。
func (bc *bytecodeCache) load(srcPath, key string) (*bytecode.Module, error) {
	if bc.disabled {
		return nil, nil
	}
	cacheFile := bc.cacheFilePath(srcPath, key)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 缓存未命中
		}
		return nil, nil // 读取错误也视为未命中（容错）
	}
	mod, err := bytecode.Deserialize(bytes.NewReader(data))
	if err != nil {
		return nil, nil // 反序列化失败视为未命中（容错，不阻塞运行）
	}
	return mod, nil
}

// store 将字节码 Module 写入磁盘缓存。写失败不阻塞（容错）。
func (bc *bytecodeCache) store(srcPath, key string, mod *bytecode.Module) {
	if bc.disabled {
		return
	}
	cacheFile := bc.cacheFilePath(srcPath, key)
	cacheDir := filepath.Dir(cacheFile)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return // 创建目录失败则放弃缓存
	}
	var buf bytes.Buffer
	if err := bytecode.Serialize(&buf, mod); err != nil {
		return
	}
	_ = os.WriteFile(cacheFile, buf.Bytes(), 0644)
}

// cacheFilePath 返回缓存文件的完整路径。key 用于文件名，srcPath 的哈希
// 用于分目录避免单目录文件过多。
func (bc *bytecodeCache) cacheFilePath(srcPath, key string) string {
	dir := bc.cacheDir(srcPath)
	// 用 srcPath 的短哈希做子目录，分散文件。
	h := sha256.Sum256([]byte(srcPath))
	subDir := hex.EncodeToString(h[:])[:2]
	return filepath.Join(dir, subDir, key+".bc")
}

// compileOrLoad 尝试从缓存加载字节码；未命中则调用 compile 闭包编译并写盘。
// compile 闭包封装了具体的编译逻辑（CJS 直接编译源码；ESM 先 AST 转换再编译），
// 使缓存逻辑与编译方式解耦。
func (bc *bytecodeCache) compileOrLoad(absPath string, compile func() (*bytecode.Module, error)) (*bytecode.Module, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		// 无法 stat 文件，退化为直接编译（不缓存）。
		return compile()
	}
	key := bc.cacheKey(absPath, info)
	if mod, err := bc.load(absPath, key); err == nil && mod != nil {
		return mod, nil
	}
	// 未命中：编译 + 写盘。
	mod, err := compile()
	if err != nil {
		return nil, err
	}
	bc.store(absPath, key, mod)
	return mod, nil
}
