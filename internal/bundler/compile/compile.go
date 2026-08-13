package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// stripBOM 剥离开头的 UTF-8 BOM（与 loader 一致，避免 lexer 死循环）。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}

// CompileSourceUnit 编译已完成前端解析的 SourceUnit。它不会再次读取或解析源码。
func CompileSourceUnit(vm *interpreter.VM, unit *module.SourceUnit) (*EntryData, error) {
	if unit == nil {
		return nil, fmt.Errorf("compile: nil source unit")
	}
	if unit.SourceKind == module.SourceJSON {
		return nil, fmt.Errorf("compile: JSON source unit is an asset, not a bytecode module")
	}
	if unit.Program == nil {
		return nil, fmt.Errorf("compile: source unit %q has no AST", unit.Path)
	}
	isESM := unit.ModuleKind == module.ModuleESM
	if !isESM {
		wrapped := module.WrapCJSSource(string(unit.Source))
		mod, err := vm.Compile(wrapped, unit.Path)
		if err != nil {
			return nil, fmt.Errorf("compile: %q: %w", unit.Path, err)
		}
		return &EntryData{Path: unit.Path, ModuleType: ModuleTypeCJS, SourceKind: unit.SourceKind, Stage: unit.Stage | module.StageWrapped, Module: mod}, nil
	}
	transformed := module.TransformESMToCJS(unit.Program, unit.Path)
	prog := module.WrapESMAST(transformed, unit.Path)
	mod, err := vm.CompileAST(prog, unit.Path)
	if err != nil {
		return nil, fmt.Errorf("compile: %q: %w", unit.Path, err)
	}
	return &EntryData{Path: unit.Path, ModuleType: ModuleTypeESM, SourceKind: unit.SourceKind, Stage: unit.Stage | module.StageESMLowered | module.StageWrapped, Module: mod}, nil
}

// ParseFileUnit 读取并解析一个源码文件，不执行模块 lower 或 bytecode 编译。
func ParseFileUnit(path, key string) (*module.SourceUnit, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("compile: cannot resolve path %q: %w", path, err)
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("compile: cannot read %q: %w", absPath, err)
	}
	resolver := module.NewResolver()
	moduleKind := resolver.SourceModuleKind(absPath)
	unit, err := module.ParseSourceUnit(src, key, moduleKind)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	// 隐式 .js/.ts 延续 package-type 兼容：仅在这里做一次语法提升。
	ext := strings.ToLower(filepath.Ext(absPath))
	if (ext == ".ts" || ext == ".js") && moduleKind == module.ModuleCommonJS && module.HasESMDecls(unit.Program) {
		unit.ModuleKind = module.ModuleESM
	}
	return unit, nil
}

// CompileFile 将单个源文件编译为可打包的 EntryData。
// 新路径先构造 SourceUnit，再交给 CompileSourceUnit，保证源码只解析一次。
func CompileFile(vm *interpreter.VM, path, key string) (*EntryData, error) {
	unit, err := ParseFileUnit(path, key)
	if err != nil {
		return nil, err
	}
	return CompileSourceUnit(vm, unit)
}

// CompileProgram 从已解析的 AST 编译（兼容旧调用方）。
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
