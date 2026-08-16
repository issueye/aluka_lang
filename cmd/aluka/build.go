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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/bundler/analyze"
	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/emit"
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
	webEntry      string
	iconPath      string
	sourcemap     bool
	watch         bool
	format        string
	globalName    string
	target        string // ""=compile（默认） | "web"
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
	fs.Bool("sourcemap", "Generate source map files (for web target)", &opts.sourcemap)
	fs.Bool("watch", "Rebuild web bundle when source files change", &opts.watch)
	fs.Var(cli.FuncValue{Fn: func(v string) error {
		if v != "esm" && v != "cjs" && v != "umd" {
			return errors.New("--format supports only esm, cjs, or umd")
		}
		opts.format = v
		return nil
	}}, "format", "Output format: esm|cjs|umd")
	fs.String("global-name", "Global name for UMD output", &opts.globalName)
	fs.Bool("gui", "Embed frontend web assets as a GUI desktop app (with --compile)", &opts.guiApp)
	fs.String("web-dir", "Frontend web assets directory to embed (default: dist; requires --gui)", &opts.webDir).MissingMsg("--web-dir requires a path")
	fs.String("web-entry", "Frontend web entry point (e.g. index.tsx / index.html; requires --gui)", &opts.webEntry).MissingMsg("--web-entry requires a path")
	fs.String("icon", "Application .ico file embedded for window/taskbar/tray icons (requires --gui)", &opts.iconPath).MissingMsg("--icon requires a path")
	fs.Var(cli.FuncValue{Fn: func(v string) error {
		if v != "web" && v != "es2018" {
			return errors.New("--target supports only web or es2018")
		}
		opts.target = v
		return nil
	}}, "target", "Build target: web or es2018")
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
	if opts.webEntry != "" && !opts.guiApp {
		fatalErr("aluka build: --web-entry requires --gui")
	}
	if opts.webEntry != "" && opts.webDir != "" {
		fatalErr("aluka build: --web-entry and --web-dir are mutually exclusive")
	}
	if opts.iconPath != "" && !opts.guiApp {
		fatalErr("aluka build: --icon requires --gui")
	}
	if opts.sourcemap && opts.target != "web" {
		fatalErr("aluka build: --sourcemap requires --target=web")
	}
	if opts.target == "web" && opts.compileOnly {
		fatalErr("aluka build: --target=web conflicts with --compile")
	}
	if opts.watch && opts.compileOnly {
		fatalErr("aluka build: --watch conflicts with --compile")
	}
	if opts.watch && opts.target != "web" {
		fatalErr("aluka build: --watch requires --target=web")
	}
	if opts.watch && len(entries) != 1 {
		fatalErr("aluka build: --watch requires one entry")
	}
	if opts.analyzeOnly && opts.watch {
		fatalErr("aluka build: --watch conflicts with --analyze-only")
	}
	if opts.target == "es2018" {
		fatalErr("aluka build: --target=es2018 syntax lowering is not implemented")
	}
	if (opts.format != "" || opts.globalName != "") && opts.target != "web" {
		fatalErr("aluka build: --format/--global-name require --target=web")
	}
	if opts.globalName != "" && !isValidJSIdentifier(opts.globalName) {
		fatalErr("aluka build: --global-name must be a valid JavaScript identifier")
	}
	if opts.target == "" && !opts.compileOnly {
		fatalErr("aluka build: specify --compile (executable) or --target=web (browser bundle)")
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

	if opts.target == "web" {
		if opts.watch {
			watchWebBuild(entries[0], opts)
			return
		}
		for _, entry := range entries {
			webBuildOne(vm, resolver, entry, opts)
		}
		return
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

	// GUI 模式：收集前端静态资源目录（--web-dir，默认 dist）或通过 --web-entry 实时打包，以及应用图标（--icon）
	var webAssets map[string][]byte
	packOpts := compile.PackOptions{}
	if opts.guiApp {
		if opts.webEntry != "" {
			webEntryPath := opts.webEntry
			if !filepath.IsAbs(webEntryPath) {
				if _, err := os.Stat(webEntryPath); err != nil {
					cand := filepath.Join(filepath.Dir(entry), webEntryPath)
					if _, err2 := os.Stat(cand); err2 == nil {
						webEntryPath = cand
					}
				}
			}
			webVM, err := interpreter.NewVM()
			if err != nil {
				fatalErr("aluka build: " + err.Error())
			}
			webResolver := module.NewResolver()
			webAssets, err = bundleWebEntry(webVM, webResolver, webEntryPath, opts)
			if err != nil {
				fatalErr("aluka build: bundle --web-entry: " + err.Error())
			}
			if _, hasHTML := webAssets["index.html"]; !hasHTML {
				var mainScript string
				for assetName := range webAssets {
					if strings.HasSuffix(assetName, ".js") {
						mainScript = assetName
						break
					}
				}
				if mainScript != "" {
					defaultHTML := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Aluka App</title></head><body><div id="root"></div><script type="module" src="%s"></script></body></html>`, mainScript)
					webAssets["index.html"] = []byte(defaultHTML)
				}
			}
		} else {
			webDir := opts.webDir
			if webDir == "" {
				webDir = "dist"
			}
			webAssets, err = collectWebAssets(webDir)
			if err != nil {
				fatalErr("aluka build: " + err.Error())
			}
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
		payloadOffset, err = writeCompiledBinary(opts.basePath, out, payload, packOpts.Icon, opts.guiApp)
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
// guiApp 为真时切换 PE 子系统为 WINDOWS_GUI（双击运行不闪控制台）；
// icon 非空时先对基座做 PE 图标注入（Explorer 展示应用图标）。
// 返回 payload 在产物中的偏移。
func writeCompiledBinary(base, outfile string, payload []byte, icon []byte, guiApp bool) (int64, error) {
	exeData, err := os.ReadFile(base)
	if err != nil {
		return 0, fmt.Errorf("read base binary %q: %w", base, err)
	}
	if guiApp {
		exeData, err = compile.SetPESubsystemGUI(exeData)
		if err != nil {
			return 0, fmt.Errorf("set PE subsystem to windows-gui: %w", err)
		}
	}
	if len(icon) > 0 {
		exeData, err = compile.InjectIcon(exeData, icon)
		if err != nil {
			return 0, fmt.Errorf("inject application icon into base: %w", err)
		}
	}

	out, err := os.Create(outfile)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", outfile, err)
	}
	defer out.Close()

	if _, err := out.Write(exeData); err != nil {
		return 0, fmt.Errorf("write base binary: %w", err)
	}
	payloadOffset := int64(len(exeData))
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

// validateWebBuiltins 返回浏览器目标下 Node 内置模块的可操作诊断。
func validateWebBuiltins(builtins []graph.BuiltinDep) error {
	if len(builtins) == 0 {
		return nil
	}
	b := builtins[0]
	return fmt.Errorf(
		"web target 不支持 Node 内置模块 %q（来源 %s）——浏览器环境请改用 Web API（如 node:fs → fetch/File System Access API），或经 --polyfill 注入（M2）",
		b.Spec, b.Source)
}

// bundleWebEntry 实现对单个 web 入口（.html / .css / .js / .ts / .tsx）的编译打包，
// 返回相对产物路径 → 文件内容字节。
func bundleWebEntry(vm *interpreter.VM, resolver *module.Resolver, entry string, opts buildOptions) (map[string][]byte, error) {
	module.SetWebConditions()
	ext := strings.ToLower(filepath.Ext(entry))
	assets := make(map[string][]byte)

	// 1. HTML 入口处理（M2-2）
	if ext == ".html" {
		htmlData, err := os.ReadFile(entry)
		if err != nil {
			return nil, fmt.Errorf("read HTML entry %q: %w", entry, err)
		}
		htmlStr := string(htmlData)
		parsed := emit.ParseHTMLEntry(htmlStr)
		replacements := make(map[string]string)
		entryDir := filepath.Dir(entry)

		for _, script := range parsed.Scripts {
			scriptPath := filepath.Join(entryDir, filepath.FromSlash(script.Original))
			subAssets, err := bundleWebEntry(vm, resolver, scriptPath, opts)
			if err != nil {
				return nil, err
			}
			for k, v := range subAssets {
				assets[k] = v
			}
			baseScript := filepath.Base(script.Original)
			baseName := strings.TrimSuffix(baseScript, filepath.Ext(baseScript)) + ".js"
			replacements[script.Original] = baseName
		}

		for _, sheet := range parsed.Stylesheets {
			sheetPath := filepath.Join(entryDir, filepath.FromSlash(sheet.Original))
			cssData, err := os.ReadFile(sheetPath)
			if err != nil {
				return nil, fmt.Errorf("read stylesheet %q: %w", sheetPath, err)
			}
			content := string(cssData)
			if opts.minify {
				content = emit.MinifyCSS(content)
			}
			baseSheet := filepath.Base(sheet.Original)
			baseName := strings.TrimSuffix(baseSheet, filepath.Ext(baseSheet)) + ".css"
			assets[baseName] = []byte(content)
			replacements[sheet.Original] = baseName
		}

		rewritten := emit.RewriteHTML(htmlStr, replacements)
		assets[filepath.Base(entry)] = []byte(rewritten)
		return assets, nil
	}

	// 2. 独立 CSS 入口（M2-1）
	if ext == ".css" {
		cssData, err := os.ReadFile(entry)
		if err != nil {
			return nil, fmt.Errorf("read CSS entry %q: %w", entry, err)
		}
		content := string(cssData)
		if opts.minify {
			content = emit.MinifyCSS(content)
		}
		base := filepath.Base(entry)
		assets[base] = []byte(content)
		return assets, nil
	}

	// 3. JS / TS / TSX 入口打包（M1/M2）
	graphResult, err := graph.Build(vm, resolver, entry)
	if err != nil {
		return nil, err
	}
	if err := validateWebBuiltins(graphResult.Builtins); err != nil {
		return nil, err
	}
	if len(graphResult.UnresolvedDynamic) > 0 {
		return nil, fmt.Errorf("web target requires a string literal for dynamic import() (source %s)", graphResult.UnresolvedDynamic[0])
	}

	kept := make(map[string]bool, len(graphResult.SourceUnits))
	for key := range graphResult.SourceUnits {
		kept[key] = true
	}
	if opts.treeShake {
		shaken, err := shake.ShakeOpts(graphResult, graphResult.Entry, shake.Options{KeepEntryExports: true})
		if err != nil {
			return nil, err
		}
		kept = shaken.Kept
	}

	asyncTargets := make(map[string]bool, len(graphResult.DynamicDeps))
	for _, dep := range graphResult.DynamicDeps {
		asyncTargets[dep.Target] = true
	}
	moduleSources := make(map[string]string)
	mainModules := staticModuleClosure(graphResult, graphResult.Entry, kept)
	modules := make([]emit.Module, 0, len(mainModules))
	mainKeys := make([]string, 0, len(mainModules))
	for key := range mainModules {
		mainKeys = append(mainKeys, key)
	}
	sort.Strings(mainKeys)
	for _, key := range mainKeys {
		unit := graphResult.SourceUnits[key]
		if unit == nil || !kept[key] {
			continue
		}
		if opts.sourcemap {
			moduleSources[key] = string(unit.Source)
		}
		if opts.minify {
			minify.Program(unit.Program)
		}
		modules = append(modules, emit.Module{
			ID:             key,
			Prog:           unit.Program,
			IsTLA:          unit.HasTLA,
			IsCJS:          unit.ModuleKind == module.ModuleCommonJS,
			Resolved:       graphResult.Resolutions[key],
			DynamicImports: dynamicImportsFor(key, graphResult.DynamicDeps),
		})
	}

	outJS, err := emit.Bundle{
		EntryID: graphResult.Entry,
		Modules: modules,
		Assets:  graphResult.Assets,
		Format:  opts.format,
		Global:  opts.globalName,
	}.Build()

	if err != nil {
		return nil, err
	}

	baseName := filepath.Base(entry)
	if e := filepath.Ext(baseName); e != "" {
		baseName = baseName[:len(baseName)-len(e)]
	}
	jsFileName := baseName + ".js"
	for _, dep := range graphResult.DynamicDeps {
		unit := graphResult.SourceUnits[dep.Target]
		if unit == nil {
			continue
		}
		chunkKeys := staticModuleClosure(graphResult, dep.Target, kept)
		chunkIDs := make([]string, 0, len(chunkKeys))
		for key := range chunkKeys {
			chunkIDs = append(chunkIDs, key)
		}
		sort.Strings(chunkIDs)
		chunkModules := make([]emit.Module, 0, len(chunkIDs))
		for _, key := range chunkIDs {
			chunkUnit := graphResult.SourceUnits[key]
			if chunkUnit == nil {
				continue
			}
			if opts.minify {
				minify.Program(chunkUnit.Program)
			}
			chunkModules = append(chunkModules, emit.Module{ID: key, Prog: chunkUnit.Program, IsTLA: chunkUnit.HasTLA, IsCJS: chunkUnit.ModuleKind == module.ModuleCommonJS, Resolved: graphResult.Resolutions[key], DynamicImports: dynamicImportsFor(key, graphResult.DynamicDeps)})
		}
		chunkText, err := (emit.Bundle{EntryID: dep.Target, Modules: chunkModules}).BuildChunk()
		if err != nil {
			return nil, err
		}
		assets[dynamicChunkName(dep.Target)] = []byte(chunkText)
	}

	if opts.sourcemap {

		mapFileName := jsFileName + ".map"
		smJSON, err := emit.GenerateSimpleSourceMap(jsFileName, moduleSources)
		if err != nil {
			return nil, fmt.Errorf("generate sourcemap: %w", err)
		}
		assets[mapFileName] = []byte(smJSON)
		outJS += "\n//# sourceMappingURL=" + mapFileName + "\n"
	}

	// 提取伴随 CSS 模块（M2-1）
	var cssFiles []emit.CSSFile
	for assetKey, data := range graphResult.Assets {
		if strings.HasSuffix(assetKey, ".css") {
			cssFiles = append(cssFiles, emit.CSSFile{ID: assetKey, Content: string(data)})
		}
	}
	if len(cssFiles) > 0 {
		cssBundle, err := emit.BundleCSS(cssFiles, opts.minify)
		if err != nil {
			return nil, fmt.Errorf("bundle CSS: %w", err)
		}
		if len(cssBundle) > 0 {
			assets[baseName+".css"] = []byte(cssBundle)
		}
	}

	assets[jsFileName] = []byte(outJS)
	return assets, nil
}

func webBuildOne(vm *interpreter.VM, resolver *module.Resolver, entry string, opts buildOptions) {
	assets, err := bundleWebEntry(vm, resolver, entry, opts)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	outDir := opts.outdir
	if outDir == "" && opts.outfile != "" {
		outDir = filepath.Dir(opts.outfile)
	}
	if outDir == "" {
		outDir = "."
	}

	if outDir != "." {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			fatalErr("aluka build: " + err.Error())
		}
	}

	// 主产物名：--outfile 指定主产物完整路径（扩展名可不同，如 .cjs），
	// chunk/CSS/sourcemap 等伴随产物始终写 outDir。
	primaryName := webPrimaryName(entry)
	var primaryOut string
	for name, data := range assets {
		var targetPath string
		if opts.outfile != "" && name == primaryName {
			targetPath = opts.outfile
		} else {
			targetPath = filepath.Join(outDir, name)
		}

		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		if primaryOut == "" || name == primaryName {
			primaryOut = targetPath
		}
	}

	fmt.Printf("Bundled %s → %s (%d assets)%s\n",
		entry, primaryOut, len(assets),
		func() string {
			if opts.minify {
				return ", minified"
			}
			return ""
		}())
}

func staticModuleClosure(gr *graph.Result, entry string, kept map[string]bool) map[string]bool {
	out := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if out[key] || !kept[key] {
			continue
		}
		out[key] = true
		for spec, target := range gr.Resolutions[key] {
			dynamic := false
			for _, dep := range gr.DynamicDeps {
				if dep.Source == key && dep.Spec == spec {
					dynamic = true
					break
				}
			}
			if !dynamic && kept[target] {
				queue = append(queue, target)
			}
		}
	}
	return out
}

func watchWebBuild(entry string, opts buildOptions) {
	outDir := webOutputDir(opts)
	written := map[string]bool{}
	for {
		vm, err := interpreter.NewVM()
		if err == nil {
			if assets, buildErr := bundleWebEntry(vm, module.NewResolver(), entry, opts); buildErr != nil {
				fmt.Fprintln(os.Stderr, "watch:", buildErr)
			} else if writeErr := writeWebAssetsTracked(entry, assets, opts, written); writeErr != nil {
				fmt.Fprintln(os.Stderr, "watch:", writeErr)
			} else {
				fmt.Println("watch: rebuilt")
			}
		}
		snapshot := watchSnapshot(entry, outDir)
		for {
			time.Sleep(300 * time.Millisecond)
			next := watchSnapshot(entry, outDir)
			if !reflect.DeepEqual(snapshot, next) {
				break
			}
		}
	}
}

// webOutputDir 统一计算 web 产物输出目录（--outfile 时为其父目录）。
func webOutputDir(opts buildOptions) string {
	if opts.outdir != "" {
		return opts.outdir
	}
	if opts.outfile != "" {
		return filepath.Dir(opts.outfile)
	}
	return "."
}

// watchSnapshot 收集入口目录下源文件的 (mtime:size) 快照；skipDir 下的文件
// （构建产物目录）不纳入，避免重建写盘再次触发变更检测。
func watchSnapshot(entry, skipDir string) map[string]string {
	out := make(map[string]string)
	root := filepath.Dir(entry)
	var skipAbs string
	if abs, err := filepath.Abs(skipDir); err == nil {
		skipAbs = abs
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if err == nil && d.IsDir() && skipAbs != "" {
				if abs, absErr := filepath.Abs(path); absErr == nil && abs == skipAbs {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".css" || ext == ".html" || filepath.Base(path) == "package.json" {
			if skipAbs != "" {
				if abs, absErr := filepath.Abs(filepath.Dir(path)); absErr == nil && abs == skipAbs {
					return nil
				}
			}
			if info, statErr := d.Info(); statErr == nil {
				out[path] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
			}
		}
		return nil
	})
	return out
}

// writeWebAssetsTracked 写出产物并清理上一轮已写但本轮不再生成的文件，
// 避免依赖删除后陈旧 chunk 残留在输出目录。
func writeWebAssetsTracked(entry string, assets map[string][]byte, opts buildOptions, written map[string]bool) error {
	if err := writeWebAssets(entry, assets, opts); err != nil {
		return err
	}
	outDir := webOutputDir(opts)
	current := map[string]bool{}
	for name := range assets {
		target := filepath.Join(outDir, name)
		if opts.outfile != "" && name == webPrimaryName(entry) {
			target = opts.outfile
		}
		current[target] = true
	}
	for target := range written {
		if !current[target] {
			_ = os.Remove(target)
		}
	}
	for target := range current {
		written[target] = true
	}
	return nil
}

// isValidJSIdentifier 校验 UMD global 名：字母/数字/_/$ 且不以数字开头，
// 且不为保留字（保留字作为全局名会生成非法脚本）。
func isValidJSIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r == '$' {
			continue
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	switch name {
	case "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends",
		"false", "finally", "for", "function", "if", "import", "in",
		"instanceof", "new", "null", "return", "super", "switch", "this",
		"throw", "true", "try", "typeof", "var", "void", "while", "with":
		return false
	}
	return true
}

// webPrimaryName 返回 web 构建的主产物资产名：HTML/CSS 入口为同名文件，
// JS/TS/TSX 入口为去扩展名 + ".js"。
func webPrimaryName(entry string) string {
	base := filepath.Base(entry)
	ext := strings.ToLower(filepath.Ext(entry))
	if ext == ".html" || ext == ".css" {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) + ".js"
}

func writeWebAssets(entry string, assets map[string][]byte, opts buildOptions) error {
	outDir := opts.outdir
	if outDir == "" && opts.outfile != "" {
		outDir = filepath.Dir(opts.outfile)
	}
	if outDir == "" {
		outDir = "."
	}
	if outDir != "." {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
	}
	for name, data := range assets {
		target := filepath.Join(outDir, name)
		if opts.outfile != "" && name == webPrimaryName(entry) {
			target = opts.outfile
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func dynamicChunkName(target string) string {
	clean := filepath.ToSlash(target)
	var h uint32 = 2166136261
	for i := 0; i < len(clean); i++ {
		h ^= uint32(clean[i])
		h *= 16777619
	}
	return fmt.Sprintf("chunk-%08x.js", h)
}

func dynamicImportsFor(source string, deps []graph.DynamicDep) map[string]emit.DynamicImport {
	out := make(map[string]emit.DynamicImport)
	for _, dep := range deps {
		if dep.Source != source {
			continue
		}
		out[dep.Spec] = emit.DynamicImport{Chunk: dynamicChunkName(dep.Target), Target: dep.Target}
	}
	return out
}
