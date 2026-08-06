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
//	--outdir <dir>                  多入口分别产出（共享模块构建期去重）
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/minify"
	"github.com/aluka-lang/aluka/internal/bundler/shake"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

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
	compileOnly := false
	outfile := ""
	outdir := ""
	basePath := ""
	treeShake := true
	doMinify := false
	var entries []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--compile":
			compileOnly = true
		case arg == "--outfile":
			if i+1 >= len(args) {
				fatalErr("aluka build: --outfile requires a path")
			}
			i++
			outfile = args[i]
		case strings.HasPrefix(arg, "--outfile="):
			outfile = strings.TrimPrefix(arg, "--outfile=")
		case arg == "--outdir":
			if i+1 >= len(args) {
				fatalErr("aluka build: --outdir requires a path")
			}
			i++
			outdir = args[i]
		case strings.HasPrefix(arg, "--outdir="):
			outdir = strings.TrimPrefix(arg, "--outdir=")
		case arg == "--base":
			if i+1 >= len(args) {
				fatalErr("aluka build: --base requires a path")
			}
			i++
			basePath = args[i]
		case strings.HasPrefix(arg, "--base="):
			basePath = strings.TrimPrefix(arg, "--base=")
		case arg == "--tree-shake":
			treeShake = true
		case arg == "--no-tree-shake":
			treeShake = false
		case arg == "--minify":
			doMinify = true
		case arg == "--sourcemap" || arg == "--target":
			fatalErr("aluka build: " + arg + " not implemented (scope: --compile only)")
		case strings.HasPrefix(arg, "-"):
			fatalErr("aluka build: unknown option " + arg)
		default:
			entries = append(entries, arg)
		}
	}

	if !compileOnly {
		fatalErr("aluka build: M1 supports only --compile (single-file executable); plain bundling is not implemented")
	}
	if len(entries) == 0 {
		fatalErr("aluka build: missing entry file")
	}
	if outdir != "" && outfile != "" {
		fatalErr("aluka build: --outdir and --outfile are mutually exclusive")
	}
	if len(entries) > 1 && outdir == "" {
		fatalErr("aluka build: multiple entries require --outdir")
	}

	vm, err := interpreter.NewVM()
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}
	resolver := module.NewResolver()

	// 基座：--base 指定（跨平台产物 = 目标平台基座 + 同一 payload，
	// 字节码平台无关）；默认当前可执行文件。
	if basePath == "" {
		if exe, err := os.Executable(); err == nil {
			basePath = exe
		} else {
			fatalErr("aluka build: cannot locate base binary: " + err.Error())
		}
	}

	for _, entry := range entries {
		buildOne(vm, resolver, basePath, entry, outfile, outdir, treeShake, doMinify)
	}
}

// buildOne 构建单个入口的产物。
func buildOne(vm *interpreter.VM, resolver *module.Resolver, basePath, entry, outfile, outdir string, treeShake, doMinify bool) {
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

	// 打包数据（tree-shake → minify → pack）。
	modules := graphResult.Modules
	resolutions := graphResult.Resolutions
	assets := graphResult.Assets
	removed := 0
	if treeShake {
		shaken, err := shake.Shake(vm, graphResult, graphResult.Entry)
		if err != nil {
			fatalErr("aluka build: " + err.Error())
		}
		modules = shaken.Modules
		resolutions = shaken.Resolutions
		assets = shaken.Assets
		removed = shaken.Removed
	}
	if doMinify {
		rootDir := graphResult.RootDir
		for i, m := range modules {
			nd, err := minifyModule(vm, rootDir, m)
			if err != nil {
				fatalErr("aluka build: " + err.Error())
			}
			modules[i] = nd
		}
	}

	payload, err := compile.Pack(graphResult.Entry, modules, resolutions, assets)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	// 输出路径：--outfile 或 <entry 基名>（去掉扩展名）；--outdir 下进入目录。
	out := outfile
	if out == "" {
		base := filepath.Base(entry)
		out = strings.TrimSuffix(base, filepath.Ext(base))
		if out == "" {
			out = "app"
		}
		if outdir != "" {
			if err := os.MkdirAll(outdir, 0755); err != nil {
				fatalErr("aluka build: " + err.Error())
			}
			out = filepath.Join(outdir, out)
		}
	}

	payloadOffset, err := writeCompiledBinary(basePath, out, payload)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	size, _ := os.Stat(out)
	shakeNote := ""
	if removed > 0 {
		shakeNote = fmt.Sprintf(", tree-shaken %d modules", removed)
	}
	fmt.Printf("Compiled %s → %s (%d bytes, payload at %d, %d modules%s, base %s)\n",
		entry, out, size.Size(), payloadOffset, len(modules), shakeNote, basePath)
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
