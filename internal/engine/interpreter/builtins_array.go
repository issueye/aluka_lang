// 内置数组：Array.prototype/Array 构造器与 length、索引元素的读写 helper。

package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

func (interp *Interpreter) getArrayLength(this engine.Value) int {
	if arr, ok := this.(*engine.ArrayValue); ok {
		return len(arr.Elems())
	}
	if interp.currentVM != nil {
		if val, err := interp.currentVM.getProperty(this, "length"); err == nil {
			if n, ok := val.Int(); ok {
				return n
			}
		}
	} else if obj, ok := this.AsObject(); ok {
		if val, err := obj.Get("length"); err == nil {
			if n, ok := val.Int(); ok {
				return n
			}
		}
	}
	return 0
}

func (interp *Interpreter) getArrayElement(this engine.Value, idx int) engine.Value {
	if arr, ok := this.(*engine.ArrayValue); ok {
		elems := arr.Elems()
		if idx >= 0 && idx < len(elems) {
			return elems[idx]
		}
		return engine.Undefined()
	}
	key := strconv.Itoa(idx)
	if interp.currentVM != nil {
		val, err := interp.currentVM.getProperty(this, key)
		if err == nil {
			return val
		}
	} else if obj, ok := this.AsObject(); ok {
		val, err := obj.Get(key)
		if err == nil {
			return val
		}
	}
	return engine.Undefined()
}

func (interp *Interpreter) setArrayElement(this engine.Value, idx int, val engine.Value) error {
	if arr, ok := this.(*engine.ArrayValue); ok {
		return arr.Set(strconv.Itoa(idx), val)
	}
	key := strconv.Itoa(idx)
	if interp.currentVM != nil {
		return interp.currentVM.setProperty(this, key, val)
	} else if obj, ok := this.AsObject(); ok {
		return obj.Set(key, val)
	}
	return nil
}

func (interp *Interpreter) setArrayLength(this engine.Value, length int) error {
	if arr, ok := this.(*engine.ArrayValue); ok {
		return arr.Set("length", engine.IntValue(length))
	}
	if interp.currentVM != nil {
		return interp.currentVM.setProperty(this, "length", engine.IntValue(length))
	} else if obj, ok := this.AsObject(); ok {
		return obj.Set("length", engine.IntValue(length))
	}
	return nil
}

func requireMutableArray(arr *engine.ArrayValue) error {
	if arr != nil && !arr.IsFullyWritable() {
		return fmt.Errorf("%w: Cannot modify frozen or sealed array", engine.ErrTypeError)
	}
	return nil
}

