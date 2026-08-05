package lexer

import (
	"testing"
)

func TestLexNumber(t *testing.T) {
	cases := map[string]string{
		"123":      "123",
		"3.14":     "3.14",
		"0x1F":     "0x1F",
		"0o17":     "0o17",
		"0b101":    "0b101",
		"1e10":     "1e10",
		"1.5e-3":   "1.5e-3",
		"1_000":    "1_000",
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
		`"hello"`:       "hello",
		`'world'`:        "world",
		`"a\nb"`:         "a\nb",
		`"tab\there"`:    "tab\there",
		`"\u0041"`:       "A",
		`"\x41"`:         "A",
		`"\u{1F600}"`:    "😀",
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
