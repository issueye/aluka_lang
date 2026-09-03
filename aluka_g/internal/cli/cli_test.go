package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestFlagSetParseForms 验证 --flag=value 与 --flag value 两种形态、
// 布尔 flag 与位置参数交错。
func TestFlagSetParseForms(t *testing.T) {
	fs := NewFlagSet("app: ")
	var b bool
	var s string
	var n int
	fs.Bool("bool", "bool flag", &b)
	fs.String("str", "string flag", &s)
	fs.Int("num", "int flag", &n)

	pos, err := fs.Parse([]string{"--bool", "x", "--str=v", "--num", "5", "--num=7", "pos", "-bool"})
	if err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Error("--bool should set b=true")
	}
	if s != "v" {
		t.Errorf("--str=v => s=%q; want v", s)
	}
	if n != 7 {
		t.Errorf("--num 5 + --num=7 => n=%d; want 7", n)
	}
	want := []string{"x", "pos"}
	if len(pos) != len(want) {
		t.Fatalf("positionals = %v; want %v", pos, want)
	}
	for i := range want {
		if pos[i] != want[i] {
			t.Errorf("pos[%d]=%q; want %q", i, pos[i], want[i])
		}
	}
}

// TestFlagSetUnknownLenient 验证默认宽松语义：未注册 flag 落入位置参数。
func TestFlagSetUnknownLenient(t *testing.T) {
	fs := NewFlagSet("app: ")
	pos, err := fs.Parse([]string{"--zzz", "a", "--bogus=1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--zzz", "a", "--bogus=1"}
	if len(pos) != len(want) {
		t.Fatalf("positionals = %v; want %v", pos, want)
	}
	for i := range want {
		if pos[i] != want[i] {
			t.Errorf("pos[%d]=%q; want %q", i, pos[i], want[i])
		}
	}
}

// TestFlagSetUnknownStrict 验证 StrictUnknown 报错（消息含完整 token）。
func TestFlagSetUnknownStrict(t *testing.T) {
	fs := NewFlagSet("app: ")
	fs.StrictUnknown = true
	if _, err := fs.Parse([]string{"--zzz", "a"}); err == nil || err.Error() != "app: unknown option --zzz" {
		t.Fatalf("err = %v; want app: unknown option --zzz", err)
	}
	if _, err := fs.Parse([]string{"--zzz=1"}); err == nil || err.Error() != "app: unknown option --zzz=1" {
		t.Fatalf("err = %v; want app: unknown option --zzz=1", err)
	}
}

// TestFlagSetMissingValue 验证缺值错误（默认消息/自定义消息/静默）。
func TestFlagSetMissingValue(t *testing.T) {
	var s string
	fs := NewFlagSet("app: ")
	fs.String("str", "string flag", &s)
	if _, err := fs.Parse([]string{"--str"}); err == nil || err.Error() != "app: missing value after --str" {
		t.Fatalf("err = %v; want app: missing value after --str", err)
	}

	fs2 := NewFlagSet("app: ")
	fs2.String("out", "output path", &s).MissingMsg("--out requires a path")
	if _, err := fs2.Parse([]string{"--out"}); err == nil || err.Error() != "app: --out requires a path" {
		t.Fatalf("err = %v; want app: --out requires a path", err)
	}

	fs3 := NewFlagSet("app: ")
	fs3.String("out", "output path", &s).LenientMissing()
	// 有值时正常消费。
	pos, err := fs3.Parse([]string{"--out", "rest"})
	if err != nil {
		t.Fatal(err)
	}
	if s != "rest" || len(pos) != 0 {
		t.Fatalf("s=%q pos=%v; want s=rest pos=[]", s, pos)
	}
	// 缺值：静默忽略（不报错、不消费）。
	pos, err = fs3.Parse([]string{"--out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 0 {
		t.Fatalf("positionals = %v; want empty", pos)
	}
}

// TestFlagSetOptionalValue 验证可选值 flag：裸形式不消费下一 token，
// 写入 implicit 值；= 形式正常。
func TestFlagSetOptionalValue(t *testing.T) {
	var f string
	fs := NewFlagSet("app: ")
	fs.Var(FuncValue{Fn: func(s string) error { f = s; return nil }}, "analyze", "analyze format").
		OptionalValue().Implicit("text")
	pos, err := fs.Parse([]string{"--analyze", "entry.js"})
	if err != nil {
		t.Fatal(err)
	}
	if f != "text" {
		t.Errorf("bare --analyze => f=%q; want text", f)
	}
	if len(pos) != 1 || pos[0] != "entry.js" {
		t.Fatalf("positionals = %v; want [entry.js]（裸形式不得消费下一 token）", pos)
	}
	f = ""
	if _, err := fs.Parse([]string{"--analyze=json"}); err != nil {
		t.Fatal(err)
	}
	if f != "json" {
		t.Errorf("--analyze=json => f=%q; want json", f)
	}
}

// TestFlagSetBoolEquals 验证布尔 flag 的 =true/false 形态与非法值报错。
func TestFlagSetBoolEquals(t *testing.T) {
	var b bool
	fs := NewFlagSet("app: ")
	fs.Bool("b", "bool flag", &b)
	if _, err := fs.Parse([]string{"--b=false"}); err != nil {
		t.Fatal(err)
	}
	if b {
		t.Error("--b=false should set b=false")
	}
	if _, err := fs.Parse([]string{"--b=banana"}); err == nil || err.Error() != `app: invalid boolean value "banana" for --b` {
		t.Fatalf("err = %v; want app: invalid boolean value %q for --b", err, "banana")
	}
}

// TestFlagSetLenientValue 验证值解析失败静默（保持默认）。
func TestFlagSetLenientValue(t *testing.T) {
	var n = 42
	fs := NewFlagSet("app: ")
	fs.Int("num", "int flag", &n).LenientValue().LenientMissing()
	pos, err := fs.Parse([]string{"--num", "abc", "--num"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Errorf("n=%d; want 42（解析失败静默保持默认）", n)
	}
	if len(pos) != 0 {
		t.Fatalf("positionals = %v; want empty", pos)
	}
}

// TestFlagSetCustomValueError 验证自定义值错误原样透传（加前缀）。
func TestFlagSetCustomValueError(t *testing.T) {
	fs := NewFlagSet("app: ")
	fs.Var(FuncValue{Fn: func(s string) error { return errTest{s} }}, "jit", "jit mode")
	if _, err := fs.Parse([]string{"--jit", "bogus"}); err == nil || err.Error() != "app: jit mode bogus invalid" {
		t.Fatalf("err = %v; want app: jit mode bogus invalid", err)
	}
}

type errTest struct{ v string }

func (e errTest) Error() string { return "jit mode " + e.v + " invalid" }

// TestFlagSetAlias 验证别名注册。
func TestFlagSetAlias(t *testing.T) {
	var b bool
	fs := NewFlagSet("app: ")
	fs.Bool("coverage", "coverage flag", &b).Alias("experimental-test-coverage")
	if _, err := fs.Parse([]string{"--experimental-test-coverage"}); err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Error("alias should set b=true")
	}
}

// TestFlagSetDoubleDashNotSpecial 验证 "--" 不做特殊处理（与现状 CLI 一致）。
func TestFlagSetDoubleDashNotSpecial(t *testing.T) {
	fs := NewFlagSet("app: ")
	pos, err := fs.Parse([]string{"--", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 || pos[0] != "--" || pos[1] != "a" {
		t.Fatalf("positionals = %v; want [-- a]", pos)
	}
}

// TestFlagSetActionValue 验证布尔类自定义值（裸形式触发、不消费值）。
func TestFlagSetActionValue(t *testing.T) {
	called := false
	fs := NewFlagSet("app: ")
	fs.Var(ActionValue{Fn: func() error { called = true; return nil }}, "act", "action flag")
	pos, err := fs.Parse([]string{"--act", "rest"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("--act should trigger the action")
	}
	if len(pos) != 1 || pos[0] != "rest" {
		t.Fatalf("positionals = %v; want [rest]", pos)
	}
}

// TestFlagSetActionValueError 验证布尔类自定义值错误原样透传。
func TestFlagSetActionValueError(t *testing.T) {
	fs := NewFlagSet("app: ")
	fs.Var(ActionValue{Fn: func() error { return errTest{"x"} }}, "act", "action flag")
	if _, err := fs.Parse([]string{"--act"}); err == nil || err.Error() != "app: jit mode x invalid" {
		t.Fatalf("err = %v; want app: jit mode x invalid", err)
	}
}

// testApp 构造测试用 App：两个子命令 + 一个全局 flag + 默认命令。
func testApp() *App {
	app := New("tool", "1.0")
	var verbose bool
	app.GlobalFlags().Bool("verbose", "verbose output", &verbose)
	app.AddCommand(&Command{
		Name:    "run",
		Aliases: []string{"r"},
		Summary: "Run something",
		Run: func(args []string, invoked string) error {
			return nil
		},
	})
	app.AddCommand(&Command{
		Name:    "-e",
		Aliases: []string{"--eval"},
		Summary: "Evaluate code",
		Run: func(args []string, invoked string) error {
			if len(args) == 0 {
				return errTest{"missing code after " + invoked}
			}
			return nil
		},
	})
	app.SetDefaultCommand(&Command{Name: "(file)", Summary: "Run a file", Run: func(args []string, invoked string) error {
		return nil
	}})
	return app
}

// TestAppDispatchHelpVersion 验证帮助/版本输出与退出码。
func TestAppDispatchHelpVersion(t *testing.T) {
	app := testApp()
	var out, errOut bytes.Buffer
	app.Out = &out
	app.ErrOut = &errOut

	if code := app.Dispatch(nil); code != 0 {
		t.Fatalf("no args => code %d; want 0", code)
	}
	if !strings.Contains(out.String(), "tool 1.0") || !strings.Contains(out.String(), "COMMANDS:") {
		t.Errorf("help output missing sections: %q", out.String())
	}
	out.Reset()
	if code := app.Dispatch([]string{"--help"}); code != 0 {
		t.Fatalf("--help => code %d; want 0", code)
	}
	out.Reset()
	if code := app.Dispatch([]string{"-v"}); code != 0 {
		t.Fatalf("-v => code %d; want 0", code)
	}
	if out.String() != "tool 1.0\n" {
		t.Errorf("-v output = %q; want %q", out.String(), "tool 1.0\n")
	}
}

// TestAppDispatchUnknownOption 验证未知选项报错与用法错误退出码。
func TestAppDispatchUnknownOption(t *testing.T) {
	app := testApp()
	var errOut bytes.Buffer
	app.ErrOut = &errOut
	if code := app.Dispatch([]string{"--bogus"}); code != app.UsageExitCode {
		t.Fatalf("unknown option => code %d; want %d", code, app.UsageExitCode)
	}
	if errOut.String() != "tool: unknown option --bogus\n" {
		t.Errorf("stderr = %q; want %q", errOut.String(), "tool: unknown option --bogus\n")
	}
	if app.UsageExitCode != 2 {
		t.Errorf("default UsageExitCode = %d; want 2", app.UsageExitCode)
	}
}

// TestAppDispatchCustomUsageExitCode 验证自定义用法错误退出码。
func TestAppDispatchCustomUsageExitCode(t *testing.T) {
	app := testApp()
	app.UsageExitCode = 1
	app.ErrOut = &bytes.Buffer{}
	if code := app.Dispatch([]string{"--bogus"}); code != 1 {
		t.Fatalf("code = %d; want 1", code)
	}
}

// TestAppDispatchCommandAndAlias 验证子命令分发与 invoked 参数。
func TestAppDispatchCommandAndAlias(t *testing.T) {
	app := testApp()
	var invoked string
	app.commands[1].Run = func(args []string, name string) error {
		invoked = name
		return nil
	}
	if code := app.Dispatch([]string{"--eval", "code"}); code != 0 {
		t.Fatalf("--eval => code %d; want 0", code)
	}
	if invoked != "--eval" {
		t.Errorf("invoked = %q; want --eval", invoked)
	}
	if code := app.Dispatch([]string{"-e"}); code != 0 {
		t.Fatalf("-e => code %d; want 0", code)
	}
	if invoked != "-e" {
		t.Errorf("invoked = %q; want -e", invoked)
	}
}

// TestAppDispatchCommandError 验证命令返回错误 → stderr 原样输出 + 退出码 1
// （命令错误消息自带前缀由调用方负责，框架不加前缀）。
func TestAppDispatchCommandError(t *testing.T) {
	app := testApp()
	app.commands[1].Run = func(args []string, invoked string) error {
		if len(args) == 0 {
			return errors.New("missing code after " + invoked)
		}
		return nil
	}
	var errOut bytes.Buffer
	app.ErrOut = &errOut
	if code := app.Dispatch([]string{"-e"}); code != 1 {
		t.Fatalf("-e 缺参 => code %d; want 1", code)
	}
	if errOut.String() != "missing code after -e\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestAppDispatchDefaultCommand 验证默认命令（位置参数 → file）。
func TestAppDispatchDefaultCommand(t *testing.T) {
	app := testApp()
	var got []string
	app.defaultCmd.Run = func(args []string, invoked string) error {
		got = args
		return nil
	}
	if code := app.Dispatch([]string{"a.js", "--flag-x"}); code != 0 {
		t.Fatalf("code = %d; want 0", code)
	}
	if len(got) != 2 || got[0] != "a.js" || got[1] != "--flag-x" {
		t.Fatalf("default cmd args = %v; want [a.js --flag-x]", got)
	}
}

// TestAppExitError 验证 ExitError 携带的退出码。
func TestAppExitError(t *testing.T) {
	app := testApp()
	app.commands[0].Run = func(args []string, invoked string) error {
		return &ExitError{Code: 3}
	}
	if code := app.Dispatch([]string{"run"}); code != 3 {
		t.Fatalf("code = %d; want 3", code)
	}
}

// TestAppRunGlobalParseError 验证全局 flag 解析错误 → stderr + 用法退出码。
func TestAppRunGlobalParseError(t *testing.T) {
	app := testApp()
	var errOut bytes.Buffer
	app.ErrOut = &errOut
	var out string
	app.GlobalFlags().String("out", "output", &out)
	if code := app.Run([]string{"--out"}); code != app.UsageExitCode {
		t.Fatalf("code = %d; want %d", code, app.UsageExitCode)
	}
	if errOut.String() != "tool: missing value after --out\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestAppRunGlobalStrip 验证全局 flag 从任意位置剥离。
func TestAppRunGlobalStrip(t *testing.T) {
	app := testApp()
	var verbose bool
	app.GlobalFlags().Bool("verbose", "verbose", &verbose)
	if code := app.Run([]string{"run", "x", "--verbose"}); code != 0 {
		t.Fatalf("code = %d; want 0", code)
	}
	if !verbose {
		t.Error("global flag not parsed from tail position")
	}
}

// TestAppHelpOverride 验证自定义帮助覆盖。
func TestAppHelpOverride(t *testing.T) {
	app := testApp()
	app.SetHelp(func(w io.Writer) {
		_, _ = w.Write([]byte("CUSTOM HELP"))
	})
	var out bytes.Buffer
	app.Out = &out
	if code := app.Dispatch([]string{"--help"}); code != 0 {
		t.Fatalf("code = %d; want 0", code)
	}
	if out.String() != "CUSTOM HELP" {
		t.Errorf("help = %q; want CUSTOM HELP", out.String())
	}
}
