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
	"reflect"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// Dep 是一个静态可解析的依赖（specifier + 解析语境）。
type Dep struct {
	Spec      string
	ImportCtx bool // true = import 语境（ESM 导入/动态 import），false = require 语境
}

// Result 是模块图构建的产物：编译后的模块列表 + 构建期解析映射 + JSON 资源。
type Result struct {
	Entry       string
	Modules     []*compile.EntryData
	Resolutions map[string]map[string]string // 父模块虚拟路径 → specifier → 目标模块虚拟路径
	Assets      map[string][]byte            // JSON 资源：虚拟路径 → 原始字节（M3）
}

// Build 从入口构建模块图并编译所有模块。
// 模块标识使用虚拟路径（相对入口文件所在目录，/ 分隔）：产物运行时的
// __filename/import.meta/错误堆栈均基于虚拟路径，与构建机位置无关。
func Build(vm *interpreter.VM, resolver *module.Resolver, entry string) (*Result, error) {
	entryAbs, err := filepath.Abs(entry)
	if err != nil {
		return nil, fmt.Errorf("graph: cannot resolve entry %q: %w", entry, err)
	}
	entryDir := filepath.Dir(entryAbs)
	r := &Result{
		Entry:       "",
		Modules:     make([]*compile.EntryData, 0),
		Resolutions: make(map[string]map[string]string),
		Assets:      make(map[string][]byte),
	}
	visited := make(map[string]bool)
	if err := r.walk(vm, resolver, entryAbs, virtualKey(entryDir, entryAbs), entryDir, visited); err != nil {
		return nil, err
	}
	return r, nil
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

// walk 编译一个模块并递归收集其依赖。fsPath 用于文件系统操作，key 是
// 模块标识（虚拟路径），entryDir 是入口目录（虚拟 key 的基准）。
func (r *Result) walk(vm *interpreter.VM, resolver *module.Resolver, fsPath, key, entryDir string, visited map[string]bool) error {
	if visited[fsPath] {
		return nil
	}
	visited[fsPath] = true

	// JSON 文件是资源而非模块：读取原始字节嵌入（M3，B2.3.4）。
	if strings.EqualFold(filepath.Ext(fsPath), ".json") {
		data, err := os.ReadFile(fsPath)
		if err != nil {
			return fmt.Errorf("graph: cannot read %q: %w", fsPath, err)
		}
		r.Assets[key] = data
		return nil
	}

	entryData, err := compile.CompileFile(vm, fsPath, key)
	if err != nil {
		return err
	}
	r.Modules = append(r.Modules, entryData)
	if r.Entry == "" {
		r.Entry = key
	}

	// 解析源码收集静态依赖（CompileFile 内部已 parse，此处重新 parse 收集
	// specifier；M2 规模下开销可接受，后续可合并为一次 parse）。
	src, err := os.ReadFile(fsPath)
	if err != nil {
		return fmt.Errorf("graph: cannot read %q: %w", fsPath, err)
	}
	prog, err := parser.ParseModule(string(stripBOM(src)))
	if err != nil {
		return fmt.Errorf("graph: parse error in %q: %w", fsPath, err)
	}
	deps := collectDeps(prog)

	// 记录本模块的解析映射并递归。
	table := make(map[string]string, len(deps))
	for _, dep := range deps {
		if isBuiltinSpecifier(dep.Spec) {
			continue // 运行时内置模块（node:fs 等），不嵌入。
		}
		var resolved string
		if dep.ImportCtx {
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
		rAbs, err := filepath.Abs(resolved)
		if err != nil {
			return err
		}
		depKey := virtualKey(entryDir, rAbs)
		table[dep.Spec] = depKey
		if err := r.walk(vm, resolver, rAbs, depKey, entryDir, visited); err != nil {
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
func collectDeps(prog *ast.Program) []Dep {
	var deps []Dep
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collectNode(v.Elem().Interface(), &deps, walk)
			walk(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			collectNode(v.Interface(), &deps, walk)
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		}
	}
	walk(reflect.ValueOf(prog))
	return deps
}

// collectNode 在节点类型层面收集依赖（在字段遍历前处理，避免重复收集）。
func collectNode(n interface{}, deps *[]Dep, walk func(reflect.Value)) {
	switch node := n.(type) {
	case *ast.ImportDecl:
		if node.Source != "" {
			*deps = append(*deps, Dep{Spec: node.Source, ImportCtx: true})
		}
	case *ast.ExportDecl:
		if node.Source != "" {
			*deps = append(*deps, Dep{Spec: node.Source, ImportCtx: true})
		}
	case *ast.CallExpr:
		if id, ok := node.Callee.(*ast.Identifier); ok && len(node.Arguments) > 0 {
			if lit, ok := node.Arguments[0].(*ast.StringLit); ok {
				switch id.Name {
				case "require":
					*deps = append(*deps, Dep{Spec: lit.Value, ImportCtx: false})
				case "__import": // 动态 import() 经 parser lower 的形式
					*deps = append(*deps, Dep{Spec: lit.Value, ImportCtx: true})
				}
			}
		}
	}
}

// isBuiltinSpecifier 判断是否为 node: 前缀的内置模块。
func isBuiltinSpecifier(spec string) bool {
	return strings.HasPrefix(spec, "node:")
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

// stripBOM 剥离开头的 UTF-8 BOM。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}
