package lexer

import (
	"strings"
	"testing"
)

func TestLexNumber(t *testing.T) {
	cases := map[string]string{
		"123":    "123",
		"3.14":   "3.14",
		"0x1F":   "0x1F",
		"0o17":   "0o17",
		"0b101":  "0b101",
		"1e10":   "1e10",
		"1.5e-3": "1.5e-3",
		"1_000":  "1_000",
	}
	for src, want := range cases {
		l := New(src)
		tok, err := l.Next()
		if err != nil {
			t.Errorf("lex %q: %v", src, err)
			continue
		}
		if tok.Type != TokenNumber || tok.Value != want {
			t.Errorf("lex %q: got %v, want Num(%s)", src, tok, want)
		}
	}
}

// TestLexUTF8BOM 验证 UTF-8 BOM 被剥离，不导致解析挂起/失败。
func TestLexUTF8BOM(t *testing.T) {
	src := "\xef\xbb\xbfvar x = 1;"
	l := New(src)
	toks, err := l.Tokens()
	if err != nil {
		t.Fatalf("lex BOM source: %v", err)
	}
	if len(toks) == 0 || toks[0].Type != TokenKeyword || toks[0].Value != "var" {
		t.Errorf("BOM stripped: first token = %v, want var", toks[0])
	}
}

func TestLexString(t *testing.T) {
	cases := map[string]string{
		`"hello"`:     "hello",
		`'world'`:     "world",
		`"a\nb"`:      "a\nb",
		`"tab\there"`: "tab\there",
		`"\u0041"`:    "A",
		`"\x41"`:      "A",
		`"\u{1F600}"`: "😀",
	}
	for src, want := range cases {
		l := New(src)
		tok, err := l.Next()
		if err != nil {
			t.Errorf("lex %q: %v", src, err)
			continue
		}
		if tok.Type != TokenString || tok.Value != want {
			t.Errorf("lex %q: got %v, want Str(%q)", src, tok, want)
		}
	}
}

func TestLexKeywords(t *testing.T) {
	src := "var let const function if else for while return true false null"
	l := New(src)
	expected := []string{"var", "let", "const", "function", "if", "else", "for", "while", "return", "true", "false", "null"}
	for _, kw := range expected {
		tok, err := l.Next()
		if err != nil {
			t.Fatalf("lex keyword %q: %v", kw, err)
		}
		if tok.Type != TokenKeyword || tok.Value != kw {
			t.Errorf("expected keyword %q, got %v", kw, tok)
		}
	}
}

func TestLexPunctuators(t *testing.T) {
	src := "=> === !== ... ** >>= >>>= == != <= >= && || ++ -- += << >> "
	l := New(src)
	expected := []string{"=>", "===", "!==", "...", "**", ">>=", ">>>=", "==", "!=", "<=", ">=", "&&", "||", "++", "--", "+=", "<<", ">>"}
	for _, p := range expected {
		tok, err := l.Next()
		if err != nil {
			t.Fatalf("lex punct %q: %v", p, err)
		}
		if tok.Type != TokenPunct || tok.Value != p {
			t.Errorf("expected %q, got %v", p, tok)
		}
	}
}

func TestLexComments(t *testing.T) {
	src := `// line comment
/* block comment */ 42`
	l := New(src)
	tok, err := l.Next()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if tok.Type != TokenNumber || tok.Value != "42" {
		t.Errorf("expected 42, got %v", tok)
	}
}

func TestLexRegex(t *testing.T) {
	cases := []string{
		`/abc/`,
		`/abc/gi`,
		`/a\/b/`,
		`/[a-z]+/`,
	}
	for _, src := range cases {
		l := New(src)
		tok, err := l.Next()
		if err != nil {
			t.Errorf("lex regex %q: %v", src, err)
			continue
		}
		if tok.Type != TokenRegex {
			t.Errorf("expected regex, got %v", tok)
		}
	}
}

