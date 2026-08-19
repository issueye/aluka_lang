package builtin

// child_process.spawnSync/execFileSync/execSync 同步实现。
// Node 语义：spawnSync 出错不抛（结果对象带 error 属性）；execFileSync/
// execSync 出错抛异常（非零退出、超时、命令不存在）。

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// execError 带 code/status/killed 的 exec 错误（execFileSync/execSync 抛错）。
type execError struct {
	code   string
	msg    string
	status int
	killed bool
}

func (e *execError) Error() string { return e.msg }
func (e *execError) Code() string  { return e.code }
func (e *execError) Status() int   { return e.status }
func (e *execError) Killed() bool  { return e.killed }

// registerChildProcessSync 在 child_process 模块注册同步三件套。
func registerChildProcessSync(m engine.Object) {
	_ = m.Set("spawnSync", engine.NewFunction("spawnSync", func(args []engine.Value) (engine.Value, error) {
		command := ""
		if len(args) > 0 {
			command = args[0].String()
		}
		var cmdArgs []string
		if len(args) > 1 {
			if a, ok := args[1].(*engine.ArrayValue); ok {
				for _, e := range a.Elems() {
					cmdArgs = append(cmdArgs, e.String())
				}
			}
		}
		var optsVal engine.Value = engine.Undefined()
		if len(args) > 2 {
			optsVal = args[2]
		}
		return spawnSyncImpl(command, cmdArgs, optsVal)
	}))

	_ = m.Set("execFileSync", engine.NewFunction("execFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("execFileSync: command required")
		}
		file := args[0].String()
		var cmdArgs []string
		if len(args) > 1 {
			if a, ok := args[1].(*engine.ArrayValue); ok {
				for _, e := range a.Elems() {
					cmdArgs = append(cmdArgs, e.String())
				}
			}
		}
		var optsVal engine.Value = engine.Undefined()
		if len(args) > 2 {
			optsVal = args[2]
		}
		cmd := exec.Command(file, cmdArgs...)
		res, runErr := runSyncCommand(cmd, optsVal)
		if runErr != nil {
			return engine.Undefined(), runErr
		}
		if e := resultError(res, cmd); e != nil {
			return engine.Undefined(), e
		}
		return resultStdout(res, optsVal)
	}))

	_ = m.Set("execSync", engine.NewFunction("execSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("execSync: command required")
		}
		// execSync(command[, options])：经 shell 执行。Windows 上 cmd.exe
		// 的引号语义与 Go exec 不兼容（引号会被二次转义），简化为按空白
		// 拆分直接执行；POSIX 用 /bin/sh -c。
		command := args[0].String()
		var optsVal engine.Value = engine.Undefined()
		if len(args) > 1 {
			optsVal = args[1]
		}
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			parts := strings.Fields(command)
			if len(parts) == 0 {
				return engine.Undefined(), fmt.Errorf("execSync: empty command")
			}
			// 剥掉参数两端引号（模拟 shell 行为；cmd.exe 会剥引号）。
			for i, p := range parts {
				parts[i] = strings.Trim(p, "\"")
			}
			cmd = exec.Command(parts[0], parts[1:]...)
		} else {
			cmd = exec.Command("/bin/sh", "-c", command)
		}
		res, runErr := runSyncCommand(cmd, optsVal)
		if runErr != nil {
			return engine.Undefined(), runErr
		}
		if e := resultError(res, cmd); e != nil {
			return engine.Undefined(), e
		}
		return resultStdout(res, optsVal)
	}))
}

// spawnSyncOptions 同步子进程选项。
type spawnSyncOptions struct {
	cwd         string
	env         []string
	input       []byte
	timeout     time.Duration
	encoding    string
	windowsHide bool
}

// parseSpawnSyncOptions 解析 options 对象。
func parseSpawnSyncOptions(optsVal engine.Value) spawnSyncOptions {
	o := spawnSyncOptions{windowsHide: runtime.GOOS == "windows"}
	if optsVal.IsUndefined() {
		return o
	}
	oo, ok := optsVal.AsObject()
	if !ok {
		return o
	}
	if v, err := oo.Get("cwd"); err == nil && v.Type() == engine.TypeString {
		o.cwd = v.String()
	}
	if v, err := oo.Get("env"); err == nil && !v.IsUndefined() {
		if eo, ok2 := v.AsObject(); ok2 {
			for _, k := range eo.Keys() {
				ev, _ := eo.Get(k)
				o.env = append(o.env, k+"="+ev.String())
			}
		}
	}
	if v, err := oo.Get("input"); err == nil && !v.IsUndefined() {
		if bv, ok2 := v.(*engine.BufferValue); ok2 {
			o.input = bv.Bytes()
		} else if data, ok2 := engine.AsArrayBuffer(v); ok2 {
			o.input = data
		} else if ta, ok2 := engine.AsTypedArray(v); ok2 {
			o.input = ta.Bytes()
		} else {
			o.input = []byte(v.String())
		}
	}
	if v, err := oo.Get("timeout"); err == nil && !v.IsUndefined() {
		if n, ok2 := v.Int(); ok2 {
			o.timeout = time.Duration(n) * time.Millisecond
		}
	}
	if v, err := oo.Get("encoding"); err == nil && v.Type() == engine.TypeString {
		o.encoding = v.String()
	}
	if v, err := oo.Get("windowsHide"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.windowsHide = b
		}
	}
	return o
}

