package interpreter

import (
	"math"
	"math/bits"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件补全 Math 对象上缺失的标准方法与常量，从 builtins.go 拆出以遵守
// "单文件 ≤ 500 行"规范。在 setupMath() 中通过 setupMathExt() 注册到 Math 对象。

// setupMathExt 注册 Math 对象上缺失的标准方法与常量。
// 全部基于 Go math 标准库 1:1 映射，语义与 ECMA-262 Math 一致。
func (interp *Interpreter) setupMathExt(m engine.Object) {
	// 单参数函数：复用与 builtins.go setupMath 相同的模式。
	mathFunc := func(name string, fn func(float64) float64) {
		_ = m.Set(name, interp.makeFunc(name, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Number(math.NaN()), nil
			}
			f, _ := args[0].Float()
			return engine.Number(fn(f)), nil
		}))
	}

	// --- 取整与符号 ----------------------------------------------------------
	mathFunc("trunc", math.Trunc) // 截断小数部分
	mathFunc("sign", mathSign)    // 符号：-1 / 0 / 1 / NaN
	mathFunc("fround", func(f float64) float64 {
		return float64(float32(f)) // 折算到 32 位浮点再回填
	})

	// --- 指数/对数族 ---------------------------------------------------------
	mathFunc("cbrt", math.Cbrt)   // 立方根
	mathFunc("log1p", math.Log1p) // ln(1+x)
	mathFunc("expm1", math.Expm1) // e^x - 1

	// --- 双曲函数族 ----------------------------------------------------------
	mathFunc("sinh", math.Sinh)
	mathFunc("cosh", math.Cosh)
	mathFunc("tanh", math.Tanh)
	mathFunc("asinh", math.Asinh)
	mathFunc("acosh", math.Acosh)
	mathFunc("atanh", math.Atanh)

	// --- 反三角函数族 --------------------------------------------------------
	mathFunc("asin", math.Asin)
	mathFunc("acos", math.Acos)
	mathFunc("atan", math.Atan)

	// --- 双参数函数 ----------------------------------------------------------
	// atan2(y, x) 返回 atan(y/x)，范围 [-π, π]，象限由两参数符号决定。
	_ = m.Set("atan2", interp.makeFunc("atan2", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Number(math.NaN()), nil
		}
		y, _ := args[0].Float()
		x, _ := args[1].Float()
		return engine.Number(math.Atan2(y, x)), nil
	}))

	// --- 32 位整数运算 -------------------------------------------------------
	// imul(a, b) 32 位整数乘法（C 风格回绕）。
	_ = m.Set("imul", interp.makeFunc("imul", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.IntValue(0), nil
		}
		a, _ := args[0].Int()
		b, _ := args[1].Int()
		return engine.IntValue(int(int32(a) * int32(b))), nil
	}))

	// clz32(x) 32 位前导零计数。
	_ = m.Set("clz32", interp.makeFunc("clz32", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(32), nil
		}
		n, _ := args[0].Int()
		return engine.IntValue(bits.LeadingZeros32(uint32(n))), nil
	}))

	// --- 多参数函数 ----------------------------------------------------------
	// hypot(...) 平方和开方（ES 21.3.2.18）：支持任意参数，任一为 NaN 返回
	// NaN（先于 Infinity 检查），全 0 返回 +0。jsToNumber 保证 ToNumber
	// 语义（hypot(3, "4") === 5）。
	_ = m.Set("hypot", interp.makeFunc("hypot", func(args []engine.Value) (engine.Value, error) {
		var sum float64
		for _, a := range args {
			f := jsToNumber(a)
			if math.IsNaN(f) {
				return engine.Number(math.NaN()), nil
			}
			sum = math.Hypot(sum, f)
		}
		return engine.Number(sum), nil
	}))

	// --- 缺失常量 ------------------------------------------------------------
	_ = m.Set("LOG2E", engine.Number(1/math.Ln2))     // log2(e) = 1/ln(2)
	_ = m.Set("LOG10E", engine.Number(math.Log10E))   // log10(e)
	_ = m.Set("SQRT1_2", engine.Number(1/math.Sqrt2)) // 1/√2 = √2/2
}

// mathSign 实现 Math.sign：负数返回 -1，正数返回 1，0 返回 ±0，NaN 返回 NaN。
func mathSign(f float64) float64 {
	switch {
	case math.IsNaN(f):
		return math.NaN()
	case f > 0:
		return 1
	case f < 0:
		return -1
	default: // +0 或 -0
		return f
	}
}