// TestLexSlashAfterBigInt is a regression for the R1-3 exception differential
// finding: a BigInt literal must not set allowRegex, so the `/` in `1n / 0n`
// lexes as division instead of starting a regex. A regex after a BigInt
// literal is likewise impossible.
func TestLexSlashAfterBigInt(t *testing.T) {
	cases := []struct {
		src     string
		bigInts int // expected number of TokenBigInt tokens
		slashes int // expected number of division punct tokens
	}{
		{`1n / 0n`, 2, 1},
		{`7n / 1n`, 2, 1},
		{`0xFFn / 2`, 1, 1},
		{`let x = 1n / 2;`, 1, 1},
	}
	for _, tc := range cases {
		l := New(tc.src)
		tokens, err := l.Tokens()
		if err != nil {
			t.Fatalf("lex %q: %v", tc.src, err)
		}
		bigInts, slashes := 0, 0
		for _, tok := range tokens {
			if tok.Type == TokenEOF {
				continue
			}
			if tok.Type == TokenBigInt {
				bigInts++
				if !strings.HasSuffix(tok.Raw, "n") {
					t.Fatalf("lex %q BigInt raw = %q, want n suffix", tc.src, tok.Raw)
				}
			}
			if tok.Type == TokenPunct && tok.Value == "/" {
				slashes++
			}
			if tok.Type == TokenRegex {
				t.Fatalf("lex %q: unexpected regex token %v", tc.src, tok)
			}
		}
		if bigInts != tc.bigInts || slashes != tc.slashes {
			t.Fatalf("lex %q: bigInts=%d slashes=%d, want %d/%d", tc.src, bigInts, slashes, tc.bigInts, tc.slashes)
		}
	}
}

func TestLexTokens(t *testing.T) {
	src := `var x = 1 + 2;`
	l := New(src)
	tokens, err := l.Tokens()
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	// var x = 1 + 2 ; EOF = 8 个 token
	if len(tokens) != 8 {
		t.Errorf("got %d tokens, want 8", len(tokens))
	}
}

// === 模板字面量行终止符规范化（ES TV/TRV：CRLF、CR → LF；vue-sfc T1）===

func TestTemplateLiteralLineTerminatorNormalization(t *testing.T) {
	cases := []struct {
		name string
		src  string // 含真实 \r\n / \r 字节
		want string
	}{
		{"CRLF 规范化为 LF", "a\r\nb", "a\nb"},
		{"孤立 CR 规范化为 LF", "a\rb", "a\nb"},
		{"CRLF+CRLF", "\r\n\r\n", "\n\n"},
		{"混合行尾", "x\r\ny\rz\nw", "x\ny\nz\nw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lx := New("`" + tc.src + "`")
			tok, err := lx.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Type != TokenTemplate {
				t.Fatalf("token type = %v, want template", tok.Type)
			}
			if tok.Value != tc.want {
				t.Errorf("cooked = %q, want %q", tok.Value, tc.want)
			}
			if tok.Raw != tc.want {
				t.Errorf("raw = %q, want %q（TRV 同样规范化）", tok.Raw, tc.want)
			}
		})
	}
}

func TestStringLineContinuationCRLF(t *testing.T) {
	// 行续（LineContinuation）：\ + 行终止符序列整体删除——CRLF 源文件
	// 下反斜杠后跟 CR+LF 不得残留 （运行时字节构造，避免源码转义歧义）。
	lx := New("\"" + "a" + string([]byte{0x5C, 0x0D, 0x0A}) + "b" + "\"")
	tok, err := lx.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tok.Value != "ab" {
		t.Errorf("value = %q, want ab（行续删除 CRLF）", tok.Value)
	}
	lx2 := New("'" + "x" + string([]byte{0x5C, 0x0D}) + "y'")
	tok2, err := lx2.Next()
	if err != nil {
		t.Fatalf("Next2: %v", err)
	}
	if tok2.Value != "xy" {
		t.Errorf("value2 = %q, want xy（孤立 CR 行续）", tok2.Value)
	}
}
