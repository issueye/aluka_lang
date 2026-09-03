// test262 兼容性测试 runner（开发计划 1B.8/1C.15/1D.14）。
//
// 用法：
//   ALUKA=<aluka 可执行路径> go run ./tests/conformance/test262 [测试目录]
//
// 流程：
//   - 遍历测试目录（默认 cases/）下的 .js 文件。
//   - 解析 frontmatter（/*--- YAML ---*/），支持 negative（phase/type）。
//   - 前置 test262 风格 assert harness，用 aluka 执行临时文件。
//   - 无 negative：期望正常退出；negative.parse 期望 SyntaxError；
//     negative.runtime 期望对应错误类型。
//   - 输出通过率。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// harness 是 test262 风格断言的最小实现。
const harness = `
var $DONOTEVALUATE = function() { throw new Error("$DONOTEVALUATE"); };
var assert = {
  sameValue: function(actual, expected, msg) {
    if (actual !== expected) {
      throw new Error("assert.sameValue: expected " + expected + " got " + actual + (msg ? " (" + msg + ")" : ""));
    }
  },
  isTrue: function(v, msg) { if (v !== true) throw new Error("assert.isTrue" + (msg ? ": " + msg : "")); },
  isFalse: function(v, msg) { if (v !== false) throw new Error("assert.isFalse" + (msg ? ": " + msg : "")); },
  sameType: function(a, b, msg) {
    if (typeof a !== typeof b) throw new Error("assert.sameType: " + typeof a + " vs " + typeof b + (msg ? ": " + msg : ""));
  },
  throws: function(expectedType, fn) {
    var thrown = false;
    try { fn(); } catch (e) { thrown = true; }
    if (!thrown) throw new Error("assert.throws: no exception thrown");
  }
};
`

// frontmatterRE 匹配文件头 /*--- ... ---*/。
var frontmatterRE = regexp.MustCompile(`(?s)^/\*---(.*?)---\*/`)

// negativeRE 匹配 negative: phase/type。
var negativePhaseRE = regexp.MustCompile(`(?m)phase:\s*(\w+)`)
var negativeTypeRE = regexp.MustCompile(`(?m)type:\s*([\w.]+)`)

func main() {
	dir := "cases"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	aluka := os.Getenv("ALUKA")
	if aluka == "" {
		aluka = "aluka"
	}

	pass, fail := 0, 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		code := string(src)
		negative, phase, errType := parseFrontmatter(code)

		execCode := harness + "\n" + stripFrontmatter(code)
		tmp, _ := os.CreateTemp("", "t262-*.js")
		_, _ = tmp.WriteString(execCode)
		tmpPath := tmp.Name()
		_ = tmp.Close()

		cmd := exec.Command(aluka, tmpPath)
		out, runErr := cmd.CombinedOutput()
		_ = os.Remove(tmpPath)

		if evalResult(negative, phase, errType, runErr, string(out)) {
			pass++
		} else {
			fail++
			fmt.Printf("FAIL  %s (negative=%v phase=%s)\n", path, negative, phase)
			if runErr != nil {
				first := strings.SplitN(string(out), "\n", 2)
				if len(first) > 0 {
					fmt.Printf("      %s\n", first[0])
				}
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk error:", err)
		os.Exit(1)
	}

	total := pass + fail
	rate := 0.0
	if total > 0 {
		rate = float64(pass) / float64(total) * 100
	}
	fmt.Printf("----------------------------------------\n")
	fmt.Printf("test262 subset: %d/%d passed (%.1f%%)\n", pass, total, rate)
	if fail > 0 {
		os.Exit(1)
	}
}

// parseFrontmatter 解析 frontmatter，返回 (negative 是否存在, phase, type)。
func parseFrontmatter(code string) (bool, string, string) {
	m := frontmatterRE.FindStringSubmatch(code)
	if m == nil {
		return false, "", ""
	}
	body := m[1]
	if !strings.Contains(body, "negative") {
		return false, "", ""
	}
	phase := "runtime"
	if pm := negativePhaseRE.FindStringSubmatch(body); pm != nil {
		phase = pm[1]
	}
	errType := "Error"
	if tm := negativeTypeRE.FindStringSubmatch(body); tm != nil {
		errType = tm[1]
	}
	return true, phase, errType
}

// stripFrontmatter 移除 frontmatter 块。
func stripFrontmatter(code string) string {
	return frontmatterRE.ReplaceAllString(code, "")
}

// evalResult 判断测试是否通过。
func evalResult(negative bool, phase, errType string, runErr error, out string) bool {
	if !negative {
		return runErr == nil
	}
	// negative：期望出错。
	if runErr == nil {
		return false
	}
	low := strings.ToLower(out)
	switch phase {
	case "parse":
		return strings.Contains(low, "syntax error") || strings.Contains(low, "syntaxerror")
	default: // runtime
		t := strings.ToLower(errType)
		if strings.Contains(low, t) {
			return true
		}
		// 引擎错误格式为 "type error"（带空格），容错匹配。
		if t == "typeerror" && strings.Contains(low, "type error") {
			return true
		}
		if t == "rangeerror" && strings.Contains(low, "range error") {
			return true
		}
		if t == "referenceerror" && strings.Contains(low, "reference error") {
			return true
		}
		if t == "syntaxerror" && strings.Contains(low, "syntax error") {
			return true
		}
		return false
	}
}
