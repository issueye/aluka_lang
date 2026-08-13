package lexer

import "testing"

func TestRegexAfterControlStatementParen(t *testing.T) {
	for _, src := range []string{
		`for (let i = 0; i < lines.length; i++) /[^ \t]/.exec(lines[i]);`,
		`if (value) /a+/.test(value);`,
	} {
		tokens, err := New(src).Tokens()
		if err != nil {
			t.Fatalf("Tokens(%q): %v", src, err)
		}
		found := false
		for _, token := range tokens {
			found = found || token.Type == TokenRegex
		}
		if !found {
			t.Fatalf("Tokens(%q) did not contain a regex", src)
		}
	}
}

func TestCallParenStillPrecedesDivision(t *testing.T) {
	tokens, err := New(`value() / 2`).Tokens()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if token.Type == TokenRegex {
			t.Fatal("slash after a call expression was parsed as a regex")
		}
	}
}

// TestRegexAfterBlock 验证块结束后语句开头的 / 被识别为正则字面量，而非
// 除法（braceControl 跟踪 { 是块还是对象字面量）。
func TestRegexAfterBlock(t *testing.T) {
	for _, src := range []string{
		`if (true) { 1; } /abc/.test('abc');`,
		`for (;;) { break; } /x/.test(s);`,
		`while (x) { x--; } /[a-z]+/.exec(s);`,
	} {
		tokens, err := New(src).Tokens()
		if err != nil {
			t.Fatalf("Tokens(%q): %v", src, err)
		}
		found := false
		for _, token := range tokens {
			found = found || token.Type == TokenRegex
		}
		if !found {
			t.Fatalf("Tokens(%q) did not contain a regex", src)
		}
	}
}

// TestObjectLiteralStillPrecedesDivision 验证对象字面量结尾的 } 后仍是除法
// （不因 braceControl 误判为正则）。
func TestObjectLiteralStillPrecedesDivision(t *testing.T) {
	for _, src := range []string{
		`var x = {a:1} / 2;`,
		`foo({a:1} / 2);`,
	} {
		tokens, err := New(src).Tokens()
		if err != nil {
			t.Fatalf("Tokens(%q): %v", src, err)
		}
		for _, token := range tokens {
			if token.Type == TokenRegex {
				t.Fatalf("slash after object literal in %q was parsed as a regex", src)
			}
		}
	}
}
