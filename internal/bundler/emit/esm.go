package emit

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// NativeESM 是默认 --format=esm 的产物：每个模块一个 hashed ESM 文件。
type NativeESM struct {
	Files     map[string][]byte
	EntryFile string   // 入口模块的 assets/<name>-<hash>.js
	Preload   []string // 入口静态图（含入口），供 modulepreload
	Async     []string // 动态 import() 目标文件
}

type nativeGraph struct {
	mods      map[string]Module
	json      map[string][]byte
	cjs       map[string]bool
	defines   map[string]string
	assetsDir string
	hash      map[string]string
	files     map[string]string
	used      map[string]bool
	visiting  map[string]bool
}

func isCSSID(id string) bool {
	return strings.HasSuffix(strings.ToLower(id), ".css")
}

func isJSONID(id string) bool {
	return strings.HasSuffix(strings.ToLower(id), ".json")
}

// BuildNativeESM 把模块图打印为原生 import/export，文件名带内容哈希。
// CSS 副作用 import 被丢掉（样式由 BundleCSS 单独产出）；JSON 变成
// `export default …` 的小 ESM 文件；CJS 依赖用 default 导出 + 导入侧互操作。
func BuildNativeESM(b Bundle) (NativeESM, error) {
	g := &nativeGraph{
		mods:      make(map[string]Module, len(b.Modules)),
		json:      make(map[string][]byte),
		cjs:       make(map[string]bool),
		defines:   b.Defines,
		assetsDir: b.AssetsDir,
		hash:      make(map[string]string),
		files:     make(map[string]string),
		used:      make(map[string]bool),
		visiting:  make(map[string]bool),
	}
	for _, m := range b.Modules {
		if m.IsTLA {
			return NativeESM{}, fmt.Errorf("emit: 模块 %s 含顶层 await，web target 暂不支持", m.ID)
		}
		g.mods[m.ID] = m
		if m.IsCJS {
			g.cjs[m.ID] = true
		}
	}
	for id, data := range b.Assets {
		if isJSONID(id) {
			g.json[id] = data
		}
	}
	if _, ok := g.mods[b.EntryID]; !ok {
		return NativeESM{}, fmt.Errorf("emit: entry module %q not found", b.EntryID)
	}

	ids := make([]string, 0, len(g.mods)+len(g.json))
	for id := range g.mods {
		ids = append(ids, id)
	}
	for id := range g.json {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g.assignFile(id)
	}

	out := NativeESM{Files: make(map[string][]byte, len(ids))}
	for _, id := range ids {
		file := g.files[id]
		text, err := g.printFile(id, file)
		if err != nil {
			return NativeESM{}, err
		}
		out.Files[file] = []byte(text)
	}

	out.EntryFile = g.files[b.EntryID]
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		file := g.files[id]
		if file == "" || seen[file] {
			return
		}
		seen[file] = true
		out.Preload = append(out.Preload, file)
		m, ok := g.mods[id]
		if !ok {
			return
		}
		dyn := map[string]bool{}
		for spec := range m.DynamicImports {
			dyn[spec] = true
		}
		var deps []string
		for spec, target := range m.Resolved {
			if dyn[spec] || isCSSID(target) {
				continue
			}
			deps = append(deps, target)
		}
		sort.Strings(deps)
		for _, target := range deps {
			walk(target)
		}
	}
	walk(b.EntryID)
	sort.Strings(out.Preload)

	asyncSeen := map[string]bool{}
	for _, m := range b.Modules {
		for _, d := range m.DynamicImports {
			file := g.files[d.Target]
			if file == "" || file == out.EntryFile || asyncSeen[file] {
				continue
			}
			asyncSeen[file] = true
			out.Async = append(out.Async, file)
		}
	}
	sort.Strings(out.Async)
	return out, nil
}

// BuildESMBarrel 生成稳定入口（--outfile / JS 入口文件名）：转发 hashed 模块。
func BuildESMBarrel(fromFile, entryFile string, exportNames []string) string {
	rel := relativeModuleSpecifier(fromFile, entryFile)
	var b strings.Builder
	b.WriteString("export * from ")
	quoteJS(&b, rel)
	b.WriteString(";")
	for _, name := range exportNames {
		if name == "default" {
			b.WriteString("export {default} from ")
			quoteJS(&b, rel)
			b.WriteString(";")
			break
		}
	}
	return b.String()
}

// CSSSideEffectImport 生成相对路径的 CSS 副作用 import（供稳定 barrel 引用样式）。
func CSSSideEffectImport(fromFile, cssFile string) string {
	if cssFile == "" {
		return ""
	}
	rel := relativeModuleSpecifier(fromFile, cssFile)
	var b strings.Builder
	b.WriteString("import ")
	quoteJS(&b, rel)
	b.WriteString(";\n")
	return b.String()
}

