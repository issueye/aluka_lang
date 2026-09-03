// 声明绑定名提取工具：从解构模式/声明语句中提取绑定标识符名。
// 统一三处重复实现（compiler/esm/shake 各自的 patternNames/declNames）。
package ast

// PatternNames 返回解构模式中绑定的全部标识符名（含嵌套模式）。
// Identifier 本身即是最简模式（单个绑定名）。
func PatternNames(p Pattern) []string {
	switch pat := p.(type) {
	case *Identifier:
		return []string{pat.Name}
	case *ArrayPattern:
		var names []string
		for _, el := range pat.Elements {
			if el.Target != nil {
				names = append(names, PatternNames(el.Target)...)
			}
		}
		return names
	case *ObjectPattern:
		var names []string
		for _, prop := range pat.Properties {
			if prop.Value != nil {
				names = append(names, PatternNames(prop.Value)...)
			}
		}
		return names
	}
	return nil
}

// DeclNames 返回声明语句（var/function/class）声明的名字。
// 解构声明（`const {a, b} = x`）的绑定名一并返回。
func DeclNames(stmt Statement) []string {
	switch n := stmt.(type) {
	case *VarDecl:
		var names []string
		for _, d := range n.Decls {
			if d.Name != nil {
				names = append(names, d.Name.Name)
			}
			if d.Pattern != nil {
				names = append(names, PatternNames(d.Pattern)...)
			}
		}
		return names
	case *FunctionDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	case *ClassDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	}
	return nil
}
