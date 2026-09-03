package engine

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// StringToNumber converts a String's content to a Number with the engine's
// JS ToNumber rules: leading/trailing whitespace is trimmed, the empty string
// is 0, 0x/0X hexadecimal and 0o/0O octal prefixes are accepted, everything
// else parses as a decimal float. Unparseable input returns NaN. This is the
// canonical string-to-number conversion used by the loose-equality helper; it
// mirrors the interpreter's ToNumber (jsStringToNumber) exactly so all tiers
// share one rule set.
func StringToNumber(s string) float64 {
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

// LooseEquals implements the JavaScript abstract equality comparison (==) for
// the engine's value domain: Undefined, Null, Boolean, Number, String, BigInt,
// Symbol and object identity. It is the shared primitive-equality helper used
// by the JIT tiers (Quick and Trace); the interpreter's looseEquals keeps its
// own implementation (see the R3-3 report for the recorded Tier 0 string-parse
// divergence this helper intentionally fixes).
//
// Semantics (per ES abstract equality, as the engine models it):
//   - BigInt compares with BigInt by value, with Number by exact numeric
//     comparison (NaN/±Infinity never equal), with Boolean after the Boolean
//     converts to 0/1, and with String by parsing the String as a decimal
//     BigInt ("" and non-decimal text never equal).
//   - Same-type values compare strictly (Number NaN != NaN, +0 == -0, String
//     by value, BigInt by value, Symbol and Object by identity).
//   - null == undefined.
//   - Number vs String converts the String with the engine's ToNumber rules
//     (StringToNumber): "" == 0, "0x10" == 16, "2" == 2.
//   - Boolean converts to 0/1 and compares numerically.
//   - Object operands never undergo ToPrimitive: objects compare by identity
//     and an object is never equal to a primitive. The JIT tiers guard object
//     operands away before calling this helper.
func LooseEquals(l, r Value) bool {
	if l == nil || r == nil {
		return false
	}
	if eq, handled := looseBigIntEquals(l, r); handled {
		return eq
	}
	if l.Type() == r.Type() {
		return looseStrictEquals(l, r)
	}
	// null == undefined.
	if (l.IsNull() && r.IsUndefined()) || (l.IsUndefined() && r.IsNull()) {
		return true
	}
	// Number vs String: the String side converts with the engine's ToNumber
	// rules. A failed parse is NaN, which never equals any Number.
	if l.Type() == TypeNumber && r.Type() == TypeString {
		ln, _ := l.Float()
		return ln == StringToNumber(r.String())
	}
	if l.Type() == TypeString && r.Type() == TypeNumber {
		rn, _ := r.Float()
		return StringToNumber(l.String()) == rn
	}
	// Boolean operands convert to 0/1 and compare numerically.
	if l.Type() == TypeBoolean {
		return LooseEquals(numberFromBool(l), r)
	}
	if r.Type() == TypeBoolean {
		return LooseEquals(l, numberFromBool(r))
	}
	return false
}

// looseStrictEquals is the same-type branch of LooseEquals: it implements ===
// semantics (Number NaN != NaN, +0 == -0, String by value, BigInt by value,
// Symbol and Object by identity).
func looseStrictEquals(l, r Value) bool {
	switch l.Type() {
	case TypeUndefined, TypeNull:
		return true
	case TypeBoolean:
		lb, _ := l.Bool()
		rb, _ := r.Bool()
		return lb == rb
	case TypeNumber:
		ln, _ := l.Float()
		rn, _ := r.Float()
		return ln == rn
	case TypeBigInt:
		lb, lok := BigIntValue(l)
		rb, rok := BigIntValue(r)
		return lok && rok && lb.Cmp(rb) == 0
	case TypeString:
		return l.String() == r.String()
	default:
		// Symbol and object identity.
		return l == r
	}
}

// looseBigIntEquals handles == where at least one side is a BigInt, mirroring
// Tier 0's bigintLooseEqual dispatch. The second return value reports whether
// the pair was handled.
func looseBigIntEquals(l, r Value) (bool, bool) {
	lb, lok := BigIntValue(l)
	rb, rok := BigIntValue(r)
	switch {
	case lok && rok:
		return lb.Cmp(rb) == 0, true
	case lok && r.Type() == TypeNumber:
		rf, _ := r.Float()
		return cmpBigIntFloat(lb, rf) == 0, true
	case rok && l.Type() == TypeNumber:
		lf, _ := l.Float()
		return cmpBigIntFloat(rb, lf) == 0, true
	case lok && r.Type() == TypeBoolean:
		// JS: ToBoolean converts to 0/1, then BigInt == Number compares
		// exactly. Tier 0's formula `(l==0n) != b` is wrong for l!=0n with
		// b==true (7n == true must be false; recorded Tier 0 bug); this helper
		// implements the spec result.
		b, _ := r.Bool()
		return cmpBigIntFloat(lb, boolFloat(b)) == 0, true
	case rok && l.Type() == TypeBoolean:
		b, _ := l.Bool()
		return cmpBigIntFloat(rb, boolFloat(b)) == 0, true
	case lok && r.Type() == TypeString:
		bi, ok := BigIntVal(r.String())
		if !ok {
			return false, true // String does not parse as a BigInt -> not equal
		}
		bb, _ := BigIntValue(bi)
		return lb.Cmp(bb) == 0, true
	case rok && l.Type() == TypeString:
		bi, ok := BigIntVal(l.String())
		if !ok {
			return false, true
		}
		bb, _ := BigIntValue(bi)
		return rb.Cmp(bb) == 0, true
	}
	return false, false
}

// cmpBigIntFloat compares a BigInt with a float64. NaN is incomparable
// (returns 2, matching the interpreter's sentinel), and ±Infinity orders
// before/after every BigInt. The comparison is exact via big.Float, identical
// to the interpreter's cmpBigIntFloat.
func cmpBigIntFloat(bi *big.Int, f float64) int {
	if math.IsNaN(f) {
		return 2
	}
	if math.IsInf(f, 1) {
		return -1 // any BigInt is smaller than +Infinity
	}
	if math.IsInf(f, -1) {
		return 1 // any BigInt is greater than -Infinity
	}
	bf := new(big.Float).SetInt(bi)
	ff := new(big.Float).SetFloat64(f)
	return bf.Cmp(ff)
}

func numberFromBool(v Value) Value {
	b, _ := v.Bool()
	if b {
		return Number(1)
	}
	return Number(0)
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