func (g *nativeGraph) assignFile(id string) {
	if _, ok := g.files[id]; ok {
		return
	}
	h := g.hashOf(id)
	p := HashedAssetPathIn(g.assetsDir, id, h, ".js")
	for g.used[p] {
		h = ContentHash(h, id)
		p = HashedAssetPathIn(g.assetsDir, id, h, ".js")
	}
	g.used[p] = true
	g.files[id] = p
}

func (g *nativeGraph) hashOf(id string) string {
	if h, ok := g.hash[id]; ok {
		return h
	}
	if g.visiting[id] {
		return ContentHash("cycle", id)
	}
	g.visiting[id] = true
	parts := []string{id, g.seedBody(id)}
	for _, dep := range g.staticDeps(id) {
		if isCSSID(dep) {
			continue
		}
		parts = append(parts, g.hashOf(dep))
	}
	g.visiting[id] = false
	h := ContentHash(parts...)
	g.hash[id] = h
	return h
}

func (g *nativeGraph) seedBody(id string) string {
	if data, ok := g.json[id]; ok {
		return strings.TrimSpace(string(data))
	}
	m, ok := g.mods[id]
	if !ok || m.Prog == nil {
		return ""
	}
	return PrintOpts(m.Prog, PrintOptions{Defines: g.defines})
}

func (g *nativeGraph) staticDeps(id string) []string {
	m, ok := g.mods[id]
	if !ok {
		return nil
	}
	dyn := map[string]bool{}
	for spec := range m.DynamicImports {
		dyn[spec] = true
	}
	var deps []string
	seen := map[string]bool{}
	for spec, target := range m.Resolved {
		if dyn[spec] || seen[target] {
			continue
		}
		seen[target] = true
		deps = append(deps, target)
	}
	sort.Strings(deps)
	return deps
}

func (g *nativeGraph) printFile(id, file string) (string, error) {
	if data, ok := g.json[id]; ok {
		body := strings.TrimSpace(string(data))
		if body == "" {
			body = "null"
		}
		return "export default " + body + ";", nil
	}
	m := g.mods[id]
	if m.IsCJS {
		return g.printCJS(m, file)
	}
	return g.printESM(m, file)
}

func (g *nativeGraph) rewriteImport(m Module, file string) func(string) (string, bool) {
	return func(spec string) (string, bool) {
		target, ok := m.Resolved[spec]
		if !ok {
			return spec, true
		}
		if isCSSID(target) || g.cjs[target] {
			return "", false
		}
		dest := g.files[target]
		if dest == "" {
			return spec, true
		}
		return relativeModuleSpecifier(file, dest), true
	}
}

func (g *nativeGraph) rewriteDynamic(m Module, file string) func(string) string {
	return func(spec string) string {
		if d, ok := m.DynamicImports[spec]; ok {
			if dest := g.files[d.Target]; dest != "" {
				return relativeModuleSpecifier(file, dest)
			}
		}
		if target, ok := m.Resolved[spec]; ok {
			if dest := g.files[target]; dest != "" {
				return relativeModuleSpecifier(file, dest)
			}
		}
		return ""
	}
}