func (interp *Interpreter) setupArrayProto() {
	p := interp.arrayProto
	_ = p.Set("push", interp.nativeMethod("push", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			n := len(args)
			if n == 0 {
				return engine.IntValue(len(arr.Elems())), nil
			}
			// 快路径只问「末尾能否扩 n 个下标」。禁止每次数组全量
			// IsFullyWritable：那是 O(length)，1M 次 push 会退化成 O(n²)。
			if !arr.CanAppend(n) {
				return nil, fmt.Errorf("%w: Cannot modify frozen or sealed array", engine.ErrTypeError)
			}
			if arr.HasTrailingIndexAttrs(n) {
				for _, a := range args {
					_ = arr.Set(strconv.Itoa(len(arr.Elems())), a)
				}
			} else if n == 1 {
				arr.Append(args[0])
			} else {
				arr.AppendValues(args)
			}
			return engine.IntValue(len(arr.Elems())), nil
		}
		length := interp.getArrayLength(this)
		for _, a := range args {
			_ = interp.setArrayElement(this, length, a)
			length++
		}
		_ = interp.setArrayLength(this, length)
		return engine.IntValue(length), nil
	}))
	_ = p.Set("pop", interp.nativeMethod("pop", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			if len(elems) == 0 {
				return engine.Undefined(), nil
			}
			if err := requireMutableArray(arr); err != nil {
				return nil, err
			}
			last := elems[len(elems)-1]
			_ = arr.Set("length", engine.IntValue(len(elems)-1))
			return last, nil
		}
		length := interp.getArrayLength(this)
		if length == 0 {
			return engine.Undefined(), nil
		}
		last := interp.getArrayElement(this, length-1)
		_ = interp.setArrayLength(this, length-1)
		return last, nil
	}))
	_ = p.Set("shift", interp.nativeMethod("shift", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			if len(elems) == 0 {
				return engine.Undefined(), nil
			}
			if err := requireMutableArray(arr); err != nil {
				return nil, err
			}
			first := elems[0]
			rest := elems[1:]
			for i, e := range rest {
				_ = arr.Set(strconv.Itoa(i), e)
			}
			_ = arr.Set("length", engine.IntValue(len(rest)))
			return first, nil
		}
		length := interp.getArrayLength(this)
		if length == 0 {
			return engine.Undefined(), nil
		}
		first := interp.getArrayElement(this, 0)
		for i := 1; i < length; i++ {
			_ = interp.setArrayElement(this, i-1, interp.getArrayElement(this, i))
		}
		_ = interp.setArrayLength(this, length-1)
		return first, nil
	}))
	_ = p.Set("unshift", interp.nativeMethod("unshift", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			if len(args) > 0 {
				if err := requireMutableArray(arr); err != nil {
					return nil, err
				}
			}
			old := arr.Elems()
			newElems := append(append([]engine.Value{}, args...), old...)
			for i, e := range newElems {
				_ = arr.Set(strconv.Itoa(i), e)
			}
			return engine.IntValue(len(newElems)), nil
		}
		oldLen := interp.getArrayLength(this)
		argCount := len(args)
		for i := oldLen - 1; i >= 0; i-- {
			_ = interp.setArrayElement(this, i+argCount, interp.getArrayElement(this, i))
		}
		for i, a := range args {
			_ = interp.setArrayElement(this, i, a)
		}
		newLen := oldLen + argCount
		_ = interp.setArrayLength(this, newLen)
		return engine.IntValue(newLen), nil
	}))
	_ = p.Set("join", interp.nativeMethod("join", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		sep := ","
		if len(args) > 0 && !args[0].IsUndefined() {
			sep = args[0].String()
		}
		if arr, ok := this.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			parts := make([]string, len(elems))
			for i, e := range elems {
				if e.IsUndefined() || e.IsNull() {
					parts[i] = ""
				} else {
					parts[i] = e.String()
				}
			}
			return engine.Str(strings.Join(parts, sep)), nil
		}
		length := interp.getArrayLength(this)
		parts := make([]string, length)
		for i := 0; i < length; i++ {
			e := interp.getArrayElement(this, i)
			if e.IsUndefined() || e.IsNull() {
				parts[i] = ""
			} else {
				parts[i] = e.String()
			}
		}
		return engine.Str(strings.Join(parts, sep)), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			parts := make([]string, len(elems))
			for i, e := range elems {
				if e.IsUndefined() || e.IsNull() {
					parts[i] = ""
				} else {
					parts[i] = e.String()
				}
			}
			return engine.Str(strings.Join(parts, ",")), nil
		}
		length := interp.getArrayLength(this)
		parts := make([]string, length)
		for i := 0; i < length; i++ {
			e := interp.getArrayElement(this, i)
			if e.IsUndefined() || e.IsNull() {
				parts[i] = ""
			} else {
				parts[i] = e.String()
			}
		}
		return engine.Str(strings.Join(parts, ",")), nil
	}))
	_ = p.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if arr, ok := this.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			start := 0
			end := len(elems)
			if len(args) > 0 {
				if n, ok := args[0].Int(); ok {
					start = n
					if start < 0 {
						start += len(elems)
						if start < 0 {
							start = 0
						}
					}
					if start > len(elems) {
						start = len(elems)
					}
				}
			}
			if len(args) > 1 {
				if n, ok := args[1].Int(); ok {
					end = n
					if end < 0 {
						end += len(elems)
					}
					if end > len(elems) {
						end = len(elems)
					}
				}
			}
			if start > end {
				start = end
			}
			result := engine.NewArray(append([]engine.Value{}, elems[start:end]...))
			engine.SetProto(result, interp.arrayProto)
			return result, nil
		}
		length := interp.getArrayLength(this)
		start := 0
		end := length
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				start = n
				if start < 0 {
					start += length
					if start < 0 {
						start = 0
					}
				}
				if start > length {
					start = length
				}
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				end = n
				if end < 0 {
					end += length
				}
				if end > length {
					end = length
				}
			}
		}
		if start > end {
			start = end
		}
		var result []engine.Value
		for i := start; i < end; i++ {
			result = append(result, interp.getArrayElement(this, i))
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("concat", interp.nativeMethod("concat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.NewArray(nil), nil
		}
		var result []engine.Value
		result = append(result, arr.Elems()...)
		for _, a := range args {
			if aArr, ok := a.(*engine.ArrayValue); ok {
				result = append(result, aArr.Elems()...)
			} else {
				result = append(result, a)
			}
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("indexOf", interp.nativeMethod("indexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		elems := arr.Elems()
		target := args[0]
		for i, e := range elems {
			if strictEqual(e, target) {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))
	_ = p.Set("lastIndexOf", interp.nativeMethod("lastIndexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		elems := arr.Elems()
		target := args[0]
		for i := len(elems) - 1; i >= 0; i-- {
			if strictEqual(elems[i], target) {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))
	_ = p.Set("includes", interp.nativeMethod("includes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Boolean(false), nil
		}
		elems := arr.Elems()
		target := args[0]
		for _, e := range elems {
			if strictEqual(e, target) {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("reverse", interp.nativeMethod("reverse", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return this, nil
		}
		if err := requireMutableArray(arr); err != nil {
			return nil, err
		}
		elems := arr.Elems()
		for i, j := 0, len(elems)-1; i < j; i, j = i+1, j-1 {
			// 先取出两侧值再写回（Elems() 是实时视图，先写会覆盖未读值）。
			vi, vj := elems[i], elems[j]
			_ = arr.Set(strconv.Itoa(i), vj)
			_ = arr.Set(strconv.Itoa(j), vi)
		}
		return arr, nil
	}))
	_ = p.Set("forEach", interp.nativeMethod("forEach", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := argsThis(args) // N22-A2：thisArg 对非箭头函数生效
		vm := interp.currentVM
		elems := arr.Elems()
		for i, e := range elems {
			_, _ = callCb3(vm, fn, thisArg, e, engine.IntValue(i), arr)
		}
		return engine.Undefined(), nil
	}))
	_ = p.Set("map", interp.nativeMethod("map", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := argsThis(args) // N22-A2
		elems := arr.Elems()
		vm := interp.currentVM
		if result, fast := tryNumericMap(fn, elems); fast {
			vm.noteNumericCallback(true)
			out := engine.NewArray(result)
			engine.SetProto(out, interp.arrayProto)
			return out, nil
		}
		vm.noteNumericCallback(false)
		result := make([]engine.Value, len(elems))
		for i, e := range elems {
			v, err := callCb3(vm, fn, thisArg, e, engine.IntValue(i), arr)
			if err != nil {
				return nil, err
			}
			result[i] = v
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("filter", interp.nativeMethod("filter", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := argsThis(args) // N22-A2
		vm := interp.currentVM
		elems := arr.Elems()
		if result, fast := tryNumericFilter(fn, elems); fast {
			vm.noteNumericCallback(true)
			out := engine.NewArray(result)
			engine.SetProto(out, interp.arrayProto)
			return out, nil
		}
		vm.noteNumericCallback(false)
		var result []engine.Value
		for i, e := range elems {
			v, err := callCb3(vm, fn, thisArg, e, engine.IntValue(i), arr)
			if err != nil {
				return nil, err
			}
			b, _ := v.Bool()
			if b {
				result = append(result, e)
			}
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("reduce", interp.nativeMethod("reduce", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		elems := arr.Elems()
		var acc engine.Value
		startIdx := 0
		if len(args) > 1 {
			acc = args[1]
		} else {
			if len(elems) == 0 {
				return nil, fmt.Errorf("%w: Reduce of empty array with no initial value", engine.ErrTypeError)
			}
			acc = elems[0]
			startIdx = 1
		}
		if result, fast := tryNumericReduce(fn, elems, acc, startIdx); fast {
			interp.currentVM.noteNumericCallback(true)
			return result, nil
		}
		interp.currentVM.noteNumericCallback(false)
		vm := interp.currentVM
		for i := startIdx; i < len(elems); i++ {
			// Node 语义：reduce 无 thisArg 参数（callback 的 this 为 undefined）。
			v, err := callCb4(vm, fn, engine.Undefined(), acc, elems[i], engine.IntValue(i), arr)
			if err != nil {
				return nil, err
			}
			acc = v
		}
		return acc, nil
	}))

	// ES5+ 基础方法与 ES2019/ES2022/ES2023 扩展（见 array_methods.go）。
	interp.setupArrayProtoExt()
}

func (interp *Interpreter) setupArrayCtor() {
	ctor := interp.makeFunc("Array", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 1 && args[0].Type() == engine.TypeNumber {
			f, _ := args[0].Float()
			if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f != math.Trunc(f) || f > float64(uint64(1)<<32-1) {
				return nil, fmt.Errorf("%w: invalid array length", engine.ErrRangeError)
			}
			arr := engine.NewArrayHoles(int(f))
			engine.SetProto(arr, interp.arrayProto)
			return arr, nil
		}
		arr := engine.NewArray(args)
		engine.SetProto(arr, interp.arrayProto)
		return arr, nil
	})
	_ = ctor.Set("isArray", interp.makeFunc("isArray", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		v := args[0]
		for {
			if p, ok := v.(*ProxyValue); ok {
				v = p.target
			} else {
				break
			}
		}
		_, ok := v.(*engine.ArrayValue)
		return engine.Boolean(ok), nil
	}))
	interp.setupArrayCtorExt(ctor)
	_ = ctor.Set("prototype", interp.arrayProto)
	_ = interp.arrayProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Array", ctor)
	interp.constructors["Array"] = ctor
}
