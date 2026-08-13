// Package shake 实现构建期 tree-shaking（T2-B1，
// docs/test-bundle-optimize-plan.md §5.2）：
//
//   - 模块级剪除：无副作用且导出未被使用的 ESM 模块从产物中移除。
//   - 导出级剪枝：保留模块的未使用导出声明删除（AST 变换后重新编译）。
//   - re-export 剪枝：`export {x} from 'm'` / `export * from 'm'`
//     中未使用的名字不传播，语句删除。
//
// CJS 模块与有副作用模块保守保留；kept 模块的依赖链（Resolutions 指向）
// 全部保留（CJS require 依赖不在 ESM 导出传播体系内，必须全量保留）。
package shake

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/bundler/astutil"
	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// Result 是 tree-shaking 后的打包数据（模块子集 + 剪枝后的解析映射）。
type Result struct {
	RootDir     string // 入口目录（虚拟路径源码读取基准）
	Modules     []*compile.EntryData
	Resolutions map[string]map[string]string
	Assets      map[string][]byte
	Removed     int // 剪除的模块数
}

// moduleInfo 是单个模块的静态分析结果。
type moduleInfo struct {
	key         string
	prog        *ast.Program // ESM 模块原始 AST；CJS 为 nil
	isEntry     bool
	exports     map[string]bool // 本地导出名（含 default）
	imports     []importInfo
	reExports   []*ast.ExportDecl
	sideEffect  bool            // 顶层副作用
	deps        map[string]bool // 依赖模块 key（Resolutions 指向）
	transformed bool            // AST 已剪枝，需要重新编译
}

// importInfo 是一条 import 语句的静态信息。
type importInfo struct {
	Spec       string
	Specifiers []ast.ImportSpecifier
}

