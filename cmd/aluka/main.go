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
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// version 在构建时通过 ldflags 注入。
var version = "0.1.0-dev"

// profileStop 是 --profile 的收尾函数（StopCPUProfile + heap dump）。
// 经 osExit 统一调用，保证错误路径也能落盘（O1-C1）。
var profileStop func()

// icStats 启用 IC 命中统计输出（--ic-stats，O1 验收）。
var icStats bool

// osExit 统一的进程退出入口：先 flush profile 再退出。
func osExit(code int) {
	if profileStop != nil {
		profileStop()
		profileStop = nil
	}
	os.Exit(code)
}

func main() {
	// 产物模式：自身携带编译产物（aluka build --compile）时直接执行。
	// 检测零开销（仅读尾部 footer），普通 aluka 不受影响。
	// 校验失败（截断/损坏）时告警并回退正常模式（B2.4.1）。
	switch payload, status := detectCompiledPayload(); status {
	case detectOK:
		osExit(runCompiled(payload))
	case detectCorrupt:
		fmt.Fprintln(os.Stderr, "aluka: warning: compiled payload failed integrity check (sha256 mismatch); falling back to normal mode")
	}

	args := os.Args[1:]

	// O1-C1：--profile <path> 全局开关——CPU profile 写 <path>，
	// 命令结束（或错误退出）时追加内存堆快照到 <path>.heap。
	if len(args) >= 2 && args[0] == "--profile" {
		profileStop = startProfile(args[1])
		args = args[2:]
	} else if len(args) >= 1 && strings.HasPrefix(args[0], "--profile=") {
		profileStop = startProfile(strings.TrimPrefix(args[0], "--profile="))
		args = args[1:]
	}
	// O1 验收：--ic-stats 在运行结束后输出内联缓存命中率（任意位置）。
	filtered := args[:0]
	for _, a := range args {
		if a == "--ic-stats" {
			icStats = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered

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
	case first == "test":
		cmdTest(args[1:])
	case first == "build":
		cmdBuild(args[1:])
	case strings.HasPrefix(first, "-"):
		fatalErr("aluka: unknown option " + first)
	default:
		// 简写：aluka <file>
		runFile(first, useVM(args), noCache(args))
	}

	// 正常结束：flush profile（CPU profile 数据在 StopCPUProfile 时落盘）。
	if profileStop != nil {
		profileStop()
		profileStop = nil
	}
}

// flushProfile 供 REPL 等长时间运行的命令在退出点调用。
func flushProfile() {
	if profileStop != nil {
		profileStop()
		profileStop = nil
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
		osExit(1)
	}
}

// runFile 读取并执行一个文件。
func runFile(path string, vm, disableCache bool) {
	if err := runModule(path, vm, disableCache); err != nil {
		fmt.Fprintln(os.Stderr, err)
		osExit(1)
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

	// 注册全局对象（console/process/timers/Buffer/Web API 等）。
	if err := registerRuntimeGlobals(ctx); err != nil {
		return err
	}

	// 使用模块加载器执行文件
	loader := modmodule.NewLoader(ctx)
	loader.SetNoCache(disableCache)
	builtin.RegisterAll(loader)
	_ = builtin.InstallGetBuiltinModule(ctx, loader)
	if err := loader.Run(path); err != nil {
		return err
	}

	// 进入事件循环：处理定时器/http 回调等异步任务，直到无 pending 任务。
	if vm, ok := ctx.(interface{ RunLoop() }); ok {
		vm.RunLoop()
	}
	if icStats {
		if vmv, ok := ctx.(*interpreter.VM); ok {
			printICStats(vmv.ICStats())
		}
	}
	return nil
}

// printICStats 输出内联缓存命中率报告（O1 验收）。
func printICStats(s engine.ICStats) {
	pct := func(hit, miss uint64) float64 {
		total := hit + miss
		if total == 0 {
			return 100
		}
		return float64(hit) / float64(total) * 100
	}
	fmt.Printf("IC stats: get %d/%d (%.1f%%), set %d/%d (%.1f%%), call %d/%d (%.1f%%)\n",
		s.GetHit, s.GetHit+s.GetMiss, pct(s.GetHit, s.GetMiss),
		s.SetHit, s.SetHit+s.SetMiss, pct(s.SetHit, s.SetMiss),
		s.CallHit, s.CallHit+s.CallMiss, pct(s.CallHit, s.CallMiss))
}

// cmdTest 实现 `aluka test` 子命令：发现并运行测试文件（node:test）。
// 无参数时按 Node 约定发现测试：cwd 下递归匹配 *.test.{js,ts,mjs,cjs}。
func cmdTest(args []string) {
	// 标志解析：--coverage / --test-update-snapshots / --test-name-pattern /
	// --test-only / --test-reporter（其余为测试文件/目录）。
	coverage := false
	updateSnaps := false
	only := false
	reporter := "spec" // spec | tap
	var namePattern *regexp.Regexp
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--coverage":
			coverage = true
		case a == "--test-update-snapshots" || a == "--update-snapshots":
			updateSnaps = true
		case a == "--test-only":
			only = true
		case a == "--test-reporter":
			if i+1 < len(args) {
				i++
				reporter = args[i]
			}
		case strings.HasPrefix(a, "--test-reporter="):
			reporter = strings.TrimPrefix(a, "--test-reporter=")
		case a == "--test-name-pattern":
			if i+1 < len(args) {
				i++
				re, err := regexp.Compile(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "aluka: invalid --test-name-pattern %q: %v\n", args[i], err)
					osExit(1)
				}
				namePattern = re
			}
		case strings.HasPrefix(a, "--test-name-pattern="):
			re, err := regexp.Compile(strings.TrimPrefix(a, "--test-name-pattern="))
			if err != nil {
				fmt.Fprintf(os.Stderr, "aluka: invalid --test-name-pattern %q: %v\n", a, err)
				osExit(1)
			}
			namePattern = re
		default:
			paths = append(paths, a)
		}
	}
	builtin.TestNamePattern = namePattern
	builtin.TestOnly = only
	files := discoverTestFiles(paths)
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "aluka: no test files found")
		osExit(1)
	}
	passed, failed, skipped, todo, cancelled := 0, 0, 0, 0, 0
	// 覆盖率统计（文件 → 已执行行集合）。
	fileCoverage := map[string]map[int]bool{}
	for _, f := range files {
		builtin.SetSnapshotFile(f)
		builtin.SetUpdateSnapshots(updateSnaps)
		results, cov, err := runTestFile(f, coverage)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			failed++
			continue
		}
		if cov != nil {
			for src, lines := range cov {
				if _, ok := fileCoverage[src]; !ok {
					fileCoverage[src] = map[int]bool{}
				}
				for ln := range lines {
					fileCoverage[src][ln] = true
				}
			}
		}
		for _, r := range results {
			switch {
			case r.Cancelled:
				cancelled++
				printTestLine(reporter, "ok", r.FullName, "# cancelled", "")
			case r.Skipped:
				skipped++
				printTestLine(reporter, "ok", r.FullName, "# SKIP", "")
			case r.Todo:
				todo++
				if r.Passed {
					printTestLine(reporter, "ok", r.FullName, "# TODO", "")
				} else {
					failed++
					printTestLine(reporter, "not ok", r.FullName, "# TODO", r.Error)
				}
			case r.Passed:
				passed++
				printTestLine(reporter, "ok", r.FullName, "", "")
			default:
				failed++
				printTestLine(reporter, "not ok", r.FullName, "", r.Error)
			}
		}
	}
	if reporter == "tap" {
		fmt.Printf("\n# tests %d\n# pass  %d\n# fail  %d\n# cancelled  %d\n# skipped  %d\n# todo  %d\n", passed+failed+skipped+todo+cancelled, passed, failed, cancelled, skipped, todo)
	} else {
		fmt.Printf("\nℹ tests %d\nℹ pass  %d\nℹ fail  %d\nℹ cancelled  %d\nℹ skipped  %d\nℹ todo  %d\n", passed+failed+skipped+todo+cancelled, passed, failed, cancelled, skipped, todo)
	}
	if coverage {
		printCoverageReport(fileCoverage)
	}
	if failed > 0 {
		osExit(1)
	}
}

