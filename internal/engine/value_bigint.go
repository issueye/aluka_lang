// BigInt 值：任意精度整数的 engine.Value 实现与构造入口。

package engine

import (
	"math/big"
)

// bigIntValue 包装 math/big.Int 实现 JS BigInt 值类型。
// 关键语义：Float() 与 Int() 都返回 (0, false) 以阻断所有 float/int 路径，
// 强制算术/位运算在分发处用类型判断走 math/big 计算。
type bigIntValue struct{ val *big.Int }

// BigInt 从 *big.Int 创建 BigInt 值（内部会拷贝，避免外部修改）。
func BigInt(i *big.Int) Value {
	if i == nil {
		return BigIntZero()
	}
	c := new(big.Int).Set(i)
	return bigIntValue{val: c}
}

// BigIntFromInt 从 int64 创建 BigInt 值。
func BigIntFromInt(i int64) Value {
	return bigIntValue{val: big.NewInt(i)}
}

// BigIntZero 返回值为 0 的 BigInt（常用，避免重复分配）。
func BigIntZero() Value { return bigIntValue{val: big.NewInt(0)} }

// BigIntVal 从字符串解析 BigInt（十进制）。
func BigIntVal(s string) (Value, bool) {
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Undefined(), false
	}
	return bigIntValue{val: bi}, true
}

func (b bigIntValue) Type() ValueType { return TypeBigInt }

func (b bigIntValue) String() string { return b.val.String() }

func (b bigIntValue) Int() (int, bool) { return 0, false } // 阻断 Int 路径

func (b bigIntValue) Float() (float64, bool) { return 0, false } // 阻断 Float 路径

func (b bigIntValue) Bool() (bool, bool) { return b.val.Sign() != 0, true }

func (b bigIntValue) IsUndefined() bool { return false }

func (b bigIntValue) IsNull() bool { return false }

func (b bigIntValue) IsObject() bool { return false }

func (b bigIntValue) IsFunction() bool { return false }

func (b bigIntValue) AsObject() (Object, bool) { return nil, false }

func (b bigIntValue) AsFunction() (Function, bool) { return nil, false }

// BigIntValue 是 bigIntValue 的公开访问器（用于 VM 算术运算取底层 *big.Int）。
// 返回底层 *big.Int（只读，调用方不应修改）。
func BigIntValue(v Value) (*big.Int, bool) {
	b, ok := v.(bigIntValue)
	if !ok {
		return nil, false
	}
	return b.val, true
}
