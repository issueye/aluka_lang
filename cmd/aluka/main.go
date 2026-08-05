// Package main 是 aluka 命令行入口。
//
// Phase 0 支持的子命令：
//
//	aluka --version | -v
//	aluka --help    | -h
//	aluka -e "<code>" | --eval "<code>"
//	aluka run <file>
//	aluka <file>              # run 的简写
//
// Phase 1B: 默认使用字节码 VM（--ast 回退到 AST 解释器）。
//
// 后续 Phase 渐进添加：repl / install / test / build。
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// version 在构建时通过 ldflags 注入。
var version = "0.1.0-dev"

func main() {
	args := os.Args[1:]

	// 无参数 → 显示帮助
	if len(args) == 0 {
		printHelp(os.Stdout)
		return
	}

	// 按第一个参数分发
	first := args[0]
	switch {
	case first == "-v" || first == "--version":
		fmt.Println("aluka " + version)
	case first == "-h" || first == "--help":
		printHelp(os.Stdout)
	case first == "-e" || first == "--eval":
		if len(args) < 2 {
			fatalErr("aluka: missing code after " + first)
		}
		runCode(args[1], "[eval]", useVM(args[1:]))
	case first == "run":
		if len(args) < 2 {
			fatalErr("aluka: missing file after 'run'")
		}
		runFile(args[1], useVM(args[1:]), noCache(args[1:]))
	case first == "repl":
		startREPL(useVM(args[1:]))
	case first == "install" || first == "add" || first == "remove" || first == "update":
		cmdPkg(first, args[1:])
	case strings.HasPrefix(first, "-"):
		fatalErr("aluka: unknown option " + first)
	default:
		// 简写：aluka <file>
		runFile(first, useVM(args), noCache(args))
	}
}

// noCache scans args for the --no-cache flag.
func noCache(args []string) bool {
	for _, a := range args {
		if a == "--no-cache" {
			return true
		}
	}
	return false
}

// useVM scans args for --ast/--vm flags. Default is VM (Phase 1B).
// Returns true to use the VM, false to use the AST interpreter.
func useVM(args []string) bool {
	for _, a := range args {
		if a == "--ast" {
			return false
		}
	}
	return true
}

