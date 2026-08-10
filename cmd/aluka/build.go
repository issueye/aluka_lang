// `aluka build` 子命令（Phase 7 打包器）。
//
// M1（docs/build-compile-plan.md）支持 --compile 单文件可执行产物：
//
//	aluka build --compile --outfile app ./src/index.ts
//
// 产物 = 当前 aluka 基座 + payload（预编译字节码 + manifest）+ footer。
// 产物在无 aluka/Go 环境的机器上可直接运行（字节码平台无关，见计划 §3）。
//
// T2（docs/test-bundle-optimize-plan.md §5.2）扩展：
//
//	--tree-shake / --no-tree-shake  模块级 tree-shaking（默认开启）
//	--minify                        死代码消除 + 未用声明删除 + 常量折叠
//	--bytecode-opt                  基础 VM 字节码优化（指令删除/跳转穿透/融合）
//	--optimize                      启用 tree-shake + minify + bytecode-opt
//	--analyze[=text|json]            输出 payload 热点和阶段收益报告
//	--outdir <dir>                  多入口分别产出（共享模块构建期去重）
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/analyze"
	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/minify"
	"github.com/aluka-lang/aluka/internal/bundler/shake"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

type buildOptions struct {
	compileOnly   bool
	outfile       string
	outdir        string
	basePath      string
	treeShake     bool
	minify        bool
	bytecodeOpt   bool
	analyzeFormat string
	analyzeOut    string
	analyzeOnly   bool
	analyzeTop    int
	maxPayload    int64
	noTreeShake   bool
	noBytecodeOpt bool
}

type buildResult struct {
	summary        string
	report         *analyze.Report
	budgetExceeded bool
}

// stripBOM 剥离开头的 UTF-8 BOM。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}

