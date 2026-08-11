package interpreter

// O-6 运行时执行器：对 bytecode.NativeCallbackDesc（微指令序列）描述的
// 简单回调（x => x*2、x => x.v+1 等箭头函数）在 Go 侧小栈求值，跳过
// 每元素完整调用链（callClosure 帧设置 + run 解释）。
//
// 语义等价性：cbBinOp/cbCmp 复刻主循环 OpAdd..OpUShr / OpEq..OpGe 的
// 逐分支逻辑（含 BigInt 分支与除零/NaN 规则）；慢路径（非 NativeCallback）
// 仍走原调用链。

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// execNativeCallback 执行简单回调（微指令小栈求值）。
// args 为回调参数（map/filter: [x]、[x, i, arr]；reduce: [acc, x] 等）。
// 参数缺失时按 JS 语义补 undefined（回调参数未传即为 undefined）。
func (v *VM) execNativeCallback(tmpl *bytecode.FuncTemplate, nc *bytecode.NativeCallbackDesc, args []engine.Value) (engine.Value, error) {
	var stackBuf [8]engine.Value
	stack := stackBuf[:0]
	arg := func(i int) engine.Value {
		if i < len(args) {
			return args[i]
		}
		return engine.Undefined()
	}
	for _, in := range nc.Instrs {
		switch in.Op {
		case bytecode.CBPushParam0:
			stack = append(stack, arg(0))
		case bytecode.CBPushParam1:
			stack = append(stack, arg(1))
		case bytecode.CBPushConst:
			stack = append(stack, tmpl.Constants[in.Operand])
		case bytecode.CBPushProp0:
			pv, err := v.nativePropGet(arg(0), tmpl.Constants[in.Operand].String())
			if err != nil {
				return engine.Undefined(), err
			}
			stack = append(stack, pv)
		case bytecode.CBPushProp1:
			pv, err := v.nativePropGet(arg(1), tmpl.Constants[in.Operand].String())
			if err != nil {
				return engine.Undefined(), err
			}
			stack = append(stack, pv)
		case bytecode.CBNeg:
			top := stack[len(stack)-1]
			if isBigInt(top) {
				stack[len(stack)-1] = bigintNeg(top)
			} else {
				n, _ := top.Float()
				stack[len(stack)-1] = engine.Number(-n)
			}
		case bytecode.CBBinOp:
			r := stack[len(stack)-1]
			l := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			res, err := v.nativeArith(bytecode.Opcode(in.Operand), l, r)
			if err != nil {
				return engine.Undefined(), err
			}
			stack = append(stack, res)
		case bytecode.CBCmp:
			r := stack[len(stack)-1]
			l := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			stack = append(stack, v.nativeCompare(bytecode.Opcode(in.Operand), l, r))
		default:
			return engine.Undefined(), fmt.Errorf("unknown native callback instr %d", in.Op)
		}
	}
	if len(stack) == 0 {
		return engine.Undefined(), fmt.Errorf("native callback produced no value")
	}
	return stack[len(stack)-1], nil
}

// callCb 调用回调：优先 NativeCallback 快路径（O-6），否则走正常调用链。
func callCb(vm *VM, fn callableValue, thisArg engine.Value, args []engine.Value) (engine.Value, error) {
	if vm != nil {
		if vc, ok := fn.(*vmClosure); ok && vc.tmpl != nil && vc.tmpl.NativeCallback != nil {
			return vm.execNativeCallback(vc.tmpl, vc.tmpl.NativeCallback, args)
		}
	}
	return fn.callWith(thisArg, args)
}

func callCb2(vm *VM, fn callableValue, thisArg, arg0, arg1 engine.Value) (engine.Value, error) {
	if vm != nil {
		if vc, ok := fn.(*vmClosure); ok && vc.tmpl != nil && vc.tmpl.NativeCallback != nil {
			args := [2]engine.Value{arg0, arg1}
			return vm.execNativeCallback(vc.tmpl, vc.tmpl.NativeCallback, args[:])
		}
	}
	return fn.callWith(thisArg, []engine.Value{arg0, arg1})
}

func callCb3(vm *VM, fn callableValue, thisArg, arg0, arg1, arg2 engine.Value) (engine.Value, error) {
	if vm != nil {
		if vc, ok := fn.(*vmClosure); ok && vc.tmpl != nil && vc.tmpl.NativeCallback != nil {
			args := [3]engine.Value{arg0, arg1, arg2}
			return vm.execNativeCallback(vc.tmpl, vc.tmpl.NativeCallback, args[:])
		}
	}
	return fn.callWith(thisArg, []engine.Value{arg0, arg1, arg2})
}

