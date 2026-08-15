// `aluka build` 子命令（Phase 7 打包器）。
//
// M1（docs/build-compile-plan.md）支持 --compile 单文件可执行产物：
//
//	aluka build --compile --outfile app ./src/index.ts
//
// 产物 = 当前 aluka 基座 + payload（预编译字节码 + manifest）+ footer。
// 产物在无 aluka/Go 环境的机器上可直接运行（字节码平台无关，见计划 §3）。
//
// GUI 模式（docs/aluka-gui-architecture-plan.md GUI-4）：
//
//	aluka build --compile --gui --web-dir ./dist --outfile app.exe ./src/main.ts
//
// 前端静态资源目录递归内嵌进 payload（manifest.webAssets，base64 + zlib），
// 产物启动时挂载到 aluka://app/ 内存虚拟协议并分离控制台（Windows 免黑框）。
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
	"errors"
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
	"github.com/aluka-lang/aluka/internal/cli"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
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
	guiApp        bool
	webDir        string
	iconPath      string
}

type buildResult struct {
	summary        string
	report         *analyze.Report
	budgetExceeded bool
}

// buildFlags 声明 `aluka build` 的 flag 集合（internal/cli 框架）。
// 错误消息与现状一致：缺值/非法值/未知选项均带 "aluka build: " 前缀。
func buildFlags(opts *buildOptions, optimize *bool) *cli.FlagSet {
	fs := cli.NewFlagSet("aluka build: ")
	fs.StrictUnknown = true
	fs.Bool("compile", "Compile a single-file executable", &opts.compileOnly)
	fs.String("outfile", "Output executable path", &opts.outfile).MissingMsg("--outfile requires a path")
	fs.String("outdir", "Output directory (required for multiple entries)", &opts.outdir).MissingMsg("--outdir requires a path")
	fs.String("base", "Base binary path (cross-platform artifact)", &opts.basePath).MissingMsg("--base requires a path")
	fs.Var(cli.ActionValue{Fn: func() error { opts.treeShake = true; opts.noTreeShake = false; return nil }}, "tree-shake", "Enable module-level tree-shaking")
	fs.Var(cli.ActionValue{Fn: func() error { opts.treeShake = false; opts.noTreeShake = true; return nil }}, "no-tree-shake", "Disable tree-shaking")
	fs.Bool("minify", "Enable dead-code elimination and minification", &opts.minify)
	fs.Var(cli.ActionValue{Fn: func() error { opts.bytecodeOpt = true; opts.noBytecodeOpt = false; return nil }}, "bytecode-opt", "Enable VM bytecode optimization")
	fs.Var(cli.ActionValue{Fn: func() error { opts.bytecodeOpt = false; opts.noBytecodeOpt = true; return nil }}, "no-bytecode-opt", "Disable bytecode optimization")
	fs.Bool("optimize", "Enable tree-shake + minify + bytecode-opt", optimize)
	fs.Var(cli.FuncValue{Fn: func(s string) error { opts.analyzeFormat = s; return nil }}, "analyze", "Report payload hotspots (text|json)").OptionalValue().Implicit("text")
	fs.String("analyze-out", "Write analysis report to a file", &opts.analyzeOut).MissingMsg("--analyze-out requires a path")
	fs.Bool("analyze-only", "Analyze payload without writing an executable", &opts.analyzeOnly)
	fs.Var(cli.FuncValue{Fn: func(s string) error { opts.analyzeTop = parseAnalyzeTop(s); return nil }}, "analyze-top", "Top N hotspots in the analysis report").MissingMsg("--analyze-top requires a number")
	fs.Var(cli.FuncValue{Fn: func(s string) error { opts.maxPayload = parsePayloadBudget(s); return nil }}, "max-payload", "Fail with exit code 2 when payload exceeds the budget").MissingMsg("--max-payload requires a size")
	fs.Var(cli.ActionValue{Fn: func() error { return errors.New("--sourcemap not implemented (scope: --compile only)") }}, "sourcemap", "Source map generation (not implemented)")
	fs.Var(cli.ActionValue{Fn: func() error { return errors.New("--target not implemented (scope: --compile only)") }}, "target", "Target platform (not implemented)")
	fs.Bool("gui", "Embed frontend web assets as a GUI desktop app (with --compile)", &opts.guiApp)
	fs.String("web-dir", "Frontend web assets directory to embed (default: dist; requires --gui)", &opts.webDir).MissingMsg("--web-dir requires a path")
	fs.String("icon", "Application .ico file embedded for window/taskbar/tray icons (requires --gui)", &opts.iconPath).MissingMsg("--icon requires a path")
	return fs
}

