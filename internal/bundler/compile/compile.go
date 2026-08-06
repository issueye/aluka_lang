package compile

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// stripBOM 剥离开头的 UTF-8 BOM（与 loader 一致，避免 lexer 死循环）。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}

// CompileFile 将单个源文件编译为可打包的 EntryData（M1：单模块产物）。
//
// path 是文件系统路径（用于读取源码）；key 是模块标识（写入字节码
// SourceFile 与 EntryData.Path，M3 起为相对入口的虚拟路径，产物运行时的
// __filename/import.meta/错误堆栈均基于它）。
//
// 编译管线与 loader 完全一致：解析 → 模块类型判定（无 import/export 且非
// .mjs 按 CJS）→ ESM 转换 + 包装（或 CJS 包装）→ 编译为字节码 Module。
// 产物在执行前已完成转换/包装，运行时零开销（docs/build-compile-plan.md §3.2）。
func CompileFile(vm *interpreter.VM, path, key string) (*EntryData, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("compile: cannot resolve path %q: %w", path, err)
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("compile: cannot read %q: %w", absPath, err)
	}
	src = stripBOM(src)

	prog, err := parser.ParseModule(string(src))
	if err != nil {
		return nil, fmt.Errorf("compile: parse error in %q: %w", absPath, err)
	}
	return CompileProgram(vm, prog, src, key)
}

// CompileProgram 从已解析的 AST 编译（tree-shaking/minify 变换后的模块
// 复用同一编译管线）。src 为原始源码（CJS 包装需要）；key 为模块标识。
// 模块类型自动判定（与 CompileFile 一致）。
func CompileProgram(vm *interpreter.VM, prog *ast.Program, src []byte, key string) (*EntryData, error) {
	return CompileProgramType(vm, prog, src, key, module.HasESMDecls(prog) || ast.HasTopLevelAwait(prog) || filepath.Ext(key) == ".mjs")
}

// CompileProgramType 按显式模块类型编译。isESM=true 走 ESM 转换路径；
// false 走 CJS 包装（仅 CompileFile 自动判定时使用——tree-shaking/minify
// 变换后的模块可能失去 import/export 声明，必须显式传入原始模块类型，
// 否则误判 CJS 会用原始源码包装而丢失变换）。
func CompileProgramType(vm *interpreter.VM, prog *ast.Program, src []byte, key string, isESM bool) (*EntryData, error) {
	if !isESM {
		wrapped := module.WrapCJSSource(string(src))
		mod, err := vm.Compile(wrapped, key)
		if err != nil {
			return nil, fmt.Errorf("compile: %q: %w", key, err)
		}
		return &EntryData{Path: key, ModuleType: ModuleTypeCJS, Module: mod}, nil
	}

	transformed := module.TransformESMToCJS(prog, key)
	prog2 := module.WrapESMAST(transformed, key)
	mod, err := vm.CompileAST(prog2, key)
	if err != nil {
		return nil, fmt.Errorf("compile: %q: %w", key, err)
	}
	return &EntryData{Path: key, ModuleType: ModuleTypeESM, Module: mod}, nil
}
