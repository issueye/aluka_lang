// Package cli 提供自研轻量级命令行框架：注册式子命令 + flag 声明式解析 +
// 统一帮助/错误/退出码。仅依赖 Go 标准库。
//
// 设计要点：
//   - FlagSet.Parse 采用扫描式解析：flag 可出现在任意位置（与位置参数交错），
//     支持 --flag=value 与 --flag value 两种形态；未注册的 flag 默认落入
//     位置参数（宽松语义），可配置为报错。
//   - 可选值 flag（如 --analyze[=text|json]）：裸 --flag 不消费下一 token。
//   - App 负责命令注册、全局 flag（任意位置剥离）、帮助/版本、错误与退出码；
//     用法错误默认退出码 2（Go flag 惯例），可通过 UsageExitCode 调整。
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Command 是一个子命令：名字/别名 + 可选 flag 集合 + 执行函数。
//
// Run 收到解析后的位置参数与本次调用的名字（invoked，可能是别名），
// 返回错误时由 App 统一输出到 stderr 并以非零码退出。
type Command struct {
	Name    string
	Aliases []string
	Summary string
	Flags   *FlagSet
	Run     func(args []string, invoked string) error
}

// match 判断 name 是否命中命令名或别名。
func (c *Command) match(name string) bool {
	if c.Name == name {
		return true
	}
	for _, a := range c.Aliases {
		if a == name {
			return true
		}
	}
	return false
}

// ExitError 携带自定义进程退出码（如 `aluka --check` 的 0/1）。
type ExitError struct {
	Code int
}

// Error 实现 error 接口。
func (e *ExitError) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

// App 是 CLI 应用：全局 flag（任意位置剥离）+ 子命令分发 + 帮助/版本 +
// 错误/退出码。
type App struct {
	Name    string
	Version string

	// UsageExitCode 用法错误（未知选项/缺值等）的退出码，默认 2（Go flag 惯例）。
	UsageExitCode int
	// ErrorPrefix 应用级错误消息前缀，默认 Name + ": "。
	ErrorPrefix string
	// Out / ErrOut 输出目标（默认 os.Stdout / os.Stderr，测试可注入）。
	Out    io.Writer
	ErrOut io.Writer

	global     *FlagSet
	commands   []*Command
	defaultCmd *Command
	helpFn     func(io.Writer)
}

// New 创建应用。全局 flag 通过 GlobalFlags() 注册。
func New(name, version string) *App {
	return &App{
		Name:          name,
		Version:       version,
		UsageExitCode: 2,
		ErrorPrefix:   name + ": ",
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		global:        NewFlagSet(name + ": "),
	}
}

// GlobalFlags 返回全局 flag 集合（解析时从任意位置剥离）。
func (a *App) GlobalFlags() *FlagSet { return a.global }

// AddCommand 注册子命令。
func (a *App) AddCommand(cmd *Command) {
	a.commands = append(a.commands, cmd)
}

// SetDefaultCommand 设置默认命令：无匹配子命令且首参数不以 "-" 开头时执行
// （如 aluka <file> 的 run 简写）。
func (a *App) SetDefaultCommand(cmd *Command) { a.defaultCmd = cmd }

// SetHelp 覆盖帮助输出（默认按注册的命令与 flag 自动生成）。
func (a *App) SetHelp(fn func(io.Writer)) { a.helpFn = fn }

// ParseGlobals 先剥离全局 flag（任意位置），返回剩余参数。
func (a *App) ParseGlobals(args []string) ([]string, error) {
	return a.global.Parse(args)
}

// Run 是完整入口：ParseGlobals + Dispatch，返回进程退出码。
func (a *App) Run(args []string) int {
	pos, err := a.ParseGlobals(args)
	if err != nil {
		fmt.Fprintln(a.ErrOut, err)
		return a.UsageExitCode
	}
	return a.Dispatch(pos)
}

