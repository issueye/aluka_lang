// Package astutil 提供构建期优化（tree-shaking/minify）共用的 AST 工具：
// 标识符引用收集、表达式副作用判定、常量折叠（docs/test-bundle-optimize-plan.md §5.2）。
package astutil

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// CollectRefs 收集节点内全部标识符引用名（不含声明位置本身——
// VarDeclarator.Name、FunctionDecl.Name、ClassDecl.Name、参数名）。
// 用于"声明是否被引用"的判定。
func CollectRefs(node interface{}) map[string]int {
	refs := make(map[string]int)
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
			walk(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
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
	collect := func(node interface{}) {
		switch n := node.(type) {
		case *ast.Identifier:
			refs[n.Name]++
		case *ast.VarDeclarator:
			// 声明名不收集；初始化表达式收集。
			if n.Init != nil {
				walk(reflect.ValueOf(n.Init))
			}
		case *ast.FunctionDecl:
			// 函数名与参数不收集；默认值与函数体收集。
			for _, d := range n.Defaults {
				if d != nil {
					walk(reflect.ValueOf(d))
				}
			}
			walk(reflect.ValueOf(n.Body))
		case *ast.ClassDecl:
			walk(reflect.ValueOf(n.SuperClass))
			walk(reflect.ValueOf(n.Body))
		default:
			walk(reflect.ValueOf(node))
		}
	}
	var walkStmt func(v reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collect(v.Elem().Interface())
			walkStmt(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			collect(v.Interface())
			walkStmt(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkStmt(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walkStmt(v.Field(i))
			}
		}
	}
	walkStmt(reflect.ValueOf(node))
	return refs
}

// HasSideEffects 判定表达式是否有副作用（调用/赋值/更新/对象创建/模板
// 插值等 → true）。字面量、标识符、成员访问、纯二元/一元运算 → false。
// 保守判定：不确定的视为有副作用。
func HasSideEffects(expr ast.Expression) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.NumberLit, *ast.StringLit, *ast.BoolLit, *ast.NullLit, *ast.UndefinedLit, *ast.BigIntLit:
		return false
	case *ast.Identifier:
		return false
	case *ast.MemberExpr:
		return HasSideEffects(n.Object) || (n.Computed && HasSideEffects(n.Property))
	case *ast.CallExpr, *ast.NewExpr, *ast.AssignExpr, *ast.UpdateExpr, *ast.YieldExpr, *ast.SpreadElement:
		return true
	case *ast.ArrayLit:
		for _, e := range n.Elements {
			if HasSideEffects(e) {
				return true
			}
		}
		return false
	case *ast.ObjectLit:
		for _, p := range n.Properties {
			if HasSideEffects(p.Value) {
				return true
			}
		}
		return false
	case *ast.UnaryExpr:
		return HasSideEffects(n.Arg)
	case *ast.BinaryExpr:
		return HasSideEffects(n.Left) || HasSideEffects(n.Right)
	case *ast.LogicalExpr:
		return HasSideEffects(n.Left) || HasSideEffects(n.Right)
	case *ast.TemplateLit:
		for _, e := range n.Expressions {
			if HasSideEffects(e) {
				return true
			}
		}
		return false
	case *ast.ConditionalExpr:
		return HasSideEffects(n.Test) || HasSideEffects(n.Consequent) || HasSideEffects(n.Alternate)
	case *ast.FunctionExpr, *ast.ArrowFunc, *ast.ClassExpr:
		return false
	}
	return true
}

