// AST 解释器错误映射：Go error 到 JS 异常值的转换与 jsError 载体。

package interpreter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// goErrorToJSValue converts a Go error to a JS Error object.
func (interp *Interpreter) goErrorToJSValue(err error) engine.Value {
	errObj := engine.NewObject()
	engine.SetProto(errObj, interp.errorProto)
	msg := err.Error()
	name := "Error"
	if errors.Is(err, engine.ErrTypeError) {
		name = "TypeError"
		msg = strings.TrimPrefix(msg, "aluka: type error: ")
		if ctor, ok := interp.constructors["TypeError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrReferenceError) {
		name = "ReferenceError"
		msg = strings.TrimPrefix(msg, "aluka: reference error: ")
		if ctor, ok := interp.constructors["ReferenceError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrSyntaxError) {
		name = "SyntaxError"
		msg = strings.TrimPrefix(msg, "aluka: syntax error: ")
		if ctor, ok := interp.constructors["SyntaxError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrRangeError) {
		name = "RangeError"
		msg = strings.TrimPrefix(msg, "aluka: range error: ")
		if ctor, ok := interp.constructors["RangeError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	}
	_ = errObj.Set("name", engine.Str(name))
	_ = errObj.Set("message", engine.Str(msg))
	// 携带 Node 风格错误码的错误（如 ERR_PARSE_ARGS_UNKNOWN_OPTION）→ err.code。
	if ce, ok := err.(interface{ Code() string }); ok {
		_ = errObj.Set("code", engine.Str(ce.Code()))
	}
	// 系统错误（*os.PathError/*os.LinkError/*fs.PathError）：Node 风格
	// code/errno/path 与 message（如 "ENOENT: no such file or directory, open 'x'"）。
	if pe, ok := asPathError(err); ok {
		code, desc, errnoNum := nodeErrnoInfo(pe.Err)
		if code != "" {
			_ = errObj.Set("code", engine.Str(code))
			_ = errObj.Set("errno", engine.IntValue(errnoNum))
			op := pe.Op
			if op == "" {
				op = "syscall"
			}
			msg = fmt.Sprintf("%s: %s, %s '%s'", code, desc, op, pe.Path)
			_ = errObj.Set("message", engine.Str(msg))
			_ = errObj.Set("path", engine.Str(pe.Path))
			_ = errObj.Set("syscall", engine.Str(op))
		}
	}
	// exec 类错误：status/killed（execFileSync/execSync 非零退出与超时）。
	if se, ok := err.(interface{ Status() int }); ok {
		_ = errObj.Set("status", engine.IntValue(se.Status()))
	}
	if ke, ok := err.(interface{ Killed() bool }); ok {
		_ = errObj.Set("killed", engine.Boolean(ke.Killed()))
	}
	// VM/runtime failures converted to JavaScript errors should expose the same
	// V8-style stack property as errors constructed from JavaScript.
	interp.setErrorStack(errObj)
	return errObj
}

// jsError wraps a thrown JS value so it can propagate as a Go error.
type jsError struct {
	value engine.Value
}

func (e *jsError) Error() string {
	return engine.FormatException(e.value)
}
