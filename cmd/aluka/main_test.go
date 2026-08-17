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

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/jit"
	"github.com/aluka-lang/aluka/internal/monitor"
	"github.com/aluka-lang/aluka/internal/project"
)

func TestValidateWebBuiltins(t *testing.T) {
	if err := project.ValidateWebBuiltins(nil); err != nil {
		t.Fatalf("ValidateWebBuiltins(nil) = %v, want nil", err)
	}
	err := project.ValidateWebBuiltins([]graph.BuiltinDep{{Spec: "node:fs", Source: "src/main.ts"}})
	if err == nil {
		t.Fatal("ValidateWebBuiltins() = nil, want error")
	}
	for _, want := range []string{"web target", `"node:fs"`, "src/main.ts", "Web API", "--polyfill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

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

func runNodeESMCheck(t *testing.T, dir, bundle, checkJS, want string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	checkPath := filepath.Join(dir, "_check.mjs")
	if err := os.WriteFile(checkPath, []byte(checkJS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("node", checkPath, bundle)
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("node check failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("unexpected node output %q, want %q", strings.TrimSpace(string(out)), want)
	}
}

func globHashedAsset(t *testing.T, dir, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no files match %s in %s", pattern, dir)
	}
	return matches[0]
}

func readDirConcatJS(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.EqualFold(filepath.Ext(path), ".js") {
			data, readErr := os.ReadFile(path)
			if readErr == nil {
				b.Write(data)
				b.WriteByte('\n')
			}
		}
		return nil
	})
	return b.String()
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

// TestOptimizePipelineCombination 验证 shake+minify+bytecode-opt 组合管线
// 在共享 AST 上顺序执行不互相破坏：优化产物与无优化产物输出一致，
// 且 tree-shake 后的依赖不被 minify 从原始源码恢复。
func TestOptimizePipelineCombination(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.js"),
		[]byte("export function used() { return 1 + 2; }\nexport function dead() { return 99; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"),
		[]byte("import { used } from './lib.js';\nconsole.log(used());\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runArtifact := func(name string, buildArgs ...string) string {
		t.Helper()
		artifact := filepath.Join(dir, name+exeSuffix())
		args := append([]string{"build", "--compile", "--outfile", artifact}, buildArgs...)
		args = append(args, "main.js")
		build := exec.Command(bin, args...)
		build.Dir = dir
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s build failed: %v\n%s", name, err, out)
		}
		run := exec.Command(artifact)
		run.Dir = dir
		out, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("%s run failed: %v", name, err)
		}
		return string(out)
	}

	plain := runArtifact("plain", "--no-tree-shake", "--no-bytecode-opt")
	optimized := runArtifact("optimized", "--optimize")
	if plain != optimized {
		t.Errorf("optimized output = %q, want plain %q", optimized, plain)
	}
	if optimized != "3\n" {
		t.Errorf("optimized output = %q, want %q", optimized, "3\n")
	}
}

