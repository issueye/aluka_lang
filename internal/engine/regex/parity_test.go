package regex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// paritySubjects 是对拍语料的目标串集合：覆盖语料模式的高频域（注释/
// URL/data URI/空白/CSS 形态）与通用形态。
var paritySubjects = []string{
	"",
	"a",
	"A",
	"ab",
	"abab",
	"0",
	"12",
	"1234567",
	"hello world",
	"Hello World",
	"foo bar baz",
	"-",
	"--",
	"a-b_c",
	"  spaced  ",
	"line1\nline2",
	"/* block */ code // line",
	"v-bind(foo)",
	"https://example.com/path?q=1#frag",
	"//cdn.example.com/x.js",
	"data:text/plain;base64,SGk=",
	"\r\n",
	"\t \n \f",
	"user@example.com",
	"AAA111aaa",
	"{}",
	"[]",
	"undefined",
	"true null",
	"class=\"btn\" :id=\"x\"",
	"éclair",
	"😀a",
	"a\u2028b",
	"a\u2029b",
}

// loadCorpus 读取 testdata/corpus.txt（pattern \t flags）。
func loadCorpus(t *testing.T) []struct{ pat, flags string } {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "corpus.txt"))
	if err != nil {
		t.Fatalf("read corpus: %v（先运行 node tools/extract-regex-corpus.mjs）", err)
	}
	var out []struct{ pat, flags string }
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		pat := parts[0]
		flags := ""
		if len(parts) == 2 && parts[1] != "-" {
			flags = parts[1]
		}
		out = append(out, struct{ pat, flags string }{pat, flags})
	}
	return out
}

// equalIdx 比较两份匹配索引（nil = 无匹配）。
func equalIdx(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEngineParityOnCorpus：同一模式在 RE2 翻译层与自研回溯引擎上的首个
// 匹配结果必须逐索引一致（语料来自 Vue demo 依赖闭包的真实模式）。
// 仅任一侧不支持的模式计 skipped（如 bt 不支持 \p{}，RE2 不支持 lookaround）。
func TestEngineParityOnCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	checked, skipped := 0, 0
	for _, c := range corpus {
		f, err := ParseFlags(c.flags)
		if err != nil {
			skipped++
			continue
		}
		// RE2 侧：translate + Go regexp（与 Compile 主路径一致）。
		prefix := ""
		if f.IgnoreCase {
			prefix += "(?i)"
		}
		if f.Multiline {
			prefix += "(?m)"
		}
		goSrc, terr := translate(c.pat, f)
		var re *regexp.Regexp
		if terr == nil {
			re, err = regexp.Compile(prefix + goSrc)
			if err != nil {
				t.Errorf("RE2 compile %q flags=%q: %v", c.pat, c.flags, err)
				continue
			}
		}
		// bt 侧：强制走回溯引擎（绕过 Compile 的引擎选择）。
		bt, berr := compileBacktrack(c.pat, f)
		if berr != nil || re == nil {
			skipped++
			continue
		}
		for _, subj := range paritySubjects {
			want := re.FindStringSubmatchIndex(subj)
			got, aborted, _ := bt.execWithLimit(subj, 0, btMaxSteps)
			if aborted {
				t.Errorf("fallback budget exhausted %q flags=%q subj=%q", c.pat, c.flags, subj)
				continue
			}
			if !equalIdx(want, got) {
				t.Errorf("parity drift %q flags=%q subj=%q:\n  RE2=%v\n  bt =%v", c.pat, c.flags, subj, want, got)
			}
		}
		checked++
	}
	t.Logf("corpus parity: checked=%d skipped=%d subjects=%d", checked, skipped, len(paritySubjects))
	if checked == 0 {
		t.Fatal("no corpus pattern was parity-checkable")
	}
}

type nodeOracleCase struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Flags   string `json:"flags"`
	Input   string `json:"input"`
	Indices []int  `json:"indices"`
	Error   string `json:"error"`
}

