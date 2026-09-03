package nodevm

// node:vm 内置模块——脚本/上下文求值与 context 隔离（开发计划 3.9）。
//
// 实现模型：每个 vm context 对应一个独立的 interpreter.VM（拥有自己的
// global 对象与内建原型）。createContext(sandbox) 把 sandbox 的自有属性
// 拷贝进子 realm 全局，并在每次 run 后把子全局写回 sandbox（Node 语义：
// sandbox 即该 context 的 global，宿主写入/context 写入互相可见）。
// 因此：上下文 A 的全局变量不会泄漏到 B，也不会泄漏到宿主。
//
// 已知差异（记录于开发文档）：
//   - 子 context 与宿主共享 engine 级内建原型对象（Object.prototype 等），
//     不做 per-realm 原型克隆（V8 语义）；但全局对象本身完全隔离。
//   - Script 编译期仅做语法校验；createCachedData 返回源码字节的 Buffer
//     （非 V8 缓存格式），传入 cachedData 时 cachedDataRejected=true。
//   - vm.Module / SourceTextModule / SyntheticModule 仅提供构造器与最小
//     方法面（experimental API）。

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
)

// vmContext 表示一个隔离的 vm 执行上下文。
type vmContext struct {
	vm      *interpreter.VM // 隔离引擎（独立 global）
	sandbox engine.Object   // 用户可见的 contextified 对象（可 nil）
}

// scriptData 保存 vm.Script 实例的编译源。
type scriptData struct {
	source   string
	filename string
}

// vmState 持有 node:vm 模块的跨调用状态（context 注册表 + Script 注册表）。
type vmState struct {
	mu       sync.Mutex
	contexts map[engine.Object]*vmContext // sandbox/子全局 → context
	scripts  map[engine.Object]*scriptData
}