// printTestLine 按 reporter 输出单个用例结果行。
// spec：`ok    name` / `not ok name`（缩进对齐）；tap：TAP 序号格式。
func printTestLine(reporter, status, name, note, errMsg string) {
	if reporter == "tap" {
		// 序号由累计行数决定：TAP 中 ok/not ok 行全局编号（含 skip/todo）。
		tapCount++
		if note != "" {
			note = " " + note
		}
		fmt.Printf("%s %d - %s%s\n", status, tapCount, name, note)
		if errMsg != "" {
			fmt.Printf("  ---\n  message: %s\n  ...\n", errMsg)
		}
		return
	}
	if note != "" {
		note = " (" + strings.TrimPrefix(note, "# ") + ")"
	}
	if status == "ok" {
		fmt.Printf("ok    %s%s\n", name, note)
	} else {
		fmt.Printf("not ok %s%s\n", name, note)
		if errMsg != "" {
			fmt.Printf("       %s\n", errMsg)
		}
	}
}

// tapCount TAP 输出序号（累计）。
var tapCount int

// printCoverageReport 输出 Node 风格覆盖率报告（line % / uncovered lines）。
func printCoverageReport(fileCoverage map[string]map[int]bool) {
	fmt.Println("# start of coverage report")
	fmt.Println("# ----------------------------------------------------------------")
	fmt.Println("# file            | line % | branch % | funcs % | uncovered lines")
	fmt.Println("# ----------------------------------------------------------------")
	totalLines, coveredLines := 0, 0
	for src, executed := range fileCoverage {
		// 文件总行数（物理行）。
		total := sourceLineCount(src)
		covered := len(executed)
		// 只统计在文件范围内的已执行行。
		for ln := range executed {
			if ln > total {
				covered--
			}
		}
		pct := 0.0
		if total > 0 {
			pct = float64(covered) / float64(total) * 100
		}
		uncovered := uncoveredLineList(executed, total)
		name := filepath.Base(src)
		fmt.Printf("# %-15s | %6.2f | %6.2f | %6.2f | %s\n", name, pct, pct, pct, uncovered)
		totalLines += total
		coveredLines += covered
	}
	allPct := 0.0
	if totalLines > 0 {
		allPct = float64(coveredLines) / float64(totalLines) * 100
	}
	fmt.Println("# ----------------------------------------------------------------")
	fmt.Printf("# %-15s | %6.2f | %6.2f | %6.2f | \n", "all files", allPct, allPct, allPct)
	fmt.Println("# ----------------------------------------------------------------")
	fmt.Println("# end of coverage report")
}

