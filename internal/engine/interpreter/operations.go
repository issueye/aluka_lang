package interpreter

import (
	"math"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// applyBinaryOp performs a JS binary operation.
func applyBinaryOp(op string, l, r engine.Value) engine.Value {
	switch op {
	case "+":
		// If either operand is a string, concatenate
		if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
			return engine.Str(toStr(l) + toStr(r))
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
		if rn == 0 {
			if ln == 0 {
				return engine.Number(math.NaN())
			}
			return engine.Number(math.Inf(1))
		}
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
		return engine.Boolean(compareValues(l, r) < 0)
	case "<=":
		return engine.Boolean(compareValues(l, r) <= 0)
	case ">":
		return engine.Boolean(compareValues(l, r) > 0)
	case ">=":
		return engine.Boolean(compareValues(l, r) >= 0)
	case "&":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(ln & rn))
	case "|":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(ln | rn))
	case "^":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(ln ^ rn))
	case "<<":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(ln << (uint(rn) & 31)))
	case ">>":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(ln >> (uint(rn) & 31)))
	case ">>>":
		ln, _ := l.Int()
		rn, _ := r.Int()
		return engine.Number(float64(uint32(ln) >> (uint(rn) & 31)))
	case "in":
		if o, ok := r.AsObject(); ok {
			_, err := o.Get(l.String())
			return engine.Boolean(err == nil)
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
	case engine.TypeString:
		return l.String() == r.String()
	default:
		// Object identity
		return l == r
	}
}

// looseEquals implements JS == comparison.
func looseEquals(l, r engine.Value) bool {
	if l.Type() == r.Type() {
		return strictEqual(l, r)
	}
	// null == undefined
	if (l.IsNull() && r.IsUndefined()) || (l.IsUndefined() && r.IsNull()) {
		return true
	}
	// number/string
	if l.Type() == engine.TypeNumber && r.Type() == engine.TypeString {
		if rf, ok := r.Float(); ok {
			ln, _ := l.Float()
			return ln == rf
		}
		return false
	}
	if l.Type() == engine.TypeString && r.Type() == engine.TypeNumber {
		if lf, ok := l.Float(); ok {
			rn, _ := r.Float()
			return lf == rn
		}
		return false
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

// toStr converts a value to its JS string representation.
func toStr(v engine.Value) string {
	return v.String()
}

// toNumber converts a value to a number.
func toNumber(v engine.Value) float64 {
	f, _ := v.Float()
	return f
}

// toInt32 converts a value to a 32-bit integer (JS ToInt32).
func toInt32(v engine.Value) int32 {
	f, _ := v.Float()
	return int32(f)
}

// formatNumber formats a float64 like JS Number.prototype.toString.
func formatNumber(n float64) string {
	if math.IsNaN(n) {
		return "NaN"
	}
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	if n == float64(int64(n)) && n >= -9007199254740991 && n <= 9007199254740991 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}