// runCode 执行一段 JS 代码字符串。
func runCode(code string, filename string, vm bool) {
	if err := execute(code, filename, vm); err != nil {
		// 退出码：语法错误/未捕获异常 → 1
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runFile 读取并执行一个文件。
func runFile(path string, vm, disableCache bool) {
	if err := runModule(path, vm, disableCache); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runModule creates a context, registers globals, and runs the file through
// the module loader (supports ESM, CJS, and JSON).
func runModule(path string, vm, disableCache bool) error {
	var eng engine.Engine
	if vm {
		eng = interpreter.NewVMEngine()
	} else {
		eng = interpreter.NewEngine()
	}
	defer eng.Shutdown()

	ctx, err := eng.NewContext()
	if err != nil {
		return err
	}
	defer ctx.Close()

	// 注册全局对象
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		return fmt.Errorf("register console: %w", err)
	}
	if err := globals.NewProcess(ctx, globals.ProcessConfig{}); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	// 注册 globalThis 引用
	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())

	// 注册定时器（事件循环基础设施）。
	if err := globals.NewTimers(ctx, globals.TimerConfig{}); err != nil {
		return fmt.Errorf("register timers: %w", err)
	}

	// 注册 Buffer 全局 + 文本编码 API（TextEncoder/TextDecoder/atob/btoa）。
	if err := globals.NewBuffer(ctx, globals.BufferConfig{}); err != nil {
		return fmt.Errorf("register Buffer: %w", err)
	}
	if err := globals.NewEncoding(ctx, globals.EncodingConfig{}); err != nil {
		return fmt.Errorf("register encoding: %w", err)
	}

	// 注册 Web API 全局：URL/URLSearchParams、AbortController、Event/EventTarget。
	if err := globals.NewURL(ctx, globals.URLConfig{}); err != nil {
		return fmt.Errorf("register URL: %w", err)
	}
	if err := globals.NewAbort(ctx, globals.AbortConfig{}); err != nil {
		return fmt.Errorf("register Abort: %w", err)
	}
	if err := globals.NewEvent(ctx, globals.EventConfig{}); err != nil {
		return fmt.Errorf("register Event: %w", err)
	}

	// 注册 Web API 全局：fetch/Request/Response、Blob/File、Streams。
	if err := globals.NewFetch(ctx, globals.FetchConfig{}); err != nil {
		return fmt.Errorf("register fetch: %w", err)
	}
	if err := globals.NewBlob(ctx, globals.BlobConfig{}); err != nil {
		return fmt.Errorf("register Blob: %w", err)
	}
	if err := globals.NewStream(ctx, globals.StreamConfig{}); err != nil {
		return fmt.Errorf("register Streams: %w", err)
	}

	// 注册 Web API 全局：crypto.subtle、URLPattern、MessageChannel。
	if err := globals.NewWebCrypto(ctx, globals.WebCryptoConfig{}); err != nil {
		return fmt.Errorf("register WebCrypto: %w", err)
	}
	if err := globals.NewURLPattern(ctx, globals.URLPatternConfig{}); err != nil {
		return fmt.Errorf("register URLPattern: %w", err)
	}
	if err := globals.NewMessageChannel(ctx, globals.MessageConfig{}); err != nil {
		return fmt.Errorf("register MessageChannel: %w", err)
	}
	if err := globals.NewWebSocket(ctx, globals.WebSocketConfig{}); err != nil {
		return fmt.Errorf("register WebSocket: %w", err)
	}
	if err := globals.NewGC(ctx, globals.GCConfig{}); err != nil {
		return fmt.Errorf("register gc: %w", err)
	}
	if err := globals.NewAluka(ctx, globals.AlukaConfig{}); err != nil {
		return fmt.Errorf("register Aluka: %w", err)
	}

	// 使用模块加载器执行文件
	loader := modmodule.NewLoader(ctx)
	loader.SetNoCache(disableCache)
	builtin.RegisterAll(loader)
	if err := loader.Run(path); err != nil {
		return err
	}

	// 进入事件循环：处理定时器/http 回调等异步任务，直到无 pending 任务。
	if vm, ok := ctx.(interface{ RunLoop() }); ok {
		vm.RunLoop()
	}
	return nil
}

// execute 创建引擎上下文、注册全局对象、执行代码。
func execute(code string, filename string, vm bool) error {
	var eng engine.Engine
	if vm {
		eng = interpreter.NewVMEngine()
	} else {
		eng = interpreter.NewEngine()
	}
	defer eng.Shutdown()

	ctx, err := eng.NewContext()
	if err != nil {
		return err
	}
	defer ctx.Close()

	// 注册全局对象
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		return fmt.Errorf("register console: %w", err)
	}
	if err := globals.NewProcess(ctx, globals.ProcessConfig{}); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	// 注册 globalThis 引用
	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())

	// 注册定时器（事件循环基础设施，-e 模式同样需要）。
	if err := globals.NewTimers(ctx, globals.TimerConfig{}); err != nil {
		return fmt.Errorf("register timers: %w", err)
	}

	// 注册 Buffer 全局 + 文本编码 API（-e 模式同样需要）。
	if err := globals.NewBuffer(ctx, globals.BufferConfig{}); err != nil {
		return fmt.Errorf("register Buffer: %w", err)
	}
	if err := globals.NewEncoding(ctx, globals.EncodingConfig{}); err != nil {
		return fmt.Errorf("register encoding: %w", err)
	}

	// 注册 Web API 全局（-e 模式同样需要）。
	if err := globals.NewURL(ctx, globals.URLConfig{}); err != nil {
		return fmt.Errorf("register URL: %w", err)
	}
	if err := globals.NewAbort(ctx, globals.AbortConfig{}); err != nil {
		return fmt.Errorf("register Abort: %w", err)
	}
	if err := globals.NewEvent(ctx, globals.EventConfig{}); err != nil {
		return fmt.Errorf("register Event: %w", err)
	}

	// 注册 Web API 全局（-e 模式同样需要）。
	if err := globals.NewFetch(ctx, globals.FetchConfig{}); err != nil {
		return fmt.Errorf("register fetch: %w", err)
	}
	if err := globals.NewBlob(ctx, globals.BlobConfig{}); err != nil {
		return fmt.Errorf("register Blob: %w", err)
	}
	if err := globals.NewStream(ctx, globals.StreamConfig{}); err != nil {
		return fmt.Errorf("register Streams: %w", err)
	}

	// 注册 Web API 全局（-e 模式同样需要）。
	if err := globals.NewWebCrypto(ctx, globals.WebCryptoConfig{}); err != nil {
		return fmt.Errorf("register WebCrypto: %w", err)
	}
	if err := globals.NewURLPattern(ctx, globals.URLPatternConfig{}); err != nil {
		return fmt.Errorf("register URLPattern: %w", err)
	}
	if err := globals.NewMessageChannel(ctx, globals.MessageConfig{}); err != nil {
		return fmt.Errorf("register MessageChannel: %w", err)
	}
	if err := globals.NewWebSocket(ctx, globals.WebSocketConfig{}); err != nil {
		return fmt.Errorf("register WebSocket: %w", err)
	}
	if err := globals.NewGC(ctx, globals.GCConfig{}); err != nil {
		return fmt.Errorf("register gc: %w", err)
	}
	if err := globals.NewAluka(ctx, globals.AlukaConfig{}); err != nil {
		return fmt.Errorf("register Aluka: %w", err)
	}

	// 执行
	result, err := ctx.Eval(code, filename)
	if err != nil {
		return err
	}

	// 进入事件循环：驱动异步 I/O（SQL/Redis/S3/fetch/timers 等），
	// 直到无 pending 任务（与 node/bun 的 -e 行为一致）。
	if vmCtx, ok := ctx.(interface{ RunLoop() }); ok {
		vmCtx.RunLoop()
	}

	// 打印非 undefined 的求值结果（-e 模式）
	if !result.IsUndefined() {
		fmt.Println(result.String())
	}
	return nil
}

