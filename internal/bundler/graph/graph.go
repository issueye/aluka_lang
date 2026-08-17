// Package graph 实现构建期静态模块图（B2.2.1，docs/build-compile-plan.md）。
//
// 从入口出发递归收集静态可达模块（import/export/require/动态 import 字面量），
// 用 Resolver 在构建期完成解析，并记录"父模块 → specifier → 解析路径"的
// 映射（resolutions）：产物运行时不再做文件系统解析，直接按映射加载嵌入的
// 预编译模块（循环依赖/重复引用经 visited 去重）。
package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/astutil"
	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/bundler/plugin"
	"github.com/aluka-lang/aluka/internal/bundler/vue"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// Dep 是一个静态可解析的依赖（specifier + 解析语境）。
type Dep struct {
	Spec      string
	ImportCtx bool // true = import 语境（ESM 导入/动态 import），false = require 语境
	Dynamic   bool // true = 字面量动态 import()
}

// DynamicDep 是一个可拆分为异步 chunk 的动态依赖边。
type DynamicDep struct {
	Source string
	Spec   string
	Target string
}

// BuiltinDep 是构建图中被识别为 Node 内置模块的依赖。
type BuiltinDep struct {
	Spec   string
	Source string
}

// SourceUnitByPath 缓存构建期已读取并解析的模块中间表示；同一模块的
// 编译与依赖收集共享这份前端结果，避免重复 ParseModule。
type SourceUnitByPath map[string]*module.SourceUnit

// Result 是一个模块图构建的产物：前端解析结果 + 构建期解析映射 + JSON 资源。
// 注意：Build 只做解析与依赖收集，不编译。字节码编译由 buildOne 在优化
// （shake/minify）之后统一执行，避免 lower 破坏 SourceUnit 的源 AST。
type Result struct {
	Entry   string
	RootDir string // 入口文件所在目录（绝对路径）：虚拟 key 的源码读取基准
	// Modules 是模块占位列表（无 Module 字节码）：供 optimize 后按序编译
	// 及 analyze 阶段度量。Module 字段为 nil，必须先经 CompileSourceUnit。
	Modules []*compile.EntryData
	// SourceUnits 按虚拟路径保存唯一前端解析结果，供后续 shake/minify 阶段复用。
	SourceUnits SourceUnitByPath
	Resolutions map[string]map[string]string
	Assets      map[string][]byte // JSON 资源：虚拟路径 → 原始字节（M3）
	// Builtins 是源码中静态引用的 node:* 内置模块及其来源。
	Builtins []BuiltinDep
	// DynamicDeps 是字面量动态 import() 的已解析边。
	DynamicDeps []DynamicDep
	// UnresolvedDynamic 无法静态解析的动态 import 所在模块。
	UnresolvedDynamic []string
	// WatchFiles 是本次图中真实存在的源文件（含 Vue src 外部块），供 --watch。
	WatchFiles []string
	plugins    plugin.Host
}

// buildConfig 收集 Build 的可选配置。
type buildConfig struct {
	vueCompiler vue.Compiler
	plugins     plugin.Host
}

// Option 是 Build 的可选配置项。
type Option func(*buildConfig)

// WithVueCompiler 指定 .vue 编译后端（nil 时用默认 subset）。
func WithVueCompiler(c vue.Compiler) Option {
	return func(cfg *buildConfig) { cfg.vueCompiler = c }
}

// WithPlugins 把 Vite 风格 resolveId/load/transform 接到 walk。
func WithPlugins(h plugin.Host) Option {
	return func(cfg *buildConfig) { cfg.plugins = h }
}

// generatedSource 是单次 Build 内的 SFC 虚拟模块，不落盘也不跨构建共享。
type generatedSource struct {
	code string
}

