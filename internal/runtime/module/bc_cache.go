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

// 本文件实现字节码磁盘缓存（1C.14）。缓存位于"最近的 package.json 项目根"
// 的 node_modules/.aluka/cache/ 下（需求文档 3.3.3），以缓存键命名。
//
// 缓存键 = sha256(源文件绝对路径 + mtime + size + 格式版本 + 编译形态) 的
// 十六进制。编译形态（kind）区分 ESM/CJS：同一 typeless .js 在 CJS 编译
// 失败后会按 ESM 重新编译，两种形态的模块函数签名不同（8 参 vs 6 参），
// 缓存必须隔离，否则二次运行时 CJS 入口会命中 ESM 字节码导致参数错位。
// 失效策略：源文件 mtime/size 变化即失效；格式版本不匹配也失效。

// bcCacheDirName 是缓存目录名（相对 node_modules）。
const bcCacheDirName = ".aluka/cache"

// pipelineVersion 是模块管线分层版本。前端分类、ESM/CJS 判定、lower/wrapper
// 语义变化时递增，使旧字节码缓存失效（不依赖 FormatVersion——字节码布局
// 未变时 FormatVersion 不变，但分类/阶段语义可能已变）。
//
// v1 → v2：TransformESMToCJS 将 import 的 require 调用提升到模块体顶部；
//
//	编译器 super 静态语境解析到父类构造器。同源文件的产物语义已变化，
//	旧缓存必须失效。
//
// v2 → v3：导出 getter / export * 改为 globalThis.Object.defineProperty
//
//	与 globalThis.Object.assign，避免模块 `import { Object }` 时把注入的
//	Object.defineProperty 改写成未初始化的 __imp.Object（TypeBox）。
//
// v3 → v4：再导出 getter（export {x} from / export {导入名 as x} /
//
//	export * as ns from）改为 liveImpRef 形态——循环依赖期间 __imp_N
//	未赋值时回退 __importReq 取部分导出活对象，替代原先返回 undefined
//	（或裸 __imp_N.x 抛 TypeError）的守卫。
const pipelineVersion = 4

// bytecodeCache 提供字节码 Module 的磁盘读写。
type bytecodeCache struct {
	disabled bool // true 时禁用缓存（对应 --no-cache）
}

// cacheKey 计算缓存键：基于源文件路径、mtime、size、字节码格式版本、
// 管线版本与分类（SourceKind + ModuleKind）。
func (bc *bytecodeCache) cacheKey(absPath string, info os.FileInfo, sourceKind SourceKind, kind ModuleKind) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%d|%s|%s", absPath, info.ModTime().UnixNano(), info.Size(), bytecode.FormatVersion, pipelineVersion, sourceKind, kind)
	return hex.EncodeToString(h.Sum(nil))
}

// cacheDir 返回字节码缓存的存放目录：以"最近的 package.json 所在目录"为
// 项目根，缓存写在 <项目根>/node_modules/.aluka/cache/ 下（与依赖同目录、
// 随项目走）。源文件位于某个 npm 包内时，包根（含 package.json）即为项目
// 根，包内文件共享包级缓存。找不到 package.json（临时脚本/裸文件）时回退
// 到源文件所在目录的 node_modules 下。
//
// 注意：查找严格止于最近的 package.json，不再向更上层爬升——避免把缓存
// 写入用户主目录等其他项目的 node_modules（此前实现会向上找"真实
// node_modules"，%TEMP% 下的临时脚本会误命中主目录里的真实 node_modules）。
func (bc *bytecodeCache) cacheDir(srcPath string) string {
	dir := filepath.Dir(srcPath)
	for {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return filepath.Join(dir, "node_modules", bcCacheDirName)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // 到达文件系统根
		}
		dir = parent
	}
	return filepath.Join(filepath.Dir(srcPath), "node_modules", bcCacheDirName)
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
// sourceKind/kind 标识源码语言与模块协议，参与缓存键——同源不同分类的模块
// 函数签名不同（ESM 8 参 vs CJS 6 参），必须隔离。
func (bc *bytecodeCache) compileOrLoad(absPath string, sourceKind SourceKind, kind ModuleKind, compile func() (*bytecode.Module, error)) (*bytecode.Module, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		// 无法 stat 文件，退化为直接编译（不缓存）。
		return compile()
	}
	key := bc.cacheKey(absPath, info, sourceKind, kind)
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