func (g *nativeGraph) printESM(m Module, file string) (string, error) {
	var b strings.Builder
	p := &printer{
		sb:             &b,
		defines:        g.defines,
		rewriteImport:  g.rewriteImport(m, file),
		rewriteDynamic: g.rewriteDynamic(m, file),
	}

	cjsAlias := map[string]string{}
	next := 0
	bindCJS := func(target string) string {
		if alias, ok := cjsAlias[target]; ok {
			return alias
		}
		alias := fmt.Sprintf("__cjs%d", next)
		next++
		cjsAlias[target] = alias
		dest := g.files[target]
		b.WriteString("import ")
		b.WriteString(alias)
		b.WriteString(" from ")
		quoteJS(&b, relativeModuleSpecifier(file, dest))
		b.WriteString(";")
		return alias
	}
	for _, stmt := range m.Prog.Body {
		switch t := stmt.(type) {
		case *ast.ImportDecl:
			if target, ok := m.Resolved[t.Source]; ok && g.cjs[target] {
				bindCJS(target)
			}
		case *ast.ExportDecl:
			if t.Source != "" {
				if target, ok := m.Resolved[t.Source]; ok && g.cjs[target] {
					bindCJS(target)
				}
			}
		}
	}

	for _, stmt := range m.Prog.Body {
		switch t := stmt.(type) {
		case *ast.ImportDecl:
			if target, ok := m.Resolved[t.Source]; ok && g.cjs[target] {
				continue
			}
			p.importDecl(t)
			p.w(";")
		case *ast.ExportDecl:
			if t.Source == "" {
				continue
			}
			if target, ok := m.Resolved[t.Source]; ok && g.cjs[target] {
				continue
			}
			p.exportDecl(t)
			p.w(";")
		}
	}

	if len(cjsAlias) > 0 {
		b.WriteString("function __interop(m){return m&&m.__esModule?m.default:m};")
		for _, stmt := range m.Prog.Body {
			d, ok := stmt.(*ast.ImportDecl)
			if !ok {
				continue
			}
			target, ok := m.Resolved[d.Source]
			if !ok || !g.cjs[target] {
				continue
			}
			alias := cjsAlias[target]
			for _, spec := range d.Specifiers {
				switch spec.Imported {
				case "":
					b.WriteString("var ")
					b.WriteString(spec.Local)
					b.WriteString("=__interop(")
					b.WriteString(alias)
					b.WriteString(");")
				case "*":
					b.WriteString("var ")
					b.WriteString(spec.Local)
					b.WriteString("=")
					b.WriteString(alias)
					b.WriteString(";")
				default:
					b.WriteString("var ")
					b.WriteString(spec.Local)
					b.WriteString("=")
					b.WriteString(alias)
					if isJSIdent(spec.Imported) {
						b.WriteByte('.')
						b.WriteString(spec.Imported)
					} else {
						b.WriteByte('[')
						quoteJS(&b, spec.Imported)
						b.WriteByte(']')
					}
					b.WriteByte(';')
				}
			}
		}
		for _, stmt := range m.Prog.Body {
			t, ok := stmt.(*ast.ExportDecl)
			if !ok || t.Source == "" {
				continue
			}
			target, ok := m.Resolved[t.Source]
			if !ok || !g.cjs[target] {
				continue
			}
			alias := cjsAlias[target]
			if t.IsStar {
				if t.StarName != "" {
					b.WriteString("var ")
					b.WriteString(t.StarName)
					b.WriteString("=")
					b.WriteString(alias)
					b.WriteString(";export{")
					b.WriteString(t.StarName)
					b.WriteString("};")
				}
				continue
			}
			for _, spec := range t.Specifiers {
				from := spec.Local
				if spec.Exported == "default" && from == "default" {
					b.WriteString("export default __interop(")
					b.WriteString(alias)
					b.WriteString(");")
					continue
				}
				b.WriteString("var ")
				b.WriteString(spec.Exported)
				b.WriteString("=")
				if from == "default" {
					b.WriteString("__interop(")
					b.WriteString(alias)
					b.WriteString(");")
				} else {
					b.WriteString(alias)
					if isJSIdent(from) {
						b.WriteByte('.')
						b.WriteString(from)
					} else {
						b.WriteByte('[')
						quoteJS(&b, from)
						b.WriteByte(']')
					}
					b.WriteByte(';')
				}
				if spec.Exported != "default" {
					b.WriteString("export{")
					b.WriteString(spec.Exported)
					b.WriteString("};")
				} else if from != "default" {
					b.WriteString("export default ")
					b.WriteString(spec.Exported)
					b.WriteString(";")
				}
			}
		}
	}

	for _, stmt := range m.Prog.Body {
		switch t := stmt.(type) {
		case *ast.ImportDecl:
			continue
		case *ast.ExportDecl:
			if t.Source != "" {
				continue
			}
			p.exportDecl(t)
			p.w(";")
		default:
			p.stmt(stmt)
			p.w(";")
		}
	}
	return b.String(), nil
}

func (g *nativeGraph) printCJS(m Module, file string) (string, error) {
	var b strings.Builder
	specs := make([]string, 0, len(m.Resolved))
	for spec, target := range m.Resolved {
		if isCSSID(target) || g.files[target] == "" {
			continue
		}
		specs = append(specs, spec)
	}
	sort.Strings(specs)
	aliases := make([]string, len(specs))
	for i, spec := range specs {
		alias := fmt.Sprintf("__d%d", i)
		aliases[i] = alias
		dest := g.files[m.Resolved[spec]]
		b.WriteString("import * as ")
		b.WriteString(alias)
		b.WriteString(" from ")
		quoteJS(&b, relativeModuleSpecifier(file, dest))
		b.WriteString(";")
	}
	b.WriteString("var exports={},module={exports};")
	b.WriteString("function require(s){")
	if len(specs) > 0 {
		b.WriteString("switch(s){")
		for i, spec := range specs {
			b.WriteString("case ")
			quoteJS(&b, spec)
			b.WriteString(":return ")
			b.WriteString(aliases[i])
			b.WriteString(".default!==undefined?")
			b.WriteString(aliases[i])
			b.WriteString(".default:")
			b.WriteString(aliases[i])
			b.WriteString(";")
		}
		b.WriteString("}")
	}
	b.WriteString("throw new Error(\"Cannot resolve module '\"+s+\"'\");}")
	if m.Prog != nil {
		p := &printer{sb: &b, defines: g.defines, rewriteDynamic: g.rewriteDynamic(m, file)}
		for _, stmt := range m.Prog.Body {
			p.stmt(stmt)
			p.w(";")
		}
	}
	b.WriteString("export default module.exports;")
	return b.String(), nil
}

func isJSIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && r != '$' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && r != '$' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