// spawnSyncImpl 执行同步子进程，返回结果对象。
func spawnSyncImpl(command string, cmdArgs []string, optsVal engine.Value) (engine.Value, error) {
	cmd := exec.Command(command, cmdArgs...)
	return runSyncCommand(cmd, optsVal)
}

// killProcess 终止进程（超时用）。
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// runSyncCommand 运行命令并构造 spawnSync 结果对象。
func runSyncCommand(cmd *exec.Cmd, optsVal engine.Value) (engine.Value, error) {
	opts := parseSpawnSyncOptions(optsVal)
	if opts.cwd != "" {
		cmd.Dir = opts.cwd
	}
	if opts.env != nil {
		cmd.Env = opts.env
	}
	applyWindowsHide(cmd, opts.windowsHide)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if opts.input != nil {
		cmd.Stdin = bytes.NewReader(opts.input)
	}
	// 超时：计时器主动 Kill（Go 的 exec 超时错误是 signal:killed，
	// 无法据此区分超时与外部 kill，需自行标记）。
	var timedOut int32
	if opts.timeout > 0 {
		timer := time.AfterFunc(opts.timeout, func() {
			atomic.StoreInt32(&timedOut, 1)
			_ = killProcess(cmd)
		})
		defer timer.Stop()
	}
	runErr := cmd.Run()

	result := engine.NewObject()
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	_ = result.Set("pid", engine.IntValue(pid))
	status := -1
	if cmd.ProcessState != nil {
		status = cmd.ProcessState.ExitCode()
	}
	signal := engine.Null()
	if cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
		signal = engine.Str("SIGKILL")
	}
	_ = result.Set("status", engine.IntValue(status))
	_ = result.Set("signal", signal)
	if opts.encoding != "" && opts.encoding != "buffer" {
		_ = result.Set("stdout", engine.Str(stdoutBuf.String()))
		_ = result.Set("stderr", engine.Str(stderrBuf.String()))
	} else {
		_ = result.Set("stdout", globals.NewBufferInstance(stdoutBuf.Bytes()))
		_ = result.Set("stderr", globals.NewBufferInstance(stderrBuf.Bytes()))
	}
	if runErr != nil {
		if atomic.LoadInt32(&timedOut) == 1 {
			// 超时：error 属性（code ETIMEDOUT，Node 语义），signal 为
			// SIGTERM（Node 在 Windows 上 timeout 的 signal 值为 'SIGTERM'）。
			errObj := engine.NewObject()
			_ = errObj.Set("code", engine.Str("ETIMEDOUT"))
			_ = errObj.Set("message", engine.Str(runErr.Error()))
			_ = result.Set("error", errObj)
			_ = result.Set("status", engine.Null())
			_ = result.Set("signal", engine.Str("SIGTERM"))
			return result, nil
		}
		if _, ok := runErr.(*exec.ExitError); ok {
			// 非零退出：不设置 error（Node spawnSync 语义），status 已反映。
			return result, nil
		}
		// 命令不存在：error 属性。
		errObj := engine.NewObject()
		_ = errObj.Set("code", engine.Str("ENOENT"))
		_ = errObj.Set("message", engine.Str(runErr.Error()))
		_ = result.Set("error", errObj)
		_ = result.Set("status", engine.Null())
	}
	return result, nil
}

// resultError 从结果对象取错误（execFileSync/execSync 抛错语义）。
func resultError(res engine.Value, cmd *exec.Cmd) error {
	ro, ok := res.AsObject()
	if !ok {
		return nil
	}
	ev, err := ro.Get("error")
	if err == nil && !ev.IsNull() && !ev.IsUndefined() {
		// 命令不存在/超时：Node 抛 code 明确的错误。
		code := "ENOENT"
		if eo, ok2 := ev.AsObject(); ok2 {
			if cv, e3 := eo.Get("code"); e3 == nil && cv.Type() == engine.TypeString {
				code = cv.String()
			}
		}
		killed := code == "ETIMEDOUT"
		return &execError{code: code, msg: fmt.Sprintf("spawnSync %s %s", cmdStrShort(cmd), code),
			status: -1, killed: killed}
	}
	// 非零退出：Node 抛 "Command failed: <cmdline>"。
	status := -1
	if sv, err := ro.Get("status"); err == nil {
		if n, ok2 := sv.Int(); ok2 {
			status = n
		}
	}
	if status != 0 {
		return &execError{code: "", msg: fmt.Sprintf("Command failed: %s", cmd.String()), status: status}
	}
	return nil
}

// cmdStrShort 命令的简短显示（路径基名 + 参数）。
func cmdStrShort(cmd *exec.Cmd) string {
	return cmd.String()
}

// resultStdout 取 stdout（encoding:'utf8' 时转字符串；'buffer' 保持 Buffer）。
func resultStdout(res engine.Value, optsVal engine.Value) (engine.Value, error) {
	opts := parseSpawnSyncOptions(optsVal)
	ro, ok := res.AsObject()
	if !ok {
		return engine.Undefined(), nil
	}
	sv, err := ro.Get("stdout")
	if err != nil {
		return engine.Undefined(), nil
	}
	if opts.encoding == "utf8" || opts.encoding == "utf-8" {
		if bv, ok := sv.(*engine.BufferValue); ok {
			return engine.Str(string(bv.Bytes())), nil
		}
	}
	return sv, nil
}