// Shake 对模块图执行 tree-shaking，返回剪枝后的打包数据。
// vm 用于重新编译剪枝后的模块；entry 为入口虚拟路径（不剪枝）。
//
// 传播模型（导入使用分析）：入口/保留模块的 import 按代码引用分析——
// 引用的导入把目标模块的对应导出标记 used 并保留目标；未引用的导入
// 在剪枝时删除（目标无副作用时目标可剪除）；side-effect import 与
// 有副作用的目标模块始终保留（ESM 语义：import 语句执行目标模块）。
func Shake(vm *interpreter.VM, gr *graph.Result, entry string) (*Result, error) {
	// 解析全部模块源码并做静态分析。
	infos := make(map[string]*moduleInfo)
	for _, m := range gr.Modules {
		if m.ModuleType != compile.ModuleTypeESM {
			// CJS 保守保留（不分析、不剪除；依赖全量保留）。
			infos[m.Path] = &moduleInfo{key: m.Path, deps: depKeys(gr, m.Path)}
			continue
		}
		unit := gr.SourceUnits[m.Path]
		if unit == nil || unit.Program == nil {
			return nil, fmt.Errorf("shake: source unit missing for %q", m.Path)
		}
		if unit.Stage&module.StageWrapped != 0 {
			// 已进入 lower/wrap 的 artifact 不再按源 ESM AST 分析，避免把
			// lowered CJS 形态与原始 ESM 语义混用。
			infos[m.Path] = &moduleInfo{key: m.Path, deps: depKeys(gr, m.Path), sideEffect: true}
			continue
		}
		infos[m.Path] = analyze(m.Path, unit.Program, gr)
	}

	keep := map[string]bool{entry: true}
	usedExports := make(map[string]map[string]bool)
	queue := []string{entry}
	processed := make(map[string]bool)

	keepMod := func(key string) {
		if !keep[key] {
			keep[key] = true
			queue = append(queue, key)
		}
	}
	markUsed := func(key, name string) {
		if usedExports[key] == nil {
			usedExports[key] = make(map[string]bool)
		}
		if !usedExports[key][name] {
			usedExports[key][name] = true
			// 新使用标记写入已处理完的模块时重新入队：re-export 传播依赖
			// 目标模块被处理时 usedExports 已完整（队列顺序不能保证——
			// 后入队的导入者可能晚于目标模块出队）。不动点迭代保证
			// re-export 链（export {x} from 'm'）的目标被保留。
			if processed[key] {
				processed[key] = false
				queue = append(queue, key)
			}
		}
	}
	// markImportUsed 把导入名映射到目标模块的导出名并保留目标。
	markImportUsed := func(t, imported string) {
		switch imported {
		case "*":
			// Namespace imports require the complete export surface. Keep a
			// wildcard marker even when this module is a pure export-* barrel
			// and therefore has no directly declared exports.
			markUsed(t, "*")
			if tin := infos[t]; tin != nil && tin.prog != nil {
				for name := range tin.exports {
					markUsed(t, name)
				}
			}
		case "":
			markUsed(t, "default")
		default:
			markUsed(t, imported)
		}
	}

	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if processed[key] {
			continue
		}
		processed[key] = true
		info := infos[key]
		if info == nil {
			continue
		}
		if info.prog == nil {
			// CJS：依赖模块的导出全量视为使用（require 的使用不可静态
			// 分析——CJS 侧可能访问任何导出），目标保留并全导出传播。
			for dep := range info.deps {
				if tin := infos[dep]; tin != nil && tin.prog != nil {
					for name := range tin.exports {
						markUsed(dep, name)
					}
				}
				keepMod(dep)
			}
			continue
		}
		refs := astutil.CollectRefs(info.prog)
		handled := make(map[string]bool)
		for _, imp := range info.imports {
			t := resolveTarget(gr, key, imp.Spec)
			if t == "" {
				continue
			}
			handled[t] = true
			if len(imp.Specifiers) == 0 {
				keepMod(t) // side-effect import：目标执行
				continue
			}
			for _, spec := range imp.Specifiers {
				if refs[spec.Local] > 0 {
					markImportUsed(t, spec.Imported)
					keepMod(t)
				} else if tin := infos[t]; tin != nil && tin.sideEffect {
					// 名字未用但目标有副作用：import 语句仍执行目标。
					keepMod(t)
				}
			}
		}
		// re-export 传播。
		for _, re := range info.reExports {
			t := resolveTarget(gr, key, re.Source)
			if t == "" {
				continue
			}
			handled[t] = true
			if re.IsStar && re.StarName != "" {
				// export * as ns：ns 被使用时，目标模块的完整命名空间
				// 都可观察，必须保留其全部导出。
				if usedExports[key]["*"] || usedExports[key][re.StarName] {
					keepMod(t)
					markUsed(t, "*")
					if tin := infos[t]; tin != nil && tin.prog != nil {
						for name := range tin.exports {
							markUsed(t, name)
						}
					}
				}
			} else if re.IsStar {
				// export *：运行时 Object.assign(module.exports, require())
				// 需加载目标；本模块的 used 导出转发到目标同名导出。
				keepMod(t)
				if usedExports[key]["*"] {
					markUsed(t, "*")
					if tin := infos[t]; tin != nil && tin.prog != nil {
						for name := range tin.exports {
							markUsed(t, name)
						}
					}
				} else {
					for name := range usedExports[key] {
						if name != "default" {
							markUsed(t, name)
						}
					}
				}
			} else {
				for _, spec := range re.Specifiers {
					if usedExports[key]["*"] || usedExports[key][spec.Exported] {
						markUsed(t, spec.Local)
						keepMod(t)
					}
				}
			}
		}
		// 兜底：require()/动态 import 的依赖不在 ImportDecl/ExportDecl 分析
		// 内（使用不可静态分析）→ 目标保留并全导出传播。
		for dep := range info.deps {
			if handled[dep] {
				continue
			}
			if tin := infos[dep]; tin != nil && tin.prog != nil {
				for name := range tin.exports {
					markUsed(dep, name)
				}
			}
			keepMod(dep)
		}
	}

	// 剪枝 + 收集结果。
	res := &Result{
		RootDir:     gr.RootDir,
		Resolutions: make(map[string]map[string]string, len(gr.Resolutions)),
		Assets:      gr.Assets,
	}
	shaken := make(map[string]*moduleInfo)
	for _, m := range gr.Modules {
		info := infos[m.Path]
		if !keep[m.Path] {
			res.Removed++
			continue
		}
		shaken[m.Path] = info
		// 剪枝 AST（仅 ESM 非入口模块）：未用导入删除 + 未用导出删除。
		if info != nil && info.prog != nil && !info.isEntry {
			if pruneModule(info, usedExports, infos, gr) {
				info.transformed = true
			}
		}
		// 解析映射：保留 kept 模块的条目（指向已剪除模块的映射无害——
		// 产物运行时只查 kept 模块实际执行的 require/import）。
		if table, ok := gr.Resolutions[m.Path]; ok {
			res.Resolutions[m.Path] = table
		}
	}

	// 重新编译剪枝后的模块（顺序与原始一致）。
	for _, m := range gr.Modules {
		if !keep[m.Path] {
			continue
		}
		info := shaken[m.Path]
		if info == nil || info.prog == nil || !info.transformed {
			res.Modules = append(res.Modules, m)
			continue
		}
		unit := gr.SourceUnits[m.Path]
		if unit == nil {
			return nil, fmt.Errorf("shake: source unit missing for %q", m.Path)
		}
		unit.Program = info.prog
		unit.Stage |= module.StageShaken
		nd, err := compile.CompileSourceUnit(vm, unit)
		if err != nil {
			return nil, fmt.Errorf("shake: recompile %q: %w", m.Path, err)
		}
		nd.Stage |= module.StageShaken
		res.Modules = append(res.Modules, nd)
	}
	return res, nil
}

