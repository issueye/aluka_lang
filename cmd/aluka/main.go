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
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/monitor"
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

// 监控器（--monitor）与内存上限（--max-memory）状态。
var (
	monitorInstance *monitor.Monitor
	monitorStopCh   chan struct{}
	monitorIC       engine.ICStats // 由 runModule 结束时捕获，供终报
	monitorICSeen   bool
)

// osExit 统一的进程退出入口：先 flush profile/监控再退出。
func osExit(code int) {
	if profileStop != nil {
		profileStop()
		profileStop = nil
	}
	finishMonitor()
	os.Exit(code)
}

// finishMonitor 停止监控并输出终报（幂等）。
func finishMonitor() {
	if monitorInstance != nil {
		monitorInstance.Stop()
		if monitorStopCh != nil {
			close(monitorStopCh)
			monitorStopCh = nil
		}
		monitorInstance = nil
	}
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

	// NODE_OPTIONS 解析（Node 22 语义）：环境变量中的白名单 flags。
	// 测试运行器参数注入到 `aluka test` 子命令；其余子命令忽略。
	nodeOpts := nodeOptionsFlags()

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

	// 监控器与内存上限（全局开关，任意位置）：
	//   --monitor[=interval]            启用指标监控（interval 如 500ms/1s；默认仅终报）
	//   --monitor-format=text|json      输出格式
	//   --monitor-out=<path>            输出目标（默认 stderr，避免污染程序 stdout）
	//   --max-memory=<bytes|NMB|NGB>    进程内存上限（env ALUKA_MAX_MEMORY 兜底）
	monitorEnabled := false
	monitorInterval := time.Duration(0)
	monitorFormat := monitor.FormatText
	monitorOutPath := ""
	maxMemory := parseMaxMemoryEnv()
	filtered = args[:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--monitor":
			monitorEnabled = true
		case strings.HasPrefix(a, "--monitor="):
			monitorEnabled = true
			if d, err := time.ParseDuration(strings.TrimPrefix(a, "--monitor=")); err == nil && d > 0 {
				monitorInterval = d
			}
		case strings.HasPrefix(a, "--monitor-format="):
			f := strings.TrimPrefix(a, "--monitor-format=")
			if f == "json" {
				monitorFormat = monitor.FormatJSON
			} else {
				monitorFormat = monitor.FormatText
			}
		case a == "--monitor-out":
			if i+1 < len(args) {
				i++
				monitorOutPath = args[i]
			}
		case strings.HasPrefix(a, "--monitor-out="):
			monitorOutPath = strings.TrimPrefix(a, "--monitor-out=")
		case a == "--max-memory":
			if i+1 < len(args) {
				i++
				if n, err := parseMemorySize(args[i]); err == nil && n > 0 {
					maxMemory = n
				}
			}
		case strings.HasPrefix(a, "--max-memory="):
			if n, err := parseMemorySize(strings.TrimPrefix(a, "--max-memory=")); err == nil && n > 0 {
				maxMemory = n
			}
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered
	if maxMemory > 0 {
		engine.SetMemoryLimit(maxMemory)
	}
	if monitorEnabled {
		out := io.Writer(os.Stderr)
		if monitorOutPath != "" {
			f, err := os.Create(monitorOutPath)
			if err != nil {
				fatalErr("aluka: cannot create --monitor-out file: " + err.Error())
			}
			out = f
		}
		monitorInstance = monitor.New(monitor.Config{
			Enabled:  true,
			Interval: monitorInterval,
			Format:   monitorFormat,
			Out:      out,
			VMMetrics: func() engine.ICStats {
				return monitorIC
			},
		})
		monitorStopCh = make(chan struct{})
		go monitorInstance.Run(monitorStopCh)
	}

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
	case first == "--check" || first == "check":
		if len(args) < 2 {
			fatalErr("aluka: missing file after --check")
		}
		os.Exit(checkSyntax(args[1]))
	case first == "run":
		if len(args) < 2 {
			fatalErr("aluka: missing file after 'run'")
		}
		runFile(args[1], useVM(args[1:]), noCache(args[1:]))
	case first == "repl":
		startREPL(useVM(args[1:]))
	case first == "install" || first == "add" || first == "remove" || first == "update":
		cmdPkg(first, args[1:])
	case first == "test" || first == "--test":
		// NODE_OPTIONS 中的测试运行器 flags 合并进 test 子命令。
		cmdTest(append(nodeOpts, args[1:]...))
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
	finishMonitor()
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

// nodeOptionsFlags 解析 NODE_OPTIONS 环境变量，返回 aluka 支持的 flags 列表。
// Node 22 语义：空格分隔的 token；含空格的 token 忽略；仅白名单 flags 生效
// （测试运行器参数与 --no-warnings 等）。带值的 flag 支持 `--flag=value`
// 与 `--flag value` 两种形态（Node 会把值 token 与 flag 配对）。
func nodeOptionsFlags() []string {
	raw, ok := os.LookupEnv("NODE_OPTIONS")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	allowed := map[string]bool{
		"--test-name-pattern":          true,
		"--test-reporter":              true,
		"--test-only":                  true,
		"--test-skip-pattern":          true,
		"--test-concurrency":           true,
		"--test-update-snapshots":      true,
		"--update-snapshots":           true,
		"--experimental-test-coverage": true,
		"--coverage":                   true,
		"--no-warnings":                true,
	}
	takesValue := map[string]bool{
		"--test-name-pattern": true,
		"--test-reporter":     true,
		"--test-skip-pattern": true,
		"--test-concurrency":  true,
	}
	// 等号形式 base 判定（--flag=value）。
	baseAllowed := func(tok string) bool {
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			return false
		}
		return allowed[tok[:eq]]
	}
	var out []string
	fields := strings.Fields(raw)
	for i := 0; i < len(fields); i++ {
		tok := fields[i]
		if strings.Contains(tok, " ") {
			continue // 含空格 token 忽略（Node 语义）
		}
		if !strings.HasPrefix(tok, "-") {
			continue
		}
		if baseAllowed(tok) {
			out = append(out, tok)
			continue
		}
		if !allowed[tok] {
			continue
		}
		out = append(out, tok)
		// 值 flag：若下一 token 非 flag，则一并纳入（--flag value 形态）。
		if takesValue[tok] && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "-") {
			out = append(out, fields[i+1])
			i++
		}
	}
	return out
}