// TestWebBuildHTMLAndCSSAndSourcemap 测试 Web 构建全链路（HTML、CSS抽取、Sourcemap、JSON内联）
func TestWebBuildHTMLAndCSSAndSourcemap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body { background: #fff; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte("h1 { color: #007acc; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"title":"Aluka Static Build","count":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	appTS := `import info from './data.json';
import './theme.css';
export const title = info.title;
export const count = info.count;
`
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(appTS), 0o644); err != nil {
		t.Fatal(err)
	}

	htmlContent := `<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="./style.css">
</head>
<body>
    <div id="root"></div>
    <script type="module" src="./app.ts"></script>
</body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(htmlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(dir, "dist")
	cmd := exec.Command(bin, "build", "--target=web", "--sourcemap", "--minify", "--outdir", distDir, "index.html")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build web HTML entry failed: %v\n%s", err, out)
	}

	// 1. 验证 HTML 生成与引用改写
	htmlOut, err := os.ReadFile(filepath.Join(distDir, "index.html"))
	if err != nil {
		t.Fatalf("read output index.html failed: %v", err)
	}
	htmlText := string(htmlOut)
	if !strings.Contains(htmlText, `src="assets/app-`) {
		t.Errorf("rewritten HTML does not reference hashed app JS:\n%s", htmlText)
	}
	if !strings.Contains(htmlText, `href="assets/style-`) {
		t.Errorf("rewritten HTML does not reference hashed style.css:\n%s", htmlText)
	}
	if !strings.Contains(htmlText, "crossorigin") {
		t.Errorf("HTML missing crossorigin:\n%s", htmlText)
	}
	if !strings.Contains(htmlText, `rel="modulepreload"`) {
		t.Errorf("HTML missing modulepreload:\n%s", htmlText)
	}

	jsPath := globHashedAsset(t, distDir, "assets/app-*.js")
	jsOut, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read hashed app JS failed: %v", err)
	}
	jsStr := string(jsOut)
	if strings.Contains(jsStr, "__def(") || strings.Contains(jsStr, "__req(") {
		t.Errorf("app JS still uses wrap runtime: %s", jsStr)
	}
	allJS := readDirConcatJS(t, distDir)
	if !strings.Contains(allJS, "Aluka Static Build") {
		t.Errorf("hashed graph missing inlined JSON data")
	}
	if !strings.Contains(jsStr, "//# sourceMappingURL=") {
		t.Errorf("app.js missing sourceMappingURL comment: %s", jsStr)
	}

	mapPath := jsPath + ".map"
	mapOut, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read output sourcemap failed: %v", err)
	}
	var sm map[string]interface{}
	if err := json.Unmarshal(mapOut, &sm); err != nil {
		t.Fatalf("invalid sourcemap json: %v", err)
	}
	if sm["version"] != float64(3) {
		t.Errorf("sourcemap version = %v, want 3", sm["version"])
	}

	// 4. 验证 CSS 文件输出（独立 style.css 与 伴随 app.css）
	styleOut, err := os.ReadFile(globHashedAsset(t, distDir, "assets/style-*.css"))
	if err != nil {
		t.Fatalf("read hashed style.css failed: %v", err)
	}
	if len(styleOut) == 0 {
		t.Errorf("style.css is empty")
	}

	themeOut, err := os.ReadFile(globHashedAsset(t, distDir, "assets/app-*.css"))
	if err != nil {
		t.Fatalf("read hashed app.css failed: %v", err)
	}
	if !strings.Contains(string(themeOut), "color:#007acc") {
		t.Errorf("app.css does not contain minified theme styles: %s", themeOut)
	}
}

// TestWebBuildMultipleEntries 测试 Web 构建多 Entry 独立产出
func TestWebBuildMultipleEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "page1.ts"), []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page2.ts"), []byte("export const b = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(dir, "dist")
	cmd := exec.Command(bin, "build", "--target=web", "--outdir", distDir, "page1.ts", "page2.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build multiple web entries failed: %v\n%s", err, out)
	}

	if _, err := os.Stat(globHashedAsset(t, distDir, "assets/page1-*.js")); err != nil {
		t.Errorf("missing hashed page1.js: %v", err)
	}
	if _, err := os.Stat(globHashedAsset(t, distDir, "assets/page2-*.js")); err != nil {
		t.Errorf("missing hashed page2.js: %v", err)
	}
	if _, err := os.Stat(filepath.Join(distDir, "page1.js")); err != nil {
		t.Errorf("missing page1.js barrel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(distDir, "page2.js")); err != nil {
		t.Errorf("missing page2.js barrel: %v", err)
	}
}

// TestBuildCompileTargetWebConflict：--compile 与 --target=web 互斥（回归：
// watch 接入期间该校验曾被移除）。
func TestBuildCompileTargetWebConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "build", "--compile", "--target=web", "a.ts")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--compile --target=web should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "conflicts with --compile") {
		t.Errorf("error output missing conflict message:\n%s", out)
	}
}

