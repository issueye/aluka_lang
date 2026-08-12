package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	"github.com/aluka-lang/aluka/internal/monitor"
)

// TestParseMemorySize 验证 --max-memory 大小解析（bytes/KB/MB/GB）。
func TestParseMemorySize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"100", 100, true},
		{"1024", 1024, true},
		{"1KB", 1024, true},
		{"1kb", 1024, true},
		{"2MB", 2 << 20, true},
		{"256MB", 256 << 20, true},
		{"1GB", 1 << 30, true},
		{"512B", 512, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"1.5MB", 0, false}, // 不支持小数
	}
	for _, c := range cases {
		got, err := parseMemorySize(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("parseMemorySize(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("parseMemorySize(%q) = %d, nil; want error", c.in, got)
		}
	}
}

func TestFormatJITStatsIncludesGuardDisableCounters(t *testing.T) {
	text := formatJITStatsSummary(jit.Stats{
		Mode: jit.Auto, QuickGuardDisabled: 1, TraceGuardDisabled: 2,
		NativeGuardDisabled: 3, NativeTraceGuardDisabled: 4, CalleeGuardDisabled: 5,
	})
	for _, want := range []string{
		"quickGuardDisabled=1", "traceGuardDisabled=2", "nativeGuardDisabled=3",
		"nativeTraceGuardDisabled=4", "calleeGuardDisabled=5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stats output missing %q: %s", want, text)
		}
	}
}

// TestFormatJITStatsIncludesR5Aggregates verifies the R5-7 derived rates
// (guard/deopt/eviction) and aggregate counters are printed by --jit-stats.
func TestFormatJITStatsIncludesR5Aggregates(t *testing.T) {
	text := formatJITStatsSummary(jit.Stats{
		Mode: jit.Auto, Compiled: 5, CompileNanos: 1000, Executed: 100,
		GuardFailures: 10, Deopts: 2, NativeCompiled: 5, NativeEvictions: 1,
		CompileBenefit: 20, Executions: 100,
	})
	for _, want := range []string{
		"executions=100", "deopts=2", "compileBenefit=20", "hotEvictions=0",
		"guardRate=9.09%", "deoptRate=2.00%", "evictionRate=20.00%",
		"compileCostPerSiteNanos=200",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stats output missing %q: %s", want, text)
		}
	}
}

// resetGlobalFlags 重置全局 flag 目标（buildCLI 绑定包级变量，测试间隔离）。
func resetGlobalFlags() {
	icStats = false
	jitMode = jit.Off
	jitThreshold = 1000
	jitBackedgeThreshold = 10000
	jitTraceBudget = 65536
	jitCodeCacheBytes = 4 << 20
	jitDumpMode = jit.DumpOff
	jitStatsOut = false
	monitorEnabled = false
	monitorInterval = 0
	monitorFormat = monitor.FormatText
	monitorOutPath = ""
	maxMemory = 0
	nodeOpts = nil
	astFlag = false
	vmFlag = false
	noCacheFlag = false
	noBytecodeOptFlag = false
}