// cmdBuild 实现 `aluka build [--compile] [--outfile <path>] [--outdir <dir>]
// [--base <path>] [--tree-shake|--no-tree-shake] [--minify] <entry>...`。
func cmdBuild(args []string) {
	opts := buildOptions{treeShake: true, analyzeTop: 10}
	optimize := false
	var entries []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--compile":
			opts.compileOnly = true
		case arg == "--outfile":
			if i+1 >= len(args) {
				fatalErr("aluka build: --outfile requires a path")
			}
			i++
			opts.outfile = args[i]
		case strings.HasPrefix(arg, "--outfile="):
			opts.outfile = strings.TrimPrefix(arg, "--outfile=")
		case arg == "--outdir":
			if i+1 >= len(args) {
				fatalErr("aluka build: --outdir requires a path")
			}
			i++
			opts.outdir = args[i]
		case strings.HasPrefix(arg, "--outdir="):
			opts.outdir = strings.TrimPrefix(arg, "--outdir=")
		case arg == "--base":
			if i+1 >= len(args) {
				fatalErr("aluka build: --base requires a path")
			}
			i++
			opts.basePath = args[i]
		case strings.HasPrefix(arg, "--base="):
			opts.basePath = strings.TrimPrefix(arg, "--base=")
		case arg == "--tree-shake":
			opts.treeShake = true
			opts.noTreeShake = false
		case arg == "--no-tree-shake":
			opts.treeShake = false
			opts.noTreeShake = true
		case arg == "--minify":
			opts.minify = true
		case arg == "--bytecode-opt":
			opts.bytecodeOpt = true
			opts.noBytecodeOpt = false
		case arg == "--no-bytecode-opt":
			opts.bytecodeOpt = false
			opts.noBytecodeOpt = true
		case arg == "--optimize":
			optimize = true
		case arg == "--analyze":
			opts.analyzeFormat = "text"
		case strings.HasPrefix(arg, "--analyze="):
			opts.analyzeFormat = strings.TrimPrefix(arg, "--analyze=")
		case arg == "--analyze-out":
			if i+1 >= len(args) {
				fatalErr("aluka build: --analyze-out requires a path")
			}
			i++
			opts.analyzeOut = args[i]
		case strings.HasPrefix(arg, "--analyze-out="):
			opts.analyzeOut = strings.TrimPrefix(arg, "--analyze-out=")
		case arg == "--analyze-only":
			opts.analyzeOnly = true
		case arg == "--analyze-top":
			if i+1 >= len(args) {
				fatalErr("aluka build: --analyze-top requires a number")
			}
			i++
			opts.analyzeTop = parseAnalyzeTop(args[i])
		case strings.HasPrefix(arg, "--analyze-top="):
			opts.analyzeTop = parseAnalyzeTop(strings.TrimPrefix(arg, "--analyze-top="))
		case arg == "--max-payload":
			if i+1 >= len(args) {
				fatalErr("aluka build: --max-payload requires a size")
			}
			i++
			opts.maxPayload = parsePayloadBudget(args[i])
		case strings.HasPrefix(arg, "--max-payload="):
			opts.maxPayload = parsePayloadBudget(strings.TrimPrefix(arg, "--max-payload="))
		case arg == "--sourcemap" || arg == "--target":
			fatalErr("aluka build: " + arg + " not implemented (scope: --compile only)")
		case strings.HasPrefix(arg, "-"):
			fatalErr("aluka build: unknown option " + arg)
		default:
			entries = append(entries, arg)
		}
	}

	if optimize {
		if opts.noTreeShake {
			fatalErr("aluka build: --optimize conflicts with --no-tree-shake")
		}
		if opts.noBytecodeOpt {
			fatalErr("aluka build: --optimize conflicts with --no-bytecode-opt")
		}
		opts.treeShake = true
		opts.minify = true
		opts.bytecodeOpt = true
	}
	if opts.analyzeOnly && opts.analyzeFormat == "" {
		opts.analyzeFormat = "text"
	}
	if opts.analyzeFormat != "" && opts.analyzeFormat != "text" && opts.analyzeFormat != "json" {
		fatalErr("aluka build: --analyze must be text or json")
	}
	if opts.analyzeOut != "" && opts.analyzeFormat == "" {
		fatalErr("aluka build: --analyze-out requires --analyze")
	}
	if !opts.compileOnly {
		fatalErr("aluka build: M1 supports only --compile (single-file executable); plain bundling is not implemented")
	}
	if len(entries) == 0 {
		fatalErr("aluka build: missing entry file")
	}
	if opts.outdir != "" && opts.outfile != "" {
		fatalErr("aluka build: --outdir and --outfile are mutually exclusive")
	}
	if len(entries) > 1 && opts.outdir == "" {
		fatalErr("aluka build: multiple entries require --outdir")
	}

	vm, err := interpreter.NewVM()
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}
	resolver := module.NewResolver()

	// 基座：--base 指定（跨平台产物 = 目标平台基座 + 同一 payload，
	// 字节码平台无关）；默认当前可执行文件。
	if opts.basePath == "" {
		if exe, err := os.Executable(); err == nil {
			opts.basePath = exe
		} else {
			fatalErr("aluka build: cannot locate base binary: " + err.Error())
		}
	}

	results := make([]buildResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, buildOne(vm, resolver, entry, opts))
	}

	jsonStdout := opts.analyzeFormat == "json" && opts.analyzeOut == ""
	for _, result := range results {
		if result.summary == "" {
			continue
		}
		if jsonStdout {
			fmt.Fprintln(os.Stderr, result.summary)
		} else {
			fmt.Println(result.summary)
		}
	}
	reports := make([]*analyze.Report, 0, len(results))
	budgetExceeded := false
	for _, result := range results {
		if result.report != nil {
			reports = append(reports, result.report)
		}
		budgetExceeded = budgetExceeded || result.budgetExceeded
	}
	if opts.analyzeFormat != "" {
		if err := writeAnalysisReports(opts, reports); err != nil {
			fatalErr("aluka build: " + err.Error())
		}
	}
	if budgetExceeded {
		fmt.Fprintf(os.Stderr, "aluka build: payload exceeds --max-payload=%d bytes\n", opts.maxPayload)
		osExit(2)
	}
}

