package builtin

// node:module 内置模块（开发计划 3.14）。
//
// 提供 createRequire / Module / builtinModules / isBuiltin / constants，
// 以及 Node 22 的诊断与编译缓存方法面（enableCompileCache/register/
// syncBuiltinESMExports/findSourceMap/stripTypeScriptTypes 等，多为 API 面）。

import (
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// builtinModules 与 Node 22 的 module.builtinModules 对齐（68 项，无 node: 前缀）。
var builtinModulesList = []string{
	"_http_agent", "_http_client", "_http_common", "_http_incoming",
	"_http_outgoing", "_http_server", "_stream_duplex", "_stream_passthrough",
	"_stream_readable", "_stream_transform", "_stream_wrap", "_stream_writable",
	"_tls_common", "_tls_wrap", "assert", "assert/strict", "async_hooks",
	"buffer", "child_process", "cluster", "console", "constants", "crypto",
	"dgram", "diagnostics_channel", "dns", "dns/promises", "domain", "events",
	"fs", "fs/promises", "http", "http2", "https", "inspector",
	"inspector/promises", "module", "net", "os", "path", "path/posix",
	"path/win32", "perf_hooks", "process", "punycode", "querystring",
	"readline", "readline/promises", "repl", "stream", "stream/consumers",
	"stream/promises", "stream/web", "string_decoder", "sys", "timers",
	"timers/promises", "tls", "trace_events", "tty", "url", "util",
	"util/types", "v8", "vm", "wasi", "worker_threads", "zlib",
}

var builtinModulesSet = func() map[string]bool {
	s := make(map[string]bool, len(builtinModulesList))
	for _, n := range builtinModulesList {
		s[n] = true
	}
	return s
}()

// NewModule 构造 node:module 模块导出对象。
// loader 由 registry.go 注入（createRequire 需要 loader 的 require 链路）。
func NewModule(ctx engine.Context, loader *modmodule.Loader) (engine.Value, error) {
	m := engine.NewObject()

	// createRequire(filename | fileURL) → require 函数。
	// Node 允许传入本机路径或 file:// URL（含 URL 对象的 href）。
	_ = m.Set("createRequire", engine.NewFunction("createRequire", func(args []engine.Value) (engine.Value, error) {
		parentPath := ""
		if len(args) > 0 {
			parentPath = createRequireFilename(args[0])
		}
		parentPath = modmodule.NormalizeModulePath(parentPath)
		if parentPath != "" {
			if abs, err := filepath.Abs(parentPath); err == nil {
				parentPath = abs
			}
		}
		return loader.MakeRequireFunc(parentPath), nil
	}))

	// isBuiltin(specifier)：支持 node: 前缀；非内置返回 false。
	_ = m.Set("isBuiltin", engine.NewFunction("isBuiltin", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		spec := strings.TrimPrefix(args[0].String(), "node:")
		return engine.Boolean(builtinModulesSet[spec]), nil
	}))

	// builtinModules：Node 22 完整列表。
	bmVals := make([]engine.Value, len(builtinModulesList))
	for i, n := range builtinModulesList {
		bmVals[i] = engine.Str(n)
	}
	_ = m.Set("builtinModules", engine.NewArray(bmVals))

	// Aluka 编译产物的外部扩展加载桥：把宿主已嵌入模块注册为
	// 虚拟模块，扩展中的同名 import/require 直接复用这些导出。
	_ = m.Set("registerVirtualModule", engine.NewFunction("registerVirtualModule", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			loader.RegisterVirtualModule(args[0].String(), args[1])
		}
		return engine.Undefined(), nil
	}))

	// constants：compileCacheStatus。
	constants := engine.NewObject()
	ccs := engine.NewObject()
	_ = ccs.Set("FAILED", engine.IntValue(0))
	_ = ccs.Set("ENABLED", engine.IntValue(1))
	_ = ccs.Set("ALREADY_ENABLED", engine.IntValue(2))
	_ = ccs.Set("DISABLED", engine.IntValue(3))
	_ = constants.Set("compileCacheStatus", ccs)
	_ = m.Set("constants", constants)

	// 诊断与编译缓存方法面（API 面，纯 Go 运行时无真实实现）。
	for _, name := range []string{
		"syncBuiltinESMExports", "register", "registerHooks", "runMain",
		"enableCompileCache", "flushCompileCache", "findPackageJSON",
		"setSourceMapsSupport", "stripTypeScriptTypes", "findSourceMap",
	} {
		_ = m.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("getSourceMapsSupport", engine.NewFunction("getSourceMapsSupport", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	_ = m.Set("getCompileCacheDir", engine.NewFunction("getCompileCacheDir", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// Module 类（Node 语义：id=filename，filename 初始为 null，loaded=false）。
	moduleProto := engine.NewObject()
	_ = moduleProto.Set("require", engine.NewFunction("require", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return loader.MakeRequireFunc("/").Call(args)
	}))
	_ = moduleProto.Set("load", engine.NewFunction("load", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = moduleProto.Set("_compile", engine.NewFunction("_compile", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = moduleProto.Set("isPreloading", engine.NewFunction("isPreloading", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	moduleCtor := engine.NewFunction("Module", func(args []engine.Value) (engine.Value, error) {
		mod := engine.NewObject()
		_ = mod.Set("exports", engine.NewObject())
		filename := ""
		if len(args) > 0 {
			filename = args[0].String()
		}
		_ = mod.Set("id", engine.Str(filename))
		_ = mod.Set("filename", engine.Null())
		_ = mod.Set("loaded", engine.Boolean(false))
		_ = mod.Set("children", engine.NewArray(nil))
		_ = mod.Set("paths", engine.NewArray(nil))
		for _, k := range moduleProto.Keys() {
			if v, err := moduleProto.Get(k); err == nil {
				_ = mod.Set(k, v)
			}
		}
		return mod, nil
	})
	if co, ok := moduleCtor.AsObject(); ok {
		_ = co.Set("prototype", moduleProto)
		_ = co.Set("runMain", engine.NewFunction("runMain", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = co.Set("wrap", engine.NewFunction("wrap", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Str(""), nil
			}
			return engine.Str("(function (exports, require, module, __filename, __dirname) { " + args[0].String() + "\n});"), nil
		}))
		_ = co.Set("_load", engine.NewFunction("_load", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = co.Set("_resolveFilename", engine.NewFunction("_resolveFilename", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = co.Set("_nodeModulePaths", engine.NewFunction("_nodeModulePaths", func(args []engine.Value) (engine.Value, error) {
			start := "."
			if len(args) > 0 && args[0].String() != "" {
				start = args[0].String()
			}
			if abs, err := filepath.Abs(start); err == nil {
				start = abs
			}
			var values []engine.Value
			for dir := start; ; dir = filepath.Dir(dir) {
				values = append(values, engine.Str(filepath.Join(dir, "node_modules")))
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
			}
			return engine.NewArray(values), nil
		}))
		_ = co.Set("globalPaths", engine.NewArray(nil))
	}
	_ = m.Set("Module", moduleCtor)

	// SourceMap 类（最小面）。
	sourceMapCtor := engine.NewFunction("SourceMap", func(args []engine.Value) (engine.Value, error) {
		inst := engine.NewObject()
		_ = inst.Set("payload", engine.NewObject())
		return inst, nil
	})
	_ = m.Set("SourceMap", sourceMapCtor)

	return m, nil
}

// createRequireFilename 从 createRequire 参数提取路径/URL 字符串。
// 支持 string 与带 href 的 URL 对象（Node 语义）。
func createRequireFilename(v engine.Value) string {
	if o, ok := v.AsObject(); ok {
		if href, err := o.Get("href"); err == nil && href != nil && !href.IsUndefined() && !href.IsNull() {
			s := href.String()
			if s != "" {
				return s
			}
		}
	}
	return v.String()
}
