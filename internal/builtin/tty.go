package builtin

// node:tty 模块（Phase 2 补充，chalk/supports-color 等包依赖）。
//
//   - isatty(fd) → boolean
//   - ReadStream / WriteStream：非 TTY fd 构造抛 ERR_TTY_INIT_FAILED（Node
//     语义）；原型面 setRawMode / clearLine / cursorTo / getColorDepth 等。
//
// 基于 Go os.File.Stat() 的 ModeCharDevice 判断终端。

import (
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

	// ReadStream 原型（setRawMode）。
	rsProto := engine.NewObject()
	_ = rsProto.Set("setRawMode", engine.NewFunction("setRawMode", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// WriteStream 原型（clearLine/clearScreenDown/cursorTo/moveCursor/
	// getColorDepth/hasColors/getWindowSize）。
	wsProto := engine.NewObject()
	mkNoop := func(name string) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		})
	}
	for _, k := range []string{"clearLine", "clearScreenDown", "cursorTo",
		"getColorDepth", "getWindowSize", "hasColors", "moveCursor", "_refreshSize"} {
		_ = wsProto.Set(k, mkNoop(k))
	}
	_ = wsProto.Set("isTTY", engine.Boolean(false))

	// ttyErr 非 TTY 构造错误（Node：ERR_TTY_INIT_FAILED）。
	ttyErr := func() error {
		return &fsCodeError{code: "ERR_TTY_INIT_FAILED",
			msg: "ERR_TTY_INIT_FAILED: TTY initialization failed: uv_tty_init returned EBADF"}
	}

	// ReadStream 构造器。
	rsCtor := engine.NewFunction("ReadStream", func(args []engine.Value) (engine.Value, error) {
		fd := -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				fd = n
			}
		}
		if fd >= 0 && !isTTYFd(fd) {
			return engine.Undefined(), ttyErr()
		}
		obj := engine.NewObject()
		isTTY := fd >= 0 && isTTYFd(fd)
		_ = obj.Set("isTTY", engine.Boolean(isTTY))
		_ = obj.Set("fd", engine.IntValue(fd))
		_ = obj.Set("isRaw", engine.Boolean(false))
		_ = obj.Set("setRawMode", engine.NewFunction("setRawMode", func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		engine.SetProto(obj, rsProto)
		return obj, nil
	})
	if co, ok := rsCtor.AsObject(); ok {
		_ = co.Set("prototype", rsProto)
	}
	_ = rsProto.Set("constructor", rsCtor)

	// WriteStream 构造器。
	wsCtor := engine.NewFunction("WriteStream", func(args []engine.Value) (engine.Value, error) {
		fd := -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				fd = n
			}
		}
		if fd >= 0 && !isTTYFd(fd) {
			return engine.Undefined(), ttyErr()
		}
		obj := engine.NewObject()
		isTTY := fd >= 0 && isTTYFd(fd)
		_ = obj.Set("isTTY", engine.Boolean(isTTY))
		_ = obj.Set("fd", engine.IntValue(fd))
		_ = obj.Set("columns", engine.IntValue(80))
		_ = obj.Set("rows", engine.IntValue(24))
		engine.SetProto(obj, wsProto)
		return obj, nil
	})
	if co, ok := wsCtor.AsObject(); ok {
		_ = co.Set("prototype", wsProto)
	}
	_ = wsProto.Set("constructor", wsCtor)

	_ = m.Set("ReadStream", rsCtor)
	_ = m.Set("WriteStream", wsCtor)

	return m, nil
}

// isTTYFd 判断 fd 是否指向终端（平台相关：Windows 用 GetConsoleMode，
// POSIX 用字符设备检测）。
func isTTYFd(fd int) bool {
	return isTTYFdPlatform(fd)
}
