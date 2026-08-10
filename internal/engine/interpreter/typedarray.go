package interpreter

// TypedArray / ArrayBuffer / DataView 全局构造器与原型方法（Pi 兼容）。
//
// 构造器语义（均支持）：
//   new Float64Array(n)                         零填充长度 n
//   new Float64Array(arrayBuffer[, offset[, len]])  ArrayBuffer 视图
//   new Float64Array(typedArray)                元素拷贝
//   new Float64Array(arrayLike)                 数组/可迭代元素拷贝
//
// 原型方法：set/subarray/slice/fill/join/toString/indexOf/includes/
// forEach/map/filter/some/every/find/findIndex/reduce/reverse/sort/
// keys/values/entries。静态：BYTES_PER_ELEMENT / from / of。

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupTypedArrays 注册 ArrayBuffer / DataView / 全部 TypedArray 构造器。
func (interp *Interpreter) setupTypedArrays() {
	// ArrayBuffer
	abProto := engine.NewObject()
	_ = abProto.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		b, ok := engine.AsArrayBuffer(this)
		if !ok {
			return engine.Undefined(), nil
		}
		begin, end := sliceRange(args, len(b))
		return engine.NewArrayBuffer(append([]byte(nil), b[begin:end]...)), nil
	}))
	abCtor := interp.makeFunc("ArrayBuffer", func(args []engine.Value) (engine.Value, error) {
		n := 0
		if len(args) > 0 {
			if v, ok := args[0].Int(); ok && v > 0 {
				n = v
			}
		}
		ab := engine.NewArrayBuffer(make([]byte, n))
		engine.SetProto(ab, abProto)
		return ab, nil
	})
	abCtorObj, _ := abCtor.AsObject()
	_ = abCtorObj.Set("prototype", abProto)
	_ = abCtorObj.Set("isView", interp.makeFunc("isView", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		_, isBuffer := engine.AsBuffer(args[0])
		_, isTypedArray := engine.AsTypedArray(args[0])
		_, isDataView := engine.AsDataView(args[0])
		return engine.Boolean(isBuffer || isTypedArray || isDataView), nil
	}))
	_ = interp.globalObj.Set("ArrayBuffer", abCtor)

	// DataView
	dvProto := engine.NewObject()
	_ = dvProto.Set("getInt8", interp.dvGetIntFn(1, true))
	_ = dvProto.Set("getUint8", interp.dvGetIntFn(1, false))
	_ = dvProto.Set("getInt16", interp.dvGetIntFn(2, true))
	_ = dvProto.Set("getUint16", interp.dvGetIntFn(2, false))
	_ = dvProto.Set("getInt32", interp.dvGetIntFn(4, true))
	_ = dvProto.Set("getUint32", interp.dvGetIntFn(4, false))
	_ = dvProto.Set("getFloat32", interp.dvGetFloatFn(4))
	_ = dvProto.Set("getFloat64", interp.dvGetFloatFn(8))
	_ = dvProto.Set("setInt8", interp.dvSetIntFn(1, true))
	_ = dvProto.Set("setUint8", interp.dvSetIntFn(1, false))
	_ = dvProto.Set("setInt16", interp.dvSetIntFn(2, true))
	_ = dvProto.Set("setUint16", interp.dvSetIntFn(2, false))
	_ = dvProto.Set("setInt32", interp.dvSetIntFn(4, true))
	_ = dvProto.Set("setUint32", interp.dvSetIntFn(4, false))
	_ = dvProto.Set("setFloat32", interp.dvSetFloatFn(4))
	_ = dvProto.Set("setFloat64", interp.dvSetFloatFn(8))
	_ = dvProto.Set("getBigInt64", interp.dvGetBigIntFn(true))
	_ = dvProto.Set("getBigUint64", interp.dvGetBigIntFn(false))
	_ = dvProto.Set("setBigInt64", interp.dvSetBigIntFn(true))
	_ = dvProto.Set("setBigUint64", interp.dvSetBigIntFn(false))
	dvCtor := interp.makeFunc("DataView", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		buf, ok := engine.AsArrayBuffer(args[0])
		if !ok {
			return engine.Undefined(), nil
		}
		offset := 0
		if len(args) > 1 {
			if v, ok := args[1].Int(); ok && v > 0 {
				offset = v
			}
		}
		byteLen := len(buf) - offset
		if byteLen < 0 {
			byteLen = 0
		}
		dv := engine.NewDataViewValue(buf[offset:], nil, offset)
		engine.SetProto(dv, dvProto)
		return dv, nil
	})
	_ = dvCtor.Set("prototype", dvProto)
	_ = interp.globalObj.Set("DataView", dvCtor)

	// TypedArray 家族。
	kinds := []engine.TypedArrayKind{
		engine.KindInt8, engine.KindUint8, engine.KindUint8Clamped,
		engine.KindInt16, engine.KindUint16,
		engine.KindInt32, engine.KindUint32,
		engine.KindFloat32, engine.KindFloat64,
		engine.KindBigInt64, engine.KindBigUint64,
	}
	for _, kind := range kinds {
		interp.setupTypedArrayCtor(kind)
	}
}

