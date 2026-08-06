package minify

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// captureStdout 捕获执行期间的 stdout（console.log 输出）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()
	return string(out)
}

// runSrc 编译执行源码（minify=true 时先变换），返回 stdout。
func runSrc(t *testing.T, src string, minifyIt bool) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if minifyIt {
		Program(prog)
	}
	return captureStdout(t, func() {
		eng := interpreter.NewVMEngine()
		defer eng.Shutdown()
		ctx, err := eng.NewContext()
		if err != nil {
			t.Fatalf("ctx: %v", err)
		}
		defer ctx.Close()
		if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
			t.Fatalf("console: %v", err)
		}
		vm, ok := ctx.(*interpreter.VM)
		if !ok {
			t.Fatal("ctx is not VM")
		}
		if _, err := vm.EvalProgram(prog, "minify_test.js"); err != nil {
			t.Fatalf("eval: %v", err)
		}
	})
}

func trim(s string) string { return strings.TrimSpace(s) }

// TestConstFold 常量折叠：1+2*3 → 7；'a'+'b' → 'ab'。
func TestConstFold(t *testing.T) {
	cases := []struct{ src, want string }{
		{"console.log(1 + 2 * 3);", "7"},
		{"console.log('a' + 'b' + 'c');", "abc"},
		{"console.log(10 - 4 - 2);", "4"},
		{"console.log(2 === 2);", "true"},
		{"console.log(3 > 5);", "false"},
		{"console.log(!false);", "true"},
	}
	for _, c := range cases {
		got := trim(runSrc(t, c.src, true))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.src, got, c.want)
		}
	}
}

// TestDeadCodeElimination 死代码消除：if(false) 分支与 return 后语句。
func TestDeadCodeElimination(t *testing.T) {
	src := `
function f(x) {
  if (false) { return "dead"; }
  if (true) { return "alive"; }
  return "unreachable";
}
console.log(f(1));
`
	got := trim(runSrc(t, src, true))
	if got != "alive" {
		t.Errorf("DCE: got %q, want %q", got, "alive")
	}
}

// TestUnusedDeclRemoval 未使用声明删除不改变行为。
func TestUnusedDeclRemoval(t *testing.T) {
	src := `
const unusedVar = 100;
function unusedFn() { return 1; }
const keep = 42;
console.log(keep);
`
	got := trim(runSrc(t, src, true))
	if got != "42" {
		t.Errorf("unused decl: got %q, want %q", got, "42")
	}
}

// TestMinifyPreservesSemantics 变换前后行为一致（含副作用保留）。
func TestMinifyPreservesSemantics(t *testing.T) {
	src := `
let count = 0;
function bump() { count++; return count; }
function g(x) { return x * 2; }
const v = g(1 + 2);
if (v > 5) { console.log("big", bump()); } else { console.log("small", bump()); }
console.log("count:", count);
`
	before := trim(runSrc(t, src, false))
	after := trim(runSrc(t, src, true))
	if before != after {
		t.Errorf("minify changed behavior: before=%q after=%q", before, after)
	}
}

// TestUnreachableRemoval return 后语句删除。
func TestUnreachableRemoval(t *testing.T) {
	src := `
function f() {
  return "ok";
  console.log("never");
}
console.log(f());
`
	got := trim(runSrc(t, src, true))
	if got != "ok" {
		t.Errorf("unreachable: got %q, want %q", got, "ok")
	}
}

// TestFoldTemplate 无插值模板字符串折叠。
func TestFoldTemplate(t *testing.T) {
	src := "console.log(`plain`);\n"
	got := trim(runSrc(t, src, true))
	if got != "plain" {
		t.Errorf("template: got %q, want %q", got, "plain")
	}
}

var _ = ast.Program{}