// checkSyntax 实现 `aluka --check <file>`：只解析不执行（Node 语义）。
// 语法正确返回 0；解析失败打印错误并返回 1。
func checkSyntax(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// 剥离 BOM（与模块加载一致）。
	src := data
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		src = src[3:]
	}
	if _, err := parser.ParseModule(string(src)); err != nil {
		fmt.Fprintln(os.Stderr, "aluka: syntax check failed:", err)
		return 1
	}
	return 0
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

	// 启动期注册阶段（全局对象 + 25+ 内置模块）产生大量中间分配，Go runtime
	// 回收后并不立即归还 OS。在执行用户代码前主动释放一次，把启动 RSS 峰值
	// 压下来（开销：一次 STW GC + FreeOSMemory，仅此处调用）。
	engine.FreeOSMemory()

	if err := loader.Run(path); err != nil {
		return err
	}

	// 进入事件循环：处理定时器/http 回调等异步任务，直到无 pending 任务。
	if vm, ok := ctx.(interface{ RunLoop() }); ok {
		vm.RunLoop()
	}
	if monitorInstance != nil {
		if vmv, ok := ctx.(*interpreter.VM); ok {
			monitorIC = vmv.ICStats()
			monitorICSeen = true
		}
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
	// 标志解析：--coverage / --experimental-test-coverage /
	// --test-update-snapshots / --test-name-pattern / --test-skip-pattern /
	// --test-only / --test-reporter / --test-concurrency（其余为测试文件/目录）。
	coverage := false
	updateSnaps := false
	only := false
	reporter := "spec" // spec | tap | dot | junit | lcov | <custom path>
	reporterDest := "stdout"
	concurrency := 1
	watch := false
	shardSpec := "" // index/total
	timeoutMs := int64(0)
	var namePattern *regexp.Regexp
	var skipPattern *regexp.Regexp
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--coverage" || a == "--experimental-test-coverage":
			coverage = true
		case a == "--test-update-snapshots" || a == "--update-snapshots":
			updateSnaps = true
		case a == "--test-only":
			only = true
		case a == "--watch":
			watch = true
		case a == "--test-reporter":
			if i+1 < len(args) {
				i++
				reporter = args[i]
			}
		case strings.HasPrefix(a, "--test-reporter="):
			reporter = strings.TrimPrefix(a, "--test-reporter=")
		case a == "--test-reporter-destination":
			if i+1 < len(args) {
				i++
				reporterDest = args[i]
			}
		case strings.HasPrefix(a, "--test-reporter-destination="):
			reporterDest = strings.TrimPrefix(a, "--test-reporter-destination=")
		case a == "--test-shard":
			if i+1 < len(args) {
				i++
				shardSpec = args[i]
			}
		case strings.HasPrefix(a, "--test-shard="):
			shardSpec = strings.TrimPrefix(a, "--test-shard=")
		case a == "--test-timeout":
			if i+1 < len(args) {
				i++
				if n, err := strconv.ParseInt(args[i], 10, 64); err == nil && n >= 0 {
					timeoutMs = n
				}
			}
		case strings.HasPrefix(a, "--test-timeout="):
			if n, err := strconv.ParseInt(strings.TrimPrefix(a, "--test-timeout="), 10, 64); err == nil && n >= 0 {
				timeoutMs = n
			}
		case a == "--test-concurrency":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					concurrency = n
				}
			}
		case strings.HasPrefix(a, "--test-concurrency="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--test-concurrency=")); err == nil && n > 0 {
				concurrency = n
			}
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
		case a == "--test-skip-pattern":
			if i+1 < len(args) {
				i++
				re, err := regexp.Compile(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "aluka: invalid --test-skip-pattern %q: %v\n", args[i], err)
					osExit(1)
				}
				skipPattern = re
			}
		case strings.HasPrefix(a, "--test-skip-pattern="):
			re, err := regexp.Compile(strings.TrimPrefix(a, "--test-skip-pattern="))
			if err != nil {
				fmt.Fprintf(os.Stderr, "aluka: invalid --test-skip-pattern %q: %v\n", a, err)
				osExit(1)
			}
			skipPattern = re
		default:
			paths = append(paths, a)
		}
	}
	builtin.TestNamePattern = namePattern
	builtin.TestSkipPattern = skipPattern
	builtin.TestOnly = only
	builtin.TestProgrammaticRun = false
	builtin.TestDefaultTimeout = time.Duration(timeoutMs) * time.Millisecond
	_ = concurrency // 接受 --test-concurrency（执行仍按注册顺序串行；见 knownDifference）
	files := discoverTestFiles(paths)

	// --test-shard=index/total：按文件路径哈希分片（Node 语义：文件级分片）。
	if shardSpec != "" {
		idx, total := 0, 0
		if _, err := fmt.Sscanf(shardSpec, "%d/%d", &idx, &total); err == nil && total > 0 && idx >= 0 && idx < total {
			var sharded []string
			for _, f := range files {
				h := fnv.New32a()
				_, _ = h.Write([]byte(f))
				if int(h.Sum32()%uint32(total)) == idx {
					sharded = append(sharded, f)
				}
			}
			files = sharded
		} else {
			fmt.Fprintf(os.Stderr, "aluka: invalid --test-shard %q (expected index/total)\n", shardSpec)
			osExit(1)
		}
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "aluka: no test files found")
		osExit(1)
	}

	// --test-reporter-destination=stdout|<path>：报告器输出重定向。
	// 近似实现：destination 为文件时，运行期全部 stdout（含用例输出）写入文件
	//（node 仅报告器输出入文件，见 knownDifference）。
	var destFile *os.File
	if reporterDest != "stdout" {
		f, err := os.Create(reporterDest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "aluka: --test-reporter-destination %s: %v\n", reporterDest, err)
			osExit(1)
		}
		defer f.Close()
		_ = destFile
		oldOut := os.Stdout
		os.Stdout = f
		defer func() { os.Stdout = oldOut }()
	}
	// 执行测试文件集合并输出报告（--watch 重跑复用 runTestFilesOnce）。
	passed, failed := runTestFilesOnce(files, reporter, reporterDest == "stdout", coverage, updateSnaps, only, namePattern, skipPattern)
	// --watch：监听测试文件变更并重跑（基础轮询实现）。
	if watch {
		fmt.Println("\n[watch] waiting for changes... (Ctrl+C to quit)")
		for {
			changed := waitForFileChange(files, 500*time.Millisecond)
			if !changed {
				continue
			}
			fmt.Println("\n[watch] change detected, re-running...")
			passed, failed = runTestFilesOnce(files, reporter, reporterDest == "stdout", coverage, updateSnaps, only, namePattern, skipPattern)
			_ = passed
			_ = failed
		}
	}
	if failed > 0 {
		osExit(1)
	}
}

