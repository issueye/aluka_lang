package interpreter

import (
	"fmt"
	"math"
	"math/big"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件集中实现 BigInt（ES2020）的算术/比较/相等运算，避免在 vm.go 的各
// case 里散布 BigInt 分支。所有函数接收 engine.Value 操作数，在任一操作数
// 为 BigInt 时走 math/big 计算；混合 BigInt 与 Number 时按 ES 规范抛 TypeError。
//
// 设计要点：BigIntValue 的 Float()/Int() 返回 (0,false)，因此现有
// l.Float() 路径遇 BigInt 会静默变 0。本文件在各运算入口先用类型判断拦截。

// isBigInt 报告值是否为 BigInt 类型。
func isBigInt(v engine.Value) bool { return v.Type() == engine.TypeBigInt }

// asBigInt 取出 BigInt 值的底层 *big.Int（调用方不应修改返回值）。
func asBigInt(v engine.Value) *big.Int {
	bi, _ := engine.BigIntValue(v)
	return bi
}

// bigIntTypeError 抛出 BigInt 混合运算的 TypeError。
func bigIntTypeError(op string) error {
	return fmt.Errorf("%w: cannot mix BigInt and other types, use explicit conversions", engine.ErrTypeError)
}

// bigintArith2 执行双操作数算术（+ - * / %），其中至少一个操作数是 BigInt。
// 返回 BigInt 结果或 TypeError（当混合 Number/其他类型时）。
func bigintArith2(l, r engine.Value, op byte) (engine.Value, error) {
	// 两侧都必须是 BigInt（BigInt 与 Number 混合算术是 TypeError）。
	if !isBigInt(l) || !isBigInt(r) {
		return nil, bigIntTypeError(string(op))
	}
	lb := asBigInt(l)
	rb := asBigInt(r)
	result := new(big.Int)
	switch op {
	case '+':
		result.Add(lb, rb)
	case '-':
		result.Sub(lb, rb)
	case '*':
		result.Mul(lb, rb)
	case '/':
		if rb.Sign() == 0 {
			return nil, fmt.Errorf("%w: Division by zero", engine.ErrRangeError)
		}
		// BigInt 除法向零截断（与 C/JS 一致）。
		result.Quo(lb, rb)
	case '%':
		if rb.Sign() == 0 {
			return nil, fmt.Errorf("%w: Division by zero", engine.ErrRangeError)
		}
		result.Rem(lb, rb)
	}
	return engine.BigInt(result), nil
}

// bigintPow 计算 BigInt 幂运算 l ** r（r 必须 >= 0 的 BigInt）。
func bigintPow(l, r engine.Value) (engine.Value, error) {
	if !isBigInt(l) || !isBigInt(r) {
		return nil, bigIntTypeError("^")
	}
	rb := asBigInt(r)
	if rb.Sign() < 0 {
		return nil, fmt.Errorf("%w: Exponent must be non-negative", engine.ErrRangeError)
	}
	result := new(big.Int).Exp(asBigInt(l), rb, nil)
	return engine.BigInt(result), nil
}

// bigintNeg 返回 BigInt 的相反数。
func bigintNeg(v engine.Value) engine.Value {
	result := new(big.Int).Neg(asBigInt(v))
	return engine.BigInt(result)
}

// bigintCompare 比较 BigInt（或 BigInt 与 Number）。返回 -1/0/1，或 2 表示不可比较（NaN）。
func bigintCompare(l, r engine.Value) int {
	// BigInt 与 BigInt
	if isBigInt(l) && isBigInt(r) {
		return asBigInt(l).Cmp(asBigInt(r))
	}
	// BigInt 与 Number（混合数值比较，ES 规范允许）。
	// NaN 必须先于求反处理：`NaN < 7n` 若把 NaN 哨兵 2 求反成 -2，
	// 会误判为“小于”，必须原样返回 2（任何与 NaN 的比较都为 false）。
	if isBigInt(l) && r.Type() == engine.TypeNumber {
		rf, _ := r.Float()
		if math.IsNaN(rf) {
			return 2
		}
		return cmpBigIntFloat(asBigInt(l), rf)
	}
	if isBigInt(r) && l.Type() == engine.TypeNumber {
		lf, _ := l.Float()
		if math.IsNaN(lf) {
			return 2
		}
		return -cmpBigIntFloat(asBigInt(r), lf)
	}
	return 2 // 不可比较（如 BigInt 与 String）
}

// cmpBigIntFloat 比较 BigInt 与 float64。NaN 不可比（返回 2），
// ±Infinity 直接按大小返回，避免 big.Float.SetFloat64(NaN) panic。
func cmpBigIntFloat(bi *big.Int, f float64) int {
	if math.IsNaN(f) {
		return 2 // 与 NaN 的任何比较都为 false
	}
	if math.IsInf(f, 1) {
		return -1 // 任何 BigInt 都小于 +Infinity
	}
	if math.IsInf(f, -1) {
		return 1 // 任何 BigInt 都大于 -Infinity
	}
	// 用 big.Float 做精确比较。
	bf := new(big.Float).SetInt(bi)
	ff := new(big.Float).SetFloat64(f)
	return bf.Cmp(ff)
}

// bigintStrictEqual 检查两个 BigInt 是否相等。
func bigintStrictEqual(l, r engine.Value) bool {
	if !isBigInt(l) || !isBigInt(r) {
		return false
	}
	return asBigInt(l).Cmp(asBigInt(r)) == 0
}

// bigintLooseEqual 处理 == 语义下 BigInt 与其他类型的比较。
// BigInt == BigInt → 值比较；BigInt == Number → 数值比较；
// BigInt == String → 将 String 转 BigInt 后比较；BigInt == Boolean → Boolean 转 Number 后比较。
func bigintLooseEqual(l, r engine.Value) (bool, bool) {
	// BigInt == BigInt
	if isBigInt(l) && isBigInt(r) {
		return asBigInt(l).Cmp(asBigInt(r)) == 0, true
	}
	// BigInt == Number
	if isBigInt(l) && r.Type() == engine.TypeNumber {
		rf, _ := r.Float()
		return cmpBigIntFloat(asBigInt(l), rf) == 0, true
	}
	if isBigInt(r) && l.Type() == engine.TypeNumber {
		lf, _ := l.Float()
		return cmpBigIntFloat(asBigInt(r), lf) == 0, true
	}
	// BigInt == Boolean: the boolean converts to 0/1 and compares exactly
	// (7n == true is false; the old formula (x != 0n) != b was wrong for
	// BigInts outside {0n, 1n}).
	if isBigInt(l) && r.Type() == engine.TypeBoolean {
		b, _ := r.Bool()
		bi := engine.BigIntFromInt(0)
		if b {
			bi = engine.BigIntFromInt(1)
		}
		return bigintStrictEqual(l, bi), true
	}
	if isBigInt(r) && l.Type() == engine.TypeBoolean {
		b, _ := l.Bool()
		bi := engine.BigIntFromInt(0)
		if b {
			bi = engine.BigIntFromInt(1)
		}
		return bigintStrictEqual(r, bi), true
	}
	// BigInt == String：将 String 转 BigInt 后比较
	if isBigInt(l) && r.Type() == engine.TypeString {
		if bi, ok := engine.BigIntVal(r.String()); ok {
			return bigintStrictEqual(l, bi), true
		}
		return false, true // String 不能解析为 BigInt → 不等
	}
	if isBigInt(r) && l.Type() == engine.TypeString {
		if bi, ok := engine.BigIntVal(l.String()); ok {
			return bigintStrictEqual(r, bi), true
		}
		return false, true
	}
	return false, false // 未处理，交回原逻辑
}

// bigintBitwise 执行 BigInt 位运算（& | ^ << >>）。
func bigintBitwise(l, r engine.Value, op string) (engine.Value, error) {
	if !isBigInt(l) || !isBigInt(r) {
		return nil, bigIntTypeError(op)
	}
	lb := asBigInt(l)
	rb := asBigInt(r)
	result := new(big.Int)
	switch op {
	case "&":
		result.And(lb, rb)
	case "|":
		result.Or(lb, rb)
	case "^":
		result.Xor(lb, rb)
	case "<<":
		if rb.Sign() < 0 {
			return nil, fmt.Errorf("%w: BigInt negative shift", engine.ErrRangeError)
		}
		result.Lsh(lb, uint(rb.Uint64()))
	case ">>":
		if rb.Sign() < 0 {
			return nil, fmt.Errorf("%w: BigInt negative shift", engine.ErrRangeError)
		}
		result.Rsh(lb, uint(rb.Uint64()))
	}
	return engine.BigInt(result), nil
}