func callCb4(vm *VM, fn callableValue, thisArg, arg0, arg1, arg2, arg3 engine.Value) (engine.Value, error) {
	if vm != nil {
		if vc, ok := fn.(*vmClosure); ok && vc.tmpl != nil && vc.tmpl.NativeCallback != nil {
			args := [4]engine.Value{arg0, arg1, arg2, arg3}
			return vm.execNativeCallback(vc.tmpl, vc.tmpl.NativeCallback, args[:])
		}
	}
	return fn.callWith(thisArg, []engine.Value{arg0, arg1, arg2, arg3})
}

func nativeCallbackClosure(fn callableValue) (*vmClosure, *bytecode.NativeCallbackDesc, bool) {
	cl, ok := fn.(*vmClosure)
	if !ok || cl.tmpl == nil || !cl.tmpl.IsArrow || cl.tmpl.NativeCallback == nil {
		return nil, nil, false
	}
	return cl, cl.tmpl.NativeCallback, true
}

// tryNumericMap recognizes x => x * K for arrays already containing Numbers.
func tryNumericMap(fn callableValue, elems []engine.Value) ([]engine.Value, bool) {
	cl, desc, ok := nativeCallbackClosure(fn)
	if !ok || len(desc.Instrs) != 3 || desc.Instrs[2].Op != bytecode.CBBinOp ||
		bytecode.Opcode(desc.Instrs[2].Operand) != bytecode.OpMul {
		return nil, false
	}
	constIndex := -1
	if desc.Instrs[0].Op == bytecode.CBPushParam0 && desc.Instrs[1].Op == bytecode.CBPushConst {
		constIndex = int(desc.Instrs[1].Operand)
	} else if desc.Instrs[1].Op == bytecode.CBPushParam0 && desc.Instrs[0].Op == bytecode.CBPushConst {
		constIndex = int(desc.Instrs[0].Operand)
	} else {
		return nil, false
	}
	if constIndex < 0 || constIndex >= len(cl.tmpl.Constants) || cl.tmpl.Constants[constIndex].Type() != engine.TypeNumber {
		return nil, false
	}
	for _, value := range elems {
		if value == nil || value.Type() != engine.TypeNumber {
			return nil, false
		}
	}
	factor, _ := cl.tmpl.Constants[constIndex].Float()
	result := make([]engine.Value, len(elems))
	for i, value := range elems {
		number, _ := value.Float()
		result[i] = engine.Number(number * factor)
	}
	return result, true
}

// tryNumericFilter recognizes x => x % K === C for Number arrays.
func tryNumericFilter(fn callableValue, elems []engine.Value) ([]engine.Value, bool) {
	cl, desc, ok := nativeCallbackClosure(fn)
	if !ok || len(desc.Instrs) != 5 || desc.Instrs[0].Op != bytecode.CBPushParam0 ||
		desc.Instrs[1].Op != bytecode.CBPushConst || desc.Instrs[2].Op != bytecode.CBBinOp ||
		bytecode.Opcode(desc.Instrs[2].Operand) != bytecode.OpMod || desc.Instrs[3].Op != bytecode.CBPushConst ||
		desc.Instrs[4].Op != bytecode.CBCmp || bytecode.Opcode(desc.Instrs[4].Operand) != bytecode.OpStrictEq {
		return nil, false
	}
	modIndex, expectedIndex := int(desc.Instrs[1].Operand), int(desc.Instrs[3].Operand)
	if modIndex < 0 || modIndex >= len(cl.tmpl.Constants) || expectedIndex < 0 || expectedIndex >= len(cl.tmpl.Constants) ||
		cl.tmpl.Constants[modIndex].Type() != engine.TypeNumber || cl.tmpl.Constants[expectedIndex].Type() != engine.TypeNumber {
		return nil, false
	}
	for _, value := range elems {
		if value == nil || value.Type() != engine.TypeNumber {
			return nil, false
		}
	}
	modulus, _ := cl.tmpl.Constants[modIndex].Float()
	expected, _ := cl.tmpl.Constants[expectedIndex].Float()
	result := make([]engine.Value, 0, len(elems))
	for _, value := range elems {
		number, _ := value.Float()
		if math.Mod(number, modulus) == expected {
			result = append(result, value)
		}
	}
	return result, true
}