// runTestFilesOnce 执行测试文件集合并输出报告（--watch 重跑复用）。
// 返回 (passed, failed) 计数；reporter 输出始终走 os.Stdout。
func runTestFilesOnce(files []string, reporter string, useStdout bool, coverage, updateSnaps, only bool, namePattern, skipPattern *regexp.Regexp) (int, int) {
	builtin.TestNamePattern = namePattern
	builtin.TestSkipPattern = skipPattern
	builtin.TestOnly = only
	passed, failed, skipped, todo, cancelled := 0, 0, 0, 0, 0
	fileCoverage := map[string]map[int]bool{}
	var junitCases []junitCase
	var dotFailed []string
	var customReporter *customReporterHandle
	if isCustomReporterPath(reporter) {
		cr, err := loadCustomReporter(reporter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "aluka: --test-reporter %s: %v\n", reporter, err)
			return 0, 1
		}
		customReporter = cr
	}
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
			status := "ok"
			note := ""
			switch {
			case r.Cancelled:
				cancelled++
				status, note = "ok", "# cancelled"
			case r.Skipped:
				skipped++
				status, note = "ok", "# SKIP"
			case r.Todo:
				todo++
				if r.Passed {
					status, note = "ok", "# TODO"
				} else {
					failed++
					status, note = "not ok", "# TODO"
				}
			case r.Passed:
				passed++
			default:
				failed++
				status = "not ok"
			}
			if customReporter != nil {
				customReporter.Write(r, status)
				continue
			}
			switch reporter {
			case "dot":
				printDotChar(r, status)
				if status == "not ok" {
					dotFailed = append(dotFailed, r.FullName)
				}
			case "junit":
				junitCases = append(junitCases, junitCase{Name: r.FullName, Status: status, Err: r.Error, DurationMs: r.Duration.Milliseconds()})
			default:
				printTestLine(reporter, status, r.FullName, note, r.Error)
			}
		}
	}
	switch {
	case customReporter != nil:
		customReporter.Finish(passed, failed, skipped, todo, cancelled)
	case reporter == "dot":
		if len(dotFailed) > 0 {
			fmt.Printf("\nFailed tests:\n")
			for _, n := range dotFailed {
				fmt.Printf("✖ %s\n", n)
			}
		}
		fmt.Printf("\n# tests %d\n# pass  %d\n# fail  %d\n# cancelled  %d\n# skipped  %d\n# todo  %d\n", passed+failed+skipped+todo+cancelled, passed, failed, cancelled, skipped, todo)
	case reporter == "junit":
		printJUnitReport(files, junitCases, passed, failed)
	case reporter == "tap":
		fmt.Printf("\n# tests %d\n# pass  %d\n# fail  %d\n# cancelled  %d\n# skipped  %d\n# todo  %d\n", passed+failed+skipped+todo+cancelled, passed, failed, cancelled, skipped, todo)
	case reporter == "lcov":
		printLcovReport(fileCoverage)
	default:
		fmt.Printf("\nℹ tests %d\nℹ pass  %d\nℹ fail  %d\nℹ cancelled  %d\nℹ skipped  %d\nℹ todo  %d\n", passed+failed+skipped+todo+cancelled, passed, failed, cancelled, skipped, todo)
	}
	if coverage {
		printCoverageReport(fileCoverage)
	}
	return passed, failed
}