// Build 从入口构建模块图并解析所有模块（不编译）。
// 模块标识使用虚拟路径（相对入口文件所在目录，/ 分隔）：产物运行时的
// __filename/import.meta/错误堆栈均基于虚拟路径，与构建机位置无关。
// vm 参数为兼容保留（旧调用方传 VM；Build 自身不再编译）——official
// SFC 后端例外：它在 vm 上执行 compiler-sfc 依赖链。
func Build(vm *interpreter.VM, resolver *module.Resolver, entry string, opts ...Option) (*Result, error) {
	cfg := buildConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.vueCompiler == nil {
		cfg.vueCompiler = vue.SubsetCompiler{}
	}
	entryAbs, err := filepath.Abs(entry)
	if err != nil {
		return nil, fmt.Errorf("graph: cannot resolve entry %q: %w", entry, err)
	}
	entryDir := filepath.Dir(entryAbs)
	r := &Result{
		Entry:       "",
		RootDir:     entryDir,
		Modules:     make([]*compile.EntryData, 0),
		Resolutions: make(map[string]map[string]string),
		SourceUnits: make(SourceUnitByPath),
		Assets:      make(map[string][]byte),
		WatchFiles:  make([]string, 0),
	}
	r.plugins = cfg.plugins
	visited := make(map[string]bool)
	generated := make(map[string]generatedSource)
	if err := r.walk(vm, resolver, entryAbs, virtualKey(entryDir, entryAbs), entryDir, visited, generated, cfg.vueCompiler); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Result) host() plugin.Host {
	if r.plugins == nil {
		return plugin.Nop{}
	}
	return r.plugins
}

func isVirtualModuleID(id string) bool {
	return strings.HasPrefix(id, "\x00")
}

func pluginModuleKey(id string) string {
	s := strings.TrimPrefix(id, "\x00")
	var b strings.Builder
	b.WriteString("plugin/")
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if !strings.Contains(filepath.Base(out), ".") {
		out += ".js"
	}
	return out
}

// virtualKey 将绝对路径转为相对入口目录的虚拟路径（/ 分隔）。
// 入口本身得到文件名（如 "main.ts"）；依赖得到相对路径
// （如 "src/util.ts"、"node_modules/smallpkg/index.js"）。
func virtualKey(entryDir, absPath string) string {
	rel, err := filepath.Rel(entryDir, absPath)
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	return filepath.ToSlash(rel)
}

func (r *Result) noteWatch(path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	for _, existing := range r.WatchFiles {
		if existing == abs {
			return
		}
	}
	r.WatchFiles = append(r.WatchFiles, abs)
}

// walk 编译一个模块并递归收集其依赖。fsPath 用于文件系统操作，key 是
// 模块标识（虚拟路径），entryDir 是入口目录（虚拟 key 的基准）。
func (r *Result) walk(vm *interpreter.VM, resolver *module.Resolver, fsPath, key, entryDir string, visited map[string]bool, generated map[string]generatedSource, vueBackend vue.Compiler) error {
	if visited[fsPath] {
		return nil
	}
	visited[fsPath] = true
	r.noteWatch(fsPath)

	if loaded, ok, err := r.host().Load(fsPath); err != nil {
		return err
	} else if ok {
		ext := filepath.Ext(fsPath)
		if strings.EqualFold(ext, ".vue") {
			return r.walkVue(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, loaded)
		}
		return r.walkSource(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, loaded, false)
	}

	ext := filepath.Ext(fsPath)
	if source, ok := generated[fsPath]; ok {
		if strings.EqualFold(ext, ".css") {
			transformed, err := r.host().Transform(fsPath, source.code)
			if err != nil {
				return err
			}
			r.Assets[key] = []byte(transformed)
			return nil
		}
		return r.walkSource(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, source.code, true)
	}

	// JSON 与 CSS 文件是资源而非 JS 模块：读取原始字节嵌入（M2/M3）。
	if strings.EqualFold(ext, ".json") || strings.EqualFold(ext, ".css") {
		data, err := os.ReadFile(fsPath)
		if err != nil {
			return fmt.Errorf("graph: cannot read %q: %w", fsPath, err)
		}
		transformed, err := r.host().Transform(fsPath, string(data))
		if err != nil {
			return err
		}
		r.Assets[key] = []byte(transformed)
		return nil
	}

	if strings.EqualFold(ext, ".vue") {
		data, readErr := os.ReadFile(fsPath)
		if readErr != nil {
			return fmt.Errorf("graph: cannot read %q: %w", fsPath, readErr)
		}
		return r.walkVue(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, string(data))
	}

	data, err := os.ReadFile(fsPath)
	if err != nil {
		return fmt.Errorf("graph: cannot read %q: %w", fsPath, err)
	}
	return r.walkSource(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, string(data), false)
}

