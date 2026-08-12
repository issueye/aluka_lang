// CLI 框架装配（internal/cli）：全局 flags 注册 + 子命令注册。
//
// 行为兼容说明（与旧手工解析完全一致）：
//   - 全局 flags（--ic-stats/--jit*/--monitor*/--max-memory）从任意位置剥离；
//   - --ast/--vm/--no-cache 不是剥离型 flag：由 useVM/noCache 在 run 系命令
//     的位置参数上扫描（现状语义：必须位于子命令/文件之后）；
//   - --profile 仅允许位于第一个参数（main 中手工处理，保持现状）；
//   - 用法错误退出码 1（现状 fatalErr 语义，非框架默认 2）。

package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/aluka-lang/aluka/internal/cli"
	"github.com/aluka-lang/aluka/internal/engine/jit"
	"github.com/aluka-lang/aluka/internal/monitor"
)

// nodeOpts 是 NODE_OPTIONS 解析出的测试运行器 flags（合并进 `aluka test`）。
var nodeOpts []string

// monitor 相关全局 flag 目标（旧实现为 main 局部变量，提升为包级以便绑定）。
var (
	monitorEnabled  bool
	monitorInterval time.Duration
	monitorFormat   = monitor.FormatText
	monitorOutPath  string
	maxMemory       int64
)

// jitModeValue 解析 --jit=off|quick|auto（错误消息与现状一致）。
type jitModeValue struct{ v *jit.Mode }

func (m jitModeValue) Set(s string) error {
	mode, err := jit.ParseMode(s)
	if err != nil {
		return err
	}
	*m.v = mode
	return nil
}

func (m jitModeValue) String() string { return m.v.String() }

// jitDumpValue 解析 --jit-dump=ir|asm。
type jitDumpValue struct{ v *jit.DumpMode }

func (d jitDumpValue) Set(s string) error {
	mode, err := jit.ParseDumpMode(s)
	if err != nil {
		return err
	}
	*d.v = mode
	return nil
}

func (d jitDumpValue) String() string { return d.v.String() }

// positiveUint32Value 解析正整数（--jit-threshold 等；0/非法 → 报错）。
type positiveUint32Value struct {
	v    *uint32
	name string
}

func (p positiveUint32Value) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return fmt.Errorf("--%s must be a positive integer", p.name)
	}
	*p.v = uint32(n)
	return nil
}

func (p positiveUint32Value) String() string { return strconv.FormatUint(uint64(*p.v), 10) }

// positiveBytesValue 解析正字节大小（--jit-code-cache，复用 parseMemorySize）。
type positiveBytesValue struct {
	v    *uint64
	name string
}

func (p positiveBytesValue) Set(s string) error {
	n, err := parseMemorySize(s)
	if err != nil || n <= 0 {
		return fmt.Errorf("--%s must be a positive byte size", p.name)
	}
	*p.v = uint64(n)
	return nil
}

func (p positiveBytesValue) String() string { return strconv.FormatUint(*p.v, 10) }

// monitorValue 解析 --monitor[=interval]：启用监控；interval 解析失败静默
// （与现状一致，永不报错）。
type monitorValue struct {
	enabled  *bool
	interval *time.Duration
}

func (m monitorValue) Set(s string) error {
	*m.enabled = true
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		*m.interval = d
	}
	return nil
}

func (m monitorValue) String() string { return "" }

// monitorFormatValue 解析 --monitor-format=text|json（非 json 一律 text）。
type monitorFormatValue struct{ v *monitor.Format }

func (m monitorFormatValue) Set(s string) error {
	if s == "json" {
		*m.v = monitor.FormatJSON
	} else {
		*m.v = monitor.FormatText
	}
	return nil
}

func (m monitorFormatValue) String() string { return string(*m.v) }

// maxMemoryValue 解析 --max-memory（解析失败静默，与现状一致）。
type maxMemoryValue struct{ v *int64 }

func (m maxMemoryValue) Set(s string) error {
	if n, err := parseMemorySize(s); err == nil && n > 0 {
		*m.v = n
	}
	return nil
}

func (m maxMemoryValue) String() string { return strconv.FormatInt(*m.v, 10) }

// patternValue 解析 --test-name-pattern/--test-skip-pattern（错误消息与现状一致）。
type patternValue struct {
	v    **regexp.Regexp
	name string
}

