package interpreter

// V8 stack-trace API (Error.captureStackTrace / prepareStackTrace /
// stackTraceLimit / callsite objects).
//
// Many npm packages (e.g. depd, http-errors, which Express depends on) rely on
// V8's stack introspection API to build deprecation/error messages. Without it
// they fail with "undefined is not a function". This file provides a functional
// subset backed by the VM's live call-frame stack.

import (
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// callsiteInfo captures the stack-trace data of a single VM frame.
type callsiteInfo struct {
	file       string
	line       int
	col        int
	funcName   string
	thisVal    engine.Value
	typeName   string
	methodName string
}

// setupErrorV8Stack attaches the V8 stack-trace API to the Error constructor.
func (interp *Interpreter) setupErrorV8Stack() {
	ctor := interp.constructors["Error"]
	_ = ctor.Set("stackTraceLimit", engine.IntValue(10))

	_ = ctor.Set("captureStackTrace", interp.makeFunc("captureStackTrace", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		target := args[0]
		stackVal := interp.formatStackTrace(target)
		if targetObj, ok := target.AsObject(); ok {
			_ = targetObj.Set("stack", stackVal)
		}
		return engine.Undefined(), nil
	}))

	// Default prepareStackTrace: formats "Name: message\n    at ...".
	_ = ctor.Set("prepareStackTrace", interp.makeFunc("prepareStackTrace", func(args []engine.Value) (engine.Value, error) {
		var sb strings.Builder
		if len(args) < 2 {
			return engine.Str(""), nil
		}
		target := args[0]
		if o, ok := target.AsObject(); ok {
			name, _ := o.Get("name")
			msg, _ := o.Get("message")
			sb.WriteString(v8Str(name, "Error"))
			if ms := v8Str(msg, ""); ms != "" {
				sb.WriteString(": " + ms)
			}
		} else {
			sb.WriteString(target.String())
		}
		if arr, ok := args[1].(*engine.ArrayValue); ok {
			for _, cs := range arr.Elems() {
				sb.WriteString("\n    at " + interp.callToString(cs))
			}
		}
		return engine.Str(sb.String()), nil
	}))
}

// setErrorStack captures the current stack and assigns it to a freshly
// constructed Error object's `.stack` property.
func (interp *Interpreter) setErrorStack(errObj engine.Object) {
	stackVal := interp.formatStackTrace(errObj)
	_ = errObj.Set("stack", stackVal)
}

// formatStackTrace builds the callsite array for the current stack and runs it
// through Error.prepareStackTrace (falling back to the default formatter).
func (interp *Interpreter) formatStackTrace(target engine.Value) engine.Value {
	infos := interp.captureStackFrames()
	if infos == nil {
		return engine.Str("")
	}
	callsites := interp.newArray(interp.makeCallSites(infos))
	prep, _ := interp.constructors["Error"].Get("prepareStackTrace")
	if cb, ok := prep.(callableValue); ok {
		if sv, err := cb.callWith(engine.Undefined(), []engine.Value{target, callsites}); err == nil {
			return sv
		}
	}
	// Default formatter.
	var sb strings.Builder
	if o, ok := target.AsObject(); ok {
		name, _ := o.Get("name")
		msg, _ := o.Get("message")
		sb.WriteString(v8Str(name, "Error"))
		if ms := v8Str(msg, ""); ms != "" {
			sb.WriteString(": " + ms)
		}
	}
	for _, cs := range callsites.(*engine.ArrayValue).Elems() {
		sb.WriteString("\n    at " + interp.callToString(cs))
	}
	return engine.Str(sb.String())
}

// captureStackFrames reads the active VM frames into callsite records, ordered
// innermost-first (matching V8). Native frames are skipped automatically
// because they do not push a vmFrame.
func (interp *Interpreter) captureStackFrames() []*callsiteInfo {
	vm := interp.currentVM
	if vm == nil || len(vm.frames) == 0 {
		return nil
	}
	limit := 10
	if limitVal, err := interp.constructors["Error"].Get("stackTraceLimit"); err == nil && !limitVal.IsUndefined() {
		if n, ok2 := limitVal.Int(); ok2 && n >= 0 {
			limit = n
		}
	}
	var infos []*callsiteInfo
	for i := len(vm.frames) - 1; i >= 0; i-- {
		if len(infos) >= limit {
			break
		}
		f := &vm.frames[i]
		tmpl := f.tmpl
		info := &callsiteInfo{
			funcName: tmpl.Name,
			file:     tmpl.SourceFile,
			line:     lineForPC(tmpl, f.pc),
			thisVal:  engine.Undefined(),
		}
		if f.base >= 0 && f.base < len(vm.stack) {
			info.thisVal = vm.stack[f.base]
		}
		info.typeName = interp.callsiteTypeName(info.thisVal)
		info.methodName = ""
		infos = append(infos, info)
	}
	return infos
}