// TestBuildInvalidGlobalName：--global-name 拒绝非法标识符（防 UMD 注入）。
func TestBuildInvalidGlobalName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "build", "--target=web", "--format=umd", "--global-name", `x"];alert(1)//`, "a.ts")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid --global-name should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "--global-name") {
		t.Errorf("error output missing --global-name message:\n%s", out)
	}
}

// TestBuildFormatRequiresWeb：--format 只在 web target 下可用。
func TestBuildFormatRequiresWeb(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "build", "--compile", "--format=cjs", "a.ts")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--compile --format=cjs should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "require --target=web") {
		t.Errorf("error output missing format/web message:\n%s", out)
	}
}

func TestWebBuildRealVueDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo", "main.ts")
	vuePackage := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo", "node_modules", "vue", "package.json")
	if _, err := os.Stat(vuePackage); err != nil {
		t.Fatalf("vendored vue fixture missing: %v", err)
	}

	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "main.js")
	build := exec.Command(bin, "build", "--target=web", "--outfile", outFile, entry)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real Vue demo failed: %v\n%s", err, out)
	}
	allJS := readDirConcatJS(t, outDir)
	if !strings.Contains(allJS, "chunk 加载成功") {
		t.Fatalf("Vue demo dynamic module missing in hashed graph")
	}
	if strings.Contains(allJS, "__req(") {
		t.Fatal("Vue ESM graph retained __req runtime")
	}
	bundle, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bundle), "process.env.NODE_ENV") {
		t.Fatal("Vue bundle retained process.env.NODE_ENV")
	}

	runNodeESMCheck(t, outDir, outFile, `import {pathToFileURL} from "node:url";
const m = await import(pathToFileURL(process.argv[2]).href);
const first = await m.renderApp();
if (!first.includes("Vue 3 Web Bundle") || !first.includes("x2 = 0")) throw new Error("bad first SSR");
const stat = await m.loadStatsOnce();
if (!stat.includes("chunk 加载成功") || !stat.includes("来源：root")) throw new Error("bad chunk result");
const second = await m.renderApp();
if (!second.includes("动态 chunk") || !second.includes("来源：root")) throw new Error("bad second SSR");
console.log("vue ssr ok");`, "vue ssr ok")
}

// TestWebBuildCJSAndUMDOutput：CJS/UMD 产物的入口导出形态正确
// （无 ESM export 语句；CJS 挂 module.exports，UMD 含三分支 wrapper）。
func TestWebBuildCJSAndUMDOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;\nexport default 2;"), 0o644); err != nil {
		t.Fatal(err)
	}

	cjsOut := filepath.Join(dir, "a.cjs")
	cmd := exec.Command(bin, "build", "--target=web", "--format=cjs", "--outfile", cjsOut, "a.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build --format=cjs failed: %v\n%s", err, out)
	}
	cjs, err := os.ReadFile(cjsOut)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cjs), "export ") {
		t.Errorf("cjs output contains ESM export:\n%s", cjs)
	}
	if !strings.Contains(string(cjs), "module.exports") {
		t.Errorf("cjs output missing module.exports:\n%s", cjs)
	}
	if strings.Count(string(cjs), "var __entry=__req(") != 1 {
		t.Errorf("cjs output should execute entry exactly once:\n%s", cjs)
	}

	umdOut := filepath.Join(dir, "a.umd.js")
	cmd = exec.Command(bin, "build", "--target=web", "--format=umd", "--global-name", "MyLib", "--outfile", umdOut, "a.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build --format=umd failed: %v\n%s", err, out)
	}
	umd, err := os.ReadFile(umdOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"define.amd", `root["MyLib"]`, "module.exports"} {
		if !strings.Contains(string(umd), want) {
			t.Errorf("umd output missing %q:\n%s", want, umd)
		}
	}
	if strings.Count(string(umd), "var __entry=__req(") != 1 {
		t.Errorf("umd output should execute entry exactly once:\n%s", umd)
	}
}