func (p patternValue) Set(s string) error {
	re, err := regexp.Compile(s)
	if err != nil {
		return fmt.Errorf("invalid --%s %q: %v", p.name, s, err)
	}
	*p.v = re
	return nil
}

func (p patternValue) String() string {
	if *p.v == nil {
		return ""
	}
	return (*p.v).String()
}

// nonNegInt64Value 解析非负 int64（--test-timeout；非法/负数报错，
// 由 LenientValue 吞掉以保持现状的静默语义）。
type nonNegInt64Value struct{ v *int64 }

func (t nonNegInt64Value) Set(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return errors.New("non-negative integer required")
	}
	*t.v = n
	return nil
}

func (t nonNegInt64Value) String() string { return strconv.FormatInt(*t.v, 10) }

// positiveIntValue 解析正整数（--test-concurrency；非法/非正报错，
// 由 LenientValue 吞掉以保持现状的静默语义）。
type positiveIntValue struct{ v *int }

func (p positiveIntValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return errors.New("positive integer required")
	}
	*p.v = n
	return nil
}

func (p positiveIntValue) String() string { return strconv.Itoa(*p.v) }

// buildCLI 装配 CLI 应用：全局 flags + 子命令注册。
func buildCLI() *cli.App {
	app := cli.New("aluka", version)
	// 用法错误退出码与现状一致（fatalErr → 1，非框架默认 2）。
	app.UsageExitCode = 1
	// 帮助文本与旧实现字节一致。
	app.SetHelp(printHelp)

	g := app.GlobalFlags()
	g.Bool("ic-stats", "Print inline-cache hit rates on exit", &icStats)
	g.Bool("jit-stats", "Print JIT candidate/compile/guard statistics", &jitStatsOut)
	g.Var(jitModeValue{v: &jitMode}, "jit", "JIT tier: off|quick|auto")
	g.Var(jitDumpValue{v: &jitDumpMode}, "jit-dump", "Dump verified IR or native bytes (ir|asm)")
	g.Var(positiveUint32Value{v: &jitThreshold, name: "jit-threshold"}, "jit-threshold", "Compile a hot leaf after n calls")
	g.Var(positiveUint32Value{v: &jitBackedgeThreshold, name: "jit-backedge-threshold"}, "jit-backedge-threshold", "Compile a numeric loop after n backedges")
	g.Var(positiveUint32Value{v: &jitTraceBudget, name: "jit-trace-budget"}, "jit-trace-budget", "Yield JIT loops after n backedges")
	g.Var(positiveBytesValue{v: &jitCodeCacheBytes, name: "jit-code-cache"}, "jit-code-cache", "Native code cache budget")
	g.Var(monitorValue{enabled: &monitorEnabled, interval: &monitorInterval}, "monitor", "Monitor performance/memory/runtime metrics [=interval]").OptionalValue()
	g.Var(monitorFormatValue{v: &monitorFormat}, "monitor-format", "Monitor output format (text|json)").OptionalValue()
	g.String("monitor-out", "Write monitor output to file (default stderr)", &monitorOutPath).LenientMissing()
	g.Var(maxMemoryValue{v: &maxMemory}, "max-memory", "Process memory limit (bytes or KB/MB/GB suffix)").LenientMissing()

	app.AddCommand(&cli.Command{
		Name:    "-e",
		Aliases: []string{"--eval"},
		Summary: "Execute inline JS code",
		Run:     runEval,
	})
	app.AddCommand(&cli.Command{
		Name:    "check",
		Aliases: []string{"--check"},
		Summary: "Syntax-check a JS/TS file",
		Run:     runCheck,
	})
	app.AddCommand(&cli.Command{Name: "run", Summary: "Execute a JS/TS file", Run: runFileCmd})
	app.AddCommand(&cli.Command{Name: "repl", Summary: "Start interactive REPL", Run: runREPL})
	app.AddCommand(&cli.Command{
		Name:    "install",
		Aliases: []string{"add", "remove", "update"},
		Summary: "Package management (install/add/remove/update)",
		Run:     runPkg,
	})
	app.AddCommand(&cli.Command{
		Name:    "test",
		Aliases: []string{"--test"},
		Summary: "Run tests (node:test)",
		Run:     runTest,
	})
	app.AddCommand(&cli.Command{Name: "build", Summary: "Bundle project", Run: runBuild})
	app.SetDefaultCommand(&cli.Command{
		Name:    "(file)",
		Summary: "Shorthand for 'run'",
		Run:     runFileShortcut,
	})
	return app
}