// tryNumericReduce recognizes (acc, x) => acc + x for Number arrays.
func tryNumericReduce(fn callableValue, elems []engine.Value, initial engine.Value, start int) (engine.Value, bool) {
	_, desc, ok := nativeCallbackClosure(fn)
	if !ok || len(desc.Instrs) != 3 || desc.Instrs[0].Op != bytecode.CBPushParam0 ||
		desc.Instrs[1].Op != bytecode.CBPushParam1 || desc.Instrs[2].Op != bytecode.CBBinOp ||
		bytecode.Opcode(desc.Instrs[2].Operand) != bytecode.OpAdd {
		return nil, false
	}
	acc, ok := initial.Float()
	if !ok {
		return nil, false
	}
	for i := start; i < len(elems); i++ {
		if elems[i] == nil || elems[i].Type() != engine.TypeNumber {
			return nil, false
		}
	}
	for i := start; i < len(elems); i++ {
		number, _ := elems[i].Float()
		acc += number
	}
	return engine.Number(acc), true
}

// nativeArith 执行算术/位运算（复刻主循环 OpAdd..OpUShr 语义）。
func (v *VM) nativeArith(op bytecode.Opcode, l, r engine.Value) (engine.Value, error) {
	lb, rb := isBigInt(l), isBigInt(r)
	switch op {
	case bytecode.OpAdd:
		if lb || rb {
			return bigintArith2(l, r, '+')
		}
		return v.binAdd(l, r), nil
	case bytecode.OpSub:
		if lb || rb {
			return bigintArith2(l, r, '-')
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln - rn), nil
	case bytecode.OpMul:
		if lb || rb {
			return bigintArith2(l, r, '*')
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln * rn), nil
	case bytecode.OpDiv:
		if lb || rb {
			return bigintArith2(l, r, '/')
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln / rn), nil
	case bytecode.OpMod:
		if lb || rb {
			return bigintArith2(l, r, '%')
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		if rn == 0 {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(math.Mod(ln, rn)), nil
	case bytecode.OpPow:
		if lb || rb {
			return bigintPow(l, r)
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(math.Pow(ln, rn)), nil
	case bytecode.OpBitAnd:
		if lb || rb {
			return bigintBitwise(l, r, "&")
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) & jsToInt32(rn))), nil
	case bytecode.OpBitOr:
		if lb || rb {
			return bigintBitwise(l, r, "|")
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) | jsToInt32(rn))), nil
	case bytecode.OpBitXor:
		if lb || rb {
			return bigintBitwise(l, r, "^")
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) ^ jsToInt32(rn))), nil
	case bytecode.OpShl:
		if lb || rb {
			return bigintBitwise(l, r, "<<")
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) << (jsToUint32(rn) & 31))), nil
	case bytecode.OpShr:
		if lb || rb {
			return bigintBitwise(l, r, ">>")
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) >> (jsToUint32(rn) & 31))), nil
	case bytecode.OpUShr:
		if lb || rb {
			return engine.Undefined(), fmt.Errorf("%w: BigInts have no unsigned right shift, use >> instead", engine.ErrTypeError)
		}
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToUint32(ln) >> (jsToUint32(rn) & 31))), nil
	}
	return engine.Undefined(), fmt.Errorf("unsupported native arith op %d", op)
}

// nativeCompare 执行比较（复刻主循环 OpEq..OpGe 语义）。
func (v *VM) nativeCompare(op bytecode.Opcode, l, r engine.Value) engine.Value {
	switch op {
	case bytecode.OpEq:
		return engine.Boolean(looseEquals(l, r))
	case bytecode.OpNe:
		return engine.Boolean(!looseEquals(l, r))
	case bytecode.OpStrictEq:
		return engine.Boolean(strictEqual(l, r))
	case bytecode.OpStrictNe:
		return engine.Boolean(!strictEqual(l, r))
	case bytecode.OpLt:
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c < 0 }))
	case bytecode.OpLe:
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c <= 0 }))
	case bytecode.OpGt:
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c > 0 }))
	case bytecode.OpGe:
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c >= 0 }))
	}
	return engine.Undefined()
}

// nativePropGet 执行属性读 x.name（数组 length 走快路径）。
func (v *VM) nativePropGet(x engine.Value, name string) (engine.Value, error) {
	if name == "length" {
		if n, ok := engine.StringLen(x); ok {
			return engine.Number(float64(n)), nil
		}
		if arr, ok := x.(*engine.ArrayValue); ok {
			return engine.Number(float64(len(arr.Elems()))), nil
		}
	}
	return v.getProperty(x, name)
}