// waitForFileChange 轮询文件 mtime，任一文件变化返回 true。
func waitForFileChange(files []string, interval time.Duration) bool {
	mtime := func(p string) time.Time {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	prev := make(map[string]time.Time, len(files))
	for _, f := range files {
		prev[f] = mtime(f)
	}
	for {
		time.Sleep(interval)
		for _, f := range files {
			if cur := mtime(f); !cur.Equal(prev[f]) {
				return true
			}
		}
	}
}

// printDotChar 输出 dot 报告器单字符（Node dot 语义：pass/skip/todo → '.'，
// fail → 'X'）。
func printDotChar(r builtin.TestResult, status string) {
	if status == "ok" {
		fmt.Print(".")
	} else {
		fmt.Print("X")
	}
}

// junitCase 是 junit 报告器的单个测试用例。
type junitCase struct {
	Name       string
	Status     string
	Err        string
	DurationMs int64
}

// printLcovReport 输出覆盖率汇总（Node lcov 近似：文件级行覆盖）。
func printLcovReport(fileCoverage map[string]map[int]bool) {
	fmt.Println("# start of coverage report")
	fmt.Println("# ----------------------------------------------------------------")
	fmt.Println("# file            | line % | branch % | funcs % | uncovered lines")
	fmt.Println("# ----------------------------------------------------------------")
	totalLines, coveredLines := 0, 0
	for src, executed := range fileCoverage {
		total := sourceLineCount(src)
		covered := len(executed)
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

// printJUnitReport 输出 Node 风格 junit XML（testsuite/failure）。
func printJUnitReport(files []string, cases []junitCase, passed, failed int) {
	fmt.Println(`<?xml version="1.0" encoding="UTF-8"?>`)
	suites := 0
	if len(files) > 0 {
		suites = 1
	}
	fmt.Printf("<testsuites name=\"node test suites\" tests=\"%d\" failures=\"%d\" errors=\"0\" time=\"0\" timestamp=\"\">\n", len(cases), failed)
	fmt.Printf("  <testsuite name=\"%s\" tests=\"%d\" failures=\"%d\" errors=\"0\" skipped=\"0\" time=\"0\">\n", xmlEscape(filepath.Base(files[0])), len(cases), failed)
	for _, c := range cases {
		fmt.Printf("    <testcase name=\"%s\" time=\"%d.%03d\">\n", xmlEscape(c.Name), c.DurationMs/1000, c.DurationMs%1000)
		if c.Status != "ok" && c.Err != "" {
			fmt.Printf("      <failure message=\"%s\"></failure>\n", xmlEscape(c.Err))
		}
		fmt.Println("    </testcase>")
	}
	fmt.Println("  </testsuite>")
	fmt.Println("</testsuites>")
	_ = suites
}

// xmlEscape 转义 XML 特殊字符。
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// customReporterHandle 包装自定义报告器（--test-reporter <path>）：
// 模块导出带 write/end 的 stream 对象，按事件喂数据（Node 22 contract）。
type customReporterHandle struct {
	eng     engine.Engine
	vm      *interpreter.VM
	writeFn engine.Function
	endFn   engine.Function
}

// isCustomReporterPath 判断 reporter 是否为内置名（非内置 → 自定义路径）。
func isCustomReporterPath(reporter string) bool {
	switch reporter {
	case "spec", "tap", "dot", "junit", "lcov":
		return false
	}
	return true
}

// loadCustomReporter 加载自定义报告器模块并构造 stream。
// 支持导出：带 write/end 的 plain object，或返回该 object 的工厂函数。
func loadCustomReporter(path string) (*customReporterHandle, error) {
	var eng engine.Engine = interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		return nil, err
	}
	if err := registerRuntimeGlobals(ctx); err != nil {
		return nil, err
	}
	loader := modmodule.NewLoader(ctx)
	builtin.RegisterAll(loader)
	_ = builtin.InstallGetBuiltinModule(ctx, loader)
	if err := loader.Run(path); err != nil {
		return nil, err
	}
	vm, ok := ctx.(*interpreter.VM)
	if !ok {
		return nil, fmt.Errorf("custom reporter requires the VM engine")
	}
	// 通过 require 取模块导出（缓存命中，不会重复执行）。
	req := loader.MakeRequireFunc(filepath.Dir(path))
	exports, err := req.Call([]engine.Value{engine.Str(path)})
	if err != nil {
		return nil, err
	}
	var streamObj engine.Object
	if fn, ok := exports.AsFunction(); ok {
		// 工厂函数：调用得到 stream 对象。
		v, cerr := vm.InvokeFn(fn, engine.Undefined(), nil)
		if cerr != nil {
			return nil, cerr
		}
		o, ok := v.AsObject()
		if !ok {
			return nil, fmt.Errorf("custom reporter factory did not return an object")
		}
		streamObj = o
	} else if o, ok := exports.AsObject(); ok {
		streamObj = o
	} else {
		return nil, fmt.Errorf("custom reporter export must be a function or an object")
	}
	writeV, err := streamObj.Get("write")
	if err != nil || !writeV.IsFunction() {
		return nil, fmt.Errorf("custom reporter stream lacks a write() method")
	}
	writeFn, _ := writeV.AsFunction()
	var endFn engine.Function
	if ev, err := streamObj.Get("end"); err == nil && ev.IsFunction() {
		endFn, _ = ev.AsFunction()
	}
	return &customReporterHandle{eng: eng, vm: vm, writeFn: writeFn, endFn: endFn}, nil
}

// Write 向报告器发送一个测试事件对象（Node 事件面：type/data）。
func (c *customReporterHandle) Write(r builtin.TestResult, status string) {
	if c == nil || c.writeFn == nil {
		return
	}
	// 事件对象：{ type: 'test:pass'|'test:fail'|..., data: { name, status, ... } }。
	evType := "test:pass"
	if status == "not ok" {
		evType = "test:fail"
	}
	data := engine.NewObject()
	_ = data.Set("name", engine.Str(r.FullName))
	_ = data.Set("status", engine.Str(status))
	if r.Skipped {
		_ = data.Set("skipped", engine.Boolean(true))
	}
	if r.Todo {
		_ = data.Set("todo", engine.Boolean(true))
	}
	if r.Error != "" {
		errObj := engine.NewObject()
		_ = errObj.Set("message", engine.Str(r.Error))
		_ = data.Set("error", errObj)
	}
	ev := engine.NewObject()
	_ = ev.Set("type", engine.Str(evType))
	_ = ev.Set("data", data)
	if f, ok := c.writeFn.AsFunction(); ok {
		res, _ := f.Call([]engine.Value{ev})
		if pv, ok := res.(*interpreter.PromiseValue); ok {
			// 报告器 write 可返回 promise（Node 允许异步 reporter）。
			if c.vm != nil {
				_, _ = c.vm.AwaitPromise(pv)
			}
		}
	}
}

// Finish 结束报告器流（调用 end()）。
func (c *customReporterHandle) Finish(passed, failed, skipped, todo, cancelled int) {
	if c == nil || c.endFn == nil {
		return
	}
	if f, ok := c.endFn.AsFunction(); ok {
		_, _ = f.Call(nil)
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
	// 测试挂起）。若脚本已调用 t.run()（程序化运行），驱动事件循环以派发
	// run() 的流事件，且不再重复执行用例。
	if builtin.TestProgrammaticRun {
		vm.RunLoop()
		return nil, nil, nil
	}
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

	// 启动期注册阶段产生大量中间分配，执行前归还一次 OS 内存（同 runModule）。
	engine.FreeOSMemory()

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
    --profile <path>     Write CPU profile (heap dump to <path>.heap)
    --ic-stats           Print inline-cache hit rates on exit
    --monitor[=interval] Monitor performance/memory/runtime metrics
                         (interval e.g. 500ms/1s for periodic samples;
                         default: final report only)
    --monitor-format=text|json   Monitor output format (default text)
    --monitor-out=<path> Write monitor output to file (default stderr)
    --max-memory=<n>     Process memory limit; n = bytes or KB/MB/GB
                         suffix (e.g. 256MB); env ALUKA_MAX_MEMORY
                         provides the same limit. On exceed: GC first,
                         then throw JS RangeError 'out of memory', then
                         kill after grace period.

EXAMPLES:
    aluka -e "console.log(1+1)"        # 输出 2
    aluka hello.js                       # 执行 hello.js
    aluka run hello.ts                   # 同上
    aluka -e "1+2" --ast                 # 用 AST 解释器执行
    aluka run app.js --no-cache          # 禁用字节码缓存强制重编译
    aluka --monitor app.js               # 结束输出性能/内存/运行时指标
    aluka --monitor=500ms --monitor-format=json app.js
    aluka --max-memory=256MB app.js      # 限制堆内存上限 256MB

Project: https://github.com/aluka-lang/aluka
Docs:    https://aluka.dev
`, version)
}

// fatalErr 输出错误到 stderr 并以非零码退出。
func fatalErr(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	osExit(1)
}

// parseMemorySize 解析内存大小（--max-memory）：
// 纯数字 = bytes；数字+KB/MB/GB（大小写不敏感）后缀换算。
func parseMemorySize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	num := t
	upper := strings.ToUpper(t)
	switch {
	case strings.HasSuffix(upper, "GB"):
		mult, num = 1<<30, strings.TrimSuffix(upper, "GB")
	case strings.HasSuffix(upper, "MB"):
		mult, num = 1<<20, strings.TrimSuffix(upper, "MB")
	case strings.HasSuffix(upper, "KB"):
		mult, num = 1<<10, strings.TrimSuffix(upper, "KB")
	case strings.HasSuffix(upper, "B"):
		mult, num = 1, strings.TrimSuffix(upper, "B")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	return n * mult, nil
}

// parseMaxMemoryEnv 读取 ALUKA_MAX_MEMORY 环境变量（--max-memory 的兜底）。
func parseMaxMemoryEnv() int64 {
	if v := os.Getenv("ALUKA_MAX_MEMORY"); v != "" {
		if n, err := parseMemorySize(v); err == nil {
			return n
		}
	}
	return 0
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