// makeCallSites wraps callsiteInfo records as JS callsite objects exposing the
// methods npm code (depd/http-errors) expects.
func (interp *Interpreter) makeCallSites(infos []*callsiteInfo) []engine.Value {
	out := make([]engine.Value, 0, len(infos))
	for _, info := range infos {
		out = append(out, interp.makeCallSite(info))
	}
	return out
}

func (interp *Interpreter) makeCallSite(info *callsiteInfo) engine.Value {
	obj := engine.NewObject()
	engine.SetProto(obj, interp.objectProto)
	file, line, col := info.file, info.line, info.col
	funcName := info.funcName
	thisVal := info.thisVal
	typeName, methodName := info.typeName, info.methodName

	_ = obj.Set("getFileName", interp.nativeMethod("getFileName", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if file == "" {
			return engine.Undefined(), nil
		}
		return engine.Str(file), nil
	}))
	_ = obj.Set("getLineNumber", interp.nativeMethod("getLineNumber", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.IntValue(line), nil
	}))
	_ = obj.Set("getColumnNumber", interp.nativeMethod("getColumnNumber", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.IntValue(col), nil
	}))
	_ = obj.Set("isEval", interp.nativeMethod("isEval", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	_ = obj.Set("getEvalOrigin", interp.nativeMethod("getEvalOrigin", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = obj.Set("getFunctionName", interp.nativeMethod("getFunctionName", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if funcName == "" {
			return engine.Undefined(), nil
		}
		return engine.Str(funcName), nil
	}))
	_ = obj.Set("getThis", interp.nativeMethod("getThis", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return thisVal, nil
	}))
	_ = obj.Set("getTypeName", interp.nativeMethod("getTypeName", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if typeName == "" {
			return engine.Undefined(), nil
		}
		return engine.Str(typeName), nil
	}))
	_ = obj.Set("getMethodName", interp.nativeMethod("getMethodName", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if methodName == "" {
			return engine.Undefined(), nil
		}
		return engine.Str(methodName), nil
	}))
	_ = obj.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(formatCallSite(file, line, col, funcName)), nil
	}))
	return obj
}

// callToString invokes a JS object's toString() method (used on callsites).
func (interp *Interpreter) callToString(v engine.Value) string {
	if o, ok := v.AsObject(); ok {
		if m, err := o.Get("toString"); err == nil {
			if cb, ok := m.(callableValue); ok {
				if s, err2 := cb.callWith(v, nil); err2 == nil {
					return v8Str(s, v.String())
				}
			}
		}
	}
	return v.String()
}

// callsiteTypeName derives a type name from a `this` value for getTypeName().
func (interp *Interpreter) callsiteTypeName(v engine.Value) string {
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	if o, ok := v.AsObject(); ok {
		if ctor, err := o.Get("constructor"); err == nil {
			if co, ok := ctor.AsObject(); ok {
				if name, err2 := co.Get("name"); err2 == nil && !name.IsUndefined() {
					if s := name.String(); s != "" {
						return s
					}
				}
			}
		}
		return "Object"
	}
	return ""
}

// lineForPC maps a bytecode PC to a source line via the function's line table.
func lineForPC(tmpl *bytecode.FuncTemplate, pc int) int {
	ls := tmpl.LineStarts
	best := 0
	lo, hi := 0, len(ls)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if ls[mid].PC <= pc {
			best = ls[mid].Line
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// formatCallSite renders a callsite as "func (file:line:col)" or "file:line:col".
func formatCallSite(file string, line, col int, funcName string) string {
	loc := file
	if line > 0 {
		loc += ":" + strconv.Itoa(line)
		if col > 0 {
			loc += ":" + strconv.Itoa(col)
		}
	}
	if loc == "" {
		loc = "<anonymous>"
	}
	if funcName != "" {
		return funcName + " (" + loc + ")"
	}
	return loc
}

// v8Str returns val.String() when val is not undefined, else def.
func v8Str(val engine.Value, def string) string {
	if val.IsUndefined() || val.IsNull() {
		return def
	}
	return val.String()
}