// TestWebBuildOutfileWithChunks：--outfile 与伴随 chunk 共存时主产物必须
// 写到 --outfile 指定路径（扩展名可与生成名不同，如 .cjs），chunk 写同目录
// （回归：旧逻辑按"资产名 == outfile 文件名"匹配，失配时主产物被写偏）。
func TestWebBuildOutfileWithChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lazy.ts"), []byte("export const v = 'lazy';"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte("export const sync = 'main';\nexport async function load() { return (await import('./lazy.ts')).v; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "dist", "app.cjs")
	cmd := exec.Command(bin, "build", "--target=web", "--format=cjs", "--outfile", out, "main.ts")
	cmd.Dir = dir
	if res, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, res)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("primary output missing at --outfile: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "dist", "chunk-*.js"))
	if len(matches) == 0 {
		t.Fatal("companion chunk missing next to --outfile")
	}
	if _, err := os.Stat(filepath.Join(dir, "dist", "main.js")); err == nil {
		t.Error("primary output was additionally written under generated name; want only --outfile copy")
	}
}

func TestWebProductionDefines(t *testing.T) {
	defines := project.ProductionDefines()
	for key, want := range map[string]string{
		"process.env.NODE_ENV":                    `"production"`,
		"__VUE_OPTIONS_API__":                     "true",
		"__VUE_PROD_DEVTOOLS__":                   "false",
		"__VUE_PROD_HYDRATION_MISMATCH_DETAILS__": "false",
	} {
		if got := defines[key]; got != want {
			t.Errorf("define %s = %q, want %q", key, got, want)
		}
	}
}

// TestWebBuildVueOfficialBackend：--vue-compiler=official 用官方 compiler-sfc
// （构建期在自研 VM 内执行）编译 demo，产物含 Vite 式代码生成标记且 SSR 通过。
func TestWebBuildVueOfficialBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo", "main.ts")
	vuePackage := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo", "node_modules", "vue", "package.json")
	if _, err := os.Stat(vuePackage); err != nil {
		t.Fatalf("vendored vue fixture missing: %v", err)
	}

	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "main.js")
	build := exec.Command(bin, "build", "--target=web", "--vue-compiler=official", "--outfile", outFile, entry)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("official backend build failed: %v\n%s", err, out)
	}
	allJS := readDirConcatJS(t, outDir)
	for _, marker := range []string{"_createElementVNode", "__sfc_render__", "_openBlock"} {
		if !strings.Contains(allJS, marker) {
			t.Errorf("official bundle missing marker %q", marker)
		}
	}
	if strings.Contains(allJS, "__req(") {
		t.Fatal("official ESM graph retained __req runtime")
	}

	runNodeESMCheck(t, outDir, outFile, `import {pathToFileURL} from "node:url";
const m = await import(pathToFileURL(process.argv[2]).href);
const first = await m.renderApp();
if (!first.includes("Vue 3 Web Bundle") || !first.includes("x2 = 0")) throw new Error("bad first SSR");
const stat = await m.loadStatsOnce();
if (!stat.includes("来源：root")) throw new Error("bad chunk result");
const second = await m.renderApp();
if (!second.includes("动态 chunk")) throw new Error("bad second SSR");
console.log("official vue ssr ok");`, "official vue ssr ok")
}

func TestWebBuildVueStyleSrcScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("theme.css", ".ext{display:block}")
	write("App.vue", `<template><div class="app">hello</div></template>
<style scoped>.app{color:red}</style>
<style src="./theme.css"></style>
<script>import { h } from 'vue'; export default { render(){ return h('div',{class:'app'},'hello') } }</script>`)
	write("main.ts", "import App from './App.vue';\nexport default App;\n")
	write("index.html", `<!doctype html><html><head></head><body><script type="module" src="./main.ts"></script></body></html>`)
	write("node_modules/vue/package.json", `{"name":"vue","version":"1.0.0","main":"./index.js"}`)
	write("node_modules/vue/index.js", `export function h(t,p,c){return {type:t,props:p||{},children:c||[]}}
export function ref(v){return {value:v}}
export function unref(v){return v}
export function toDisplayString(v){return String(v)}`)

	outDir := t.TempDir()
	cmd := exec.Command(bin, "build", "--target=web", "--outdir", outDir, "index.html")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vue style build failed: %v\n%s", err, out)
	}
	cssPath := globHashedAsset(t, outDir, "assets/main-*.css")
	css, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("expected hashed main.css: %v", err)
	}
	cssText := string(css)
	if !strings.Contains(cssText, "data-v-") {
		t.Fatalf("scoped css missing data-v: %s", cssText)
	}
	if !strings.Contains(cssText, ".ext") {
		t.Fatalf("src css missing .ext: %s", cssText)
	}
	htmlOut, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	htmlText := string(htmlOut)
	if !strings.Contains(htmlText, `href="assets/main-`) {
		t.Fatalf("HTML missing injected Vue CSS link:\n%s", htmlText)
	}
	if !strings.Contains(htmlText, "crossorigin") || !strings.Contains(htmlText, `rel="modulepreload"`) {
		t.Fatalf("HTML missing Vite-style module attrs:\n%s", htmlText)
	}
}

// TestWebBuildVueCompilerFlagValidation：--vue-compiler 拒绝未知值，且只在
// web target 下可用（compile 路径不能静默忽略）。
func TestWebBuildVueCompilerFlagValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "build", "--target=web", "--vue-compiler", "fast", "a.ts")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid --vue-compiler should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "--vue-compiler") {
		t.Errorf("error output missing --vue-compiler message:\n%s", out)
	}

	cmd = exec.Command(bin, "build", "--compile", "--vue-compiler=official", "a.ts")
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--compile --vue-compiler should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "requires --target=web") {
		t.Errorf("target conflict output missing --target=web message:\n%s", out)
	}
}

// TestWebBuildVueOfficialErrorMapping：official 后端的 SFC 错误映射为结构化
// filename:line:column 诊断，并验证相邻修复不会被误报。
func TestWebBuildVueOfficialErrorMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	demoDir := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo")
	badSFC := filepath.Join(demoDir, "_bad_probe.vue")
	badSource := "<template>\n  <div>{{ total + }}</div>\n</template>\n<script>export default {};</script>\n"
	if err := os.WriteFile(badSFC, []byte(badSource), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(badSFC)
	cmd := exec.Command(bin, "build", "--target=web", "--vue-compiler=official", "--outfile", filepath.Join(t.TempDir(), "x.js"), "_bad_probe.vue")
	cmd.Dir = demoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bad SFC should fail official build, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "_bad_probe.vue:2:11") {
		t.Errorf("error output missing structured .vue location:\n%s", out)
	}

	validSource := "<template>\n  <div>{{ total + 1 }}</div>\n</template>\n<script>export default { data(){ return { total: 1 }; } };</script>\n"
	if err := os.WriteFile(badSFC, []byte(validSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(bin, "build", "--target=web", "--vue-compiler=official", "--outfile", filepath.Join(t.TempDir(), "x.js"), "_bad_probe.vue")
	cmd.Dir = demoDir
	if repairedOut, repairedErr := cmd.CombinedOutput(); repairedErr != nil {
		t.Fatalf("repaired SFC should build, got %v:\n%s", repairedErr, repairedOut)
	}
}

// TestWebBuildVueOfficialTypeScriptScopes 验证 official 的 TS script 通过 graph
// 独立模块解析，并允许 script/template 生成同名 render 绑定。
func TestWebBuildVueOfficialTypeScriptScopes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	demoDir := filepath.Join(repoRoot, "demo", "web-bundle-vue-demo")
	sfcPath := filepath.Join(demoDir, "_ts_scope_probe.vue")
	src := `<template><div>{{ render }}</div></template>
<script lang="ts">
const render: string = "scope-ok";
export default { data(): { render: string } { return { render }; } };
</script>`
	if err := os.WriteFile(sfcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(sfcPath)
	outFile := filepath.Join(t.TempDir(), "scope.js")
	cmd := exec.Command(bin, "build", "--target=web", "--vue-compiler=official", "--outfile", outFile, "_ts_scope_probe.vue")
	cmd.Dir = demoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("official TS scope build failed: %v\n%s", err, out)
	}
	bundle, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bundle), "scope-ok") && !strings.Contains(readDirConcatJS(t, filepath.Dir(outFile)), "scope-ok") {
		t.Fatalf("official TS scope bundle missing script content:\n%s", bundle)
	}
}

