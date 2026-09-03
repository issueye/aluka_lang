package compile

import (
	"fmt"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// Embedded 是产物模式的嵌入式模块存储（module.EmbeddedResolver 的实现）。
//
// 产物运行时不做文件系统解析：按构建期解析映射（manifest.Resolutions）
// 定位模块，从 payload 数据区反序列化预编译字节码（带缓存）。
type Embedded struct {
	manifest *Manifest
	data     []byte

	mu    sync.Mutex
	cache map[string]*bytecode.Module
}

// NewEmbedded 创建嵌入式存储。
func NewEmbedded(manifest *Manifest, data []byte) *Embedded {
	return &Embedded{
		manifest: manifest,
		data:     data,
		cache:    make(map[string]*bytecode.Module),
	}
}

// RootDir 返回入口文件所在目录（构建机绝对路径）。空串表示旧产物
// （无 rootDir 字段）：运行时保持仅绝对路径可回退文件系统。
func (e *Embedded) RootDir() string {
	return e.manifest.RootDir
}

// ResolveEmbedded 按构建期解析映射解析 specifier。
// parentPath 是发起解析的模块路径（产物模式下为构建时记录的模块路径）。
// 未命中（构建期未静态解析到，或动态拼装）返回 false。
func (e *Embedded) ResolveEmbedded(specifier, parentPath string) (string, bool) {
	table, ok := e.manifest.Resolutions[parentPath]
	if !ok {
		return "", false
	}
	target, ok := table[specifier]
	return target, ok
}

// ModuleTypeOf 返回模块类型（ModuleTypeESM | ModuleTypeCJS | "json"——
// JSON 资源不在模块表，经 Assets 判定）。
func (e *Embedded) ModuleTypeOf(key string) string {
	if t := e.manifest.ModuleTypeOf(key); t != "" {
		return t
	}
	if _, ok := e.manifest.Assets[key]; ok {
		return "json"
	}
	return ""
}

// LoadModule 反序列化嵌入的预编译模块（带缓存）。
func (e *Embedded) LoadModule(key string) (*bytecode.Module, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if mod, ok := e.cache[key]; ok {
		return mod, nil
	}
	mod, err := e.manifest.LoadModule(e.data, key)
	if err != nil {
		return nil, fmt.Errorf("module: compiled mode: %w", err)
	}
	e.cache[key] = mod
	return mod, nil
}

// LoadJSON 读取嵌入的 JSON 资源（M3，B2.3.4）。未找到返回 ok=false。
func (e *Embedded) LoadJSON(key string) ([]byte, bool) {
	raw, ok := e.manifest.Assets[key]
	if !ok {
		return nil, false
	}
	return []byte(raw), true
}