// testFlags 是 `aluka test` 的 flag 解析结果。
type testFlags struct {
	coverage, updateSnaps, only, watch, noWarnings bool
	reporter, reporterDest, shardSpec              string
	concurrency                                    int
	timeoutMs                                      int64
	namePattern, skipPattern                       *regexp.Regexp
}

// parseTestFlags 解析 `aluka test` 的 flags（internal/cli 框架），返回剩余
// 位置参数（测试文件/目录）。未知 flag 按现状作为路径处理；--no-warnings
// 接受但忽略（NODE_OPTIONS 白名单兼容，不再被当作路径报 stat 错误）。
func parseTestFlags(args []string) ([]string, *testFlags, error) {
	o := &testFlags{reporter: "spec", reporterDest: "stdout", concurrency: 1}
	fs := cli.NewFlagSet("aluka: ")
	fs.Bool("coverage", "Enable test coverage", &o.coverage).Alias("experimental-test-coverage")
	fs.Bool("update-snapshots", "Update snapshot files", &o.updateSnaps).Alias("test-update-snapshots")
	fs.Bool("test-only", "Run only tests marked with only: true", &o.only)
	fs.Bool("watch", "Watch test files and re-run on change", &o.watch)
	fs.Bool("no-warnings", "Suppress warnings (accepted for NODE_OPTIONS compatibility)", &o.noWarnings)
	fs.String("test-reporter", "Reporter: spec|tap|dot|junit|lcov|<path>", &o.reporter).LenientMissing()
	fs.String("test-reporter-destination", "Reporter destination: stdout|<path>", &o.reporterDest).LenientMissing()
	fs.String("test-shard", "Shard spec index/total", &o.shardSpec).LenientMissing()
	fs.Var(nonNegInt64Value{v: &o.timeoutMs}, "test-timeout", "Per-test timeout in milliseconds").LenientMissing().LenientValue()
	fs.Var(positiveIntValue{v: &o.concurrency}, "test-concurrency", "Test concurrency (accepted; execution is serial)").LenientMissing().LenientValue()
	fs.Var(patternValue{v: &o.namePattern, name: "test-name-pattern"}, "test-name-pattern", "Run only tests matching the regexp").LenientMissing()
	fs.Var(patternValue{v: &o.skipPattern, name: "test-skip-pattern"}, "test-skip-pattern", "Skip tests matching the regexp").LenientMissing()
	rest, err := fs.Parse(args)
	if err != nil {
		return nil, nil, err
	}
	return rest, o, nil
}

// runEval 实现 `aluka -e <code>` / `--eval`。
func runEval(pos []string, invoked string) error {
	if len(pos) == 0 {
		return fmt.Errorf("aluka: missing code after %s", invoked)
	}
	runCode(pos[0], "[eval]", useVM(pos[1:]))
	return nil
}

// runCheck 实现 `aluka --check <file>` / `check`：只解析不执行（Node 语义）。
func runCheck(pos []string, _ string) error {
	if len(pos) == 0 {
		return fmt.Errorf("aluka: missing file after --check")
	}
	if c := checkSyntax(pos[0]); c != 0 {
		return &cli.ExitError{Code: c}
	}
	return nil
}

// runFileCmd 实现 `aluka run <file>`。
func runFileCmd(pos []string, _ string) error {
	if len(pos) == 0 {
		return fmt.Errorf("aluka: missing file after 'run'")
	}
	runFile(pos[0], useVM(pos[1:]), noCache(pos[1:]))
	return nil
}

// runREPL 实现 `aluka repl`。
func runREPL(pos []string, _ string) error {
	startREPL(useVM(pos))
	return nil
}

// runPkg 实现 install/add/remove/update（invoked 为实际调用的名字）。
func runPkg(pos []string, invoked string) error {
	cmdPkg(invoked, pos)
	return nil
}

// runTest 实现 `aluka test`：NODE_OPTIONS 中的测试运行器 flags 合并进来。
func runTest(pos []string, _ string) error {
	cmdTest(append(nodeOpts, pos...))
	return nil
}

// runBuild 实现 `aluka build`。
func runBuild(pos []string, _ string) error {
	cmdBuild(pos)
	return nil
}

// runFileShortcut 实现 aluka <file>（run 的简写）。
func runFileShortcut(pos []string, _ string) error {
	runFile(pos[0], useVM(pos), noCache(pos))
	return nil
}
