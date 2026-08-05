package parser

import (
	"testing"
)

func TestZZBisect(t *testing.T) {
	cases := map[string]string{
		"for-less-than + generic fn": `for (let i = 0; i < n; i++) {} function f<T = Record<string, unknown>>(x: string): T { return x as T; }`,
		"generic fn only":            `function f<T = Record<string, unknown>>(x: string): T { return x as T; }`,
		"less-than only":             `for (let i = 0; i < n; i++) {} function g() {}`,
	}
	for name, src := range cases {
		p, err := NewFromString(src)
		if err != nil {
			t.Logf("%s: LEX err %v", name, err)
			continue
		}
		if _, err := p.parseProgram(); err != nil {
			t.Logf("%s: FAIL %v", name, err)
		} else {
			t.Logf("%s: OK", name)
		}
	}
}