// TestGUIWebEntryBuild 测试 aluka build --gui --web-entry 前端源码直出桌面 exe 闭环
func TestGUIWebEntryBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()

	// 桌面主进程源码
	mainTS := `console.log("GUI backend initialized");`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(mainTS), 0o644); err != nil {
		t.Fatal(err)
	}

	// 前端 TSX 源码
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	webTSX := `export const App = () => "<div>Aluka GUI Studio</div>";`
	if err := os.WriteFile(filepath.Join(dir, "src", "index.tsx"), []byte(webTSX), 0o644); err != nil {
		t.Fatal(err)
	}

	artifact := filepath.Join(dir, "gui-app"+exeSuffix())
	cmd := exec.Command(bin, "build", "--compile", "--gui", "--web-entry", "src/index.tsx", "--outfile", artifact, "main.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build --gui --web-entry failed: %v\n%s", err, out)
	}

	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("cannot stat artifact: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("artifact is empty")
	}
}

// TestWebBuildDynamicProjectConfig 验证配置发现走脚本而非 Go 写死文件名：
// 项目钩子 aluka.config.js 提供 outDir/base/alias/define；CLI --outdir 仍优先。
func TestWebBuildDynamicProjectConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires building the aluka binary")
	}
	bin := alukaTestBinary(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"package.json": `{"name":"cfg-demo","private":true}`,
		"src/util.ts":  `export const tag = "from-alias";`,
		"src/main.ts":  `import { tag } from "@/util"; export const marker = tag + ":" + __FLAG__;`,
		"index.html":   `<!DOCTYPE html><html><body><script type="module" src="./src/main.ts"></script></body></html>`,
		"aluka.config.js": `
module.exports = {
  base: "/cdn/",
  outDir: "web-out",
  assetsDir: "media",
  alias: { "@": "./src" },
  define: { __FLAG__: JSON.stringify("cfg") },
};
`,
		"jest.config.js": `throw new Error("jest.config.js must not run during web build");`,
	}
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bin, "build", "--target=web", "index.html")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build with aluka.config.js failed: %v\n%s", err, out)
	}
	htmlOut, err := os.ReadFile(filepath.Join(dir, "web-out", "index.html"))
	if err != nil {
		t.Fatalf("config outDir not used: %v", err)
	}
	htmlText := string(htmlOut)
	if !strings.Contains(htmlText, `src="/cdn/media/main-`) {
		t.Errorf("HTML missing publicBase+assetsDir rewrite:\n%s", htmlText)
	}
	allJS := readDirConcatJS(t, filepath.Join(dir, "web-out"))
	if !strings.Contains(allJS, "from-alias") {
		t.Errorf("alias @ not applied, JS:\n%s", allJS)
	}
	if strings.Contains(allJS, "__def(") || strings.Contains(allJS, "__req(") {
		t.Errorf("ESM graph still uses wrap runtime")
	}

	cliDir := filepath.Join(dir, "cli-dist")
	cmd = exec.Command(bin, "build", "--target=web", "--outdir", cliDir, "index.html")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build with CLI --outdir failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(cliDir, "index.html")); err != nil {
		t.Fatalf("CLI --outdir should win over aluka.config.js outDir: %v", err)
	}
}