func (r *Result) walkSource(vm *interpreter.VM, resolver *module.Resolver, fsPath, key, entryDir string, visited map[string]bool, generated map[string]generatedSource, vueBackend vue.Compiler, code string, generatedESM bool) error {
	ext := filepath.Ext(fsPath)
	if ext == "" {
		ext = filepath.Ext(key)
	}
	transformed, err := r.host().Transform(fsPath, code)
	if err != nil {
		return err
	}
	if strings.EqualFold(ext, ".json") || strings.EqualFold(ext, ".css") {
		r.Assets[key] = []byte(transformed)
		return nil
	}
	var unit *module.SourceUnit
	if generatedESM {
		unit, err = module.ParseSourceUnit([]byte(transformed), key, module.ModuleESM)
	} else {
		hint := fsPath
		if isVirtualModuleID(fsPath) {
			hint = key
		}
		unit, err = module.ParseFileSource([]byte(transformed), key, hint)
	}
	if err != nil {
		return err
	}
	return r.finishWalk(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, unit)
}

func (r *Result) walkVue(vm *interpreter.VM, resolver *module.Resolver, fsPath, key, entryDir string, visited map[string]bool, generated map[string]generatedSource, vueBackend vue.Compiler, source string) error {
	compiled, compileErr := vueBackend.Compile(vue.CompileRequest{
		Source:   source,
		Name:     key,
		Filename: fsPath,
		ReadFile: os.ReadFile,
		Resolve: func(spec, from string) (string, error) {
			return resolver.ResolveImport(spec, from)
		},
	})
	if compileErr != nil {
		return fmt.Errorf("graph: %w", compileErr)
	}
	for _, extra := range compiled.ExtraFiles {
		r.noteWatch(extra)
	}
	registerGenerated := func(generatedModule vue.GeneratedModule) error {
		generatedPath := filepath.Join(filepath.Dir(fsPath), filepath.FromSlash(generatedModule.Name))
		if _, exists := generated[generatedPath]; exists {
			return fmt.Errorf("graph: duplicate generated Vue module %q", generatedModule.Name)
		}
		generated[generatedPath] = generatedSource{code: generatedModule.Source}
		return nil
	}
	for _, generatedModule := range compiled.Modules {
		if err := registerGenerated(generatedModule); err != nil {
			return err
		}
	}
	for _, styleMod := range compiled.Styles {
		if err := registerGenerated(styleMod); err != nil {
			return err
		}
	}
	return r.walkSource(vm, resolver, fsPath, key, entryDir, visited, generated, vueBackend, compiled.Facade, true)
}

func (r *Result) finishWalk(vm *interpreter.VM, resolver *module.Resolver, fsPath, key, entryDir string, visited map[string]bool, generated map[string]generatedSource, vueBackend vue.Compiler, unit *module.SourceUnit) error {
	if unit.SourceKind == module.SourceJSON {
		r.Assets[key] = unit.Source
		return nil
	}
	r.SourceUnits[key] = unit
	// 占位 EntryData：Module 留空，由 buildOne 在优化后统一编译。
	r.Modules = append(r.Modules, &compile.EntryData{
		Path:       key,
		ModuleType: compile.ModuleTypeOf(unit.ModuleKind),
		SourceKind: unit.SourceKind,
		ModuleKind: unit.ModuleKind,
	})
	if r.Entry == "" {
		r.Entry = key
	}
	deps := collectDeps(unit.Program, key, &r.UnresolvedDynamic)
	for _, dep := range deps {
		if isBuiltinSpecifier(dep.Spec) {
			r.Builtins = append(r.Builtins, BuiltinDep{Spec: dep.Spec, Source: key})
		}
	}

	// 反射遍历可能重复收集同一调用节点——去重。
	if len(r.UnresolvedDynamic) > 0 {
		seen := make(map[string]bool)
		uniq := r.UnresolvedDynamic[:0]
		for _, k := range r.UnresolvedDynamic {
			if !seen[k] {
				seen[k] = true
				uniq = append(uniq, k)
			}
		}
		r.UnresolvedDynamic = uniq
	}

	// 记录本模块的解析映射并递归。
	var err error
	table := make(map[string]string, len(deps))
	for _, dep := range deps {
		if isBuiltinSpecifier(dep.Spec) {
			continue // 运行时内置模块（node:fs 等），不嵌入。
		}
		var resolved string
		generatedCandidate := filepath.Clean(filepath.Join(filepath.Dir(fsPath), filepath.FromSlash(dep.Spec)))
		if pid, ok, perr := r.host().ResolveId(dep.Spec, fsPath); perr != nil {
			return perr
		} else if ok {
			if pid == "" {
				continue // resolveId(false) → external
			}
			resolved = pid
			err = nil
		} else if _, ok := generated[generatedCandidate]; ok {
			resolved = generatedCandidate
			err = nil
		} else if dep.ImportCtx {
			resolved, err = resolver.ResolveImport(dep.Spec, fsPath)
		} else {
			resolved, err = resolver.Resolve(dep.Spec, fsPath)
		}
		if err != nil {
			// 裸名解析失败可能命中运行时内置（如 require('path')）——
			// 跳过并记录映射为空（运行时仍会先查内置，再查映射）。
			if isBareSpecifier(dep.Spec) {
				continue
			}
			return fmt.Errorf("graph: cannot resolve %q from %q: %w", dep.Spec, key, err)
		}
		var rAbs, depKey string
		if isVirtualModuleID(resolved) {
			rAbs = resolved
			depKey = pluginModuleKey(resolved)
		} else {
			rAbs, err = filepath.Abs(resolved)
			if err != nil {
				return err
			}
			depKey = virtualKey(entryDir, rAbs)
		}
		table[dep.Spec] = depKey
		if dep.Dynamic {
			r.DynamicDeps = append(r.DynamicDeps, DynamicDep{Source: key, Spec: dep.Spec, Target: depKey})
		}
		if err := r.walk(vm, resolver, rAbs, depKey, entryDir, visited, generated, vueBackend); err != nil {
			return err
		}
	}
	if len(table) > 0 {
		r.Resolutions[key] = table
	}
	return nil
}

