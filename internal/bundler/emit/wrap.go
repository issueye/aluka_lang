// Package emit —— web bundle 模块包裹与产物拼接。
//
// 采用 CJS 风格包裹：每个模块体包进独立函数作用域（天然隔离顶层声明，
// 免跨模块符号改名），import 重写为对 __req(id) 的解构取值，export 重写
// 为 exports.xxx 赋值；入口模块的导出再映射为产物顶层 ESM export。
//
// M1 已知语义近似（记录于 static-build-plan.md）：
//   - 导出为快照而非 live binding（模块内导出后再修改不会反映）；
//   - 顶层 await（TLA）暂不支持，构建期报错；
//   - JSON import 暂不支持，构建期报错（M2 内联）。
package emit

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// RuntimePrelude 是产物内嵌的微型模块运行时（__def/__req/__mkreq/__interop）。
// 包装签名为 (exports, module, require)，兼容 CJS 依赖（module.exports /
// require()）；__resolve 表由 Bundle.Build 按解析映射生成（仅含 CJS 运行
// 期 require 所需条目；ESM import 在构建期已内联解析后的模块 ID）。
const RuntimePrelude = `var __alukaMods=globalThis.__alukaMods||{},__alukaCache=globalThis.__alukaCache||{},__alukaRes=globalThis.__alukaRes||{};function __def(i,f){__alukaMods[i]=f}function __mkreq(from){return function(s){var t=__alukaRes[from];var id=t?t[s]:s;if(!__alukaMods[id])throw new Error("Cannot resolve module '"+s+"' from "+from);return __req(id)}}function __interop(m){return m&&m.__esModule?m.default:m}function __req(i){var m=__alukaCache[i];if(m)return m.exports;m={exports:{}};__alukaCache[i]=m;__alukaMods[i](m.exports,m,__mkreq(i));return m.exports}function __alukaImport(c,i){return import("./"+c).then(function(){return __req(i)})}globalThis.__alukaMods=__alukaMods;globalThis.__alukaCache=__alukaCache;globalThis.__alukaRes=__alukaRes;globalThis.__alukaRegister=__def;`

// Module 是参与拼接的一个模块。
type Module struct {
	ID       string            // 模块标识（graph 解析键，产物内唯一）
	Prog     *ast.Program      // 模块 AST（已 shake/minify）
	IsTLA    bool              // 是否含顶层 await
	IsCJS    bool              // CJS 模块（module.exports / require）
	Resolved map[string]string // import specifier → 模块 ID（graph 构建期解析）
	// DynamicImports 将动态 import 的 specifier 映射到 chunk 文件与模块 ID。
	DynamicImports map[string]DynamicImport
}

// DynamicImport 描述一个异步 chunk 的加载目标。
type DynamicImport struct {
	Chunk  string
	Target string
}

// bundleCtx 是一次 Bundle.Build 的跨模块上下文。
type bundleCtx struct {
	cjsMods map[string]bool // ID → 是否 CJS（default 导入互操作用）
}

// WrapModule 将模块 AST 变换为包裹函数体语句序列并打印。
func WrapModule(m Module) (string, error) {
	return wrapModule(&bundleCtx{}, m)
}

func wrapModule(ctx *bundleCtx, m Module) (string, error) {
	if m.IsTLA {
		return "", fmt.Errorf("emit: 模块 %s 含顶层 await，web target 暂不支持（M2）", m.ID)
	}
	var body strings.Builder
	p := &printer{sb: &body, resolved: m.Resolved, ctx: ctx, dynamic: m.DynamicImports}

	for _, stmt := range m.Prog.Body {
		switch t := stmt.(type) {
		case *ast.ImportDecl:
			p.importToReq(t)

		case *ast.ExportDecl:
			if t.Source != "" {
				p.reExport(t)
				continue
			}
			if t.Declaration != nil {
				// 声明保留 + 追加 exports 赋值
				p.stmt(t.Declaration)
				p.w(";")
				p.exportDeclAssigns(t.Declaration)
				p.w(";")
				continue
			}
			// export {a as b}
			for _, spec := range t.Specifiers {
				p.w("exports.")
				p.w(spec.Exported)
				p.w("=")
				p.w(spec.Local)
				p.w(";")
			}

		case *ast.ExportDefaultDecl:
			p.w("exports.default=")
			p.expr(t.Expression, precAssign)
			p.w(";")

		default:
			p.stmt(stmt)
			p.w(";")
		}
	}

	inner := body.String()
	var out strings.Builder
	out.WriteString("__def(")
	quoteJS(&out, m.ID)
	out.WriteString(",(exports,module,require)=>{")
	out.WriteString(inner)
	out.WriteString("});")
	return out.String(), nil
}

