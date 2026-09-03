// Quick IR 指令集：Op 枚举与其字符串表示。

package jit

import (
	"fmt"
)

type Op uint8

const (
	OpConst Op = iota
	OpConstString
	OpLoadLocal
	OpStoreLocal
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpNeg
	OpNot
	OpBitNot
	OpUnaryPlus
	OpEq
	OpNe
	OpStrictEq
	OpStrictNe
	OpBitAnd
	OpBitOr
	OpBitXor
	OpShl
	OpShr
	OpUShr
	OpLt
	OpLe
	OpGt
	OpGe
	OpPop
	OpReturn
	OpReturnUndef
	OpJump
	OpJumpTrue
	OpJumpFalse
	OpJumpTrueKeep
	OpJumpFalseKeep
	OpJumpNullishKeep
	OpPushSelf
	OpSelfCall
	OpDup
	OpSwap
	OpGetProp
	OpTraceExit
	OpSetProp
	OpGuardNoopCall
	OpGuardMethodGet
	// OpLoadUpvalueNum / OpStoreUpvalueNum 读写 Number 值的捕获单元
	// （operand = upvalue 索引）。仅 trace tier：单元指针在 Go 侧，执行器在
	// 入口把数值读入缓存，写回经两阶段提交在语义出口/预算让出时一次完成。
	OpLoadUpvalueNum
	OpStoreUpvalueNum
)

func (op Op) String() string {
	names := [...]string{
		"const", "const_string", "load_local", "store_local", "add_f64", "sub_f64", "mul_f64", "div_f64", "mod_f64", "pow_f64",
		"neg_f64", "not_bool", "bit_not_i32", "number_identity",
		"eq_f64", "ne_f64", "strict_eq", "strict_ne", "bit_and_i32", "bit_or_i32", "bit_xor_i32", "shl_i32", "shr_i32", "ushr_u32", "lt_f64", "le_f64", "gt_f64", "ge_f64", "pop", "return", "return_undef", "jump", "jump_true",
		"jump_false", "jump_true_keep", "jump_false_keep", "jump_nullish_keep", "push_self", "self_call", "dup", "swap", "get_prop", "trace_exit", "set_prop", "guard_noop_call", "guard_method_get",
		"load_upvalue_f64", "store_upvalue_f64",
	}
	if int(op) >= len(names) {
		return fmt.Sprintf("op_%d", op)
	}
	return names[op]
}