// analyze 解析模块 AST 并提取导出/导入/副作用信息。
func analyze(key string, prog *ast.Program, gr *graph.Result) *moduleInfo {
	info := &moduleInfo{
		key:     key,
		prog:    prog,
		exports: make(map[string]bool),
		deps:    depKeys(gr, key),
	}
	for _, stmt := range prog.Body {
		switch n := stmt.(type) {
		case *ast.ImportDecl:
			info.imports = append(info.imports, importInfo{Spec: n.Source, Specifiers: n.Specifiers})
		case *ast.ExportDecl:
			if n.Declaration != nil {
				for _, name := range declNames(n.Declaration) {
					info.exports[name] = true
				}
				if stmtHasSideEffects(n.Declaration) {
					info.sideEffect = true
				}
			} else if n.Source != "" {
				info.reExports = append(info.reExports, n)
				if n.IsStar && n.StarName != "" {
					info.exports[n.StarName] = true
				}
			} else {
				for _, spec := range n.Specifiers {
					info.exports[spec.Exported] = true
				}
			}
		case *ast.ExportDefaultDecl:
			info.exports["default"] = true
			if n.Expression != nil && astutil.HasSideEffects(n.Expression) {
				info.sideEffect = true
			}
		default:
			if stmtHasSideEffects(stmt) {
				info.sideEffect = true
			}
		}
	}
	return info
}

// pruneModule 剪除未使用的导入（specifier 与语句）、导出声明与 re-export。
// 返回 true 表示 AST 有变换（需要重新编译）。
func pruneModule(info *moduleInfo, usedExports map[string]map[string]bool, infos map[string]*moduleInfo, gr *graph.Result) bool {
	used := usedExports[info.key]
	refs := astutil.CollectRefs(info.prog)
	changed := false
	var newBody []ast.Statement
	for _, stmt := range info.prog.Body {
		switch n := stmt.(type) {
		case *ast.ImportDecl:
			// 未在代码中引用的导入名删除；目标无副作用时可整句删除，
			// 否则保留为 side-effect import（import 语句执行目标模块）。
			target := resolveTarget(gr, info.key, n.Source)
			var specs []ast.ImportSpecifier
			for _, spec := range n.Specifiers {
				if refs[spec.Local] > 0 {
					specs = append(specs, spec)
				}
			}
			if len(specs) == len(n.Specifiers) {
				newBody = append(newBody, stmt)
				continue
			}
			changed = true
			if len(specs) > 0 {
				n.Specifiers = specs
				newBody = append(newBody, stmt)
				continue
			}
			// 全未用：目标有副作用 → 保留 side-effect 形态；否则删除。
			tin := infos[target]
			if target != "" && tin != nil && tin.sideEffect {
				n.Specifiers = nil
				newBody = append(newBody, stmt)
			}
		case *ast.ExportDecl:
			if n.Declaration != nil {
				names := declNames(n.Declaration)
				allUsed := true
				for _, name := range names {
					if !used["*"] && !used[name] {
						allUsed = false
						break
					}
				}
				if allUsed {
					newBody = append(newBody, stmt)
					continue
				}
				// 未全用：模块内引用的名字保留声明（去 export 包装），
				// 未被引用且无副作用的声明整体删除。
				changed = true
				refs := astutil.CollectRefs(stmt)
				keepDecl := false
				for _, name := range names {
					if !used[name] && refs[name] > 0 {
						keepDecl = true
					}
				}
				if keepDecl || stmtHasSideEffects(n.Declaration) {
					newBody = append(newBody, n.Declaration)
				}
				continue
			}
			if n.Source != "" {
				// re-export：名字未使用 → 删除语句。
				if n.IsStar && n.StarName != "" {
					if !used["*"] && !used[n.StarName] {
						changed = true
						continue
					}
					newBody = append(newBody, stmt)
					continue
				}
				if n.IsStar {
					// export *：本模块 used 名非空则保留（运行时
					// Object.assign 传播全部导出，需加载目标模块）。
					hasUsed := false
					for name := range used {
						if name != "default" {
							hasUsed = true
							break
						}
					}
					if !hasUsed {
						changed = true
						continue
					}
					newBody = append(newBody, stmt)
					continue
				}
				kept := false
				for _, spec := range n.Specifiers {
					if used["*"] || used[spec.Exported] {
						kept = true
						break
					}
				}
				if !kept {
					changed = true
					continue
				}
				newBody = append(newBody, stmt)
				continue
			}
			// export {a, b as c}：过滤未使用的 specifier。
			var specs []ast.ExportSpecifier
			for _, spec := range n.Specifiers {
				if used["*"] || used[spec.Exported] {
					specs = append(specs, spec)
				}
			}
			if len(specs) == 0 {
				changed = true
				continue
			}
			if len(specs) != len(n.Specifiers) {
				changed = true
				n.Specifiers = specs
			}
			newBody = append(newBody, stmt)
		case *ast.ExportDefaultDecl:
			if used["*"] || used["default"] {
				newBody = append(newBody, stmt)
			} else {
				changed = true
			}
		default:
			newBody = append(newBody, stmt)
		}
	}
	info.prog.Body = newBody
	return changed
}