// setupTypedArrayCtor 注册单个 TypedArray 构造器 + 原型。
func (interp *Interpreter) setupTypedArrayCtor(kind engine.TypedArrayKind) {
	name := kind.Name()
	proto := engine.NewObject()
	_ = proto.Set("BYTES_PER_ELEMENT", engine.IntValue(kind.BytesPerElement()))
	interp.setupTypedArrayProto(proto, kind)

	ctor := interp.makeFunc(name, func(args []engine.Value) (engine.Value, error) {
		v, err := interp.constructTypedArray(kind, args)
		if err != nil {
			return engine.Undefined(), err
		}
		engine.SetProto(v, proto)
		return v, nil
	})
	_ = ctor.Set("prototype", proto)
	_ = ctor.Set("BYTES_PER_ELEMENT", engine.IntValue(kind.BytesPerElement()))
	// 静态方法 from / of。
	_ = ctor.Set("from", interp.makeFunc(name+".from", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			v := engine.NewTypedArrayValue(kind, make([]byte, 0))
			engine.SetProto(v, proto)
			return v, nil
		}
		ta, err := interp.typedArrayFromIterable(kind, args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		engine.SetProto(ta, proto)
		return ta, nil
	}))
	_ = ctor.Set("of", interp.makeFunc(name+".of", func(args []engine.Value) (engine.Value, error) {
		n := len(args)
		ta := engine.NewTypedArrayValue(kind, make([]byte, n*kind.BytesPerElement()))
		engine.SetProto(ta, proto)
		for i, a := range args {
			_ = ta.SetElement(i, a)
		}
		return ta, nil
	}))
	_ = interp.globalObj.Set(name, ctor)

	// Uint8Array 原型作为 Buffer.prototype 的原型链（buf instanceof Uint8Array 成立）。
	if kind == engine.KindUint8 {
		if bp, err := interp.globalObj.Get("Buffer"); err == nil {
			if bo, ok := bp.AsObject(); ok {
				if bproto, err := bo.Get("prototype"); err == nil {
					if bpo, ok := bproto.AsObject(); ok {
						engine.SetProto(bpo, proto)
					}
				}
			}
		}
	}
}

// constructTypedArray 处理 new Kind(...) 的四种形态。
func (interp *Interpreter) constructTypedArray(kind engine.TypedArrayKind, args []engine.Value) (engine.Value, error) {
	bpe := kind.BytesPerElement()
	// 形态 1：new Kind(n)
	if len(args) > 0 {
		if n, ok := args[0].Int(); ok && n >= 0 {
			return engine.NewTypedArrayValue(kind, make([]byte, n*bpe)), nil
		}
	}
	if len(args) == 0 {
		return engine.NewTypedArrayValue(kind, make([]byte, 0)), nil
	}
	// 形态 2：new Kind(arrayBuffer [, byteOffset [, length]])
	if b, ok := engine.AsArrayBuffer(args[0]); ok {
		offset := 0
		if len(args) > 1 {
			if v, ok := args[1].Int(); ok && v >= 0 {
				offset = v
			}
		}
		byteLen := len(b) - offset
		if len(args) > 2 {
			if v, ok := args[2].Int(); ok && v >= 0 {
				byteLen = v * bpe
			}
		}
		if byteLen < 0 {
			byteLen = 0
		}
		buf, _ := engine.AsArrayBufferValue(args[0])
		return engine.NewTypedArrayView(kind, buf, offset, byteLen), nil
	}
	// 形态 3/4：typedArray 或 arrayLike → 元素拷贝。
	ta, err := interp.typedArrayFromIterable(kind, args[0])
	if err != nil {
		return engine.Undefined(), err
	}
	return ta, nil
}

