// VM 闭包对象与 upvalue：vmClosure 的 engine.Value 实现、upvalue 捕获与关闭/重开。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// upvalue is a closure capture. Open upvalues point at a stack slot; when the
// owning frame exits, open upvalues referencing its slots are closed (the
// value is copied into `closed`).
type upvalue struct {
	slot   *engine.Value // non-nil while open
	index  int           // absolute stack index while open (for stack growth rebasing)
	closed engine.Value  // set when closed
}

// captureUpvalues creates upvalue objects for a closure based on its template.
func (v *VM) captureUpvalues(tmpl *bytecode.FuncTemplate) []*upvalue {
	if len(tmpl.Upvalues) == 0 {
		return nil
	}
	frame := v.cur()
	uvs := make([]*upvalue, len(tmpl.Upvalues))
	for i, cap := range tmpl.Upvalues {
		if cap.IsLocal {
			// Open upvalue: point at the parent frame's local slot.
			absIndex := frame.base + cap.Index
			slot := &v.stack[absIndex]
			// Reuse an existing open upvalue for the same slot so that
			// multiple closures capturing the same variable share state
			// (writes by one closure are visible to others).
			var existing *upvalue
			for _, ou := range frame.openUpvalues {
				if ou.slot == slot || (ou.slot != nil && ou.index == absIndex) {
					existing = ou
					break
				}
			}
			if existing != nil {
				uvs[i] = existing
			} else {
				uv := &upvalue{slot: slot, index: absIndex}
				frame.openUpvalues = append(frame.openUpvalues, uv)
				uvs[i] = uv
			}
		} else {
			// Inherited upvalue: share the parent's upvalue.
			uvs[i] = frame.upvalues[cap.Index]
		}
	}
	return uvs
}

// upvalueClose 记录被关闭的 upvalue 及其栈槽绝对索引（async 恢复时用于
// 把闭包修改的值同步回函数体读写的栈槽）。
type upvalueClose struct {
	uv     *upvalue
	absIdx int
}

// reopenUpvalues rebinds captures that were closed while an async frame was
// suspended. Rebinding is required because the resumed function reads the
// local stack slot directly while nested closures read/write through upvalues.
// Keeping an upvalue closed after resume lets those two copies diverge.
func (v *VM) reopenUpvalues(frame *vmFrame, closed []upvalueClose, base int) {
	for _, cu := range closed {
		relIdx := cu.absIdx - base
		if relIdx < 0 || relIdx >= len(v.stack)-base {
			continue
		}
		absIdx := base + relIdx
		v.stack[absIdx] = cu.uv.closed
		cu.uv.index = absIdx
		cu.uv.slot = &v.stack[absIdx]
		frame.openUpvalues = append(frame.openUpvalues, cu.uv)
	}
}

// closeUpvalues closes all open upvalues pointing at stack slots >= threshold.
// 返回被关闭的 upvalue 列表（供 async 挂起/恢复时同步捕获的局部变量）。
func (v *VM) closeUpvalues(threshold int) []upvalueClose {
	frame := v.cur()
	kept := frame.openUpvalues[:0]
	var closed []upvalueClose
	for _, uv := range frame.openUpvalues {
		if uv.slot == nil {
			continue
		}
		idx := uv.index
		if idx < 0 || idx >= len(v.stack) {
			// Defensive fallback for upvalues created before index tracking.
			idx = -1
			for i := range v.stack {
				if &v.stack[i] == uv.slot {
					idx = i
					break
				}
			}
		}
		if idx >= threshold {
			uv.closed = *uv.slot
			uv.slot = nil
			closed = append(closed, upvalueClose{uv: uv, absIdx: idx})
		} else {
			kept = append(kept, uv)
		}
	}
	frame.openUpvalues = kept
	return closed
}

// vmClosure is a function value backed by a bytecode template + captured upvalues.
type vmClosure struct {
	obj           engine.Object // function object (name, length, prototype, ...)
	vm            *VM
	tmpl          *bytecode.FuncTemplate
	upvalues      []*upvalue
	module        *bytecode.Module // 定义时的 module（OpMakeClosure 内部创建子闭包时用）
	asyncCtx      interface{}      // 创建时捕获的异步上下文（AsyncLocalStorage 传播用）
	jitState      *quickJITState   // VM-local hot state; nil while JIT is disabled/cold
	jitGeneration uint64
}

// newVMClosure creates a vmClosure with a fresh function object.
func newVMClosure(vm *VM, tmpl *bytecode.FuncTemplate, upvalues []*upvalue) *vmClosure {
	c := &vmClosure{
		obj:           engine.NewObject(),
		vm:            vm,
		tmpl:          tmpl,
		upvalues:      upvalues,
		module:        vm.module,
		jitState:      vm.jitStateFor(tmpl),
		jitGeneration: vm.jitGeneration,
	}
	if AsyncContextCapture != nil {
		c.asyncCtx = AsyncContextCapture()
	}
	return c
}

func (c *vmClosure) Type() engine.ValueType { return engine.TypeFunction }

func (c *vmClosure) String() string {
	if name, _ := c.obj.Get("name"); !name.IsUndefined() {
		return "[Function: " + name.String() + "]"
	}
	return "[Function (anonymous)]"
}

func (c *vmClosure) Int() (int, bool) { return 0, false }

func (c *vmClosure) Float() (float64, bool) { return 0, false }

func (c *vmClosure) Bool() (bool, bool) { return true, true }

func (c *vmClosure) IsUndefined() bool { return false }

func (c *vmClosure) IsNull() bool { return false }

func (c *vmClosure) IsObject() bool { return true }

func (c *vmClosure) IsFunction() bool { return true }

func (c *vmClosure) AsObject() (engine.Object, bool) { return c, true }

func (c *vmClosure) AsFunction() (engine.Function, bool) { return c, true }

func (c *vmClosure) Get(key string) (engine.Value, error) { return c.obj.Get(key) }

func (c *vmClosure) Set(key string, val engine.Value) error { return c.obj.Set(key, val) }

func (c *vmClosure) Keys() []string { return c.obj.Keys() }

func (c *vmClosure) Delete(key string) bool { return c.obj.Delete(key) }

func (c *vmClosure) Proto() engine.Object { return engine.GetProto(c.obj) }

func (c *vmClosure) SetProto(proto engine.Object) { engine.SetProto(c.obj, proto) }

// UnwrapObject 暴露承载属性存储的底层对象（engine.ObjectUnwrapper）。
func (c *vmClosure) UnwrapObject() engine.Object { return c.obj }

// Call implements engine.Function — calls the closure with this=undefined.
func (c *vmClosure) Call(args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, engine.Undefined(), args, false)
}

// callWith 以指定 this 调用（实现 callableValue，供 Function.prototype
// call/apply/bind 正确绑定 this；P0-2 配套修复）。
func (c *vmClosure) callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, thisVal, args, false)
}

// construct 以 new 语义调用（供 Function.prototype.call/apply 对构造器路径）。
func (c *vmClosure) construct(args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, engine.Undefined(), args, true)
}