// TestGlobalFlagParsing 验证全局 flags：任意位置剥离 + --flag=value /
// --flag value 两种形态。
func TestGlobalFlagParsing(t *testing.T) {
	resetGlobalFlags()
	app := buildCLI()
	pos, err := app.ParseGlobals([]string{
		"--ic-stats", "--jit=auto", "--jit-threshold", "5",
		"--jit-backedge-threshold=2", "--jit-stats", "--jit-dump=ir",
		"--monitor=500ms", "--monitor-format=json", "--monitor-out=mon.log",
		"--max-memory=256MB", "run", "app.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "app.js"}
	if len(pos) != len(want) {
		t.Fatalf("positionals = %v; want %v", pos, want)
	}
	for i := range want {
		if pos[i] != want[i] {
			t.Errorf("pos[%d] = %q; want %q", i, pos[i], want[i])
		}
	}
	if !icStats {
		t.Error("--ic-stats not parsed")
	}
	if jitMode != jit.Auto {
		t.Errorf("jitMode = %v; want auto", jitMode)
	}
	if jitThreshold != 5 {
		t.Errorf("jitThreshold = %d; want 5", jitThreshold)
	}
	if jitBackedgeThreshold != 2 {
		t.Errorf("jitBackedgeThreshold = %d; want 2", jitBackedgeThreshold)
	}
	if !jitStatsOut {
		t.Error("--jit-stats not parsed")
	}
	if jitDumpMode != jit.DumpIR {
		t.Errorf("jitDumpMode = %v; want ir", jitDumpMode)
	}
	if !monitorEnabled {
		t.Error("--monitor not parsed")
	}
	if monitorInterval != 500*time.Millisecond {
		t.Errorf("monitorInterval = %v; want 500ms", monitorInterval)
	}
	if monitorFormat != monitor.FormatJSON {
		t.Errorf("monitorFormat = %v; want json", monitorFormat)
	}
	if monitorOutPath != "mon.log" {
		t.Errorf("monitorOutPath = %q; want mon.log", monitorOutPath)
	}
	if maxMemory != 256<<20 {
		t.Errorf("maxMemory = %d; want %d", maxMemory, 256<<20)
	}
}

// TestGlobalFlagErrors 验证全局 flags 的报错消息与现状一致。
func TestGlobalFlagErrors(t *testing.T) {
	app := buildCLI()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--jit"}, "aluka: missing value after --jit"},
		{[]string{"--jit=bogus"}, `aluka: invalid JIT mode "bogus" (want off, quick, or auto)`},
		{[]string{"--jit-dump=asmx"}, `aluka: invalid JIT dump mode "asmx" (want off, ir, or asm)`},
		{[]string{"--jit-threshold=0"}, "aluka: --jit-threshold must be a positive integer"},
		{[]string{"--jit-code-cache=abc"}, "aluka: --jit-code-cache must be a positive byte size"},
	}
	for _, c := range cases {
		resetGlobalFlags()
		if _, err := app.ParseGlobals(c.args); err == nil || err.Error() != c.want {
			t.Errorf("ParseGlobals(%v) err = %v; want %q", c.args, err, c.want)
		}
	}
}

// TestGlobalFlagsLenient 验证监控系 flags 的静默语义：缺值/非法值不报错，
// --monitor 裸形式不消费下一 token（与现状一致）。
func TestGlobalFlagsLenient(t *testing.T) {
	app := buildCLI()

	// --monitor-out 无条件消费下一 token；--max-memory 非法值静默。
	resetGlobalFlags()
	pos, err := app.ParseGlobals([]string{"--monitor-out", "--max-memory=abc", "run", "x.js"})
	if err != nil {
		t.Fatal(err)
	}
	if monitorOutPath != "--max-memory=abc" {
		t.Errorf("monitorOutPath = %q; want %q", monitorOutPath, "--max-memory=abc")
	}
	if maxMemory != 0 {
		t.Errorf("maxMemory = %d; want 0（非法值静默）", maxMemory)
	}
	want := []string{"run", "x.js"}
	if len(pos) != len(want) || pos[0] != want[0] || pos[1] != want[1] {
		t.Errorf("positionals = %v; want %v", pos, want)
	}

	// --monitor 裸形式不消费下一 token。
	resetGlobalFlags()
	pos, err = app.ParseGlobals([]string{"--monitor", "app.js"})
	if err != nil {
		t.Fatal(err)
	}
	if !monitorEnabled {
		t.Error("--monitor not parsed")
	}
	if len(pos) != 1 || pos[0] != "app.js" {
		t.Errorf("positionals = %v; want [app.js]", pos)
	}

	// --monitor-out 缺值：静默。
	resetGlobalFlags()
	pos, err = app.ParseGlobals([]string{"--monitor-out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 0 {
		t.Errorf("positionals = %v; want empty", pos)
	}
}

// TestEngineFlagsAreGlobal 验证 --ast/--vm/--no-cache/--no-bytecode-opt 是
// 剥离型全局 flag：ParseGlobals 从任意位置剥离并写入绑定变量，剩余参数不含它们。
// 这样「flags before script」（如 `aluka --no-cache app.js`）与文件后置形式均可用，
// 对齐 Bun/Node 惯例。
func TestEngineFlagsAreGlobal(t *testing.T) {
	resetGlobalFlags()
	app := buildCLI()
	pos, err := app.ParseGlobals([]string{"--no-cache", "run", "app.js", "--ast", "--no-bytecode-opt", "--vm"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "app.js"}
	if len(pos) != len(want) || pos[0] != want[0] || pos[1] != want[1] {
		t.Fatalf("positionals = %v; want %v", pos, want)
	}
	if !astFlag {
		t.Error("astFlag should be true with --ast present")
	}
	if !vmFlag {
		t.Error("vmFlag should be true with --vm present")
	}
	if !noCacheFlag {
		t.Error("noCacheFlag should be true with --no-cache present")
	}
	if !noBytecodeOptFlag {
		t.Error("noBytecodeOptFlag should be true with --no-bytecode-opt present")
	}
}

// TestDispatchHelpVersionAndUnknown 验证帮助/版本/未知选项的退出码与输出
// （帮助走 stdout、错误走 stderr、退出码 0/1）。
func TestDispatchHelpVersionAndUnknown(t *testing.T) {
	app := buildCLI()
	var out, errOut bytes.Buffer
	app.Out = &out
	app.ErrOut = &errOut

	if code := app.Dispatch([]string{"--help"}); code != 0 {
		t.Fatalf("--help exit code = %d; want 0", code)
	}
	if !strings.Contains(out.String(), "aluka "+version) {
		t.Errorf("help output missing version line")
	}
	out.Reset()
	if code := app.Dispatch([]string{"-v"}); code != 0 {
		t.Fatalf("-v exit code = %d; want 0", code)
	}
	if out.String() != "aluka "+version+"\n" {
		t.Errorf("-v output = %q; want %q", out.String(), "aluka "+version+"\n")
	}
	out.Reset()
	errOut.Reset()
	if code := app.Dispatch([]string{"--bogus"}); code != 1 {
		t.Fatalf("--bogus exit code = %d; want 1", code)
	}
	if errOut.String() != "aluka: unknown option --bogus\n" {
		t.Errorf("stderr = %q; want %q", errOut.String(), "aluka: unknown option --bogus\n")
	}
	if out.String() != "" {
		t.Errorf("stdout should be empty on error, got %q", out.String())
	}
	// 空参数 → 帮助（stdout，退出码 0）。
	out.Reset()
	if code := app.Dispatch(nil); code != 0 {
		t.Fatalf("no-args exit code = %d; want 0", code)
	}
	if !strings.Contains(out.String(), "USAGE:") {
		t.Errorf("no-args output missing usage section")
	}
}

// TestHelpOutputSections 验证帮助文本的关键段落（与旧实现字节一致）。
func TestHelpOutputSections(t *testing.T) {
	var buf bytes.Buffer
	printHelp(&buf)
	for _, want := range []string{
		"aluka " + version + " — Bun-compatible JavaScript runtime (pure Go)",
		"USAGE:",
		"SUBCOMMANDS:",
		"run <file>",
		"-e, --eval <code>",
		"--jit=off|quick|auto",
		"--monitor[=interval]",
		"BUILD OPTIMIZATION:",
		"EXAMPLES:",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("help output missing %q", want)
		}
	}
}

// TestParseTestFlags 验证 `aluka test` 的 flag 解析（internal/cli 框架）：
// 未知 flag 作为路径、模式错误报错、缺值静默。
func TestParseTestFlags(t *testing.T) {
	paths, o, err := parseTestFlags([]string{
		"--coverage", "--test-only", "--test-reporter=dot",
		"--test-name-pattern", "^foo", "a.test.js", "--bogus-flag",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !o.coverage || !o.only {
		t.Errorf("coverage=%v only=%v; want true true", o.coverage, o.only)
	}
	if o.reporter != "dot" {
		t.Errorf("reporter = %q; want dot", o.reporter)
	}
	if o.namePattern == nil {
		t.Error("namePattern not parsed")
	}
	want := []string{"a.test.js", "--bogus-flag"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("paths = %v; want %v", paths, want)
	}

	// 非法正则报错（消息与现状一致：aluka: invalid --test-name-pattern %q: %v）。
	if _, _, err := parseTestFlags([]string{"--test-name-pattern", "("}); err == nil ||
		!strings.HasPrefix(err.Error(), `aluka: invalid --test-name-pattern "(": error parsing regexp`) {
		t.Errorf("pattern error = %v; want prefix %q", err, `aluka: invalid --test-name-pattern "(": error parsing regexp`)
	}

	// 缺值/非法值静默：--test-reporter 置于末尾（缺值不报错），
	// --test-timeout=abc 非法值不报错（现状语义；注意 --test-reporter
	// 若后跟其它 token 会无条件消费它，与旧实现一致）。
	_, o2, err := parseTestFlags([]string{"--test-timeout=abc", "f.test.js", "--test-reporter"})
	if err != nil {
		t.Fatal(err)
	}
	if o2.reporter != "spec" {
		t.Errorf("reporter = %q; want spec（--test-reporter 缺值静默）", o2.reporter)
	}
	if o2.timeoutMs != 0 {
		t.Errorf("timeoutMs = %d; want 0（非法值静默）", o2.timeoutMs)
	}
}

var (
	alukaBinOnce sync.Once
	alukaBinPath string
	alukaBinErr  error
)

// alukaTestBinary 构建一次 aluka 可执行文件（sync.Once + 独立临时目录，
// t.TempDir 生命周期不适合 sync.Once），返回路径。
func alukaTestBinary(t *testing.T) string {
	t.Helper()
	alukaBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aluka-cli-test-")
		if err != nil {
			alukaBinErr = err
			return
		}
		bin := filepath.Join(dir, "aluka"+exeSuffix())
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			alukaBinErr = fmt.Errorf("go build: %v\n%s", err, out)
			return
		}
		alukaBinPath = bin
	})
	if alukaBinErr != nil {
		t.Fatal(alukaBinErr)
	}
	return alukaBinPath
}

// exeSuffix 返回当前平台的可执行文件后缀。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// TestCompiledPayloadArgvPassthrough 验证打包产物参数穿透：产物模式下 argv
// 解析与普通模式完全一致（框架在主二进制，payload 字节码不参与参数解析）。
// 需要 go 工具链构建 aluka 二进制（每测试进程一次），可用 -short 跳过。
func TestCompiledPayloadArgvPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "argv.js")
	if err := os.WriteFile(script, []byte("console.log(JSON.stringify(process.argv));"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "app"+exeSuffix())
	build := exec.Command(bin, "build", "--compile", "--outfile", artifact, script)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build --compile failed: %v\n%s", err, out)
	}

	// 传参运行：--help/--jit=auto/--no-cache 必须原样穿透（不触发框架解析）。
	args := []string{"--help", "--jit=auto", "--no-cache", "plain-arg"}
	run := exec.Command(artifact, args...)
	run.Dir = dir
	out, err := run.Output()
	if err != nil {
		t.Fatalf("artifact run failed: %v", err)
	}
	var argv []string
	if err := json.Unmarshal(out, &argv); err != nil {
		t.Fatalf("unexpected artifact output %q: %v", out, err)
	}
	if len(argv) != 2+len(args) {
		t.Fatalf("argv = %v; want [artifact, entry, args...]", argv)
	}
	if argv[0] != artifact {
		t.Errorf("argv[0] = %q; want %q", argv[0], artifact)
	}
	// argv[1] 为虚拟入口路径（不断言具体值），argv[2:] 必须与传参一致。
	for i, want := range args {
		if argv[2+i] != want {
			t.Errorf("argv[%d] = %q; want %q", 2+i, argv[2+i], want)
		}
	}

	// 无参数运行：argv 仅含 artifact 与虚拟入口。
	run = exec.Command(artifact)
	run.Dir = dir
	out, err = run.Output()
	if err != nil {
		t.Fatalf("artifact run (no args) failed: %v", err)
	}
	if err := json.Unmarshal(out, &argv); err != nil {
		t.Fatalf("unexpected artifact output %q: %v", out, err)
	}
	if len(argv) != 2 || argv[0] != artifact {
		t.Errorf("argv = %v; want [artifact, entry]", argv)
	}
}