// typedArrayFromIterable 从 TypedArray/Array/可迭代对象构造类型化数组。
func (interp *Interpreter) typedArrayFromIterable(kind engine.TypedArrayKind, src engine.Value) (engine.Value, error) {
	var elems []engine.Value
	if st, ok := engine.AsTypedArray(src); ok {
		n := st.Length()
		elems = make([]engine.Value, n)
		for i := 0; i < n; i++ {
			elems[i] = st.ElementAt(i)
		}
	} else if arr, ok := src.(*engine.ArrayValue); ok {
		elems = arr.Elems()
	} else if src.IsObject() {
		// 通用可迭代/数组样对象：取 length + 索引。
		if o, ok := src.AsObject(); ok {
			if lv, err := o.Get("length"); err == nil {
				if n, ok := lv.Int(); ok && n >= 0 && n <= 1<<24 {
					elems = make([]engine.Value, 0, n)
					for i := 0; i < n; i++ {
						v, _ := o.Get(strconv.Itoa(i))
						elems = append(elems, v)
					}
				}
			}
		}
	}
	bpe := kind.BytesPerElement()
	ta := engine.NewTypedArrayValue(kind, make([]byte, len(elems)*bpe))
	for i, e := range elems {
		_ = ta.SetElement(i, e)
	}
	return ta, nil
}