// stripBOM 剥离开头的 UTF-8 BOM。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}

// collectWebAssets 递归收集前端静态资源目录（--gui 模式），
// 返回 相对路径（/ 分隔）→ 原始字节。单文件上限 64MB，总量上限 512MB。
func collectWebAssets(dir string) (map[string][]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("--web-dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--web-dir %s: not a directory", dir)
	}

	const maxFileBytes = 64 << 20
	const maxTotalBytes = 512 << 20
	assets := make(map[string][]byte)
	var total int64

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read web asset %s: %w", rel, err)
		}
		if len(data) > maxFileBytes {
			return fmt.Errorf("web asset %s too large (%d bytes > 64MB)", rel, len(data))
		}
		total += int64(len(data))
		if total > maxTotalBytes {
			return fmt.Errorf("web assets total size exceeds 512MB")
		}
		assets[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return assets, nil
}

// cmdBuild 实现 `aluka build [--compile] [--outfile <path>] [--outdir <dir>]
// [--base <path>] [--tree-shake|--no-tree-shake] [--minify] <entry>...`。
func cmdBuild(args []string) {
	opts := buildOptions{treeShake: true, analyzeTop: 10}
	optimize := false
	// flag 解析（internal/cli 框架）：未知 flag 报错，风格与现状一致。
	entries, err := buildFlags(&opts, &optimize).Parse(args)
	if err != nil {
		fatalErr(err.Error())
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
	if opts.webDir != "" && !opts.guiApp {
		fatalErr("aluka build: --web-dir requires --gui")
	}
	if opts.iconPath != "" && !opts.guiApp {
		fatalErr("aluka build: --icon requires --gui")
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
	// 编译管线默认开启字节码优化；build 显式对齐 --bytecode-opt 开关语义
	// （默认关闭，保持 build 历史行为；开启时 CompileAST 内已优化，
	// 下方显式 OptimizeModule 调用为幂等兜底）。
	vm.SetOptimizeBytecode(opts.bytecodeOpt)
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
	// 构建模块图：解析 + 依赖收集（不编译字节码，AST 供优化阶段共享）。
	graphResult, err := graph.Build(vm, resolver, entry)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	// T2-B4：无法静态解析的动态 import 构建期警告（产物运行时会失败）。
	for _, key := range graphResult.UnresolvedDynamic {
		fmt.Fprintf(os.Stderr, "aluka build: warning: %s: dynamic import with non-constant specifier cannot be precompiled; it will fail at runtime\n", key)
	}

	// 优化管线：tree-shake → minify 在共享 SourceUnit AST 上顺序执行，最后
	// 统一编译一次。度量编译使用 clone，避免 ESM lower 破坏共享 AST。
	var rawStage, shakenStage, minifiedStage, optimizedStage analyze.StageMeasurement
	if needAnalysis {
		rawModules, err := compile.CompileUnitsForMeasure(vm, graphResult.SourceUnits)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		rawStage = mustMeasureStage(rawModules)
	}

	// 保留集合：默认全保留；tree-shake 后按 kept 过滤。
	kept := make(map[string]bool, len(graphResult.SourceUnits))
	for key := range graphResult.SourceUnits {
		kept[key] = true
	}
	resolutions := graphResult.Resolutions
	assets := graphResult.Assets
	removed := 0
	if opts.treeShake {
		shaken, err := shake.Shake(graphResult, graphResult.Entry)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		kept = shaken.Kept
		resolutions = shaken.Resolutions
		assets = shaken.Assets
		removed = shaken.Removed
	}
	if needAnalysis {
		shakenStage = mustMeasureStage(measureUnits(vm, graphResult.SourceUnits, kept))
	}
	if opts.minify {
		// minify 直接作用于共享 AST（含 shake 剪枝后的模块），CJS 跳过。
		for key, unit := range graphResult.SourceUnits {
			if !kept[key] || unit.ModuleKind != module.ModuleESM {
				continue
			}
			minify.Program(unit.Program)
			if err := unit.MarkStage(module.StageMinified); err != nil {
				fatalErr("aluka build: " + err.Error())
			}
		}
	}
	if needAnalysis {
		minifiedStage = mustMeasureStage(measureUnits(vm, graphResult.SourceUnits, kept))
	}

	// 统一编译保留模块（此时 AST 已含 shake/minify 结果）。
	modules, err := compile.CompileUnits(vm, keptSourceUnits(graphResult.SourceUnits, kept))
	if err != nil {
		fatalErr("aluka build: " + err.Error())
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

	// GUI 模式：收集前端静态资源目录（--web-dir，默认 dist）与应用图标（--icon）
	var webAssets map[string][]byte
	packOpts := compile.PackOptions{}
	if opts.guiApp {
		webDir := opts.webDir
		if webDir == "" {
			webDir = "dist"
		}
		webAssets, err = collectWebAssets(webDir)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		packOpts.WebAssets = webAssets

		if opts.iconPath != "" {
			iconData, err := os.ReadFile(opts.iconPath)
			if err != nil {
				fatalErr("aluka build: --icon: " + err.Error())
			}
			if len(iconData) > 4<<20 {
				fatalErr("aluka build: --icon: file too large (max 4MB)")
			}
			packOpts.Icon = iconData
		}
	}

	payload, err := compile.PackWithOptions(graphResult.Entry, modules, resolutions, assets, packOpts)
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
	guiNote := ""
	if opts.guiApp {
		guiNote = fmt.Sprintf(", %d web assets", len(webAssets))
		if len(packOpts.Icon) > 0 {
			guiNote += ", app icon"
		}
	}
	result := buildResult{budgetExceeded: budgetExceeded}
	switch {
	case budgetExceeded:
		result.summary = fmt.Sprintf("Analyzed %s (payload %d bytes exceeds budget %d bytes; artifact not written)", entry, len(payload), opts.maxPayload)
	case opts.analyzeOnly:
		result.summary = fmt.Sprintf("Analyzed %s (%d payload bytes, %d modules%s%s; artifact not written)", entry, len(payload), len(modules), shakeNote, guiNote)
	default:
		result.summary = fmt.Sprintf("Compiled %s → %s (%d bytes, payload at %d, %d modules%s%s, base %s)",
			entry, out, artifactSize, payloadOffset, len(modules), shakeNote, guiNote, opts.basePath)
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

// keptSourceUnits 返回保留集合对应的 SourceUnit 子集。
func keptSourceUnits(units map[string]*module.SourceUnit, kept map[string]bool) map[string]*module.SourceUnit {
	out := make(map[string]*module.SourceUnit, len(kept))
	for key := range kept {
		if u := units[key]; u != nil {
			out[key] = u
		}
	}
	return out
}

// measureUnits 编译保留模块的 clone 副本用于阶段度量（不破坏共享 AST）。
func measureUnits(vm *interpreter.VM, units map[string]*module.SourceUnit, kept map[string]bool) []*compile.EntryData {
	mods, err := compile.CompileUnitsForMeasure(vm, keptSourceUnits(units, kept))
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}
	return mods
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
