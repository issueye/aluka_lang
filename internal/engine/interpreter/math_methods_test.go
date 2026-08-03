package interpreter

import (
	"math"
	"strconv"
	"testing"
)

// 本文件覆盖 Math 对象补全的方法与常量。浮点结果用 strconv.FormatFloat
// 产出与 JS Number.toString 一致的最短精确表示，再与引擎输出对照。
// 风格对齐 vm_test.go。

// fmtNum 将 float64 转为与 JS Number.toString 一致的字符串。
// JS 用最短的可逆十进制表示，等价于 Go 的 strconv.FormatFloat(f, 'g', -1, 64)，
// 但需对整数结果去掉科学计数法。这里复用引擎自身的 numberValue.String()：
// 把数字直接作为字面量求值，让引擎自己格式化，确保格式一致。
func fmtNum(f float64) string {
	t := &testing.T{}
	return vmEvalStr(t, `(`+strconv.FormatFloat(f, 'g', -1, 64)+`)`)
}

func TestMathSignTrunc(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{`Math.sign(5)`, 1},
		{`Math.sign(-5)`, -1},
		{`Math.sign(0)`, 0},
		{`Math.trunc(3.7)`, 3},
		{`Math.trunc(-3.7)`, -3},
		{`Math.cbrt(27)`, 3},
		{`Math.cbrt(-8)`, -2},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		want := fmtNum(c.want)
		if got != want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, want)
		}
	}
}

func TestMathExpLog(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{`Math.log1p(0)`, 0},
		{`Math.log1p(Math.E - 1)`, 1}, // ln(1 + (e-1)) = ln(e) = 1
		{`Math.expm1(0)`, 0},
		{`Math.expm1(1)`, math.E - 1}, // e^1 - 1
		{`Math.log(Math.E)`, 1},
	}
	for _, c := range cases {
		// 比较数值相等（避免 Go 与 JS 的 Number.toString 末位舍入差异）。
		v, err := vmEvalTest(t, c.code)
		if err != nil {
			t.Fatalf("Eval(%q) error: %v", c.code, err)
		}
		got, ok := v.Float()
		if !ok {
			t.Errorf("Eval(%q) not a number: %q", c.code, v.String())
			continue
		}
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("Eval(%q) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestMathHyperbolic(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{`Math.sinh(0)`, 0},
		{`Math.cosh(0)`, 1},
		{`Math.tanh(0)`, 0},
		{`Math.asinh(0)`, 0},
		{`Math.acosh(1)`, 0},
		{`Math.atanh(0)`, 0},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		want := fmtNum(c.want)
		if got != want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, want)
		}
	}
}

func TestMathInverseTrig(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{`Math.asin(1)`, math.Pi / 2},
		{`Math.acos(1)`, 0},
		{`Math.atan(1)`, math.Pi / 4},
		{`Math.atan2(1, 1)`, math.Pi / 4},
		{`Math.atan2(0, -1)`, math.Pi}, // +0, -1 → π
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		want := fmtNum(c.want)
		if got != want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, want)
		}
	}
}

func TestMathInt32Ops(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Math.imul(3, 4)`, "12"},
		{`Math.imul(0x10000, 0x10000)`, "0"}, // 32 位回绕溢出
		{`Math.clz32(1)`, "31"},
		{`Math.clz32(0)`, "32"},
		{`Math.clz32(0x80000000)`, "0"},
		{`Math.clz32(0x40000000)`, "1"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestMathConstants(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{`Math.LOG2E`, 1 / math.Ln2},
		{`Math.LOG10E`, math.Log10E},
		{`Math.SQRT1_2`, 1 / math.Sqrt2},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		want := fmtNum(c.want)
		if got != want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, want)
		}
	}
}

func TestMathFround(t *testing.T) {
	// fround 折算到 float32 精度。1.5 可精确表示。
	got := vmEvalStr(t, `Math.fround(1.5)`)
	if got != "1.5" {
		t.Errorf("fround(1.5) = %q, want 1.5", got)
	}
	// 0 保持 0
	got = vmEvalStr(t, `Math.fround(0)`)
	if got != "0" {
		t.Errorf("fround(0) = %q, want 0", got)
	}
}