// printHelp 输出帮助信息。
func printHelp(w io.Writer) {
	fmt.Fprintf(w, `aluka %s — Bun-compatible JavaScript runtime (pure Go)

USAGE:
    aluka [SUBCOMMAND] [OPTIONS] [ARGS]

SUBCOMMANDS:
    run <file>           Execute a JS/TS file
    -e, --eval <code>    Execute inline JS code
    (none) <file>        Shorthand for 'run'
    repl                 Start interactive REPL
    install [pkg]        Install dependencies (Phase 5)
    add <pkg>            Add a dependency to package.json
    remove <pkg>         Remove a dependency from package.json
    update               Re-resolve and reinstall dependencies
    test                 Run tests (Phase 6+)
    build                Bundle project (Phase 7+)

OPTIONS:
    -v, --version        Print version and exit
    -h, --help           Print this help and exit
    --vm                 Use bytecode VM (default, Phase 1B)
    --ast                Use AST-walking interpreter (Phase 1A)
    --no-cache           Disable bytecode disk cache

EXAMPLES:
    aluka -e "console.log(1+1)"        # 输出 2
    aluka hello.js                       # 执行 hello.js
    aluka run hello.ts                   # 同上
    aluka -e "1+2" --ast                 # 用 AST 解释器执行
    aluka run app.js --no-cache          # 禁用字节码缓存强制重编译

Project: https://github.com/aluka-lang/aluka
Docs:    https://aluka.dev
`, version)
}

// fatalErr 输出错误到 stderr 并以非零码退出。
func fatalErr(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// 触发未使用 import 错误检测，防止 errors 在未来扩展时遗漏。
var _ = errors.New
