package lexer

import "testing"

func TestZZTmpTemplateEscape(t *testing.T) {
	bs := "\\"
	tk := "`"
	cases := map[string]string{
		"double-slash-u-full": "const x = " + tk + bs + bs + "u${1}" + tk + "; console.log(x);",
		"double-slash-u":      tk + bs + bs + "u${1}" + tk,
		"double-n":            tk + bs + bs + "n${1}" + tk,
		"bare-u-no-interp":    tk + bs + "u0024{1}" + tk,
	}
	for name, src := range cases {
		tok, err := New(src).Next()
		if err != nil {
			t.Errorf("%s src=%q ERROR at first: %v", name, src, err)
			continue
		}
		t.Logf("%s first: type=%d value=%q raw=%q", name, int(tok.Type), tok.Value, tok.Raw)
		if tok.Type == TokenEOF {
			continue
		}
		tok2, err := New(src).Next()
		_ = tok2
		_ = err
	}
}
