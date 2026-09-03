package interpreter

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// updateNumeric implements the ToNumeric part of ++/--. BigInt preserves its
// type, Symbol throws, and all other currently modeled values use ToNumber.
func updateNumeric(value engine.Value, delta int64) (engine.Value, error) {
	if isBigInt(value) {
		bi := new(big.Int).Set(asBigInt(value))
		return engine.BigInt(bi.Add(bi, big.NewInt(delta))), nil
	}
	if value != nil && value.Type() == engine.TypeSymbol {
		return nil, fmt.Errorf("%w: Cannot convert a Symbol value to a number", engine.ErrTypeError)
	}
	return engine.Number(jsToNumber(value) + float64(delta)), nil
}

const uint32Range = 4294967296.0

// jsToUint32/jsToInt32 implement the Number conversion used by JavaScript
// bitwise operators. Host-sized int conversion is not equivalent on 64-bit
// systems (for example, 2147483648 | 0 must become -2147483648).
func jsToUint32(n float64) uint32 {
	if math.IsNaN(n) || math.IsInf(n, 0) || n == 0 {
		return 0
	}
	n = math.Mod(math.Trunc(n), uint32Range)
	if n < 0 {
		n += uint32Range
	}
	return uint32(n)
}

func jsToInt32(n float64) int32 { return int32(jsToUint32(n)) }

// applyBinaryOp performs a JS binary operation.
func applyBinaryOp(op string, l, r engine.Value) engine.Value {
	switch op {
	case "+":
		// If either operand is a string, concatenate
		if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
			return engine.ConcatStrings(l, r)
		}
		ln, lok := l.Float()
		rn, rok := r.Float()
		if lok && rok {
			return engine.Number(ln + rn)
		}
		return engine.Number(math.NaN())
	case "-":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln - rn)
	case "*":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln * rn)
	case "/":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(ln / rn)
	case "%":
		ln, _ := l.Float()
		rn, _ := r.Float()
		if rn == 0 {
			return engine.Number(math.NaN())
		}
		return engine.Number(math.Mod(ln, rn))
	case "**":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(math.Pow(ln, rn))
	case "==":
		return engine.Boolean(looseEquals(l, r))
	case "!=":
		return engine.Boolean(!looseEquals(l, r))
	case "===":
		return engine.Boolean(strictEqual(l, r))
	case "!==":
		return engine.Boolean(!strictEqual(l, r))
	case "<":
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c < 0 }))
	case "<=":
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c <= 0 }))
	case ">":
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c > 0 }))
	case ">=":
		return engine.Boolean(compareBool(l, r, func(c int) bool { return c >= 0 }))
	case "&":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) & jsToInt32(rn)))
	case "|":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) | jsToInt32(rn)))
	case "^":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) ^ jsToInt32(rn)))
	case "<<":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) << (jsToUint32(rn) & 31)))
	case ">>":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToInt32(ln) >> (jsToUint32(rn) & 31)))
	case ">>>":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return engine.Number(float64(jsToUint32(ln) >> (jsToUint32(rn) & 31)))
	case "in":
		if o, ok := r.AsObject(); ok {
			key := propertyKeyOf(l)
			for cur := o; cur != nil; cur = engine.GetProto(cur) {
				if hasOwn(cur, key) {
					return engine.Boolean(true)
				}
			}
		}
		return engine.Boolean(false)
	case "instanceof":
		// Simplified: check if r is a function and l's prototype chain includes r.prototype
		if f, ok := r.AsFunction(); ok {
			_ = f
			if o, ok := l.AsObject(); ok {
				proto, _ := r.AsObject()
				if proto != nil {
					if p, err := proto.Get("prototype"); err == nil {
						if protoObj, ok := p.AsObject(); ok {
							cur := engine.GetProto(o)
							for cur != nil {
								if cur == protoObj {
									return engine.Boolean(true)
								}
								cur = engine.GetProto(cur)
							}
						}
					}
				}
			}
		}
		return engine.Boolean(false)
	}
	return engine.Undefined()
}