func TestNodeOracleFixture(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "node_oracle.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var tc nodeOracleCase
		if err := json.Unmarshal(scanner.Bytes(), &tc); err != nil {
			t.Fatalf("decode Node oracle: %v", err)
		}
		t.Run(tc.Name, func(t *testing.T) {
			compiled, err := Compile(tc.Pattern, tc.Flags)
			if tc.Error != "" {
				if err == nil {
					t.Fatalf("Compile succeeded, want %s", tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.Exec(tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || tc.Indices == nil {
				if !equalIdx(got, tc.Indices) {
					t.Fatalf("Exec = %v, Node indices = %v", got, tc.Indices)
				}
				return
			}
			if !equalIdx(got, tc.Indices) {
				t.Fatalf("UTF-16 indices = %v, Node indices = %v", got, tc.Indices)
			}

		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestFlagSemanticsV8Baseline：flag 语义以 node 22 实测为期望
// （对齐 bt_debug_test.go 约定），经生产 Compile 路径断言。
func TestFlagSemanticsV8Baseline(t *testing.T) {
	cases := []struct {
		pat, flags, subj string
		want             []int // nil = 无匹配
	}{
		{"hello", "i", "HeLLo world", []int{0, 5}},
		{"^[a-z]+$", "im", "ABC\nDEF", []int{0, 3}},
		{`\bword\b`, "i", "A WORD here", []int{2, 6}},
		{"line.", "m", "first\nline2\n", []int{6, 11}},
		{"a.b", "s", "a\nb", []int{0, 3}},
		{"(a+)+b", "", strings.Repeat("a", 28), nil},
	}
	for _, c := range cases {
		compiled, err := Compile(c.pat, c.flags)
		if err != nil {
			t.Errorf("Compile(%q, %q): %v", c.pat, c.flags, err)
			continue
		}
		got, err := compiled.Exec(c.subj)
		if err != nil {
			t.Fatal(err)
		}
		if !equalIdx(got, c.want) {
			t.Errorf("%q flags=%q subj=%q: got %v, want %v", c.pat, c.flags, c.subj, got, c.want)
		}
	}
}

// TestBacktrackGuardBoundsCatastrophic：灾难性回溯在步数护栏内快速返回
// 不匹配（正确答案本就是不匹配），不挂死构建。测试强制走 bt 引擎，
// 不能用 Compile（这些模式 RE2 本身可编译，会绕过护栏）。
func TestBacktrackGuardBoundsCatastrophic(t *testing.T) {
	patterns := []string{`(a+)+b`, `(a|aa)+c`, `(a*)*b`}
	for _, pat := range patterns {
		t.Run(pat, func(t *testing.T) {
			start := time.Now()
			f, err := ParseFlags("")
			if err != nil {
				t.Fatal(err)
			}
			bt, err := compileBacktrack(pat, f)
			if err != nil {
				t.Fatalf("compileBacktrack(%q): %v", pat, err)
			}
			// 极低测试预算：必须明确触发 aborted（防止"快速返回但没走护栏"
			// 的虚假覆盖）。
			m, aborted, steps := bt.execWithLimit(strings.Repeat("a", 64), 0, 32)
			if m != nil {
				t.Errorf("%q matched catastrophic subject: %v", pat, m)
			}
			if !aborted {
				t.Errorf("%q did not trigger guard: steps=%d", pat, steps)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("%q guard failed to bound execution: %v", pat, elapsed)
			}
		})
	}
	// 护栏不能误杀正常可匹配路径。
	f, _ := ParseFlags("")
	bt, err := compileBacktrack(`(a+)+b`, f)
	if err != nil {
		t.Fatal(err)
	}
	m, aborted, _ := bt.execWithLimit("aaaab", 0, 1024)
	if aborted || !equalIdx(m, []int{0, 5, 0, 4}) {
		t.Errorf("guard killed normal match: m=%v aborted=%v", m, aborted)
	}
}

// TestBacktrackRealCorpusRegressions 锁住语料对拍发现的三类回溯引擎缺陷。
// 期望值以 Node 22 实测为准；生产 Compile 通常走 RE2，因此直接强制 bt。
func TestBacktrackRealCorpusRegressions(t *testing.T) {
	cases := []struct {
		name, pat, subj string
		want            []int
	}{
		// 懒量词在捕获组内多吃字符后必须更新组终点（旧实现卡在首次停止点）。
		{"lazy-capture-end", `(?:\s+|^)([\w-]+):?(.*?)$`, "hello world", []int{0, 11, 0, 5, 5, 11}},
		// 转义字面作为字符类区间端点（旧实现 lo=0，误构造超宽区间匹配 a）。
		{"escaped-range-endpoint", `[ -,\.\/:-@\[-\^` + "`" + `{\-~]`, "a", nil},
		{"escaped-range-space", `[ -,\.\/:-@\[-\^` + "`" + `{\-~]`, "hello world", []int{5, 6}},
		// JS 传统任意字符写法 [^]（空集合整体取反）；旧实现塞全范围后二次取反。
		{"negated-empty-class", `\/\*([^]*?)\*\/`, "/* block */", []int{0, 11, 2, 9}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := ParseFlags("")
			bt, err := compileBacktrack(c.pat, f)
			if err != nil {
				t.Fatalf("compileBacktrack(%q): %v", c.pat, err)
			}
			got := bt.exec(c.subj, 0)
			if !equalIdx(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