// sourceLineCount 统计文件物理行数。
func sourceLineCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// uncoveredLineList 未覆盖行列表（逗号分隔；Node 格式）。
func uncoveredLineList(executed map[int]bool, total int) string {
	var parts []string
	for ln := 1; ln <= total; ln++ {
		if !executed[ln] {
			parts = append(parts, fmt.Sprintf("%d", ln))
		}
	}
	return strings.Join(parts, ",")
}

// discoverTestFiles 收集测试文件：显式路径或递归发现。
func discoverTestFiles(args []string) []string {
	if len(args) > 0 {
		var out []string
		for _, a := range args {
			info, err := os.Stat(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "aluka: %v\n", err)
				continue
			}
			if !info.IsDir() {
				out = append(out, a)
				continue
			}
			out = append(out, findTestFilesIn(a)...)
		}
		return out
	}
	return findTestFilesIn(".")
}

// findTestFilesIn 递归查找 *.test.{js,ts,mjs,cjs}（跳过 node_modules/.git）。
func findTestFilesIn(dir string) []string {
	var out []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name == "node_modules" || name == ".git" || name == ".aluka" {
				continue
			}
			out = append(out, findTestFilesIn(filepath.Join(dir, name))...)
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".test.js") || strings.HasSuffix(lower, ".test.ts") ||
			strings.HasSuffix(lower, ".test.mjs") || strings.HasSuffix(lower, ".test.cjs") {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// runTestFile 加载一个测试文件并执行其中注册的用例，返回结果列表。
// coverage 非 nil 时启用行级覆盖率统计。
func runTestFile(path string, enableCoverage bool) ([]builtin.TestResult, map[string]map[int]bool, error) {
	var eng engine.Engine = interpreter.NewVMEngine()
	defer eng.Shutdown()

	ctx, err := eng.NewContext()
	if err != nil {
		return nil, nil, err
	}
	defer ctx.Close()

	if err := registerRuntimeGlobals(ctx); err != nil {
		return nil, nil, err
	}

	loader := modmodule.NewLoader(ctx)
	builtin.RegisterAll(loader)
	_ = builtin.InstallGetBuiltinModule(ctx, loader)
	builtin.ResetTestRegistry()
	// 覆盖率统计需覆盖文件加载阶段（顶层注册代码），故在 loader.Run 前启用。
	if enableCoverage {
		vm, _ := ctx.(*interpreter.VM)
		if vm != nil {
			vm.EnableCoverage()
		}
	}
	if err := loader.Run(path); err != nil {
		return nil, nil, err
	}

	vm, ok := ctx.(*interpreter.VM)
	if !ok {
		return nil, nil, fmt.Errorf("aluka: test runner requires the VM engine")
	}
	// 注意：此处不调用 vm.RunLoop()——用例执行（RunRegisteredTests）期间
	// 的异步任务（定时器/IO）由 AwaitPromise 内部驱动；若先 RunLoop 会
	// 在无 pending 任务时置 loopDone，导致后续 PostTask 被丢弃（async
	// 测试挂起）。
	results := builtin.RunRegisteredTests(vm)
	return results, vm.CoverageLines(), nil
}

// registerRuntimeGlobals 注册全局对象（runModule 与测试运行器共用）。
func registerRuntimeGlobals(ctx engine.Context) error {
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		return fmt.Errorf("register console: %w", err)
	}
	if err := globals.NewProcess(ctx, globals.ProcessConfig{}); err != nil {
		return fmt.Errorf("register process: %w", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())
	if err := globals.NewPerformance(ctx, globals.PerformanceConfig{}); err != nil {
		return fmt.Errorf("register performance: %w", err)
	}
	if err := globals.NewTimers(ctx, globals.TimerConfig{}); err != nil {
		return fmt.Errorf("register timers: %w", err)
	}
	if err := globals.NewBuffer(ctx, globals.BufferConfig{}); err != nil {
		return fmt.Errorf("register Buffer: %w", err)
	}
	if err := globals.NewEncoding(ctx, globals.EncodingConfig{}); err != nil {
		return fmt.Errorf("register encoding: %w", err)
	}
	if err := globals.NewURL(ctx, globals.URLConfig{}); err != nil {
		return fmt.Errorf("register URL: %w", err)
	}
	if err := globals.NewIntl(ctx, globals.IntlConfig{}); err != nil {
		return err
	}
	if err := globals.NewAbort(ctx, globals.AbortConfig{}); err != nil {
		return fmt.Errorf("register Abort: %w", err)
	}
	if err := globals.NewEvent(ctx, globals.EventConfig{}); err != nil {
		return fmt.Errorf("register Event: %w", err)
	}
	if err := globals.NewDOMException(ctx, globals.DOMExceptionConfig{}); err != nil {
		return fmt.Errorf("register DOMException: %w", err)
	}
	if err := globals.NewFetch(ctx, globals.FetchConfig{}); err != nil {
		return fmt.Errorf("register fetch: %w", err)
	}
	if err := globals.NewBlob(ctx, globals.BlobConfig{}); err != nil {
		return fmt.Errorf("register Blob: %w", err)
	}
	if err := globals.NewStream(ctx, globals.StreamConfig{}); err != nil {
		return fmt.Errorf("register Streams: %w", err)
	}
	if err := globals.NewWebCrypto(ctx, globals.WebCryptoConfig{}); err != nil {
		return fmt.Errorf("register WebCrypto: %w", err)
	}
	if err := globals.NewURLPattern(ctx, globals.URLPatternConfig{}); err != nil {
		return fmt.Errorf("register URLPattern: %w", err)
	}
	if err := globals.NewMessageChannel(ctx, globals.MessageConfig{}); err != nil {
		return fmt.Errorf("register MessageChannel: %w", err)
	}
	// N22-C4：navigator + BroadcastChannel。
	if err := globals.NewNavigator(ctx, globals.NavigatorConfig{}); err != nil {
		return fmt.Errorf("register navigator: %w", err)
	}
	if err := globals.NewBroadcastChannel(ctx, globals.MessageConfig{}); err != nil {
		return fmt.Errorf("register BroadcastChannel: %w", err)
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
	if err := globals.NewPerformance(ctx, globals.PerformanceConfig{}); err != nil {
		return fmt.Errorf("register performance: %w", err)
	}

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
	if err := globals.NewIntl(ctx, globals.IntlConfig{}); err != nil {
		return err
	}
	if err := globals.NewAbort(ctx, globals.AbortConfig{}); err != nil {
		return fmt.Errorf("register Abort: %w", err)
	}
	if err := globals.NewEvent(ctx, globals.EventConfig{}); err != nil {
		return fmt.Errorf("register Event: %w", err)
	}
	if err := globals.NewDOMException(ctx, globals.DOMExceptionConfig{}); err != nil {
		return fmt.Errorf("register DOMException: %w", err)
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
	// N22-C4：navigator + BroadcastChannel。
	if err := globals.NewNavigator(ctx, globals.NavigatorConfig{}); err != nil {
		return fmt.Errorf("register navigator: %w", err)
	}
	if err := globals.NewBroadcastChannel(ctx, globals.MessageConfig{}); err != nil {
		return fmt.Errorf("register BroadcastChannel: %w", err)
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
	osExit(1)
}

// startProfile 启动 CPU profiling（O1-C1）：profile 写 path，
// 返回的收尾函数停止采样并追加内存堆快照到 path.heap。
func startProfile(path string) func() {
	f, err := os.Create(path)
	if err != nil {
		fatalErr("aluka: cannot create profile file: " + err.Error())
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		fatalErr("aluka: cannot start CPU profile: " + err.Error())
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
		if hf, err := os.Create(path + ".heap"); err == nil {
			_ = pprof.WriteHeapProfile(hf)
			_ = hf.Close()
		}
	}
}

// 触发未使用 import 错误检测，防止 errors 在未来扩展时遗漏。
var _ = errors.New