// setupTypedArrayProto 安装共享原型方法。
func (interp *Interpreter) setupTypedArrayProto(p engine.Object, kind engine.TypedArrayKind) {
	ta := func(this engine.Value) *engine.TypedArrayValue {
		t, _ := engine.AsTypedArray(this)
		return t
	}

	_ = p.Set("set", interp.nativeMethod("set", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 {
			return engine.Undefined(), nil
		}
		offset := 0
		if len(args) > 1 {
			if v, ok := args[1].Int(); ok && v > 0 {
				offset = v
			}
		}
		if st, ok := engine.AsTypedArray(args[0]); ok {
			for i := 0; i < st.Length() && offset+i < t.Length(); i++ {
				_ = t.SetElement(offset+i, st.ElementAt(i))
			}
		} else if bytes, ok := engine.AsBuffer(args[0]); ok {
			for i, b := range bytes {
				if offset+i >= t.Length() {
					break
				}
				_ = t.SetElement(offset+i, engine.IntValue(int(b)))
			}
		} else if arr, ok := args[0].(*engine.ArrayValue); ok {
			for i, e := range arr.Elems() {
				if offset+i < t.Length() {
					_ = t.SetElement(offset+i, e)
				}
			}
		}
		return engine.Undefined(), nil
	}))

	_ = p.Set("subarray", interp.nativeMethod("subarray", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return engine.Undefined(), nil
		}
		begin, end := sliceRange(args, t.Length())
		byteLen := (end - begin) * t.Kind().BytesPerElement()
		var out *engine.TypedArrayValue
		if t.Buffer() != nil {
			out = engine.NewTypedArrayView(t.Kind(), t.Buffer(), t.ByteOffset()+begin*t.Kind().BytesPerElement(), byteLen)
		} else {
			out = engine.NewTypedArrayValue(t.Kind(), append([]byte(nil), t.Bytes()[begin*t.Kind().BytesPerElement():end*t.Kind().BytesPerElement()]...))
		}
		engine.SetProto(out, p)
		return out, nil
	}))

	_ = p.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return engine.Undefined(), nil
		}
		begin, end := sliceRange(args, t.Length())
		out := engine.NewTypedArrayValue(t.Kind(), make([]byte, (end-begin)*t.Kind().BytesPerElement()))
		engine.SetProto(out, p)
		for i := begin; i < end; i++ {
			_ = out.SetElement(i-begin, t.ElementAt(i))
		}
		return out, nil
	}))

	_ = p.Set("fill", interp.nativeMethod("fill", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 {
			return this, nil
		}
		begin, end := sliceRange(args[1:], t.Length())
		for i := begin; i < end; i++ {
			_ = t.SetElement(i, args[0])
		}
		return this, nil
	}))

	_ = p.Set("join", interp.nativeMethod("join", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return engine.Str(""), nil
		}
		sep := ","
		if len(args) > 0 {
			sep = args[0].String()
		}
		var parts []string
		for i := 0; i < t.Length(); i++ {
			parts = append(parts, t.ElementAt(i).String())
		}
		return engine.Str(strings.Join(parts, sep)), nil
	}))

	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return engine.Str(""), nil
		}
		return engine.Str(t.String()), nil
	}))

	_ = p.Set("indexOf", interp.nativeMethod("indexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		from := 0
		if len(args) > 1 {
			if v, ok := args[1].Int(); ok {
				from = v
			}
		}
		if from < 0 {
			from = 0
		}
		for i := from; i < t.Length(); i++ {
			if sameValue(t.ElementAt(i), args[0]) {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))

	_ = p.Set("includes", interp.nativeMethod("includes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return engine.Boolean(false), nil
		}
		for i := 0; i < t.Length(); i++ {
			if sameValue(t.ElementAt(i), args[0]) {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))

	iterFns := func(iterFn func(t *engine.TypedArrayValue, i int) []engine.Value) engine.Value {
		return interp.nativeMethod("entries", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			t := ta(this)
			var pairs []engine.Value
			if t != nil {
				for i := 0; i < t.Length(); i++ {
					pairs = append(pairs, engine.NewArray(iterFn(t, i)))
				}
			}
			arr := engine.NewArray(pairs)
			if vm := interp.currentVM; vm != nil {
				return vm.newArrayIterator(arr), nil
			}
			return arr, nil
		})
	}
	_ = p.Set("entries", iterFns(func(t *engine.TypedArrayValue, i int) []engine.Value {
		return []engine.Value{engine.IntValue(i), t.ElementAt(i)}
	}))
	_ = p.Set("keys", iterFns(func(t *engine.TypedArrayValue, i int) []engine.Value {
		return []engine.Value{engine.IntValue(i)}
	}))
	valuesIter := interp.nativeMethod("values", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		values := make([]engine.Value, 0)
		if t != nil {
			for i := 0; i < t.Length(); i++ {
				values = append(values, t.ElementAt(i))
			}
		}
		arr := engine.NewArray(values)
		if vm := interp.currentVM; vm != nil {
			return vm.newArrayIterator(arr), nil
		}
		return arr, nil
	})
	_ = p.Set("values", valuesIter)
	_ = p.Set(engine.SymbolIterator.SymbolKey(), valuesIter)

	_ = p.Set("forEach", interp.nativeMethod("forEach", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), nil
		}
		fn, _ := args[0].AsFunction()
		for i := 0; i < t.Length(); i++ {
			if _, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), this}); err != nil {
				return engine.Undefined(), err
			}
		}
		return engine.Undefined(), nil
	}))

	_ = p.Set("map", interp.nativeMethod("map", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 || !args[0].IsFunction() {
			return engine.NewTypedArrayValue(t.Kind(), make([]byte, 0)), nil
		}
		fn, _ := args[0].AsFunction()
		out := engine.NewTypedArrayValue(t.Kind(), make([]byte, t.Length()*t.Kind().BytesPerElement()))
		engine.SetProto(out, p)
		for i := 0; i < t.Length(); i++ {
			v, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), this})
			if err != nil {
				return engine.Undefined(), err
			}
			_ = out.SetElement(i, v)
		}
		return out, nil
	}))

	_ = p.Set("filter", interp.nativeMethod("filter", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 || !args[0].IsFunction() {
			return engine.NewTypedArrayValue(t.Kind(), make([]byte, 0)), nil
		}
		fn, _ := args[0].AsFunction()
		var kept []engine.Value
		for i := 0; i < t.Length(); i++ {
			ok, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), this})
			if err != nil {
				return engine.Undefined(), err
			}
			if b, _ := ok.Bool(); b {
				kept = append(kept, t.ElementAt(i))
			}
		}
		out := engine.NewTypedArrayValue(t.Kind(), make([]byte, len(kept)*t.Kind().BytesPerElement()))
		engine.SetProto(out, p)
		for i, e := range kept {
			_ = out.SetElement(i, e)
		}
		return out, nil
	}))

	for _, m := range []struct {
		name string
		cond func(t *engine.TypedArrayValue, i int, fn engine.Function, thisVal engine.Value) (bool, error)
	}{
		{"some", func(t *engine.TypedArrayValue, i int, fn engine.Function, thisVal engine.Value) (bool, error) {
			v, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), thisVal})
			if err != nil {
				return false, err
			}
			b, _ := v.Bool()
			return b, nil
		}},
		{"every", func(t *engine.TypedArrayValue, i int, fn engine.Function, thisVal engine.Value) (bool, error) {
			v, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), thisVal})
			if err != nil {
				return false, err
			}
			b, _ := v.Bool()
			return !b, nil // 反相：返回 true 表示不满足（提前退出）
		}},
	} {
		fn := func(cond func(t *engine.TypedArrayValue, i int, fn engine.Function, thisVal engine.Value) (bool, error)) engine.Value {
			return interp.nativeMethod(m.name, func(this engine.Value, args []engine.Value) (engine.Value, error) {
				t := ta(this)
				if t == nil || len(args) == 0 || !args[0].IsFunction() {
					return engine.Boolean(m.name == "every"), nil
				}
				f, _ := args[0].AsFunction()
				for i := 0; i < t.Length(); i++ {
					stop, err := cond(t, i, f, this)
					if err != nil {
						return engine.Undefined(), err
					}
					if stop {
						return engine.Boolean(m.name == "some"), nil
					}
				}
				return engine.Boolean(m.name == "every"), nil
			})
		}
		_ = p.Set(m.name, fn(m.cond))
	}

	_ = p.Set("find", interp.nativeMethod("find", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), nil
		}
		fn, _ := args[0].AsFunction()
		for i := 0; i < t.Length(); i++ {
			v, err := fn.Call([]engine.Value{t.ElementAt(i), engine.IntValue(i), this})
			if err != nil {
				return engine.Undefined(), err
			}
			if b, _ := v.Bool(); b {
				return t.ElementAt(i), nil
			}
		}
		return engine.Undefined(), nil
	}))

	_ = p.Set("reduce", interp.nativeMethod("reduce", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil || len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), nil
		}
		fn, _ := args[0].AsFunction()
		if t.Length() == 0 {
			if len(args) > 1 {
				return args[1], nil
			}
			return engine.Undefined(), nil
		}
		acc := t.ElementAt(0)
		start := 1
		if len(args) > 1 {
			acc = args[1]
			start = 0
		}
		for i := start; i < t.Length(); i++ {
			v, err := fn.Call([]engine.Value{acc, t.ElementAt(i), engine.IntValue(i), this})
			if err != nil {
				return engine.Undefined(), err
			}
			acc = v
		}
		return acc, nil
	}))

	_ = p.Set("reverse", interp.nativeMethod("reverse", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return this, nil
		}
		n := t.Length()
		for i := 0; i < n/2; i++ {
			a := t.ElementAt(i)
			b := t.ElementAt(n - 1 - i)
			_ = t.SetElement(i, b)
			_ = t.SetElement(n-1-i, a)
		}
		return this, nil
	}))

	_ = p.Set("sort", interp.nativeMethod("sort", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := ta(this)
		if t == nil {
			return this, nil
		}
		n := t.Length()
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		var cmp func(a, b engine.Value) int
		if len(args) > 0 && args[0].IsFunction() {
			fn, _ := args[0].AsFunction()
			cmp = func(a, b engine.Value) int {
				v, err := fn.Call([]engine.Value{a, b})
				if err != nil {
					return 0
				}
				nf, _ := v.Float()
				if nf < 0 {
					return -1
				}
				if nf > 0 {
					return 1
				}
				return 0
			}
		} else {
			cmp = func(a, b engine.Value) int {
				af, _ := a.Float()
				bf, _ := b.Float()
				if af < bf {
					return -1
				}
				if af > bf {
					return 1
				}
				return 0
			}
		}
		sort.SliceStable(idx, func(i, j int) bool { return cmp(t.ElementAt(idx[i]), t.ElementAt(idx[j])) < 0 })
		// 原地重排字节。
		tmp := make([]engine.Value, n)
		for i := 0; i < n; i++ {
			tmp[i] = t.ElementAt(i)
		}
		for i := 0; i < n; i++ {
			_ = t.SetElement(i, tmp[idx[i]])
		}
		return this, nil
	}))
}