// buildOne 构建单个入口的产物。
func buildOne(vm *interpreter.VM, resolver *module.Resolver, entry string, opts buildOptions) buildResult {
	needAnalysis := opts.analyzeFormat != "" || opts.maxPayload > 0
	// 构建模块图：入口 + 静态可达依赖（import/export/require/动态 import
	// 字面量与可折叠常量），编译全部模块并记录构建期解析映射。
	graphResult, err := graph.Build(vm, resolver, entry)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	// T2-B4：无法静态解析的动态 import 构建期警告（产物运行时会失败）。
	for _, key := range graphResult.UnresolvedDynamic {
		fmt.Fprintf(os.Stderr, "aluka build: warning: %s: dynamic import with non-constant specifier cannot be precompiled; it will fail at runtime\n", key)
	}

	var rawStage, shakenStage, minifiedStage, optimizedStage analyze.StageMeasurement
	if needAnalysis {
		rawStage = mustMeasureStage(graphResult.Modules)
	}

	// 打包数据（tree-shake → minify → pack）。
	modules := graphResult.Modules
	resolutions := graphResult.Resolutions
	assets := graphResult.Assets
	removed := 0
	if opts.treeShake {
		shaken, err := shake.Shake(vm, graphResult, graphResult.Entry)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		modules = shaken.Modules
		resolutions = shaken.Resolutions
		assets = shaken.Assets
		removed = shaken.Removed
	}
	if needAnalysis {
		shakenStage = mustMeasureStage(modules)
	}
	if opts.minify {
		rootDir := graphResult.RootDir
		for i, m := range modules {
			nd, err := minifyModule(vm, rootDir, m)
			if err != nil {
				fatalErr("aluka build: " + err.Error())
			}
			modules[i] = nd
		}
	}
	if needAnalysis {
		minifiedStage = mustMeasureStage(modules)
	}
	var bytecodeStats bytecode.OptimizationStats
	if opts.bytecodeOpt {
		for _, mod := range modules {
			stats, err := bytecode.OptimizeModule(mod.Module)
			if err != nil {
				fatalErr("aluka build: bytecode optimize " + mod.Path + ": " + err.Error())
			}
			mergeBytecodeStats(&bytecodeStats, stats)
		}
	}
	if needAnalysis {
		optimizedStage = mustMeasureStage(modules)
	}

	payload, err := compile.Pack(graphResult.Entry, modules, resolutions, assets)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	out := buildOutputPath(entry, opts.outfile, opts.outdir)
	baseSize := fileSize(opts.basePath)
	budgetExceeded := opts.maxPayload > 0 && int64(len(payload)) > opts.maxPayload
	payloadOffset := baseSize
	artifactSize := int64(0)
	if !opts.analyzeOnly && !budgetExceeded {
		if opts.outdir != "" {
			if err := os.MkdirAll(opts.outdir, 0755); err != nil {
				fatalErr("aluka build: " + err.Error())
			}
		}
		var err error
		payloadOffset, err = writeCompiledBinary(opts.basePath, out, payload)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		artifactSize = fileSize(out)
	}

	shakeNote := ""
	if removed > 0 {
		shakeNote = fmt.Sprintf(", tree-shaken %d modules", removed)
	}
	result := buildResult{budgetExceeded: budgetExceeded}
	switch {
	case budgetExceeded:
		result.summary = fmt.Sprintf("Analyzed %s (payload %d bytes exceeds budget %d bytes; artifact not written)", entry, len(payload), opts.maxPayload)
	case opts.analyzeOnly:
		result.summary = fmt.Sprintf("Analyzed %s (%d payload bytes, %d modules%s; artifact not written)", entry, len(payload), len(modules), shakeNote)
	default:
		result.summary = fmt.Sprintf("Compiled %s → %s (%d bytes, payload at %d, %d modules%s, base %s)",
			entry, out, artifactSize, payloadOffset, len(modules), shakeNote, opts.basePath)
	}
	if needAnalysis {
		report, err := analyze.BuildReport(analyze.Input{
			Entry:             graphResult.Entry,
			Output:            out,
			RootDir:           graphResult.RootDir,
			Resolutions:       graphResult.Resolutions,
			UnresolvedDynamic: graphResult.UnresolvedDynamic,
			Assets:            assets,
			Raw:               rawStage,
			Shaken:            shakenStage,
			Minified:          minifiedStage,
			BytecodeOptimized: optimizedStage,
			PayloadBytes:      int64(len(payload)),
			BaseBytes:         baseSize,
			ArtifactBytes:     artifactSize,
			Options: analyze.Options{
				TreeShake:        opts.treeShake,
				Minify:           opts.minify,
				BytecodeOptimize: opts.bytecodeOpt,
				MaxPayloadBytes:  opts.maxPayload,
			},
			BytecodeStats: bytecodeStats,
		})
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		result.report = report
	}
	return result
}

