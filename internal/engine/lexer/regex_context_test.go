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
