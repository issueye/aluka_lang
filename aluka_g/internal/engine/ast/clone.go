package ast

import "reflect"

// DeepCopy 对 AST 节点做结构深拷贝（P2-4：模块 IR 复用/阶段幂等）。
//
// 用途：同一 SourceUnit 需要被多次消费时（如 raw 度量编译 + 优化后编译），
// 调用方持有一份 clone；原始 AST 不被 lower/minify 原地破坏。AST 是纯数据
// 结构（指针/接口/切片/基本类型 + 极少量 map），无循环引用，通用反射复制
// 即可覆盖全部节点类型，后续新增节点无需维护独立 clone 逻辑。
func DeepCopy[T any](v T) T {
	return deepCopyValue(reflect.ValueOf(v)).Interface().(T)
}

// deepCopyValue 递归复制一个 reflect.Value。
func deepCopyValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		// 取具体类型复制后赋回接口。
		cp := deepCopyValue(v.Elem())
		out := reflect.New(v.Type()).Elem()
		out.Set(cp)
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(v.Type().Elem())
		cp.Elem().Set(deepCopyValue(v.Elem()))
		return cp
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return cp
	case reflect.Array:
		cp := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, k := range v.MapKeys() {
			cp.SetMapIndex(deepCopyValue(k), deepCopyValue(v.MapIndex(k)))
		}
		return cp
	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			fv := v.Field(i)
			if !fv.CanInterface() {
				continue // 未导出字段：拷贝不可行，跳过（AST 无状态未导出字段）
			}
			cp.Field(i).Set(deepCopyValue(fv))
		}
		return cp
	default:
		// 基本类型值：不可变，直接构造新值返回。
		return reflect.ValueOf(v.Interface())
	}
}