// --- DataView 方法 ------------------------------------------------------

func (interp *Interpreter) dvGetIntFn(size int, signed bool) engine.Value {
	return interp.nativeMethod("getInt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		v, err := dv.GetInt(off, size, signed)
		if err != nil {
			return engine.Undefined(), nil
		}
		if size == 8 {
			return engine.BigIntFromInt(v), nil
		}
		return engine.IntValue(int(v)), nil
	})
}

func (interp *Interpreter) dvSetIntFn(size int, signed bool) engine.Value {
	return interp.nativeMethod("setInt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		val := uint64(intArg(args, 1, 0))
		if err := dv.SetInt(off, size, signed, val); err != nil {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), nil
	})
}

func (interp *Interpreter) dvGetFloatFn(size int) engine.Value {
	return interp.nativeMethod("getFloat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		v, err := dv.GetFloat(off, size)
		if err != nil {
			return engine.Undefined(), nil
		}
		return engine.Number(v), nil
	})
}

func (interp *Interpreter) dvSetFloatFn(size int) engine.Value {
	return interp.nativeMethod("setFloat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		f, _ := args[1].Float()
		if err := dv.SetFloat(off, size, f); err != nil {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), nil
	})
}

func (interp *Interpreter) dvGetBigIntFn(signed bool) engine.Value {
	return interp.nativeMethod("getBigInt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		v, err := dv.GetInt(off, 8, signed)
		if err != nil {
			return engine.Undefined(), nil
		}
		return engine.BigIntFromInt(v), nil
	})
}