// importToReq 将 ImportDecl 打印为 __req 解构取值。
func (p *printer) importToReq(d *ast.ImportDecl) {
	var named []string
	for _, spec := range d.Specifiers {
		switch spec.Imported {
		case "": // default：目标为 CJS 时取 module.exports（__esModule 互操作）
			p.w("var ")
			p.w(spec.Local)
			p.w("=")
			target, ok := "", false
			if p.resolved != nil {
				target, ok = p.resolved[d.Source]
			}
			if ok && p.ctx != nil && p.ctx.cjsMods[target] {
				p.w("__interop(")
				p.reqCall(d.Source)
				p.w(")")
			} else {
				p.reqCall(d.Source)
				p.w(".default")
			}
			p.w(";")
		case "*": // namespace
			p.w("var ")
			p.w(spec.Local)
			p.w("=")
			p.reqCall(d.Source)
			p.w(";")
		default: // named
			if spec.Imported == spec.Local {
				named = append(named, spec.Local)
			} else {
				named = append(named, spec.Imported+":"+spec.Local)
			}
		}
	}
	if len(named) > 0 {
		p.w("var{")
		p.w(strings.Join(named, ","))
		p.w("}=")
		p.reqCall(d.Source)
		p.w(";")
	} else if len(d.Specifiers) == 0 {
		// 副作用导入
		p.reqCall(d.Source)
		p.w(";")
	}
}

func (p *printer) reqCall(specifier string) {
	id := specifier
	if p.resolved != nil {
		if resolved, ok := p.resolved[specifier]; ok {
			id = resolved
		}
	}
	p.w("__req(")
	p.string(id)
	p.w(")")
}

// exportDeclAssigns 为 export <decl> 形式输出 exports 赋值。
func (p *printer) exportDeclAssigns(decl ast.Statement) {
	first := true
	emitAssign := func(name string) {
		if !first {
			p.w(";")
		}
		first = false
		p.w("exports.")
		p.w(name)
		p.w("=")
		p.w(name)
	}
	switch t := decl.(type) {
	case *ast.VarDecl:
		for _, d := range t.Decls {
			if d.Name != nil {
				emitAssign(d.Name.Name)
			} else if d.Pattern != nil {
				// 解构导出：导出模式中的全部标识符
				for _, name := range patternIdentifiers(d.Pattern) {
					emitAssign(name)
				}
			}
		}
	case *ast.FunctionDecl:
		if t.Name != nil {
			emitAssign(t.Name.Name)
		}
	case *ast.ClassDecl:
		if t.Name != nil {
			emitAssign(t.Name.Name)
		}
	}
}

// reExport 处理 export {a as b} from / export * from。
func (p *printer) reExport(t *ast.ExportDecl) {
	if t.IsStar {
		if t.StarName != "" {
			// export * as ns from 'mod'
			p.w("exports.")
			p.w(t.StarName)
			p.w("=")
			p.reqCall(t.Source)
			p.w(";")
			return
		}
		// export * from 'mod'：非 default 全量转发
		p.w("var __re=")
		p.reqCall(t.Source)
		p.w(";for(var __k in __re){if(__k!==\"default\")exports[__k]=__re[__k];}")
		return
	}
	for _, spec := range t.Specifiers {
		p.w("exports.")
		p.w(spec.Exported)
		p.w("=")
		p.reqCall(t.Source)
		p.w(".")
		p.w(spec.Local)
		p.w(";")
	}
}

// patternIdentifiers 收集解构模式绑定的全部标识符。
func patternIdentifiers(pat ast.Pattern) []string {
	var out []string
	var walk func(ast.Pattern)
	walk = func(pt ast.Pattern) {
		switch t := pt.(type) {
		case *ast.Identifier:
			out = append(out, t.Name)
		case *ast.ArrayPattern:
			for _, el := range t.Elements {
				if el.Target != nil {
					walk(el.Target)
				}
			}
		case *ast.ObjectPattern:
			for _, prop := range t.Properties {
				if prop.Value != nil {
					walk(prop.Value)
				}
			}
		}
	}
	walk(pat)
	return out
}

// Bundle 输入：入口、全部模块与静态资源。
type Bundle struct {
	EntryID string
	Modules []Module
	Assets  map[string][]byte
	Format  string // esm（默认）、cjs、umd
	Global  string // UMD global name
}

