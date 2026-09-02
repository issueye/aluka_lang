package builtin

// 内置模块注册表：node:fs / node:path / node:os 等。
// 在 Loader 创建后调用 RegisterAll 注册所有内置模块。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/builtin/nodeassert"
	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/builtin/nodecrypto"
	"github.com/aluka-lang/aluka/internal/builtin/nodediag"
	"github.com/aluka-lang/aluka/internal/builtin/nodeevents"
	"github.com/aluka-lang/aluka/internal/builtin/nodefs"
	"github.com/aluka-lang/aluka/internal/builtin/nodehttp"
	"github.com/aluka-lang/aluka/internal/builtin/nodenet"
	"github.com/aluka-lang/aluka/internal/builtin/nodeos"
	"github.com/aluka-lang/aluka/internal/builtin/nodeproc"
	"github.com/aluka-lang/aluka/internal/builtin/noderepl"
	"github.com/aluka-lang/aluka/internal/builtin/nodesqlite"
	"github.com/aluka-lang/aluka/internal/builtin/nodestream"
	"github.com/aluka-lang/aluka/internal/builtin/nodetest"
	"github.com/aluka-lang/aluka/internal/builtin/nodetimers"
	"github.com/aluka-lang/aluka/internal/builtin/nodeutil"
	"github.com/aluka-lang/aluka/internal/builtin/nodevm"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// RegisterAll 将所有内置模块注册到 Loader。
// 新增模块时在此添加一行 RegisterBuiltin 调用。
func RegisterAll(loader *module.Loader) {
	loader.RegisterBuiltin("path", nodeos.NewPath)
	loader.RegisterBuiltin("path/posix", nodeos.NewPathPosix)
	loader.RegisterBuiltin("path/win32", nodeos.NewPathWin32)
	loader.RegisterBuiltin("os", nodeos.NewOS)
	loader.RegisterBuiltin("url", nodeutil.NewURL)
	loader.RegisterBuiltin("util", nodeutil.NewUtil)
	loader.RegisterBuiltin("util/types", nodeutil.NewUtilTypes)
	loader.RegisterBuiltin("events", nodeevents.NewEvents)
	loader.RegisterBuiltin("diagnostics_channel", nodediag.NewDiagnosticsChannel)
	loader.RegisterBuiltin("async_hooks", nodediag.NewAsyncHooks)
	loader.RegisterBuiltin("fs", nodefs.NewFS)
	loader.RegisterBuiltin("assert", nodeassert.NewAssert)
	loader.RegisterBuiltin("assert/strict", nodeassert.NewAssertStrict)
	loader.RegisterBuiltin("constants", nodeos.NewConstants)
	loader.RegisterBuiltin("crypto", nodecrypto.NewCrypto)
	loader.RegisterBuiltin("stream", nodestream.NewStream)
	loader.RegisterBuiltin("stream/web", nodestream.NewStreamWeb)
	loader.RegisterBuiltin("stream/promises", nodestream.NewStreamPromises)
	loader.RegisterBuiltin("stream/consumers", nodestream.NewStreamConsumers)
	loader.RegisterBuiltin("querystring", nodeutil.NewQueryString)
	loader.RegisterBuiltin("string_decoder", nodestream.NewStringDecoder)
	loader.RegisterBuiltin("http", nodehttp.NewHTTP)
	loader.RegisterBuiltin("https", nodehttp.NewHTTPS)
	loader.RegisterBuiltin("net", nodenet.NewNet)
	loader.RegisterBuiltin("tls", nodenet.NewTLS)
	loader.RegisterBuiltin("dns", nodenet.NewDNS)
	loader.RegisterBuiltin("dns/promises", func(ctx engine.Context) (engine.Value, error) {
		// Node 语义：require('node:dns/promises') === require('node:dns').promises
		// （同一对象身份）。
		dnsV, err := loader.GetBuiltin("dns")
		if err != nil || dnsV == nil || dnsV.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("dns/promises: dns not initialized")
		}
		if obj, ok := dnsV.AsObject(); ok {
			if p, err := obj.Get("promises"); err == nil && !p.IsUndefined() {
				return p, nil
			}
		}
		return engine.Undefined(), fmt.Errorf("dns/promises: dns.promises not found")
	})
	loader.RegisterBuiltin("zlib", nodeutil.NewZlib)
	loader.RegisterBuiltin("perf_hooks", nodediag.NewPerfHooks)
	loader.RegisterBuiltin("timers", nodetimers.NewTimersModule)
	loader.RegisterBuiltin("timers/promises", nodetimers.NewTimersPromises)
	loader.RegisterBuiltin("v8", nodediag.NewV8)
	loader.RegisterBuiltin("vm", nodevm.NewVMModule)
	loader.RegisterBuiltin("inspector", nodediag.NewInspector)
	loader.RegisterBuiltin("inspector/promises", nodediag.NewInspectorPromises)
	loader.RegisterBuiltin("dgram", nodenet.NewDgram)
	loader.RegisterBuiltin("http2", nodehttp.NewHTTP2)
	loader.RegisterBuiltin("cluster", nodeproc.NewCluster)
	loader.RegisterBuiltin("trace_events", nodediag.NewTraceEvents)
	loader.RegisterBuiltin("readline", noderepl.NewReadline)
	loader.RegisterBuiltin("readline/promises", noderepl.NewReadlinePromises)
	loader.RegisterBuiltin("repl", noderepl.NewReplModule)
	loader.RegisterBuiltin("child_process", nodeproc.NewChildProcess)
	loader.RegisterBuiltin("worker_threads", NewWorkerThreads)
	loader.RegisterBuiltin("fs/promises", nodefs.NewFSPromises)
	// node:module 需要 loader（createRequire 基于 loader 的 require 链路）。
	loader.RegisterBuiltin("module", func(ctx engine.Context) (engine.Value, error) {
		return nodevm.NewModule(ctx, loader)
	})
	loader.RegisterBuiltin("buffer", globals.NewBufferModule)
	loader.RegisterBuiltin("tty", nodeos.NewTTY)
	loader.RegisterBuiltin("sqlite", nodesqlite.NewSQLite)
	// node:domain / node:punycode / node:wasi（M9：废弃/实验模块，含
	// DEP0003 / DEP0040 废弃警告与 WASI 方法面）。
	loader.RegisterBuiltin("domain", nodediag.NewDomain)
	loader.RegisterBuiltin("punycode", nodeutil.NewPunycode)
	loader.RegisterBuiltin("wasi", nodeutil.NewWASI)
	// node:process —— 返回全局 process 对象（require('process') 语义，
	// 与 Node 一致：裸名 process 解析为内置而非 node_modules 包）。
	loader.RegisterBuiltin("process", NewProcessModule)
	loader.RegisterBuiltin("console", NewConsoleModule)
	loader.RegisterBuiltin("test", nodetest.NewTest)
	loader.RegisterBuiltin("test/reporters", nodetest.NewTestReporters)
	loader.RegisterBuiltin("markdown", nodeutil.NewMarkdownModule)
	loader.RegisterBuiltin("aluka:markdown", nodeutil.NewMarkdownModule)
	// node:sys —— node:util 兼容别名（废弃，DEP0140）。与 util 同一对象身份。
	loader.RegisterBuiltin("sys", func(ctx engine.Context) (engine.Value, error) {
		// 仅首次加载打印一次废弃警告（Node 每次 require 发出，这里由缓存保证一次）。
		nodebase.EmitDeprecation("sys", "The sys module is deprecated. Use util instead.")
		return loader.GetBuiltin("util")
	})
}