// FoldConst 常量折叠：返回 (值, 类型, 是否成功)。支持：
//   - 字面量本身（数字/字符串/布尔/null/undefined/bigint 文本）
//   - 一元（! / - / +）与二元（算术/比较/字符串 +）字面量运算
//   - 无插值模板字符串
//   - 字符串拼接（BinaryExpr "+"）
//
// 失败返回 ok=false（调用方保留原表达式）。
func FoldConst(expr ast.Expression) (value interface{}, ok bool) {
	switch n := expr.(type) {
	case *ast.NumberLit:
		return n.Value, true
	case *ast.StringLit:
		return n.Value, true
	case *ast.BoolLit:
		return n.Value, true
	case *ast.NullLit:
		return nil, true
	case *ast.UndefinedLit:
		return undefined{}, true
	case *ast.BigIntLit:
		return n.Text, true
	case *ast.TemplateLit:
		if len(n.Expressions) == 0 {
			return n.Quasis[0], true
		}
		return nil, false
	case *ast.UnaryExpr:
		v, ok := FoldConst(n.Arg)
		if !ok {
			return nil, false
		}
		return foldUnary(n.Op, v)
	case *ast.BinaryExpr:
		l, ok := FoldConst(n.Left)
		if !ok {
			return nil, false
		}
		r, ok := FoldConst(n.Right)
		if !ok {
			return nil, false
		}
		return foldBinary(n.Op, l, r)
	case *ast.LogicalExpr:
		// && / || 短路：两侧可折叠且值已知。
		l, ok := FoldConst(n.Left)
		if !ok {
			return nil, false
		}
		r, ok := FoldConst(n.Right)
		if !ok {
			return nil, false
		}
		lb, _ := truthy(l)
		switch n.Op {
		case "&&":
			if !lb {
				return l, true
			}
			return r, true
		case "||":
			if lb {
				return l, true
			}
			return r, true
		case "??":
			if _, isU := l.(undefined); isU || l == nil {
				return r, true
			}
			return l, true
		}
		return nil, false
	case *ast.ConditionalExpr:
		t, ok := FoldConst(n.Test)
		if !ok {
			return nil, false
		}
		tb, _ := truthy(t)
		if tb {
			return FoldConst(n.Consequent)
		}
		return FoldConst(n.Alternate)
	}
	return nil, false
}

// undefined 标记 undefined 字面量折叠值。
type undefined struct{}

// foldUnary 一元字面量运算。
func foldUnary(op string, v interface{}) (interface{}, bool) {
	switch op {
	case "!":
		b, _ := truthy(v)
		return !b, true
	case "-":
		if n, ok := toNumber(v); ok {
			return -n, true
		}
	case "+":
		if n, ok := toNumber(v); ok {
			return n, true
		}
	case "typeof":
		return typeofFold(v), true
	}
	return nil, false
}

// foldBinary 二元字面量运算（仅纯运算：算术/比较/字符串拼接）。
func foldBinary(op string, l, r interface{}) (interface{}, bool) {
	// 字符串拼接（任一为字符串且 op 为 +）。
	if op == "+" {
		if ls, ok := l.(string); ok {
			if rs, ok2 := r.(string); ok2 {
				return ls + rs, true
			}
			if n, ok2 := toNumber(r); ok2 {
				return ls + strconv.FormatFloat(n, 'g', -1, 64), true
			}
		}
		if rs, ok := r.(string); ok {
			if n, ok2 := toNumber(l); ok2 {
				return strconv.FormatFloat(n, 'g', -1, 64) + rs, true
			}
		}
	}
	ln, lok := toNumber(l)
	rn, rok := toNumber(r)
	if !lok || !rok {
		return nil, false
	}
	switch op {
	case "+":
		return ln + rn, true
	case "-":
		return ln - rn, true
	case "*":
		return ln * rn, true
	case "/":
		if rn == 0 {
			return nil, false // 除零不折叠（可能产生 Infinity/NaN 语义差异）
		}
		return ln / rn, true
	case "%":
		return modFold(ln, rn)
	case "==":
		return ln == rn, true
	case "!=":
		return ln != rn, true
	case "===":
		return ln == rn, true
	case "!==":
		return ln != rn, true
	case "<":
		return ln < rn, true
	case "<=":
		return ln <= rn, true
	case ">":
		return ln > rn, true
	case ">=":
		return ln >= rn, true
	case "**":
		return powFold(ln, rn)
	}
	return nil, false
}

// modFold 取模（Go 的 % 与 JS 语义一致，除零返回 NaN 不折叠）。
func modFold(l, r float64) (interface{}, bool) {
	if r == 0 {
		return nil, false
	}
	m := float64(int64(l) % int64(r))
	// JS % 保留被除数符号：Go int64 取模同符号，直接可用。
	return m, true
}

func powFold(l, r float64) (interface{}, bool) {
	// 仅折叠整数小指数（避免浮点误差引入行为差异）。
	if r == 2 {
		return l * l, true
	}
	if r == 3 {
		return l * l * l, true
	}
	if r == 1 {
		return l, true
	}
	return nil, false
}

// toNumber 把折叠值转数字（字符串/数字/布尔；null→0；undefined→NaN 不折叠）。
func toNumber(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case nil:
		return 0, true
	}
	return 0, false
}

// truthy JS 真值判定（折叠值）。
func truthy(v interface{}) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0 && !isNaN(t), true
	case string:
		return t != "", true
	case nil:
		return false, true
	case undefined:
		return false, true
	}
	return false, false
}

func isNaN(f float64) bool { return f != f }

// typeofFold typeof 折叠。
func typeofFold(v interface{}) string {
	switch v.(type) {
	case undefined:
		return "undefined"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case nil:
		return "object"
	}
	return "unknown"
}
