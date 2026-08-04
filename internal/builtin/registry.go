package builtin

// 内置模块注册表：node:fs / node:path / node:os 等。
// 在 Loader 创建后调用 RegisterAll 注册所有内置模块。

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// RegisterAll 将所有内置模块注册到 Loader。
// 新增模块时在此添加一行 RegisterBuiltin 调用。
func RegisterAll(loader *module.Loader) {
	loader.RegisterBuiltin("path", NewPath)
	loader.RegisterBuiltin("os", NewOS)
	loader.RegisterBuiltin("url", NewURL)
	loader.RegisterBuiltin("util", NewUtil)
	loader.RegisterBuiltin("events", NewEvents)
	loader.RegisterBuiltin("fs", NewFS)
	loader.RegisterBuiltin("assert", NewAssert)
	loader.RegisterBuiltin("crypto", NewCrypto)
	loader.RegisterBuiltin("stream", NewStream)
	loader.RegisterBuiltin("querystring", NewQueryString)
	loader.RegisterBuiltin("string_decoder", NewStringDecoder)
	loader.RegisterBuiltin("http", NewHTTP)
	loader.RegisterBuiltin("buffer", globals.NewBufferModule)
}

// --- 公共辅助函数 -------------------------------------------------------

// strArg 安全取第 i 个字符串参数（越界返回空串）。
func strArg(args []engine.Value, i int) string {
	if i < len(args) {
		return args[i].String()
	}
	return ""
}

// intArg 安全取第 i 个整数参数（越界或非数字返回 def）。
func intArg(args []engine.Value, i int, def int) int {
	if i < len(args) {
		if n, ok := args[i].Int(); ok {
			return n
		}
	}
	return def
}

// boolArg 安全取第 i 个布尔参数。
func boolArg(args []engine.Value, i int) bool {
	if i < len(args) {
		if b, ok := args[i].Bool(); ok {
			return b
		}
	}
	return false
}