// NewProcessModule 返回全局 process 对象（require('process')）。
func NewProcessModule(ctx engine.Context) (engine.Value, error) {
	v, err := ctx.Global().Get("process")
	if err != nil || v == nil || v.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("process: global process not initialized")
	}
	return v, nil
}

// NewConsoleModule returns the initialized global console object. Node's
// node:console module exposes additional constructors, but its logging methods
// are the same callable surface needed by packages that import it directly.
func NewConsoleModule(ctx engine.Context) (engine.Value, error) {
	v, err := ctx.Global().Get("console")
	if err != nil || v == nil || v.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("console: global console not initialized")
	}
	return v, nil
}

// InstallGetBuiltinModule 将 process.getBuiltinModule 注入全局 process 对象
// （Node ≥ 22.3 API：按 specifier 返回内置模块导出对象，非内置返回 undefined）。
// 在 RegisterAll 之后调用（此时 loader 已持有全部内置模块工厂）。
func InstallGetBuiltinModule(ctx engine.Context, loader *module.Loader) error {
	procV, err := ctx.Global().Get("process")
	if err != nil || procV == nil || procV.IsUndefined() {
		return nil
	}
	procObj, ok := procV.AsObject()
	if !ok {
		return nil
	}
	fn := engine.NewFunction("getBuiltinModule", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		v, err := loader.GetBuiltin(args[0].String())
		if err != nil {
			return engine.Undefined(), nil
		}
		return v, nil
	})
	return procObj.Set("getBuiltinModule", fn)
}

// --- 公共辅助函数 -------------------------------------------------------
