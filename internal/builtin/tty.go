package builtin

// node:tty 模块（Phase 2 补充，chalk/supports-color 等包依赖）。
//
//   - isatty(fd) → boolean
//   - ReadStream / WriteStream（简化为带 isTTY 属性的 EventEmitter 子类）
//
// 基于 Go os.File.Stat() 的 ModeCharDevice 判断终端。

import (
	"os"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTTY 构造 node:tty 模块导出。
func NewTTY(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("isatty", engine.NewFunction("isatty", func(args []engine.Value) (engine.Value, error) {
		fd := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				fd = n
			}
		}
		return engine.Boolean(isTTYFd(fd)), nil
	}))

	// ReadStream / WriteStream：构造函数，实例含 isTTY 属性。
	// 复用 EventEmitter（newEmitterInstance 在 globals 包，这里经 events 模块导出取）。
	makeStreamCtor := func(name string, defaultTTY bool) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			fd := -1
			if len(args) > 0 {
				if n, ok := args[0].Int(); ok {
					fd = n
				}
			}
			obj := engine.NewObject()
			isTTY := defaultTTY
			if fd >= 0 {
				isTTY = isTTYFd(fd)
			}
			_ = obj.Set("isTTY", engine.Boolean(isTTY))
			_ = obj.Set("fd", engine.IntValue(fd))
			return obj, nil
		})
	}
	_ = m.Set("ReadStream", makeStreamCtor("ReadStream", false))
	_ = m.Set("WriteStream", makeStreamCtor("WriteStream", true))

	return m, nil
}

// isTTYFd 判断 fd 是否指向终端。
func isTTYFd(fd int) bool {
	var f *os.File
	switch fd {
	case 0:
		f = os.Stdin
	case 1:
		f = os.Stdout
	case 2:
		f = os.Stderr
	default:
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
