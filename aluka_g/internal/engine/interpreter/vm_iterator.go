// VM 迭代协议：同步/异步迭代器获取与内置数组、字符串迭代器。

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// getIterator obtains an iterator from an iterable value using the ES2015
// protocol. Arrays and strings are special-cased for efficiency; other objects
// must have a [Symbol.iterator] method.
func (v *VM) getIterator(iterable engine.Value) (engine.Value, error) {
	if iterable.IsNull() || iterable.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.String())
	}
	// Array: built-in array iterator.
	if arr, ok := iterable.(*engine.ArrayValue); ok {
		return v.newArrayIterator(arr), nil
	}
	// String: built-in string iterator.
	if iterable.Type() == engine.TypeString {
		return v.newStringIterator(iterable.String()), nil
	}
	// Generator: generators are their own iterators.
	if gen, ok := iterable.(*GeneratorValue); ok {
		return gen, nil
	}
	// Object with [Symbol.iterator]: look up and call the method.
	if obj, ok := iterable.AsObject(); ok {
		symKey := engine.SymbolIterator.SymbolKey()
		if iterMethod, err := obj.Get(symKey); err == nil && !iterMethod.IsUndefined() {
			return v.invoke(iterMethod, iterable, nil, false)
		}
		// 裸迭代器（有 callable next 但无 [Symbol.iterator]）：undici 等库的
		// 迭代器对象（FastIterableIterator）只实现 next。yield*/for...of 应直接
		// 使用该迭代器（ES 迭代协议；宽松兼容 Node 生态）。
		if nextFn, err := obj.Get("next"); err == nil && isCallable(nextFn) {
			return iterable, nil
		}
	}
	return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.Type())
}

// getAsyncIterator 使用 ES2018 异步迭代协议从可迭代值中获取异步迭代器。
// 优先查找 [Symbol.asyncIterator]()；若不存在则回退到 [Symbol.iterator]()
// （回退场景下 next() 返回普通值，由后续 OpAwait 经 promiseResolve 包装）。
// 数组/字符串/生成器无内置 asyncIterator，自动回退到同步迭代器。
func (v *VM) getAsyncIterator(iterable engine.Value) (engine.Value, error) {
	if iterable.IsNull() || iterable.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.String())
	}
	// 优先：对象上的 [Symbol.asyncIterator] 方法。
	if obj, ok := iterable.AsObject(); ok {
		asyncKey := engine.SymbolAsyncIterator.SymbolKey()
		if iterMethod, err := obj.Get(asyncKey); err == nil && !iterMethod.IsUndefined() {
			return v.invoke(iterMethod, iterable, nil, false)
		}
	}
	// 回退：同步迭代器协议（数组/字符串/生成器/带 [Symbol.iterator] 的对象）。
	return v.getIterator(iterable)
}

func (v *VM) newArrayIterator(arr *engine.ArrayValue) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, v.interp.objectProto)
	nextFn := v.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, v.interp.objectProto)
		elems := arr.Elems()
		if idx >= len(elems) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			_ = result.Set("value", elems[idx])
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	// Store [Symbol.iterator] so the iterator itself is iterable.
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), v.interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}

// newStringIterator creates an iterator object for a string.
func (v *VM) newStringIterator(s string) engine.Value {
	idx := 0
	runes := []rune(s)
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, v.interp.objectProto)
	nextFn := v.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, v.interp.objectProto)
		if idx >= len(runes) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			_ = result.Set("value", engine.Str(string(runes[idx])))
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), v.interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}
