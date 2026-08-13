package module

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// compileUnit 把前端解析结果（SourceUnit）编译为字节码（经磁盘缓存），不执行。
// ESM 走 clone→lower→AST wrap；CJS 走字符串 wrapper（P2-3 决策）。
func (l *Loader) compileUnit(unit *SourceUnit, fsPath string) (*bytecode.Module, error) {
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return nil, fmt.Errorf("module: bytecode compilation requires the bytecode VM engine")
	}
	if unit.ModuleKind == ModuleESM {
		// lower 是原地变换：先深拷贝，保证 unit 可被后续阶段/多次编译复用。
		prog := ast.DeepCopy(unit.Program)
		transformed := TransformESMToCJS(prog, unit.Path)
		wrapped := WrapESMAST(transformed, unit.Path)
		return l.bcCache.compileOrLoad(fsPath, unit.SourceKind, unit.ModuleKind, func() (*bytecode.Module, error) {
			return vm.CompileAST(wrapped, unit.Path)
		})
	}
	if HasESMDecls(unit.Program) {
		return nil, fmt.Errorf("module: %q: module type is commonjs but source contains ESM import/export syntax", unit.Path)
	}
	wrapped := WrapCJSSource(string(unit.Source))
	return l.bcCache.compileOrLoad(fsPath, unit.SourceKind, unit.ModuleKind, func() (*bytecode.Module, error) {
		return vm.Compile(wrapped, unit.Path)
	})
}

// runUnit 编译并执行一个 SourceUnit（run/require 统一出口）。
func (l *Loader) runUnit(unit *SourceUnit, fsPath string) (engine.Value, error) {
	mod, err := l.compileUnit(unit, fsPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", unit.Path, err)
	}
	return l.RunPrecompiled(unit.Path, mod, unit.ModuleKind == ModuleESM)
}

// loadModuleFile 是文件模式与 require 模式统一的模块加载入口：
// 一次读取/分类/解析（ParseFileUnit），编译（缓存）后执行。
// 非 VM 引擎（AST 解释器）的 CJS 回退到旧全局方案（P0 兼容）。
func (l *Loader) loadModuleFile(absPath string) (engine.Value, error) {
	unit, err := ParseFileUnit(absPath, absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: %w", err)
	}
	if _, ok := l.ctx.(*interpreter.VM); !ok && unit.ModuleKind == ModuleCommonJS {
		exports := l.newExports()
		moduleObj := engine.NewObject()
		_ = moduleObj.Set("exports", exports)
		return l.loadCJSViaGlobals(absPath, string(unit.Source), moduleObj, exports)
	}
	return l.runUnit(unit, absPath)
}