// collectDeps 遍历 AST 收集静态依赖：
//
//	import x from 'mod' / import 'mod'        → ImportDecl.Source（import 语境）
//	export * from 'mod' / export {a} from 'm' → ExportDecl.Source（import 语境）
//	import('mod')（动态）                      → __import('mod')（import 语境）
//	require('mod')                            → require('mod')（require 语境）
//
// T2-B4：动态 import 的非字面量参数尝试常量折叠（字符串拼接/模板字符串
// 等）；无法折叠的记入 unresolved（模块 key）。
//
// 基于统一遍历 ast.Walk（internal/engine/ast/walk.go），取代原先的反射遍历。
func collectDeps(prog *ast.Program, key string, unresolved *[]string) []Dep {
	var deps []Dep
	ast.Walk(prog, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ImportDecl:
			if node.Source != "" {
				deps = append(deps, Dep{Spec: node.Source, ImportCtx: true})
			}
		case *ast.ExportDecl:
			if node.Source != "" {
				deps = append(deps, Dep{Spec: node.Source, ImportCtx: true})
			}
		case *ast.CallExpr:
			if id, ok := node.Callee.(*ast.Identifier); ok && len(node.Arguments) > 0 {
				arg := node.Arguments[0]
				switch id.Name {
				case "require":
					if lit, ok := arg.(*ast.StringLit); ok {
						deps = append(deps, Dep{Spec: lit.Value, ImportCtx: false})
					}
				case "__import": // 动态 import() 经 parser lower 的形式
					if lit, ok := arg.(*ast.StringLit); ok {
						deps = append(deps, Dep{Spec: lit.Value, ImportCtx: true, Dynamic: true})
						break
					}
					// T2-B4：非字面量 → 常量折叠（字符串拼接/无插值模板等）。
					if v, ok := astutil.FoldConst(arg); ok {
						if s, isStr := v.(string); isStr {
							deps = append(deps, Dep{Spec: s, ImportCtx: true, Dynamic: true})
							break
						}
					}
					// 无法静态解析：构建期警告，产物运行时报错。
					*unresolved = append(*unresolved, key)
				}
			}
		}
		return true
	})
	return deps
}

// isBuiltinSpecifier 判断是否为 node: 或 aluka: 前缀的内置模块。
func isBuiltinSpecifier(spec string) bool {
	return strings.HasPrefix(spec, "node:") || strings.HasPrefix(spec, "aluka:")
}

// isBareSpecifier 判断是否为裸模块名（非相对/绝对路径）。
func isBareSpecifier(spec string) bool {
	if spec == "" {
		return false
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") {
		return false
	}
	if len(spec) >= 2 && spec[1] == ':' {
		return false // Windows 盘符（如 C:\...）。
	}
	return true
}
