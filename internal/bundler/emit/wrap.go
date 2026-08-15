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

// RuntimePrelude 是产物内嵌的微型模块运行时（__def/__req）。
const RuntimePrelude = `var __alukaMods={},__alukaCache={};function __def(i,f){__alukaMods[i]=f}function __req(i){var m=__alukaCache[i];if(m)return m.exports;m={exports:{}};__alukaCache[i]=m;__alukaMods[i](m.exports);return m.exports}`

// Module 是参与拼接的一个模块。
type Module struct {
	ID       string            // 模块标识（graph 解析键，产物内唯一）
	Prog     *ast.Program      // 模块 AST（已 shake/minify）
	IsTLA    bool              // 是否含顶层 await
	Resolved map[string]string // import specifier → 模块 ID（graph 构建期解析）
}

// WrapModule 将模块 AST 变换为包裹函数体语句序列并打印。
func WrapModule(m Module) (string, error) {
	if m.IsTLA {
		return "", fmt.Errorf("emit: 模块 %s 含顶层 await，web target 暂不支持（M2）", m.ID)
	}
	var body strings.Builder
	p := &printer{sb: &body, resolved: m.Resolved}

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
	out.WriteString(",(exports)=>{")
	out.WriteString(inner)
	out.WriteString("});")
	return out.String(), nil
}

// importToReq 将 ImportDecl 打印为 __req 解构取值。
func (p *printer) importToReq(d *ast.ImportDecl) {
	var named []string
	for _, spec := range d.Specifiers {
		switch spec.Imported {
		case "": // default
			p.w("var ")
			p.w(spec.Local)
			p.w("=")
			p.reqCall(d.Source)
			p.w(".default;")
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

// Bundle 输入：入口与全部模块。
type Bundle struct {
	EntryID string
	Modules []Module
}

// Build 拼接最终产物（ESM 单文件）。
func (b Bundle) Build() (string, error) {
	var out strings.Builder
	out.WriteString(RuntimePrelude)
	out.WriteString(";")

	entry := findModule(b.Modules, b.EntryID)
	if entry == nil {
		return "", fmt.Errorf("emit: entry module %q not found", b.EntryID)
	}

	entryExports, err := collectExports(entry.Prog)
	if err != nil {
		return "", err
	}

	for _, m := range b.Modules {
		text, err := WrapModule(m)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}

	out.WriteString("var __entry=__req(")
	quoteJS(&out, b.EntryID)
	out.WriteString(");")

	// 入口导出映射为产物顶层 ESM export
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
