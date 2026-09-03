package compile

import (
	"fmt"
	"sort"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

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
		// 显式 CJS（.cjs/.cts 或 package type commonjs）中出现 ESM-only 语法
		// 属于源文件错误：CJS wrapper 无法承载 import/export，给明确诊断而非
		// 含糊的 "unsupported statement"。
		if module.HasESMDecls(unit.Program) {
			return nil, fmt.Errorf("compile: %q: module type is commonjs but source contains ESM import/export syntax", unit.Path)
		}
		// CJS 字符串 wrapper（P2-3 决策）：仅消费 Source 原始字节，不改 AST，
		// 同一 SourceUnit 可幂等重复编译。语义契约：wrapper 形参
		// (require, module, exports, __filename, __dirname, __import)。
		wrapped := module.WrapCJSSource(string(unit.Source))
		mod, err := vm.Compile(wrapped, unit.Path)
		if err != nil {
			return nil, fmt.Errorf("compile: %q: %w", unit.Path, err)
		}
		return &EntryData{Path: unit.Path, ModuleType: ModuleTypeCJS, SourceKind: unit.SourceKind, ModuleKind: unit.ModuleKind, Stage: unit.Stage | module.StageWrapped, Module: mod}, nil
	}
	// ESM lower（P2-2 纯化）：TransformESMToCJS 会原地改写导入引用，先深拷贝
	// 保证调用方持有的 SourceUnit AST 不被破坏——同一 unit 可被度量/优化/最终
	// 编译多次，均安全幂等。
	prog := ast.DeepCopy(unit.Program)
	transformed := module.TransformESMToCJS(prog, unit.Path)
	wrappedProg := module.WrapESMAST(transformed, unit.Path)
	mod, err := vm.CompileAST(wrappedProg, unit.Path)
	if err != nil {
		return nil, fmt.Errorf("compile: %q: %w", unit.Path, err)
	}
	return &EntryData{Path: unit.Path, ModuleType: ModuleTypeESM, SourceKind: unit.SourceKind, ModuleKind: unit.ModuleKind, Stage: unit.Stage | module.StageESMLowered | module.StageWrapped, Module: mod}, nil
}

// ParseFileUnit 委托 module.ParseFileUnit（统一前端：读取/分类/解析/隐式提升）。
func ParseFileUnit(path, key string) (*module.SourceUnit, error) {
	return module.ParseFileUnit(path, key)
}

// CompileUnits 编译一批 SourceUnit（确定性按键排序），返回对应 EntryData。
// 调用方应保证每个 unit 已处于最终优化状态（shake/minify 已完成）。
func CompileUnits(vm *interpreter.VM, units map[string]*module.SourceUnit) ([]*EntryData, error) {
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*EntryData, 0, len(keys))
	for _, key := range keys {
		nd, err := CompileSourceUnit(vm, units[key])
		if err != nil {
			return nil, err
		}
		out = append(out, nd)
	}
	return out, nil
}

// CompileUnitsForMeasure 为每个 SourceUnit 先深拷贝 AST 再编译，返回不破坏
// 原始 AST 的度量用 EntryData（raw stage）。避免度量编译把 ESM lower 写进
// 后续优化阶段的共享 AST。
func CompileUnitsForMeasure(vm *interpreter.VM, units map[string]*module.SourceUnit) ([]*EntryData, error) {
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*EntryData, 0, len(keys))
	for _, key := range keys {
		u := *units[key]
		if u.Program != nil {
			u.Program = ast.DeepCopy(u.Program)
		}
		nd, err := CompileSourceUnit(vm, &u)
		if err != nil {
			return nil, err
		}
		out = append(out, nd)
	}
	return out, nil
}

// CompileFile 将单个源文件编译为可打包的 EntryData。
// 先构造 SourceUnit，再交给 CompileSourceUnit，保证源码只解析一次。
func CompileFile(vm *interpreter.VM, path, key string) (*EntryData, error) {
	unit, err := ParseFileUnit(path, key)
	if err != nil {
		return nil, err
	}
	return CompileSourceUnit(vm, unit)
}