// Dispatch 按剩余参数分发：帮助/版本/子命令/默认命令/未知选项。
func (a *App) Dispatch(pos []string) int {
	if len(pos) == 0 {
		a.writeHelp(a.Out)
		return 0
	}
	first := pos[0]
	switch first {
	case "-h", "--help":
		a.writeHelp(a.Out)
		return 0
	case "-v", "--version":
		fmt.Fprintln(a.Out, a.Name+" "+a.Version)
		return 0
	}
	if cmd := a.find(first); cmd != nil {
		return a.runCommand(cmd, pos[1:], first)
	}
	if strings.HasPrefix(first, "-") {
		fmt.Fprintln(a.ErrOut, a.ErrorPrefix+"unknown option "+first)
		return a.UsageExitCode
	}
	if a.defaultCmd != nil {
		return a.runCommand(a.defaultCmd, pos, first)
	}
	fmt.Fprintf(a.ErrOut, "%sunknown command %q\n", a.ErrorPrefix, first)
	return a.UsageExitCode
}

// find 按名字/别名查找子命令。
func (a *App) find(name string) *Command {
	for _, c := range a.commands {
		if c.match(name) {
			return c
		}
	}
	return nil
}

// runCommand 解析子命令 flags 并执行。
func (a *App) runCommand(cmd *Command, args []string, invoked string) int {
	fs := cmd.Flags
	if fs == nil {
		fs = NewFlagSet("")
	}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(a.ErrOut, err)
		return a.UsageExitCode
	}
	if err := cmd.Run(rest, invoked); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return ee.Code
		}
		fmt.Fprintln(a.ErrOut, err)
		return 1
	}
	return 0
}

// writeHelp 输出帮助（自定义或自动生成）。
func (a *App) writeHelp(w io.Writer) {
	if a.helpFn != nil {
		a.helpFn(w)
		return
	}
	a.writeAutoHelp(w)
}

// writeAutoHelp 自动生成帮助：子命令 + 全局 flags + 各命令 flags。
func (a *App) writeAutoHelp(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n\n", a.Name, a.Version)
	fmt.Fprintf(w, "USAGE:\n    %s [COMMAND] [OPTIONS] [ARGS]\n\n", a.Name)
	if len(a.commands) > 0 {
		fmt.Fprintln(w, "COMMANDS:")
		for _, c := range a.commands {
			names := flagNameDisplay(c.Name)
			if len(c.Aliases) > 0 {
				var als []string
				for _, al := range c.Aliases {
					als = append(als, flagNameDisplay(al))
				}
				names += " (" + strings.Join(als, ", ") + ")"
			}
			fmt.Fprintf(w, "    %-32s %s\n", names, c.Summary)
		}
		fmt.Fprintln(w)
	}
	if len(a.global.order) > 0 {
		fmt.Fprintln(w, "GLOBAL OPTIONS:")
		writeFlagsHelp(w, a.global)
		fmt.Fprintln(w)
	}
	for _, c := range a.commands {
		if c.Flags == nil || len(c.Flags.order) == 0 {
			continue
		}
		fmt.Fprintf(w, "OPTIONS (%s):\n", c.Name)
		writeFlagsHelp(w, c.Flags)
		fmt.Fprintln(w)
	}
}

// writeFlagsHelp 输出单个 FlagSet 的 flag 列表。
func writeFlagsHelp(w io.Writer, fs *FlagSet) {
	for _, f := range fs.order {
		names := flagNameDisplay(f.name)
		for _, al := range f.aliases {
			names += ", " + flagNameDisplay(al)
		}
		fmt.Fprintf(w, "    %-32s %s\n", names, f.usage)
	}
}

// flagNameDisplay 展示 flag/别名名字：已带 "-" 前缀的（如 -e）原样输出。
func flagNameDisplay(name string) string {
	if strings.HasPrefix(name, "-") {
		return name
	}
	return "--" + name
}
