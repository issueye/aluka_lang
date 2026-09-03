// VM class 语义：class 声明/表达式的原型链、静态成员与构造器装配。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// doMakeClass assembles a class from its ClassTemplate. If the template has a
// superclass, it is popped from the stack. The assembled constructor function
// is returned (the caller pushes it).
func (v *VM) doMakeClass(classIdx int) (engine.Value, error) {
	classTpl := v.module.Classes[classIdx]

	// 编译器先压入父类，再按声明顺序压入计算键，因此必须先逆序弹出
	// 计算键，最后才能取得父类构造器。
	computedKeys := make([]string, len(classTpl.ComputedIdx))
	for i := len(classTpl.ComputedIdx) - 1; i >= 0; i-- {
		computedKeys[i] = propertyKeyOf(v.pop())
	}

	var superCtor engine.Value
	if classTpl.HasSuper {
		superCtor = v.pop()
	}

	// Create the constructor closure.
	ctorTmpl := v.module.Functions[classTpl.CtorIdx]
	ctor := newVMClosure(v, ctorTmpl, v.captureUpvalues(ctorTmpl))
	engine.SetProto(ctor.obj, v.interp.functionProto)
	_ = ctor.obj.Set("name", engine.Str(classTpl.Name))
	_ = ctor.obj.Set("length", engine.IntValue(ctorTmpl.NumParams))

	// Create the prototype object.
	proto := engine.NewObject()
	if classTpl.HasSuper {
		// proto = Object.create(super.prototype)
		if superProto, err := v.getProperty(superCtor, "prototype"); err == nil && !superProto.IsUndefined() {
			if po, ok := superProto.(engine.Object); ok {
				engine.SetProto(proto, po)
			}
		}
	} else {
		engine.SetProto(proto, v.interp.objectProto)
	}
	_ = proto.Set("constructor", ctor)
	// class 原型成员不可枚举（ES：Object.keys(A.prototype) 为空、for-in
	// 不泄漏 constructor）。
	_ = engine.DefineOwnProperty(proto, "constructor", engine.Descriptor{HasEnumerable: true, Enumerable: false})
	_ = ctor.obj.Set("prototype", proto)

	// Wire up static inheritance: constructor's [[Prototype]] = superclass.
	// Use the underlying *objectValue so prototype-chain walks in Get() work.
	if classTpl.HasSuper {
		if superCl, ok := superCtor.(*vmClosure); ok {
			engine.SetProto(ctor.obj, superCl.obj)
		} else if superObj, ok := superCtor.AsObject(); ok {
			engine.SetProto(ctor.obj, superObj)
		}
	}

	// Install methods / accessors.
	computedPos := 0
	for mi, m := range classTpl.Methods {
		mTmpl := v.module.Functions[m.TmplIdx]
		mClosure := newVMClosure(v, mTmpl, v.captureUpvalues(mTmpl))
		engine.SetProto(mClosure.obj, v.interp.functionProto)
		_ = mClosure.obj.Set("name", engine.Str(m.Name))
		_ = mClosure.obj.Set("length", engine.IntValue(mTmpl.NumParams))

		// Create prototype for the method (so it can be used as a constructor).
		mProto := engine.NewObject()
		engine.SetProto(mProto, v.interp.objectProto)
		_ = mProto.Set("constructor", mClosure)
		_ = mClosure.obj.Set("prototype", mProto)

		var target engine.Object
		if m.Static {
			target = ctor.obj
		} else {
			target = proto
		}

		// 计算键方法：用运行时求值的键安装。
		name := m.Name
		if computedPos < len(classTpl.ComputedIdx) && classTpl.ComputedIdx[computedPos] == mi {
			name = computedKeys[computedPos]
			computedPos++
		}

		switch m.Kind {
		case bytecode.MethodKindNormal:
			_ = target.Set(name, mClosure)
			// class 方法（含静态）不可枚举（ES class 元素语义）。
			_ = engine.DefineOwnProperty(target, name, engine.Descriptor{HasEnumerable: true, Enumerable: false})
		case bytecode.MethodKindGetter:
			engine.UpdateAccessor(target, name, true, mClosure)
		case bytecode.MethodKindSetter:
			engine.UpdateAccessor(target, name, false, mClosure)
		}
	}

	return ctor, nil
}
