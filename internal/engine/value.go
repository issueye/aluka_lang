package engine

import (
	"strings"
	"sync"
)

// stringifyGuard 跟踪当前正在格式化的对象。
// 对象打印/字符串化（String()/console.log）沿对象图递归时，若遇自引用
// （循环引用）会无限递归导致 Go 栈溢出崩溃。此处用"进行中"集合检测环，
// 命中时返回 "[Circular]" 而非继续递归。
// 引擎 JS 为单线程执行，String() 仅在 JS 线程被调用，故全局表安全。
var (
	stringifyMu         sync.Mutex
	stringifyInProgress = map[*objectValue]bool{}
)

// markStringify 标记对象正在格式化；若已在格式化路径上（环）返回 false。
func markStringify(o *objectValue) bool {
	stringifyMu.Lock()
	defer stringifyMu.Unlock()
	if stringifyInProgress[o] {
		return false
	}
	stringifyInProgress[o] = true
	return true
}

// unmarkStringify 取消格式化标记（对象自身格式化完成后调用）。
func unmarkStringify(o *objectValue) {
	stringifyMu.Lock()
	delete(stringifyInProgress, o)
	stringifyMu.Unlock()
}

// === 值类型实现 ============================================================
//
// 各 Value/Object/Function 实现按值类型分文件：value_primitive.go（
// undefined/null/boolean）、value_number.go、value_bigint.go、
// value_string.go、value_symbol.go、value_object.go、value_array.go、
// value_function.go；属性描述符语义在 value_descriptor.go。

// --- 辅助函数 --------------------------------------------------------------

// inspectValue 返回值的可读字符串表示（用于 console.log 输出）。
// 比 String() 更详细：字符串不带引号，对象展开属性。
func inspectValue(v Value) string {
	if v == nil {
		return "undefined"
	}
	switch v.Type() {
	case TypeString:
		return v.String() // console.log 输出字符串时不带引号
	case TypeUndefined, TypeNull, TypeBoolean, TypeNumber:
		return v.String()
	default:
		return v.String()
	}
}

// InspectValues 将多个 Value 用空格连接（console.log 多参数行为）。
func InspectValues(vs []Value) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = inspectValue(v)
	}
	return strings.Join(parts, " ")
}
