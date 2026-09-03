package interpreter

import (
	"fmt"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupStructuredClone 注册全局 structuredClone（P1-4）。
//
// 按结构化克隆语义深拷贝：普通对象/数组/Map/Set/Date/Buffer 递归克隆，
// 函数与不可克隆对象抛 TypeError（DataCloneError 近似）。
func (interp *Interpreter) setupStructuredClone() {
	_ = interp.globalObj.Set("structuredClone", interp.makeFunc("structuredClone", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		v, err := interp.cloneValue(args[0], make(map[engine.Value]engine.Value))
		if err != nil {
			return engine.Undefined(), err
		}
		return v, nil
	}))
}

// cloneValue 深拷贝一个值。seen 用于打破对象引用环（同一对象引用多次时
// 保持共享引用，与结构化克隆语义一致）。
func (interp *Interpreter) cloneValue(v engine.Value, seen map[engine.Value]engine.Value) (engine.Value, error) {
	if v == nil {
		return engine.Undefined(), nil
	}
	switch v.Type() {
	case engine.TypeUndefined, engine.TypeNull, engine.TypeBoolean, engine.TypeNumber, engine.TypeString, engine.TypeSymbol, engine.TypeBigInt:
		return v, nil
	}

	// 函数不可克隆。
	if v.IsFunction() {
		return nil, fmt.Errorf("%w: function is not cloneable", engine.ErrTypeError)
	}

	// 循环引用：返回已克隆的实例。
	if c, ok := seen[v]; ok {
		return c, nil
	}

	// Date 克隆时间值。
	if d, ok := engine.AsDate(v); ok {
		nd := engine.NewDateValue(d.TimeMs())
		if p := engine.GetProto(v); p != nil {
			engine.SetProto(nd, p)
		}
		seen[v] = nd
		return nd, nil
	}

	// Buffer 克隆字节。
	if b, ok := engine.AsBuffer(v); ok {
		nb := engine.NewBuffer(append([]byte(nil), b...))
		seen[v] = nb
		return nb, nil
	}

	// Array 克隆元素。
	if arr, ok := v.(*engine.ArrayValue); ok {
		na := engine.NewArray(nil)
		if p := engine.GetProto(v); p != nil {
			engine.SetProto(na, p)
		}
		seen[v] = na
		for _, e := range arr.Elems() {
			ce, err := interp.cloneValue(e, seen)
			if err != nil {
				return nil, err
			}
			na.Append(ce)
		}
		// 复制非索引自定义属性。
		for _, k := range arr.Keys() {
			if k == "length" {
				continue
			}
			if _, err := strconv.Atoi(k); err == nil {
				continue
			}
			if pv, err := arr.Get(k); err == nil {
				cp, cerr := interp.cloneValue(pv, seen)
				if cerr != nil {
					return nil, cerr
				}
				_ = na.Set(k, cp)
			}
		}
		return na, nil
	}

	// Map：克隆键值。
	if mv, ok := v.(*MapValue); ok {
		nm := NewMapValue(interp)
		seen[v] = nm
		for _, k := range mv.keys {
			entry := mv.entries[k]
			if entry == nil {
				continue
			}
			ck, err := interp.cloneValue(entry.key, seen)
			if err != nil {
				return nil, err
			}
			cv, err := interp.cloneValue(entry.value, seen)
			if err != nil {
				return nil, err
			}
			nm.mapSet(ck, cv)
		}
		return nm, nil
	}

	// Set：克隆元素。
	if sv, ok := v.(*SetValue); ok {
		ns := NewSetValue(interp)
		seen[v] = ns
		for _, k := range sv.keys {
			e := sv.values[k]
			ce, err := interp.cloneValue(e, seen)
			if err != nil {
				return nil, err
			}
			ns.setAdd(ce)
		}
		return ns, nil
	}

	// 普通对象：克隆 own 属性（含继承原型设置）。
	if o, ok := v.AsObject(); ok {
		no := engine.NewObject()
		if p := engine.GetProto(v); p != nil {
			engine.SetProto(no, p)
		}
		seen[v] = no
		for _, k := range o.Keys() {
			pv, err := o.Get(k)
			if err != nil {
				return nil, err
			}
			cp, cerr := interp.cloneValue(pv, seen)
			if cerr != nil {
				return nil, cerr
			}
			_ = no.Set(k, cp)
		}
		return no, nil
	}

	return engine.Undefined(), nil
}