// depKeys 返回模块的依赖 key 集合（Resolutions 指向）。
func depKeys(gr *graph.Result, key string) map[string]bool {
	table := gr.Resolutions[key]
	deps := make(map[string]bool, len(table))
	for _, target := range table {
		deps[target] = true
	}
	return deps
}

// resolveTarget 按 Resolutions 映射解析 specifier 的目标模块 key。
func resolveTarget(gr *graph.Result, from, spec string) string {
	table := gr.Resolutions[from]
	if table == nil {
		return ""
	}
	return table[spec]
}

// declNames 返回导出声明中的名字（var/function/class/let/const）。
func declNames(d ast.Statement) []string {
	switch n := d.(type) {
	case *ast.VarDecl:
		var names []string
		for _, decl := range n.Decls {
			if decl.Name != nil {
				names = append(names, decl.Name.Name)
			}
		}
		return names
	case *ast.FunctionDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	case *ast.ClassDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	}
	return nil
}

// stmtHasSideEffects 语句级副作用判定（仅顶层分析用）。
func stmtHasSideEffects(s ast.Statement) bool {
	switch n := s.(type) {
	case *ast.ExprStmt:
		return astutil.HasSideEffects(n.Expr)
	case *ast.VarDecl:
		for _, d := range n.Decls {
			if d.Init != nil && astutil.HasSideEffects(d.Init) {
				return true
			}
		}
		return false
	case *ast.FunctionDecl:
		return false
	case *ast.ClassDecl:
		return n.SuperClass != nil && astutil.HasSideEffects(n.SuperClass)
	case *ast.IfStmt:
		if astutil.HasSideEffects(n.Test) {
			return true
		}
		if stmtHasSideEffects(n.Consequent) {
			return true
		}
		return n.Alternate != nil && stmtHasSideEffects(n.Alternate)
	case *ast.BlockStmt:
		for _, b := range n.Body {
			if stmtHasSideEffects(b) {
				return true
			}
		}
		return false
	case *ast.ImportDecl, *ast.ExportDecl:
		// import/export 声明：转换后生成 require（目标模块由 kept 传播
		// 保证），本身不算副作用。
		return false
	case *ast.ExportDefaultDecl:
		return false
	case *ast.ReturnStmt, *ast.ThrowStmt, *ast.ForStmt, *ast.WhileStmt,
		*ast.DoWhileStmt, *ast.SwitchStmt, *ast.TryStmt, *ast.LabeledStmt,
		*ast.ForInStmt, *ast.ForOfStmt:
		return true // 保守
	default:
		return true
	}
}

func findSourcePath(gr *graph.Result, key string) string {
	for path, unit := range gr.SourceUnits {
		if unit != nil && unit.Path == key {
			return path
		}
	}
	return ""
}

// stripBOM 保留给 shake 后重编译的兼容路径使用。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}
