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
// 编译管线与 loader 完全一致：解析 → 模块类型判定（无 import/export 且非
// .mjs 按 CJS）→ ESM 转换 + 包装（或 CJS 包装）→ 编译为字节码 Module。
// 产物在执行前已完成转换/包装，运行时零开销（docs/build-compile-plan.md §3.2）。
func CompileFile(vm *interpreter.VM, path string) (*EntryData, error) {
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

	// 模块类型判定（与 loader.loadESMModule 一致）：.mjs 强制 ESM；含顶层
	// await（TLA）按 ESM；无 import/export 声明的其他文件按 CJS。
	if !module.HasESMDecls(prog) && !ast.HasTopLevelAwait(prog) && filepath.Ext(absPath) != ".mjs" {
		wrapped := module.WrapCJSSource(string(src))
		mod, err := vm.Compile(wrapped, absPath)
		if err != nil {
			return nil, fmt.Errorf("compile: %q: %w", absPath, err)
		}
		return &EntryData{Path: absPath, ModuleType: ModuleTypeCJS, Module: mod}, nil
	}

	transformed := module.TransformESMToCJS(prog, absPath)
	prog2 := module.WrapESMAST(transformed, absPath)
	mod, err := vm.CompileAST(prog2, absPath)
	if err != nil {
		return nil, fmt.Errorf("compile: %q: %w", absPath, err)
	}
	return &EntryData{Path: absPath, ModuleType: ModuleTypeESM, Module: mod}, nil
}
