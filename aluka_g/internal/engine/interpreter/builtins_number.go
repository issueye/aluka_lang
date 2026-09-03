// 内置数值与布尔：Number.prototype/Number 构造器、Boolean 构造器、BigInt.prototype 与 Math 命名空间。

package interpreter

import (
	"math"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

func (interp *Interpreter) setupBigIntProto() {
	p := interp.bigintProto
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		bi, ok := engine.BigIntValue(this)
		if !ok {
			return engine.Str("0"), nil
		}
		radix := 10
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok && n >= 2 && n <= 36 {
				radix = n
			}
		}
		return engine.Str(bi.Text(radix)), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return this, nil
	}))
}

func (interp *Interpreter) setupNumberProto() {
	p := interp.numberProto
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			radix, ok := args[0].Int()
			if ok && radix >= 2 && radix <= 36 {
				n, _ := this.Float()
				// 整数：按 radix 进制输出（Node 语义，如 4660..toString(16) → "1234"）。
				if !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) {
					return engine.Str(strconv.FormatInt(int64(n), radix)), nil
				}
				// 非整数：Node 输出近似小数，M2 简化为十进制。
				return engine.Str(strconv.FormatFloat(n, 'f', -1, 64)), nil
			}
		}
		return engine.Str(this.String()), nil
	}))
	_ = p.Set("toFixed", interp.nativeMethod("toFixed", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		digits := 0
		if len(args) > 0 {
			if d, ok := args[0].Int(); ok {
				digits = d
			}
		}
		if digits < 0 {
			digits = 0
		}
		if digits > 20 {
			digits = 20
		}
		return engine.Str(strconv.FormatFloat(n, 'f', digits, 64)), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		f, _ := this.Float()
		return engine.Number(f), nil
	}))
	_ = p.Set("toPrecision", interp.nativeMethod("toPrecision", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Str(strconv.FormatFloat(n, 'g', -1, 64)), nil
		}
		prec, _ := args[0].Int()
		return engine.Str(strconv.FormatFloat(n, 'g', prec, 64)), nil
	}))
	_ = p.Set("toExponential", interp.nativeMethod("toExponential", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		digits := -1
		if len(args) > 0 && !args[0].IsUndefined() {
			digits, _ = args[0].Int()
		}
		return engine.Str(strconv.FormatFloat(n, 'e', digits, 64)), nil
	}))
}

func (interp *Interpreter) setupNumberCtor() {
	ctor := interp.makeFunc("Number", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(0), nil
		}
		// BigInt → Number：十进制文本解析为 float64（ES ToNumber 语义：
		// 超精度时取近似值）。
		if bi, ok := engine.BigIntValue(args[0]); ok {
			f, _ := strconv.ParseFloat(bi.String(), 64)
			return engine.Number(f), nil
		}
		f, ok := args[0].Float()
		if !ok {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(f), nil
	})
	_ = ctor.Set("MAX_SAFE_INTEGER", engine.Number(9007199254740991))
	_ = ctor.Set("MIN_SAFE_INTEGER", engine.Number(-9007199254740991))
	_ = ctor.Set("MAX_VALUE", engine.Number(math.MaxFloat64))
	_ = ctor.Set("MIN_VALUE", engine.Number(5e-324))
	_ = ctor.Set("POSITIVE_INFINITY", engine.Number(math.Inf(1)))
	_ = ctor.Set("NEGATIVE_INFINITY", engine.Number(math.Inf(-1)))
	_ = ctor.Set("NaN", engine.Number(math.NaN()))
	_ = ctor.Set("EPSILON", engine.Number(2.220446049250313e-16))
	_ = ctor.Set("isFinite", interp.makeFunc("isFinite", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0)), nil
	}))
	_ = ctor.Set("isNaN", interp.makeFunc("isNaN", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(!ok || math.IsNaN(f)), nil
	}))
	_ = ctor.Set("isInteger", interp.makeFunc("isInteger", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == float64(int64(f))), nil
	}))
	_ = ctor.Set("isSafeInteger", interp.makeFunc("isSafeInteger", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0) &&
			f == math.Trunc(f) && math.Abs(f) <= 9007199254740991), nil
	}))
	_ = ctor.Set("prototype", interp.numberProto)
	_ = interp.numberProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Number", ctor)
	interp.constructors["Number"] = ctor
}

func (interp *Interpreter) setupBooleanCtor() {
	ctor := interp.makeFunc("Boolean", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		b, _ := args[0].Bool()
		return engine.Boolean(b), nil
	})
	_ = ctor.Set("prototype", interp.booleanProto)
	_ = interp.booleanProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Boolean", ctor)
	interp.constructors["Boolean"] = ctor
}

func (interp *Interpreter) setupMath() {
	m := engine.NewObject()
	mathFunc := func(name string, fn func(float64) float64) {
		_ = m.Set(name, interp.makeFunc(name, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Number(math.NaN()), nil
			}
			f, _ := args[0].Float()
			return engine.Number(fn(f)), nil
		}))
	}
	mathFunc("abs", math.Abs)
	mathFunc("floor", math.Floor)
	mathFunc("ceil", math.Ceil)
	mathFunc("round", func(f float64) float64 { return math.RoundToEven(f) })
	mathFunc("sqrt", math.Sqrt)
	mathFunc("sin", math.Sin)
	mathFunc("cos", math.Cos)
	mathFunc("tan", math.Tan)
	mathFunc("log", math.Log)
	mathFunc("log2", math.Log2)
	mathFunc("log10", math.Log10)
	mathFunc("exp", math.Exp)
	_ = m.Set("PI", engine.Number(math.Pi))
	_ = m.Set("E", engine.Number(math.E))
	_ = m.Set("LN2", engine.Number(math.Ln2))
	_ = m.Set("LN10", engine.Number(math.Log(10)))
	_ = m.Set("SQRT2", engine.Number(math.Sqrt2))
	_ = m.Set("max", interp.makeFunc("max", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.Inf(-1)), nil
		}
		result := math.Inf(-1)
		for _, a := range args {
			f, _ := a.Float()
			if math.IsNaN(f) {
				return engine.Number(math.NaN()), nil
			}
			if f > result {
				result = f
			}
		}
		return engine.Number(result), nil
	}))
	_ = m.Set("min", interp.makeFunc("min", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.Inf(1)), nil
		}
		result := math.Inf(1)
		for _, a := range args {
			f, _ := a.Float()
			if math.IsNaN(f) {
				return engine.Number(math.NaN()), nil
			}
			if f < result {
				result = f
			}
		}
		return engine.Number(result), nil
	}))
	_ = m.Set("pow", interp.makeFunc("pow", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Number(math.NaN()), nil
		}
		x, _ := args[0].Float()
		y, _ := args[1].Float()
		return engine.Number(math.Pow(x, y)), nil
	}))
	_ = m.Set("random", interp.makeFunc("random", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(0.5), nil // simplified; no math/rand for determinism
	}))
	// Math 扩展方法与常量（见 math_methods.go）。
	interp.setupMathExt(m)
	_ = interp.globalObj.Set("Math", m)
}
