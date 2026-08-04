package builtin

// node:module 内置模块（开发计划 3.14）。
// createRequire(path) 基于指定模块路径创建 require 函数。

import (
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// NewModule 构造 node:module 模块导出对象。
// loader 由 registry.go 注入（createRequire 需要 loader 的 require 链路）。
func NewModule(ctx engine.Context, loader *modmodule.Loader) (engine.Value, error) {
	m := engine.NewObject()

	// createRequire(filename) → require 函数。
	_ = m.Set("createRequire", engine.NewFunction("createRequire", func(args []engine.Value) (engine.Value, error) {
		parentPath := ""
		if len(args) > 0 {
			parentPath = args[0].String()
		}
		// 若传入文件名，解析为绝对路径作为 require 的解析基准。
		if parentPath != "" {
			if abs, err := filepath.Abs(parentPath); err == nil {
				parentPath = abs
			}
		}
		return loader.MakeRequireFunc(parentPath), nil
	}))

	// Module 类（简化：暴露 runMain 占位）。
	ctor := engine.NewFunction("Module", func(args []engine.Value) (engine.Value, error) {
		mod := engine.NewObject()
		_ = mod.Set("exports", engine.NewObject())
		if len(args) > 0 {
			_ = mod.Set("filename", engine.Str(args[0].String()))
		}
		return mod, nil
	})
	_ = m.Set("Module", ctor)

	return m, nil
}
