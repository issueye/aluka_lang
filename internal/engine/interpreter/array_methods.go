package interpreter

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件存放 Array.prototype 与 Array 构造器的新增方法，从 builtins.go 拆出
// 以遵守"单文件 ≤ 500 行"的工程规范。所有方法在 setupArrayProto() 末尾经由
// setupArrayProtoExt() 注册，静态方法经由 setupArrayCtorExt() 注册。

// setupArrayProtoExt 注册 Array.prototype 上 ES5+/ES2019/ES2022/ES2023 扩展方法。
func (interp *Interpreter) setupArrayProtoExt() {
	p := interp.arrayProto

	// --- ES5 基础方法 -------------------------------------------------------

	// splice(start, deleteCount, ...items) 原地删除并插入元素，返回被删除元素。
	_ = p.Set("splice", interp.nativeMethod("splice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.NewArray(nil), nil
		}
		elems := arr.Elems()
		len_ := len(elems)

		// 无参数：什么都不做。
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}

		start := toIndex(args[0], len_)
		deleteCount := len_ - start // 默认删到末尾
		if len(args) > 1 {
			deleteCount = toIndexClamped(args[1], 0, len_-start)
		}

		// 收集插入项。
		var items []engine.Value
		if len(args) > 2 {
			items = append([]engine.Value{}, args[2:]...)
		}

		// 提取被删除片段。
		removed := append([]engine.Value{}, elems[start:start+deleteCount]...)

		// 重组：tail + items，写回数组。
		tail := append([]engine.Value{}, elems[start+deleteCount:]...)
		newElems := make([]engine.Value, 0, start+len(items)+len(tail))
		newElems = append(newElems, elems[:start]...)
		newElems = append(newElems, items...)
		newElems = append(newElems, tail...)
		// 通过 Set length 重建（触发 length 同步与扩缩容）。
		_ = arr.Set("length", engine.IntValue(len(newElems)))
		for i, e := range newElems {
			_ = arr.Set(strconv.Itoa(i), e)
		}

		out := engine.NewArray(removed)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))

	// sort(compareFn) 原地排序。无比较函数时按字符串 UTF-16 码点升序。
	_ = p.Set("sort", interp.nativeMethod("sort", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return this, nil
		}
		elems := arr.Elems()

		var cmp func(a, b engine.Value) int
		if len(args) > 0 {
			fn, err := asCallable(args[0])
			if err != nil {
				return nil, err
			}
			cmp = func(a, b engine.Value) int {
				v, err := fn.callWith(engine.Undefined(), []engine.Value{a, b})
				if err != nil {
					panic(err) // 由 recover 捕获并转为 error
				}
				if n, ok := v.Int(); ok {
					return n
				}
				if f, ok := v.Float(); ok {
					if f < 0 {
						return -1
					}
					if f > 0 {
						return 1
					}
					return 0
				}
				return 0
			}
		} else {
			cmp = func(a, b engine.Value) int {
				sa, sb := a.String(), b.String()
				if sa < sb {
					return -1
				}
				if sa > sb {
					return 1
				}
				return 0
			}
		}

		// 比较函数可能 panic（回调中抛出 JS 异常），这里捕获转为 error。
		var sortErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					if e, ok := r.(error); ok {
						sortErr = e
					} else {
						sortErr = fmt.Errorf("%v", r)
					}
				}
			}()
			sort.SliceStable(elems, func(i, j int) bool {
				return cmp(elems[i], elems[j]) < 0
			})
		}()
		if sortErr != nil {
			return nil, sortErr
		}

		// sort.SliceStable 已在原切片上重排，写回确保 length 正确。
		_ = arr.Set("length", engine.IntValue(len(elems)))
		return arr, nil
	}))

	// find(predicate, thisArg) 返回第一个满足谓词的元素。
	_ = p.Set("find", interp.nativeMethod("find", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		for i, e := range arr.Elems() {
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); b {
				return e, nil
			}
		}
		return engine.Undefined(), nil
	}))

	// findIndex(predicate, thisArg) 返回第一个满足谓词的索引，未找到返回 -1。
	_ = p.Set("findIndex", interp.nativeMethod("findIndex", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		for i, e := range arr.Elems() {
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); b {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))

	// some(predicate, thisArg) 是否存在满足谓词的元素。
	_ = p.Set("some", interp.nativeMethod("some", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Boolean(false), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		for i, e := range arr.Elems() {
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); b {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))

	// every(predicate, thisArg) 是否所有元素都满足谓词。
	_ = p.Set("every", interp.nativeMethod("every", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Boolean(true), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		for i, e := range arr.Elems() {
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); !b {
				return engine.Boolean(false), nil
			}
		}
		return engine.Boolean(true), nil
	}))

	// reduceRight(callback, initialValue) 从右向左归约。
	_ = p.Set("reduceRight", interp.nativeMethod("reduceRight", func(this engine.Value, args []engine.Value) (engine.Value, error) {
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
		startIdx := len(elems) - 1
		if len(args) > 1 {
			acc = args[1]
		} else {
			if len(elems) == 0 {
				return nil, fmt.Errorf("%w: Reduce of empty array with no initial value", engine.ErrTypeError)
			}
			acc = elems[len(elems)-1]
			startIdx = len(elems) - 2
		}
		for i := startIdx; i >= 0; i-- {
			v, err := fn.callWith(engine.Undefined(), []engine.Value{acc, elems[i], engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			acc = v
		}
		return acc, nil
	}))

	// fill(value, start, end) 用 value 填充 [start, end) 区间。
	_ = p.Set("fill", interp.nativeMethod("fill", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return this, nil
		}
		value := engine.Undefined()
		if len(args) > 0 {
			value = args[0]
		}
		elems := arr.Elems()
		len_ := len(elems)
		start, end := normalizeRange(len_, args[1:])
		for i := start; i < end; i++ {
			_ = arr.Set(strconv.Itoa(i), value)
		}
		return arr, nil
	}))

	// copyWithin(target, start, end) 将 [start,end) 拷贝到 target 位置（原地）。
	_ = p.Set("copyWithin", interp.nativeMethod("copyWithin", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return this, nil
		}
		elems := arr.Elems()
		len_ := len(elems)
		if len(args) == 0 {
			return arr, nil
		}
		target := toIndex(args[0], len_)
		start := 0
		if len(args) > 1 {
			start = toIndex(args[1], len_)
		}
		end := len_
		if len(args) > 2 {
			end = toIndex(args[2], len_)
		}
		if target >= len_ || start >= end || start >= len_ {
			return arr, nil
		}
		if end > len_ {
			end = len_
		}
		count := end - start
		if target+count > len_ {
			count = len_ - target
		}
		// 注意：必须先拷贝到临时切片，避免源/目标重叠时数据损坏。
		tmp := append([]engine.Value{}, elems[start:start+count]...)
		for i := 0; i < count; i++ {
			_ = arr.Set(strconv.Itoa(target+i), tmp[i])
		}
		return arr, nil
	}))

	// --- 迭代器方法（ES2015） -----------------------------------------------

	// keys() 返回索引迭代器。
	_ = p.Set("keys", interp.nativeMethod("keys", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		return interp.arrayKeyIterator(arr), nil
	}))

	// values() 返回元素迭代器（与 for...of 默认迭代器一致）。
	_ = p.Set("values", interp.nativeMethod("values", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		return interp.currentVM.newArrayIterator(arr), nil
	}))

	// entries() 返回 [index, value] 对的迭代器。
	_ = p.Set("entries", interp.nativeMethod("entries", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		return interp.arrayEntryIterator(arr), nil
	}))

	// [Symbol.iterator]() 数组默认迭代器（与 values() 一致）。缺失时
	// `[][Symbol.iterator]()` 会报 "undefined is not a function"（get-intrinsic
	// 等 npm 包依赖此协议）。
	_ = p.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		return interp.currentVM.newArrayIterator(arr), nil
	}))

	// --- ES2019 / ES2023 扩展 ------------------------------------------------

	// flat(depth) 按深度展平嵌套数组。
	_ = p.Set("flat", interp.nativeMethod("flat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.NewArray(nil), nil
		}
		depth := 1
		if len(args) > 0 && !args[0].IsUndefined() {
			// 优先按 Float 取值，以正确处理 Infinity（其 Int() 转换为垃圾值）。
			if f, ok := args[0].Float(); ok {
				switch {
				case f > 1e9 || math.IsInf(f, 1):
					depth = 1 << 30
				case f < 0:
					depth = 0
				default:
					depth = int(f)
				}
			}
		}
		result := flattenDeep(arr.Elems(), depth)
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))

	// flatMap(callback, thisArg) 先 map 再 flat(1)。
	_ = p.Set("flatMap", interp.nativeMethod("flatMap", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		var result []engine.Value
		for i, e := range arr.Elems() {
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if sub, ok := v.(*engine.ArrayValue); ok {
				result = append(result, sub.Elems()...)
			} else {
				result = append(result, v)
			}
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))

	// findLast(predicate, thisArg) 从末尾向前查找。
	_ = p.Set("findLast", interp.nativeMethod("findLast", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		elems := arr.Elems()
		for i := len(elems) - 1; i >= 0; i-- {
			e := elems[i]
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); b {
				return e, nil
			}
		}
		return engine.Undefined(), nil
	}))

	// findLastIndex(predicate, thisArg) 从末尾向前查找索引。
	_ = p.Set("findLastIndex", interp.nativeMethod("findLastIndex", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		elems := arr.Elems()
		for i := len(elems) - 1; i >= 0; i-- {
			e := elems[i]
			v, err := fn.callWith(thisArg, []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			if b, _ := v.Bool(); b {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))

	// at(index) 支持负索引的索引访问。
	_ = p.Set("at", interp.nativeMethod("at", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		elems := arr.Elems()
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		idx, ok := args[0].Int()
		if !ok {
			return engine.Undefined(), nil
		}
		if idx < 0 {
			idx += len(elems)
		}
		if idx < 0 || idx >= len(elems) {
			return engine.Undefined(), nil
		}
		return elems[idx], nil
	}))
}

// setupArrayCtorExt 注册 Array 构造器上的静态方法。
func (interp *Interpreter) setupArrayCtorExt(ctor engine.Object) {
	// Array.from(arrayLike, mapFn?, thisArg?) 从可迭代或类数组对象创建数组。
	_ = ctor.Set("from", interp.makeFunc("from", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		src := args[0]
		var mapFn callableValue
		if len(args) > 1 {
			fn, err := asCallable(args[1])
			if err != nil {
				return nil, err
			}
			mapFn = fn
		}
		thisArg := engine.Undefined()
		if len(args) > 2 {
			thisArg = args[2]
		}

		// 路径 1：源是数组，直接遍历元素。
		if arr, ok := src.(*engine.ArrayValue); ok {
			elems := arr.Elems()
			out := make([]engine.Value, len(elems))
			for i, e := range elems {
				if mapFn != nil {
					v, err := mapFn.callWith(thisArg, []engine.Value{e, engine.IntValue(i)})
					if err != nil {
						return nil, err
					}
					out[i] = v
				} else {
					out[i] = e
				}
			}
			result := engine.NewArray(out)
			engine.SetProto(result, interp.arrayProto)
			return result, nil
		}

		// 路径 2：源是字符串，按码点遍历。
		if src.Type() == engine.TypeString {
			s := src.String()
			runes := []rune(s)
			out := make([]engine.Value, len(runes))
			for i, r := range runes {
				val := engine.Str(string(r))
				if mapFn != nil {
					v, err := mapFn.callWith(thisArg, []engine.Value{val, engine.IntValue(i)})
					if err != nil {
						return nil, err
					}
					out[i] = v
				} else {
					out[i] = val
				}
			}
			result := engine.NewArray(out)
			engine.SetProto(result, interp.arrayProto)
			return result, nil
		}

		// 路径 3：类数组对象（有 length 属性的数字索引对象）。
		if obj, ok := src.AsObject(); ok {
			if lv, err := obj.Get("length"); err == nil {
				if n, ok := lv.Int(); ok && n >= 0 {
					out := make([]engine.Value, n)
					for i := 0; i < n; i++ {
						e := engine.Undefined()
						if v, err := obj.Get(strconv.Itoa(i)); err == nil {
							e = v
						}
						if mapFn != nil {
							v, err := mapFn.callWith(thisArg, []engine.Value{e, engine.IntValue(i)})
							if err != nil {
								return nil, err
							}
							out[i] = v
						} else {
							out[i] = e
						}
					}
					result := engine.NewArray(out)
					engine.SetProto(result, interp.arrayProto)
					return result, nil
				}
			}
		}

		return engine.NewArray(nil), nil
	}))

	// Array.of(...items) 将参数列表转为数组。
	_ = ctor.Set("of", interp.makeFunc("of", func(args []engine.Value) (engine.Value, error) {
		out := engine.NewArray(append([]engine.Value{}, args...))
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
}

// --- 辅助函数 ------------------------------------------------------------

// toIndex 将参数解析为 [0, len_] 范围内的索引，支持负索引。
func toIndex(v engine.Value, len_ int) int {
	n, ok := v.Int()
	if !ok {
		return 0
	}
	if n < 0 {
		n += len_
		if n < 0 {
			n = 0
		}
	} else if n > len_ {
		n = len_
	}
	return n
}

// toIndexClamped 将参数解析为 [lo, hi] 范围内的索引。
func toIndexClamped(v engine.Value, lo, hi int) int {
	n, ok := v.Int()
	if !ok {
		return lo
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// normalizeRange 从可空的 [start, end) 参数对解析出有效区间。
func normalizeRange(len_ int, args []engine.Value) (int, int) {
	start := 0
	end := len_
	if len(args) > 0 {
		start = toIndex(args[0], len_)
	}
	if len(args) > 1 {
		end = toIndex(args[1], len_)
	}
	if end < start {
		end = start
	}
	return start, end
}

// flattenDeep 递归展平嵌套数组至指定深度。
func flattenDeep(elems []engine.Value, depth int) []engine.Value {
	out := make([]engine.Value, 0, len(elems))
	var walk func(values []engine.Value, d int)
	walk = func(values []engine.Value, d int) {
		for _, v := range values {
			if sub, ok := v.(*engine.ArrayValue); ok && d > 0 {
				walk(sub.Elems(), d-1)
			} else {
				out = append(out, v)
			}
		}
	}
	walk(elems, depth)
	return out
}

// arrayKeyIterator 创建一个产出索引的迭代器对象。
func (interp *Interpreter) arrayKeyIterator(arr *engine.ArrayValue) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, interp.objectProto)
	nextFn := interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		elems := arr.Elems()
		if idx >= len(elems) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			_ = result.Set("value", engine.IntValue(idx))
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}

// arrayEntryIterator 创建一个产出 [index, value] 对的迭代器对象。
func (interp *Interpreter) arrayEntryIterator(arr *engine.ArrayValue) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, interp.objectProto)
	nextFn := interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		elems := arr.Elems()
		if idx >= len(elems) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			pair := []engine.Value{engine.IntValue(idx), elems[idx]}
			pairArr := engine.NewArray(pair)
			engine.SetProto(pairArr, interp.arrayProto)
			_ = result.Set("value", pairArr)
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}
