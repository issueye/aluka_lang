// `aluka build` 子命令（Phase 7 打包器）。
//
// M1（docs/build-compile-plan.md）支持 --compile 单文件可执行产物：
//
//	aluka build --compile --outfile app ./src/index.ts
//
// 产物 = 当前 aluka 基座 + payload（预编译字节码 + manifest）+ footer。
// 产物在无 aluka/Go 环境的机器上可直接运行（字节码平台无关，见计划 §3）。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// cmdBuild 实现 `aluka build [--compile] [--outfile <path>] <entry>`。
func cmdBuild(args []string) {
	compileOnly := false
	outfile := ""
	var entry string

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
		case strings.HasPrefix(arg, "--outdir"):
			fatalErr("aluka build: --outdir (bundle mode) is not implemented in M1; use --compile --outfile <path>")
		case arg == "--minify" || arg == "--sourcemap" || arg == "--target":
			fatalErr("aluka build: " + arg + " not implemented (M1 scope: --compile only)")
		case strings.HasPrefix(arg, "-"):
			fatalErr("aluka build: unknown option " + arg)
		default:
			if entry != "" {
				fatalErr("aluka build: multiple entry files not supported in M1")
			}
			entry = arg
		}
	}

	if !compileOnly {
		fatalErr("aluka build: M1 supports only --compile (single-file executable); plain bundling is not implemented")
	}
	if entry == "" {
		fatalErr("aluka build: missing entry file")
	}

	// 构建模块图：入口 + 静态可达依赖（import/export/require/动态 import
	// 字面量），编译全部模块并记录构建期解析映射。
	vm, err := interpreter.NewVM()
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}
	graphResult, err := graph.Build(vm, module.NewResolver(), entry)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	// 打包 payload（多模块 + 解析映射 + JSON 资源）。
	payload, err := compile.Pack(graphResult.Entry, graphResult.Modules, graphResult.Resolutions, graphResult.Assets)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	// 输出路径：--outfile 或 <entry 基名>（去掉扩展名）。
	if outfile == "" {
		base := filepath.Base(entry)
		outfile = strings.TrimSuffix(base, filepath.Ext(base))
		if outfile == "" {
			outfile = "app"
		}
	}

	// 复制基座 + 追加 payload + footer。
	base, err := os.Executable()
	if err != nil {
		fatalErr("aluka build: cannot locate base binary: " + err.Error())
	}
	payloadOffset, err := writeCompiledBinary(base, outfile, payload)
	if err != nil {
		fatalErr("aluka build: " + err.Error())
	}

	size, _ := os.Stat(outfile)
	fmt.Printf("Compiled %s → %s (%d bytes, payload at %d, %d modules)\n",
		entry, outfile, size.Size(), payloadOffset, len(graphResult.Modules))
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