// NewVMModule 构造 node:vm 模块导出对象。
func NewVMModule(ctx engine.Context) (engine.Value, error) {
	state := &vmState{
		contexts: make(map[engine.Object]*vmContext),
		scripts:  make(map[engine.Object]*scriptData),
	}
	m := engine.NewObject()

	// --- runInThisContext(code[, options]) -------------------------------
	_ = m.Set("runInThisContext", engine.NewFunction("runInThisContext", func(args []engine.Value) (engine.Value, error) {
		code := nodebase.StrArg(args, 0)
		filename := "evalmachine.<anonymous>"
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
			}
		}
		return ctx.Eval(code, filename)
	}))

	// --- createContext([contextObject]) -----------------------------------
	_ = m.Set("createContext", engine.NewFunction("createContext", func(args []engine.Value) (engine.Value, error) {
		var sandbox engine.Object
		if len(args) > 0 && args[0].IsObject() {
			sandbox, _ = args[0].AsObject()
		}
		if sandbox == nil {
			// 无 sandbox：新建隔离 context，返回其全局对象。
			subVM, err := interpreter.NewVM()
			if err != nil {
				return engine.Undefined(), err
			}
			vc := &vmContext{vm: subVM}
			state.mu.Lock()
			state.contexts[subVM.Global()] = vc
			state.mu.Unlock()
			return subVM.Global(), nil
		}
		vc, err := state.contextify(sandbox)
		if err != nil {
			return engine.Undefined(), err
		}
		return vc.sandbox, nil
	}))

	// --- isContext(object) ------------------------------------------------
	_ = m.Set("isContext", engine.NewFunction("isContext", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Boolean(false), nil
		}
		obj, _ := args[0].AsObject()
		return engine.Boolean(state.lookupContext(obj) != nil), nil
	}))

	// --- runInContext(code, contextifiedObject[, options]) ----------------
	_ = m.Set("runInContext", engine.NewFunction("runInContext", func(args []engine.Value) (engine.Value, error) {
		code := nodebase.StrArg(args, 0)
		if len(args) < 2 || !args[1].IsObject() {
			return engine.Undefined(), fmt.Errorf("vm.runInContext: contextifiedObject must be an object")
		}
		obj, _ := args[1].AsObject()
		vc := state.lookupContext(obj)
		if vc == nil {
			return engine.Undefined(), fmt.Errorf("The argument 'contextifiedObject' is not a vm.Context")
		}
		filename := "evalmachine.<anonymous>"
		if len(args) > 2 && args[2].IsObject() {
			if o, ok := args[2].AsObject(); ok {
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
			}
		}
		return state.runIn(vc, code, filename)
	}))

	// --- runInNewContext(code[, contextObject][, options]) ----------------
	_ = m.Set("runInNewContext", engine.NewFunction("runInNewContext", func(args []engine.Value) (engine.Value, error) {
		code := nodebase.StrArg(args, 0)
		var sandbox engine.Object
		if len(args) > 1 && args[1].IsObject() {
			sandbox, _ = args[1].AsObject()
		}
		filename := "evalmachine.<anonymous>"
		if len(args) > 2 && args[2].IsObject() {
			if o, ok := args[2].AsObject(); ok {
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
			}
		}
		if sandbox == nil {
			subVM, err := interpreter.NewVM()
			if err != nil {
				return engine.Undefined(), err
			}
			return subVM.Eval(code, filename)
		}
		vc, err := state.contextify(sandbox)
		if err != nil {
			return engine.Undefined(), err
		}
		return state.runIn(vc, code, filename)
	}))

	// --- compileFunction(code, params[, options]) --------------------------
	_ = m.Set("compileFunction", engine.NewFunction("compileFunction", func(args []engine.Value) (engine.Value, error) {
		code := nodebase.StrArg(args, 0)
		params := make([]string, 0)
		if len(args) > 1 {
			if arr, ok := args[1].(*engine.ArrayValue); ok {
				for _, p := range arr.Elems() {
					params = append(params, p.String())
				}
			}
		}
		name := "" // Node 默认函数名为空串
		filename := "evalmachine.<anonymous>"
		if len(args) > 2 && args[2].IsObject() {
			if o, ok := args[2].AsObject(); ok {
				if n, err := o.Get("name"); err == nil && !n.IsUndefined() {
					name = n.String()
				}
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
			}
		}
		// 构造 `(function (p1, p2) { code })` 并在当前 context 求值。
		paramStr := ""
		for i, p := range params {
			if i > 0 {
				paramStr += ", "
			}
			paramStr += p
		}
		src := fmt.Sprintf("(function (%s) {\n%s\n})", paramStr, code)
		fnVal, err := ctx.Eval(src, filename)
		if err != nil {
			return engine.Undefined(), err
		}
		// 显式设置函数名（Node 语义：默认空串）。
		if fo, ok := fnVal.AsObject(); ok {
			_ = fo.Set("name", engine.Str(name))
		}
		return fnVal, nil
	}))

	// --- measureMemory() → Promise -----------------------------------------
	_ = m.Set("measureMemory", engine.NewFunction("measureMemory", func(args []engine.Value) (engine.Value, error) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		entry := func(estimate, rangeVal float64) engine.Value {
			o := engine.NewObject()
			_ = o.Set("jsMemoryEstimate", engine.Number(estimate))
			_ = o.Set("jsMemoryRange", engine.NewArray([]engine.Value{engine.Number(rangeVal), engine.Number(rangeVal)}))
			return o
		}
		total := engine.NewObject()
		_ = total.Set("jsMemoryEstimate", engine.Number(float64(ms.Sys)))
		_ = total.Set("jsMemoryRange", engine.NewArray([]engine.Value{engine.Number(float64(ms.Sys)), engine.Number(float64(ms.Sys))}))
		out := engine.NewObject()
		_ = out.Set("total", total)
		_ = out.Set("js", entry(float64(ms.HeapAlloc), float64(ms.HeapAlloc)))
		return nodebase.PromiseResolved(ctx, out)
	}))

	// --- vm.Script ---------------------------------------------------------
	scriptProto := engine.NewObject()
	_ = scriptProto.Set("runInThisContext", interpreter.NewNativeMethod("runInThisContext", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		data := state.scriptDataOf(this)
		if data == nil {
			return engine.Undefined(), fmt.Errorf("vm.Script.runInThisContext: not a Script instance")
		}
		filename := data.filename
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
			}
		}
		return ctx.Eval(data.source, filename)
	}))
	_ = scriptProto.Set("runInNewContext", interpreter.NewNativeMethod("runInNewContext", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		data := state.scriptDataOf(this)
		if data == nil {
			return engine.Undefined(), fmt.Errorf("vm.Script.runInNewContext: not a Script instance")
		}
		var sandbox engine.Object
		if len(args) > 0 && args[0].IsObject() {
			sandbox, _ = args[0].AsObject()
		}
		if sandbox == nil {
			subVM, err := interpreter.NewVM()
			if err != nil {
				return engine.Undefined(), err
			}
			return subVM.Eval(data.source, data.filename)
		}
		vc, err := state.contextify(sandbox)
		if err != nil {
			return engine.Undefined(), err
		}
		return state.runIn(vc, data.source, data.filename)
	}))
	_ = scriptProto.Set("runInContext", interpreter.NewNativeMethod("runInContext", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		data := state.scriptDataOf(this)
		if data == nil {
			return engine.Undefined(), fmt.Errorf("vm.Script.runInContext: not a Script instance")
		}
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Undefined(), fmt.Errorf("vm.Script.runInContext: contextifiedObject required")
		}
		obj, _ := args[0].AsObject()
		vc := state.lookupContext(obj)
		if vc == nil {
			return engine.Undefined(), fmt.Errorf("The argument 'contextifiedObject' is not a vm.Context")
		}
		return state.runIn(vc, data.source, data.filename)
	}))
	_ = scriptProto.Set("createCachedData", interpreter.NewNativeMethod("createCachedData", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		data := state.scriptDataOf(this)
		if data == nil {
			return engine.Undefined(), fmt.Errorf("vm.Script.createCachedData: not a Script instance")
		}
		return gbuffer.NewBufferInstance([]byte(data.source)), nil
	}))

	scriptCtor := engine.NewFunction("Script", func(args []engine.Value) (engine.Value, error) {
		code := nodebase.StrArg(args, 0)
		filename := "evalmachine.<anonymous>"
		hasCachedData := false
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if f, err := o.Get("filename"); err == nil && !f.IsUndefined() {
					filename = f.String()
				}
				if c, err := o.Get("cachedData"); err == nil && !c.IsUndefined() && !c.IsNull() {
					hasCachedData = true
				}
			}
		}
		// 构造期编译校验：语法错误在 new Script 时抛出（Node 语义）。
		if vm := nodebase.CurrentVM(ctx); vm != nil {
			if _, err := vm.Compile(code, filename); err != nil {
				return engine.Undefined(), err
			}
		}
		inst := engine.NewObject()
		state.mu.Lock()
		state.scripts[inst] = &scriptData{source: code, filename: filename}
		state.mu.Unlock()
		if hasCachedData {
			_ = inst.Set("cachedDataRejected", engine.Boolean(true))
		}
		// 实例继承 Script.prototype 的方法（方法作为 own props 供调用）。
		for _, k := range scriptProto.Keys() {
			if v, err := scriptProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := scriptCtor.AsObject(); ok {
		_ = co.Set("prototype", scriptProto)
	}
	_ = m.Set("Script", scriptCtor)

	// --- vm.constants（Node 22：空对象）与 vm.createScript（废弃别名） --------
	_ = m.Set("constants", engine.NewObject())
	_ = m.Set("createScript", engine.NewFunction("createScript", func(args []engine.Value) (engine.Value, error) {
		// createScript(code[, options]) 是 `new vm.Script(code, options)` 的别名
		// （DEP0094）。这里通过调用 Script 构造器函数实现。
		sc, ok := scriptCtor.AsFunction()
		if !ok {
			return engine.Undefined(), fmt.Errorf("vm.createScript: Script constructor unavailable")
		}
		return sc.Call(args)
	}))

	return m, nil
}