func parseAnalyzeTop(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 100 {
		fatalErr("aluka build: --analyze-top must be an integer from 1 to 100")
	}
	return n
}

func parsePayloadBudget(value string) int64 {
	n, err := parseMemorySize(value)
	if err != nil || n <= 0 {
		fatalErr("aluka build: invalid --max-payload value " + value)
	}
	return n
}

func buildOutputPath(entry, outfile, outdir string) string {
	if outfile != "" {
		return outfile
	}
	base := filepath.Base(entry)
	out := strings.TrimSuffix(base, filepath.Ext(base))
	if out == "" {
		out = "app"
	}
	if outdir != "" {
		out = filepath.Join(outdir, out)
	}
	return out
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		fatalErr("aluka build: stat " + path + ": " + err.Error())
	}
	return info.Size()
}

func mustMeasureStage(modules []*compile.EntryData) analyze.StageMeasurement {
	stage, err := analyze.MeasureStage(modules)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}
	return stage
}

func mergeBytecodeStats(dst *bytecode.OptimizationStats, src bytecode.OptimizationStats) {
	dst.FunctionsBefore += src.FunctionsBefore
	dst.FunctionsAfter += src.FunctionsAfter
	dst.InstructionsBefore += src.InstructionsBefore
	dst.InstructionsAfter += src.InstructionsAfter
	dst.RemovedInstructions += src.RemovedInstructions
	dst.FusedInstructions += src.FusedInstructions
	dst.ThreadedJumps += src.ThreadedJumps
}

func writeAnalysisReports(opts buildOptions, reports []*analyze.Report) error {
	var w io.Writer = os.Stdout
	var file *os.File
	if opts.analyzeOut != "" {
		dir := filepath.Dir(opts.analyzeOut)
		if dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("create analyze output directory: %w", err)
			}
		}
		var err error
		file, err = os.Create(opts.analyzeOut)
		if err != nil {
			return fmt.Errorf("create analyze output: %w", err)
		}
		defer file.Close()
		w = file
	}
	if opts.analyzeFormat == "json" {
		return analyze.WriteJSON(w, reports)
	}
	for i, report := range reports {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := analyze.WriteText(w, report, opts.analyzeTop); err != nil {
			return err
		}
	}
	return nil
}

// minifyModule 重新解析并最小化 ESM 模块（CJS 保守跳过——字符串包装
// 无法在 AST 层重编译，保持原样）。
func minifyModule(vm *interpreter.VM, rootDir string, m *compile.EntryData) (*compile.EntryData, error) {
	if m.ModuleType != compile.ModuleTypeESM {
		return m, nil
	}
	src, err := os.ReadFile(filepath.Join(rootDir, m.Path))
	if err != nil {
		return nil, fmt.Errorf("minify: cannot read %q: %w", m.Path, err)
	}
	prog, err := parser.ParseModule(string(stripBOM(src)))
	if err != nil {
		return nil, fmt.Errorf("minify: parse error in %q: %w", m.Path, err)
	}
	minify.Program(prog)
	// 显式 ESM：minify 后可能失去 import/export 声明，自动判定会误判 CJS。
	nd, err := compile.CompileProgramType(vm, prog, stripBOM(src), m.Path, true)
	if err != nil {
		return nil, fmt.Errorf("minify: recompile %q: %w", m.Path, err)
	}
	return nd, nil
}

// writeCompiledBinary 复制基座到 outfile 并追加 payload + footer。
// 返回 payload 在产物中的偏移。
func writeCompiledBinary(base, outfile string, payload []byte) (int64, error) {
	in, err := os.Open(base)
	if err != nil {
		return 0, fmt.Errorf("open base binary %q: %w", base, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat base binary: %w", err)
	}

	out, err := os.Create(outfile)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", outfile, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return 0, fmt.Errorf("copy base binary: %w", err)
	}
	payloadOffset := info.Size()
	if _, err := out.Write(payload); err != nil {
		return 0, fmt.Errorf("write payload: %w", err)
	}
	footer := compile.MakeFooter(uint64(payloadOffset), uint64(len(payload)), payload)
	if _, err := out.Write(footer); err != nil {
		return 0, fmt.Errorf("write footer: %w", err)
	}
	if err := out.Close(); err != nil {
		return 0, fmt.Errorf("close %q: %w", outfile, err)
	}
	return payloadOffset, nil
}