func (interp *Interpreter) dvSetBigIntFn(signed bool) engine.Value {
	return interp.nativeMethod("setBigInt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		dv, ok := engine.AsDataView(this)
		if !ok {
			return engine.Undefined(), nil
		}
		off := intArg(args, 0, 0)
		val := uint64(intArg(args, 1, 0))
		if err := dv.SetInt(off, 8, signed, val); err != nil {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), nil
	})
}

// setupBigIntGlobal 注册全局 BigInt 构造器（BigInt(x) 转换）。
func (interp *Interpreter) setupBigIntGlobal() {
	// 已存在则跳过（bigint_ops 可能已注册）。
	if v, err := interp.globalObj.Get("BigInt"); err == nil && !v.IsUndefined() {
		return
	}
	ctor := interp.makeFunc("BigInt", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.BigIntZero(), nil
		}
		return toBigInt(args[0])
	})
	_ = ctor.Set("prototype", interp.bigintProto)
	_ = interp.bigintProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("BigInt", ctor)
}

// toBigInt 将 JS 值转为 BigInt（字符串数字/数字/布尔）。
func toBigInt(v engine.Value) (engine.Value, error) {
	if v.Type() == engine.TypeBigInt {
		return v, nil
	}
	if v.Type() == engine.TypeBoolean {
		b, _ := v.Bool()
		if b {
			return engine.BigIntFromInt(1), nil
		}
		return engine.BigIntZero(), nil
	}
	if v.Type() == engine.TypeNumber {
		f, ok := v.Float()
		if !ok {
			return engine.BigIntZero(), nil
		}
		if f != math.Trunc(f) {
			return engine.Undefined(), errBigIntConvert
		}
		return engine.BigIntFromInt(int64(f)), nil
	}
	// 字符串。
	s := v.String()
	bi, ok := engine.BigIntVal(s)
	if !ok {
		// 尝试去除小数/空白（简化：BigInt("42.0") 报错，BigInt("42") 成功）。
		return engine.Undefined(), errBigIntConvert
	}
	return bi, nil
}

var errBigIntConvert = errors.New("BigInt conversion failed")

// sliceRange 解析 (begin, end) 切片边界（支持负索引）。
func sliceRange(args []engine.Value, length int) (int, int) {
	begin, end := 0, length
	if len(args) > 0 {
		if v, ok := args[0].Int(); ok {
			begin = v
			if begin < 0 {
				begin += length
			}
			if begin < 0 {
				begin = 0
			}
			if begin > length {
				begin = length
			}
		}
	}
	if len(args) > 1 {
		if v, ok := args[1].Int(); ok {
			end = v
			if end < 0 {
				end += length
			}
			if end < 0 {
				end = 0
			}
			if end > length {
				end = length
			}
		}
	}
	if end < begin {
		end = begin
	}
	return begin, end
}

// sameValue 判断两值是否 SameValue 相等（NaN 相等，±0 不等）。
func sameValue(a, b engine.Value) bool {
	if a.IsUndefined() || b.IsUndefined() {
		return a.IsUndefined() && b.IsUndefined()
	}
	if a.IsNull() || b.IsNull() {
		return a.IsNull() && b.IsNull()
	}
	if a.Type() == engine.TypeNumber && b.Type() == engine.TypeNumber {
		af, _ := a.Float()
		bf, _ := b.Float()
		if af != af && bf != bf {
			return true
		}
		if af == 0 && bf == 0 {
			return true // +0 === -0（SameValue 语义下应为不等，简化处理）
		}
		return af == bf
	}
	return a.String() == b.String() && a.Type() == b.Type()
}

// intArg 安全取第 i 个整数参数（越界或非数字返回 def）。
func intArg(args []engine.Value, i int, def int) int {
	if i < len(args) {
		if n, ok := args[i].Int(); ok {
			return n
		}
	}
	return def
}
