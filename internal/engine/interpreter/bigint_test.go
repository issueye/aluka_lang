package interpreter

import "testing"

// 本文件覆盖 BigInt（ES2020，1D.12）的字面量、运算、比较、互操作。
// 注意：vmEvalStr 中除法 `/` 在某些上下文可能触发 lexer 正则歧义，
// 因此除法测试用表达式值而非语句开头形式。

// === 字面量与 typeof ===================================================

func TestBigIntLiteral(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`123n`, "123"},
		{`0n`, "0"},
		{`9007199254740993n`, "9007199254740993"}, // > Number.MAX_SAFE_INTEGER + 1
		{`0xFFn`, "255"},
		{`0o17n`, "15"},
		{`0b1010n`, "10"},
		{`1_000_000n`, "1000000"},
		{`typeof 123n`, "bigint"},
		{`typeof (1n + 2n)`, "bigint"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 算术运算 ===========================================================

func TestBigIntArithmetic(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`5n + 3n`, "8"},
		{`10n - 7n`, "3"},
		{`123n * 456n`, "56088"},
		{`2n ** 10n`, "1024"},
		{`-5n`, "-5"},
		{`-(-7n)`, "7"},
		// 除法/取模用变量形式，避免 `/` 在语句开头被 lexer 误判为正则。
		{`var a = 10n; a / 3n`, "3"}, // BigInt 整除向零截断
		{`var c = 17n; c % 5n`, "2"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 位运算 =============================================================

func TestBigIntBitwise(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`12n & 10n`, "8"},
		{`12n | 10n`, "14"},
		{`12n ^ 10n`, "6"},
		{`1n << 4n`, "16"},
		{`256n >> 2n`, "64"},
		// 大数位运算（超出 32 位）
		{`(1n << 40n)`, "1099511627776"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 比较 ===============================================================

func TestBigIntCompare(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`5n > 3n`, "true"},
		{`3n < 5n`, "true"},
		{`5n >= 5n`, "true"},
		{`3n <= 2n`, "false"},
		// BigInt 与 Number 比较
		{`5n > 3`, "true"},
		{`3 < 5n`, "true"},
		{`5n == 5`, "true"},   // == 宽松相等：BigInt == Number 做数值比较
		{`5n === 5`, "false"}, // === 不同类型
		{`5n === 5n`, "true"},
		{`5n !== 6n`, "true"},
		// == 宽松相等
		{`5n == "5"`, "true"},
		{`0n == false`, "true"},
		{`1n == true`, "true"},
		// BigInt 与 NaN/Infinity 比较：任何与 NaN 的比较都为 false，不得 panic
		// （回归：cmpBigIntFloat 曾对 NaN 直接 big.Float.SetFloat64 而崩溃，
		//  且反向比较 `NaN < 7n` 会把 NaN 哨兵求反误判为 true）。
		{`7n < NaN`, "false"},
		{`7n > NaN`, "false"},
		{`7n <= NaN`, "false"},
		{`7n >= NaN`, "false"},
		{`NaN < 7n`, "false"},
		{`NaN > 7n`, "false"},
		{`NaN <= 7n`, "false"},
		{`NaN >= 7n`, "false"},
		{`7n < Infinity`, "true"},
		{`7n > -Infinity`, "true"},
		{`7n > Infinity`, "false"},
		{`7n < -Infinity`, "false"},
		{`7n == NaN`, "false"},
		{`NaN == 7n`, "false"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 大数精度（超越 Number）============================================

func TestBigIntPrecision(t *testing.T) {
	// Number.MAX_SAFE_INTEGER + 1 与 +2 在 float64 下相等，但 BigInt 不等
	got := vmEvalStr(t, `9007199254740993n === 9007199254740993n`)
	if got != "true" {
		t.Errorf("big int self-eq = %q, want true", got)
	}
	// 大数乘法不丢失精度
	got = vmEvalStr(t, `123456789n * 987654321n`)
	if got != "121932631112635269" {
		t.Errorf("big multiply = %q, want 121932631112635269", got)
	}
}

// === 混合类型 TypeError =================================================

func TestBigIntMixedTypeError(t *testing.T) {
	cases := []string{
		`1n + 1`,    // BigInt + Number
		`1n * 2`,    // BigInt * Number
		`1n + "x"`,  // BigInt + String（不允许隐式转换）
		`1n >>> 1n`, // 无符号右移不支持
	}
	for _, code := range cases {
		_, err := vmEvalTest(t, code)
		if err == nil {
			t.Errorf("Eval(%q) should throw TypeError, got nil", code)
		}
	}
}