// --- vmState 方法 -----------------------------------------------------------

// contextify 把 sandbox 对象转化为隔离 context（重复调用幂等，Node 语义）。
func (s *vmState) contextify(sandbox engine.Object) (*vmContext, error) {
	s.mu.Lock()
	if vc, ok := s.contexts[sandbox]; ok {
		s.mu.Unlock()
		return vc, nil
	}
	s.mu.Unlock()

	subVM, err := interpreter.NewVM()
	if err != nil {
		return nil, err
	}
	// 把 sandbox 自有属性拷贝进子 realm 全局。
	for _, k := range sandbox.Keys() {
		if v, e := sandbox.Get(k); e == nil {
			_ = subVM.Global().Set(k, v)
		}
	}
	_ = subVM.Global().Set("globalThis", subVM.Global())
	_ = subVM.Global().Set("global", subVM.Global())
	vc := &vmContext{vm: subVM, sandbox: sandbox}

	s.mu.Lock()
	s.contexts[sandbox] = vc
	s.contexts[subVM.Global()] = vc
	s.mu.Unlock()

	// 初始同步：sandbox 获得子 realm 的内建全局（Node：sandbox 即 global）。
	s.syncBack(vc)
	return vc, nil
}

// lookupContext 根据用户传入的对象查找隔离 context（支持 sandbox 与子全局）。
func (s *vmState) lookupContext(obj engine.Object) *vmContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contexts[obj]
}

// scriptDataOf 根据 Script 实例取编译源。
func (s *vmState) scriptDataOf(inst engine.Value) *scriptData {
	if inst == nil || !inst.IsObject() {
		return nil
	}
	o, _ := inst.AsObject()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scripts[o]
}

// syncBack 把子 realm 全局的自有属性写回 sandbox（context 内写入对宿主可见）。
func (s *vmState) syncBack(vc *vmContext) {
	if vc.sandbox == nil {
		return
	}
	global := vc.vm.Global()
	for _, k := range global.Keys() {
		if v, e := global.Get(k); e == nil {
			_ = vc.sandbox.Set(k, v)
		}
	}
}

// runIn 在指定 context 中执行代码并同步回 sandbox。
func (s *vmState) runIn(vc *vmContext, code, filename string) (engine.Value, error) {
	// 宿主侧对 sandbox 的写入对 context 可见（Node：sandbox 即 global）。
	if vc.sandbox != nil {
		for _, k := range vc.sandbox.Keys() {
			if v, e := vc.sandbox.Get(k); e == nil {
				_ = vc.vm.Global().Set(k, v)
			}
		}
	}
	result, err := vc.vm.Eval(code, filename)
	if err != nil {
		return engine.Undefined(), err
	}
	s.syncBack(vc)
	return result, nil
}