// strictEqual implements JS === comparison.
func strictEqual(l, r engine.Value) bool {
	if l.Type() != r.Type() {
		return false
	}
	switch l.Type() {
	case engine.TypeUndefined, engine.TypeNull:
		return true
	case engine.TypeBoolean:
		lb, _ := l.Bool()
		rb, _ := r.Bool()
		return lb == rb
	case engine.TypeNumber:
		ln, _ := l.Float()
		rn, _ := r.Float()
		return ln == rn
	case engine.TypeBigInt:
		return bigintStrictEqual(l, r)
	case engine.TypeString:
		return l.String() == r.String()
	default:
		// Object identity
		return l == r
	}
}

// looseEquals implements JS == comparison.
func looseEquals(l, r engine.Value) bool {
	// BigInt 相关的宽松相等（BigInt == Number/String/Boolean）。
	if ok, handled := bigintLooseEqual(l, r); handled {
		return ok
	}
	if l.Type() == r.Type() {
		return strictEqual(l, r)
	}
	// null == undefined
	if (l.IsNull() && r.IsUndefined()) || (l.IsUndefined() && r.IsNull()) {
		return true
	}
	// number/string
	if l.Type() == engine.TypeNumber && r.Type() == engine.TypeString {
		ln, _ := l.Float()
		return ln == engine.StringToNumber(r.String())
	}
	if l.Type() == engine.TypeString && r.Type() == engine.TypeNumber {
		rn, _ := r.Float()
		return engine.StringToNumber(l.String()) == rn
	}
	// bool → number
	if l.Type() == engine.TypeBoolean {
		lb, _ := l.Bool()
		v := engine.Number(0)
		if lb {
			v = engine.Number(1)
		}
		return looseEquals(v, r)
	}
	if r.Type() == engine.TypeBoolean {
		rb, _ := r.Bool()
		v := engine.Number(0)
		if rb {
			v = engine.Number(1)
		}
		return looseEquals(l, v)
	}
	return false
}

// compareValues implements JS relational comparison (< > <= >=).
func compareValues(l, r engine.Value) int {
	// BigInt 相关的比较（BigInt vs BigInt / Number）。
	if isBigInt(l) || isBigInt(r) {
		return bigintCompare(l, r)
	}
	if l.Type() == engine.TypeString && r.Type() == engine.TypeString {
		return strings.Compare(l.String(), r.String())
	}
	ln, _ := l.Float()
	rn, _ := r.Float()
	if math.IsNaN(ln) || math.IsNaN(rn) {
		return 2 // undefined comparison; caller treats any non-zero as false
	}
	if ln < rn {
		return -1
	}
	if ln > rn {
		return 1
	}
	return 0
}

// compareBool applies a relational operator to compareValues with JS NaN
// semantics: the NaN sentinel (2) makes every relational comparison false
// (< > <= >=), regardless of which side carries the NaN.
func compareBool(l, r engine.Value, op func(int) bool) bool {
	cmp := compareValues(l, r)
	if cmp == 2 || cmp == -2 {
		return false
	}
	return op(cmp)
}

// jsToNumber 实现 JS ToNumber 语义（ES 规范 §7.1.3）。
// 修正：非数字字符串返回 NaN（而非 0），undefined → NaN，null → 0。
func jsToNumber(v engine.Value) float64 {
	if v == nil {
		return math.NaN()
	}
	switch v.Type() {
	case engine.TypeNumber:
		f, _ := v.Float()
		return f
	case engine.TypeBoolean:
		b, _ := v.Bool()
		if b {
			return 1
		}
		return 0
	case engine.TypeString:
		return jsStringToNumber(v.String())
	case engine.TypeNull:
		return 0
	case engine.TypeUndefined:
		return math.NaN()
	default:
		// 对象：简化经 String() 转字符串再解析。
		return jsStringToNumber(v.String())
	}
}

// jsStringToNumber 解析字符串为 JS 数字（Number("") == 0，Number("0xFF") 十六进制，
// 非法 → NaN）。
func jsStringToNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		if n, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
			return float64(n)
		}
		return math.NaN()
	}
	if len(s) > 2 && (s[:2] == "0o" || s[:2] == "0O") {
		if n, err := strconv.ParseInt(s[2:], 8, 64); err == nil {
			return float64(n)
		}
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// toInt32 converts a value to a 32-bit integer (JS ToInt32).
func toInt32(v engine.Value) int32 {
	f, _ := v.Float()
	return jsToInt32(f)
}
