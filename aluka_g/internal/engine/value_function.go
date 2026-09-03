// 函数值与访问器：原生函数包装（含 length）、getter/setter 载体。

package engine

// functionValue 包装一个 Go Func 为 JS Function 对象。
// objectValue 值嵌入：闭包/函数对象创建是热路径，合成一次分配。
type functionValue struct {
	objectValue
	fn   Func
	name string
}

// NewFunction 创建函数对象。
func NewFunction(name string, fn Func) Function {
	return NewFunctionLen(name, 0, fn)
}

// NewFunctionLen 创建带显式 length（形参数量）的函数对象——Node 兼容面
// 需要 length 与真实 API 对齐（如 module.register.length === 1）。
func NewFunctionLen(name string, length int, fn Func) Function {
	f := &functionValue{
		fn:   fn,
		name: name,
	}
	f.shape = rootShape
	register(&f.objectValue)
	_ = f.Set("name", Str(name))
	_ = f.Set("length", IntValue(length))
	// ES 语义：普通函数都有 .prototype 属性（一个对象，constructor 指向自身）。
	// engine.NewFunction 常用于原生模块构造器（如 stream.Transform），npm 包常
	// 访问 <Ctor>.prototype（iconv-lite 的 Object.create(Transform.prototype)）。
	proto := NewObject()
	_ = proto.Set("constructor", f)
	_ = f.objectValue.Set("prototype", proto)
	return f
}

func (f *functionValue) Type() ValueType { return TypeFunction }

func (f *functionValue) String() string {
	if f.name == "" {
		return "[Function (anonymous)]"
	}
	return "[Function: " + f.name + "]"
}

func (f *functionValue) IsFunction() bool { return true }

func (f *functionValue) IsObject() bool { return true }

func (f *functionValue) AsObject() (Object, bool) { return &f.objectValue, true }

func (f *functionValue) AsFunction() (Function, bool) { return f, true }

func (f *functionValue) Call(args []Value) (Value, error) {
	return f.fn(args)
}

// AccessorValue wraps a getter/setter pair. It is stored as a property value
// on an object to model ES2015 class accessors and Object.defineProperty
// accessors. The VM/interpreter detect it via type assertion and invoke the
// getter/setter with the appropriate `this` binding instead of returning the
// accessor itself.
type AccessorValue struct {
	Getter Value // function or Undefined
	Setter Value // function or Undefined
}

// NewAccessor creates an accessor value. Either getter or setter may be nil
// (treated as undefined — i.e. a no-op getter/setter).
func NewAccessor(getter, setter Value) *AccessorValue {
	if getter == nil {
		getter = Undefined()
	}
	if setter == nil {
		setter = Undefined()
	}
	return &AccessorValue{Getter: getter, Setter: setter}
}

func (a *AccessorValue) Type() ValueType { return TypeObject } // internal sentinel

func (a *AccessorValue) String() string { return "[Accessor]" }

func (a *AccessorValue) Int() (int, bool) { return 0, false }

func (a *AccessorValue) Float() (float64, bool) { return 0, false }

func (a *AccessorValue) Bool() (bool, bool) { return false, true }

func (a *AccessorValue) IsUndefined() bool { return false }

func (a *AccessorValue) IsNull() bool { return false }

func (a *AccessorValue) IsObject() bool { return false }

func (a *AccessorValue) IsFunction() bool { return false }

func (a *AccessorValue) AsObject() (Object, bool) { return nil, false }

func (a *AccessorValue) AsFunction() (Function, bool) { return nil, false }

// SetAccessor installs a getter/setter pair as an own property on obj.
func SetAccessor(obj Object, key string, getter, setter Value) {
	// 直接 objectValue。
	if ov, ok := obj.(*objectValue); ok {
		ov.setSlot(key, NewAccessor(getter, setter))
		return
	}
	// 嵌入 objectValue 的类型（functionValue/ArrayValue/BufferValue）。
	if embedded := embeddedObjectValue(obj); embedded != nil {
		embedded.setSlot(key, NewAccessor(getter, setter))
		return
	}
	// Fall back to plain set (accessors unsupported on this type).
	_ = obj.Set(key, NewAccessor(getter, setter))
}

// IsAccessorValue 判断槽位值是否为访问器。
func IsAccessorValue(v Value) bool {
	_, ok := v.(*AccessorValue)
	return ok
}

// UpdateAccessor installs or updates a single getter or setter on obj. If an
// accessor already exists for key, only the requested half (getter or setter)
// is updated, preserving the other. Used by class assembly when get/set pairs
// are installed as separate method definitions.
func UpdateAccessor(obj Object, key string, isGetter bool, fn Value) {
	ov, ok := obj.(*objectValue)
	if !ok {
		return
	}
	if existing, exists := ov.getSlot(key); exists {
		if acc, ok := existing.(*AccessorValue); ok {
			if isGetter {
				acc.Getter = fn
			} else {
				acc.Setter = fn
			}
			return
		}
	}
	getter, setter := Undefined(), Undefined()
	if isGetter {
		getter = fn
	} else {
		setter = fn
	}
	ov.setSlot(key, NewAccessor(getter, setter))
}

// FindAccessor walks the prototype chain of obj looking for an accessor
// stored under key. Returns the accessor and true if found.
func FindAccessor(obj Value, key string) (*AccessorValue, bool) {
	cur := obj
	for cur != nil {
		if o, ok := cur.(*objectValue); ok {
			if v, exists := o.getSlot(key); exists {
				if acc, ok := v.(*AccessorValue); ok {
					return acc, true
				}
				// Non-accessor own property shadows accessors up the chain.
				return nil, false
			}
			if o.proto == nil {
				return nil, false
			}
			// 交给循环重新分发（functionValue/ArrayValue/闭包等原型类型）。
			cur = o.proto
		} else if a, ok := cur.(*ArrayValue); ok {
			{
				if v, exists := a.objectValue.getSlot(key); exists {
					if acc, ok := v.(*AccessorValue); ok {
						return acc, true
					}
					return nil, false
				}
			}
			cur = GetProto(cur)
		} else if f, ok := cur.(*functionValue); ok {
			{
				if v, exists := f.objectValue.getSlot(key); exists {
					if acc, ok := v.(*AccessorValue); ok {
						return acc, true
					}
					return nil, false
				}
			}
			cur = GetProto(cur)
		} else if p := GetProto(cur); p != nil {
			// 自定义类型（如闭包）作为原型：经 Proto() 解包继续。
			cur = p
		} else {
			return nil, false
		}
	}
	return nil, false
}