// Build 拼接最终产物（ESM/CJS/UMD）。
func (b Bundle) Build() (string, error) {
	format := b.Format
	if format == "" {
		format = "esm"
	}
	if format != "esm" && format != "cjs" && format != "umd" {
		return "", fmt.Errorf("emit: unsupported format %q", format)
	}
	ctx := &bundleCtx{cjsMods: map[string]bool{}}
	hasCJS := false
	for _, m := range b.Modules {
		if m.IsCJS {
			ctx.cjsMods[m.ID] = true
			hasCJS = true
		}
	}

	var out strings.Builder
	out.WriteString(RuntimePrelude)
	out.WriteString(";")
	if hasCJS {
		// CJS 依赖的运行期 require() 解析表：from → {specifier → ID}
		out.WriteString("__alukaRes={")
		pq := &printer{sb: &out}
		first := true
		for _, m := range b.Modules {
			if !m.IsCJS || len(m.Resolved) == 0 {
				continue
			}
			if !first {
				out.WriteString(",")
			}
			first = false
			pq.string(m.ID)
			out.WriteString(":{")
			firstSpec := true
			for spec, id := range m.Resolved {
				if !firstSpec {
					out.WriteString(",")
				}
				firstSpec = false
				pq.string(spec)
				out.WriteString(":")
				pq.string(id)
			}
			out.WriteString("}")
		}
		out.WriteString("};")
	}

	// 注册静态资源模块（JSON 数据内联、CSS 副作用占位）
	for id, data := range b.Assets {
		if strings.HasSuffix(id, ".json") {
			out.WriteString("__def(")
			quoteJS(&out, id)
			out.WriteString(",(exports,module)=>{module.exports=")
			out.WriteString(strings.TrimSpace(string(data)))
			out.WriteString(";exports.default=module.exports;});")
		} else if strings.HasSuffix(id, ".css") {
			out.WriteString("__def(")
			quoteJS(&out, id)
			out.WriteString(",()=>{});")
		}
	}

	entry := findModule(b.Modules, b.EntryID)
	if entry == nil {
		return "", fmt.Errorf("emit: entry module %q not found", b.EntryID)
	}

	entryExports, err := collectExports(entry.Prog)
	if err != nil {
		return "", err
	}

	for _, m := range b.Modules {
		text, err := wrapModule(ctx, m)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}

	if format == "cjs" || format == "umd" {
		var body strings.Builder
		body.WriteString("var __entry=__req(")
		quoteJS(&body, b.EntryID)
		body.WriteString(");")
		body.WriteString("var __out={};")
		for _, name := range entryExports {
			body.WriteString("__out[")
			quoteJS(&body, name)
			body.WriteString("]=__entry[")
			quoteJS(&body, name)
			body.WriteString("];")
		}
		if format == "cjs" {
			body.WriteString("module.exports=__out;")
			return out.String() + body.String(), nil
		}
		global := b.Global
		if global == "" {
			global = "AlukaBundle"
		}
		wrapped := "(function(root,factory){if(typeof module===\"object\"&&module.exports)module.exports=factory();else if(typeof define===\"function\"&&define.amd)define([],factory);else root[\"" + global + "\"]=factory()})(typeof globalThis!==\"undefined\"?globalThis:this,function(){" + body.String() + "return __out;});"
		return out.String() + wrapped, nil
	}

	out.WriteString("var __entry=__req(")
	quoteJS(&out, b.EntryID)
	out.WriteString(");")
	hasDefault := false
	for _, name := range entryExports {
		if name == "default" {
			hasDefault = true
			continue
		}
		out.WriteString("export var ")
		out.WriteString(name)
		out.WriteString("=__entry.")
		out.WriteString(name)
		out.WriteString(";")
	}
	if hasDefault {
		out.WriteString("export default __entry.default;")
	}
	return out.String(), nil
}

// BuildChunk 生成可由主 bundle 动态加载的模块 chunk。
func (b Bundle) BuildChunk() (string, error) {
	ctx := &bundleCtx{cjsMods: map[string]bool{}}
	for _, m := range b.Modules {
		if m.IsCJS {
			ctx.cjsMods[m.ID] = true
		}
	}
	var out strings.Builder
	out.WriteString(RuntimePrelude)
	for _, m := range b.Modules {
		text, err := wrapModule(ctx, m)
		if err != nil {
			return "", err
		}
		text = strings.Replace(text, "__def(", "globalThis.__alukaRegister(", 1)
		out.WriteString(text)
	}
	return out.String(), nil
}

func findModule(mods []Module, id string) *Module {
	for i := range mods {
		if mods[i].ID == id {
			return &mods[i]
		}
	}
	return nil
}

// collectExports 提取模块导出名（named + default）。
func collectExports(prog *ast.Program) ([]string, error) {
	var out []string
	add := func(n string) { out = append(out, n) }
	for _, stmt := range prog.Body {
		switch t := stmt.(type) {
		case *ast.ExportDecl:
			if t.Declaration != nil {
				switch d := t.Declaration.(type) {
				case *ast.VarDecl:
					for _, vd := range d.Decls {
						if vd.Name != nil {
							add(vd.Name.Name)
						} else if vd.Pattern != nil {
							out = append(out, patternIdentifiers(vd.Pattern)...)
						}
					}
				case *ast.FunctionDecl:
					if d.Name != nil {
						add(d.Name.Name)
					}
				case *ast.ClassDecl:
					if d.Name != nil {
						add(d.Name.Name)
					}
				}
				continue
			}
			for _, spec := range t.Specifiers {
				add(spec.Exported)
			}
		case *ast.ExportDefaultDecl:
			add("default")
		}
	}
	return out, nil
}

func quoteJS(sb *strings.Builder, s string) {
	p := &printer{sb: sb}
	p.string(s)
}
