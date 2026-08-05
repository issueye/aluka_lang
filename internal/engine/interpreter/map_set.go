package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"unsafe"

	"github.com/aluka-lang/aluka/internal/engine"
)

// mapKey produces a canonical string key for a JS value under the
// SameValueZero comparison (used by Map/Set key matching):
//   - NaN matches NaN
//   - -0 matches +0
//   - Objects compared by reference identity
//
// The result is only used internally as a Go map key; it is never exposed.
func mapKey(v engine.Value) string {
	switch v.Type() {
	case engine.TypeNumber:
		f, _ := v.Float()
		if math.IsNaN(f) {
			return "\x00num:NaN"
		}
		// Normalize -0 to +0 so they match under SameValueZero.
		if f == 0 {
			f = 0
		}
		return "\x00num:" + strconv.FormatFloat(f, 'g', -1, 64)
	case engine.TypeString:
		return "\x00str:" + v.String()
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return "\x00bool:" + strconv.FormatBool(b)
	case engine.TypeUndefined:
		return "\x00undef"
	case engine.TypeNull:
		return "\x00null"
	case engine.TypeSymbol:
		if s, ok := v.(*engine.SymbolValue); ok {
			return "\x00sym:" + s.SymbolKey()
		}
		return "\x00sym:" + v.String()
	default:
		// Objects (including functions, arrays, custom values) use identity.
		// We use the Go pointer address as the identity key.
		if v == nil {
			return "\x00nil"
		}
		return "\x00obj:" + strconv.FormatUint(uint64(uintptr(unsafe.Pointer(reflectValuePtr(v)))), 16)
	}
}

// reflectValuePtr returns a pointer to the underlying interface value's data
// word, usable as a unique identity token for object comparison.
// We avoid importing reflect for performance; instead we use unsafe to read
// the interface's data word directly.
func reflectValuePtr(v engine.Value) unsafe.Pointer {
	// An interface in Go is a two-word struct: (type pointer, data pointer).
	// We return the data pointer as the identity.
	type ifaceHeader struct {
		_   unsafe.Pointer
		ptr unsafe.Pointer
	}
	return (*ifaceHeader)(unsafe.Pointer(&v)).ptr
}

// === Map ===================================================================

// mapEntry holds a single key/value pair in a Map, preserving insertion order.
type mapEntry struct {
	key   engine.Value
	value engine.Value
}

// MapValue is a JS Map: an ordered collection of key/value pairs with
// SameValueZero key equality.
type MapValue struct {
	obj    engine.Object
	interp *Interpreter
	keys   []string          // canonical keys in insertion order
	entries map[string]*mapEntry
}

// NewMapValue creates an empty Map bound to the given prototype.
func NewMapValue(interp *Interpreter) *MapValue {
	m := &MapValue{
		interp:  interp,
		entries: make(map[string]*mapEntry),
	}
	obj := engine.NewObject()
	engine.SetProto(obj, interp.mapProto)
	m.obj = obj
	return m
}

func (m *MapValue) Type() engine.ValueType { return engine.TypeObject }
func (m *MapValue) String() string         { return "[object Map]" }
func (m *MapValue) Int() (int, bool)       { return 0, false }
func (m *MapValue) Float() (float64, bool) { return 0, false }
func (m *MapValue) Bool() (bool, bool)     { return true, true }
func (m *MapValue) IsUndefined() bool      { return false }
func (m *MapValue) IsNull() bool           { return false }
func (m *MapValue) IsObject() bool         { return true }
func (m *MapValue) IsFunction() bool       { return false }
func (m *MapValue) AsObject() (engine.Object, bool) { return m, true }
func (m *MapValue) AsFunction() (engine.Function, bool) { return nil, false }

func (m *MapValue) Get(key string) (engine.Value, error) { return m.obj.Get(key) }
func (m *MapValue) Set(key string, v engine.Value) error { return m.obj.Set(key, v) }
func (m *MapValue) Keys() []string                       { return m.obj.Keys() }
func (m *MapValue) Delete(key string) bool               { return m.obj.Delete(key) }

// mapGet returns the value associated with key, or undefined if absent.
func (m *MapValue) mapGet(key engine.Value) engine.Value {
	if e, ok := m.entries[mapKey(key)]; ok {
		return e.value
	}
	return engine.Undefined()
}

// mapSet associates key with value, preserving insertion order.
func (m *MapValue) mapSet(key, value engine.Value) {
	k := mapKey(key)
	if _, exists := m.entries[k]; !exists {
		m.keys = append(m.keys, k)
	}
	m.entries[k] = &mapEntry{key: key, value: value}
}

// mapHas reports whether key is present.
func (m *MapValue) mapHas(key engine.Value) bool {
	_, ok := m.entries[mapKey(key)]
	return ok
}

// mapDelete removes key, returning whether it was present.
func (m *MapValue) mapDelete(key engine.Value) bool {
	k := mapKey(key)
	if _, ok := m.entries[k]; !ok {
		return false
	}
	delete(m.entries, k)
	for i, ek := range m.keys {
		if ek == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
	return true
}

// mapSize returns the number of entries.
func (m *MapValue) mapSize() int { return len(m.keys) }

// mapClear removes all entries.
func (m *MapValue) mapClear() {
	m.keys = m.keys[:0]
	m.entries = make(map[string]*mapEntry)
}

// === Set ===================================================================

// SetValue is a JS Set: an ordered collection of unique values with
// SameValueZero equality.
type SetValue struct {
	obj    engine.Object
	interp *Interpreter
	keys   []string // canonical keys in insertion order
	values map[string]engine.Value
}

// NewSetValue creates an empty Set bound to the given prototype.
func NewSetValue(interp *Interpreter) *SetValue {
	s := &SetValue{
		interp: interp,
		values: make(map[string]engine.Value),
	}
	obj := engine.NewObject()
	engine.SetProto(obj, interp.setProto)
	s.obj = obj
	return s
}

func (s *SetValue) Type() engine.ValueType { return engine.TypeObject }
func (s *SetValue) String() string         { return "[object Set]" }
func (s *SetValue) Int() (int, bool)       { return 0, false }
func (s *SetValue) Float() (float64, bool) { return 0, false }
func (s *SetValue) Bool() (bool, bool)     { return true, true }
func (s *SetValue) IsUndefined() bool      { return false }
func (s *SetValue) IsNull() bool           { return false }
func (s *SetValue) IsObject() bool         { return true }
func (s *SetValue) IsFunction() bool       { return false }
func (s *SetValue) AsObject() (engine.Object, bool) { return s, true }
func (s *SetValue) AsFunction() (engine.Function, bool) { return nil, false }

func (s *SetValue) Get(key string) (engine.Value, error) { return s.obj.Get(key) }
func (s *SetValue) Set(key string, v engine.Value) error { return s.obj.Set(key, v) }
func (s *SetValue) Keys() []string                       { return s.obj.Keys() }
func (s *SetValue) Delete(key string) bool               { return s.obj.Delete(key) }

func (s *SetValue) setAdd(val engine.Value) {
	k := mapKey(val)
	if _, exists := s.values[k]; !exists {
		s.keys = append(s.keys, k)
	}
	s.values[k] = val
}

func (s *SetValue) setHas(val engine.Value) bool {
	_, ok := s.values[mapKey(val)]
	return ok
}

func (s *SetValue) setDelete(val engine.Value) bool {
	k := mapKey(val)
	if _, ok := s.values[k]; !ok {
		return false
	}
	delete(s.values, k)
	for i, ek := range s.keys {
		if ek == k {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			break
		}
	}
	return true
}

func (s *SetValue) setSize() int { return len(s.keys) }

func (s *SetValue) setClear() {
	s.keys = s.keys[:0]
	s.values = make(map[string]engine.Value)
}

// === WeakMap ===============================================================

// WeakMapValue is a JS WeakMap: keys must be objects; entries are not iterable.
// We use the object identity (pointer) as the Go map key. This is not a true
// weak reference (entries prevent GC of keys), but matches the observable API.
type WeakMapValue struct {
	obj    engine.Object
	interp *Interpreter
	values map[uintptr]engine.Value
}

// NewWeakMapValue creates an empty WeakMap bound to the given prototype.
func NewWeakMapValue(interp *Interpreter) *WeakMapValue {
	w := &WeakMapValue{
		interp: interp,
		values: make(map[uintptr]engine.Value),
	}
	obj := engine.NewObject()
	engine.SetProto(obj, interp.weakMapProto)
	w.obj = obj
	return w
}

func (w *WeakMapValue) Type() engine.ValueType { return engine.TypeObject }
func (w *WeakMapValue) String() string         { return "[object WeakMap]" }
func (w *WeakMapValue) Int() (int, bool)       { return 0, false }
func (w *WeakMapValue) Float() (float64, bool) { return 0, false }
func (w *WeakMapValue) Bool() (bool, bool)     { return true, true }
func (w *WeakMapValue) IsUndefined() bool      { return false }
func (w *WeakMapValue) IsNull() bool           { return false }
func (w *WeakMapValue) IsObject() bool         { return true }
func (w *WeakMapValue) IsFunction() bool       { return false }
func (w *WeakMapValue) AsObject() (engine.Object, bool) { return w, true }
func (w *WeakMapValue) AsFunction() (engine.Function, bool) { return nil, false }

func (w *WeakMapValue) Get(key string) (engine.Value, error) { return w.obj.Get(key) }
func (w *WeakMapValue) Set(key string, v engine.Value) error { return w.obj.Set(key, v) }
func (w *WeakMapValue) Keys() []string                       { return w.obj.Keys() }
func (w *WeakMapValue) Delete(key string) bool               { return w.obj.Delete(key) }

func weakKey(key engine.Value) (uintptr, bool) {
	if !key.IsObject() {
		return 0, false
	}
	return uintptr(reflectValuePtr(key)), true
}

func (w *WeakMapValue) wmGet(key engine.Value) engine.Value {
	if k, ok := weakKey(key); ok {
		if v, found := w.values[k]; found {
			return v
		}
	}
	return engine.Undefined()
}

func (w *WeakMapValue) wmSet(key, value engine.Value) bool {
	k, ok := weakKey(key)
	if !ok {
		return false
	}
	w.values[k] = value
	return true
}

func (w *WeakMapValue) wmHas(key engine.Value) bool {
	k, ok := weakKey(key)
	if !ok {
		return false
	}
	_, found := w.values[k]
	return found
}

func (w *WeakMapValue) wmDelete(key engine.Value) bool {
	k, ok := weakKey(key)
	if !ok {
		return false
	}
	if _, found := w.values[k]; !found {
		return false
	}
	delete(w.values, k)
	return true
}

// === WeakSet ===============================================================

// WeakSetValue is a JS WeakSet: values must be objects; not iterable.
type WeakSetValue struct {
	obj    engine.Object
	interp *Interpreter
	values map[uintptr]bool
}

// NewWeakSetValue creates an empty WeakSet bound to the given prototype.
func NewWeakSetValue(interp *Interpreter) *WeakSetValue {
	w := &WeakSetValue{
		interp: interp,
		values: make(map[uintptr]bool),
	}
	obj := engine.NewObject()
	engine.SetProto(obj, interp.weakSetProto)
	w.obj = obj
	return w
}

func (w *WeakSetValue) Type() engine.ValueType { return engine.TypeObject }
func (w *WeakSetValue) String() string         { return "[object WeakSet]" }
func (w *WeakSetValue) Int() (int, bool)       { return 0, false }
func (w *WeakSetValue) Float() (float64, bool) { return 0, false }
func (w *WeakSetValue) Bool() (bool, bool)     { return true, true }
func (w *WeakSetValue) IsUndefined() bool      { return false }
func (w *WeakSetValue) IsNull() bool           { return false }
func (w *WeakSetValue) IsObject() bool         { return true }
func (w *WeakSetValue) IsFunction() bool       { return false }
func (w *WeakSetValue) AsObject() (engine.Object, bool) { return w, true }
func (w *WeakSetValue) AsFunction() (engine.Function, bool) { return nil, false }

func (w *WeakSetValue) Get(key string) (engine.Value, error) { return w.obj.Get(key) }
func (w *WeakSetValue) Set(key string, v engine.Value) error { return w.obj.Set(key, v) }
func (w *WeakSetValue) Keys() []string                       { return w.obj.Keys() }
func (w *WeakSetValue) Delete(key string) bool               { return w.obj.Delete(key) }

func (w *WeakSetValue) wsAdd(val engine.Value) bool {
	k, ok := weakKey(val)
	if !ok {
		return false
	}
	w.values[k] = true
	return true
}

func (w *WeakSetValue) wsHas(val engine.Value) bool {
	k, ok := weakKey(val)
	if !ok {
		return false
	}
	return w.values[k]
}

func (w *WeakSetValue) wsDelete(val engine.Value) bool {
	k, ok := weakKey(val)
	if !ok {
		return false
	}
	if !w.values[k] {
		return false
	}
	delete(w.values, k)
	return true
}

// === Iterator helpers ======================================================

// mapIterKind controls what a Map/Set iterator yields.
type mapIterKind int

const (
	mapIterEntries mapIterKind = iota
	mapIterKeys
	mapIterValues
)

// === setupMap / setupSet / setupWeakMap / setupWeakSet =====================

func (interp *Interpreter) setupMap() {
	interp.mapProto = engine.NewObject()
	engine.SetProto(interp.mapProto, interp.objectProto)

	// Map.prototype.get(key)
	_ = interp.mapProto.Set("get", interp.nativeMethod("get", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.get called on non-Map", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return m.mapGet(engine.Undefined()), nil
		}
		return m.mapGet(args[0]), nil
	}))

	// Map.prototype.set(key, value)
	_ = interp.mapProto.Set("set", interp.nativeMethod("set", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.set called on non-Map", engine.ErrTypeError)
		}
		var key, val engine.Value = engine.Undefined(), engine.Undefined()
		if len(args) > 0 {
			key = args[0]
		}
		if len(args) > 1 {
			val = args[1]
		}
		m.mapSet(key, val)
		return m, nil
	}))

	// Map.prototype.has(key)
	_ = interp.mapProto.Set("has", interp.nativeMethod("has", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: Map.prototype.has called on non-Map", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(m.mapHas(engine.Undefined())), nil
		}
		return engine.Boolean(m.mapHas(args[0])), nil
	}))

	// Map.prototype.delete(key)
	_ = interp.mapProto.Set("delete", interp.nativeMethod("delete", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: Map.prototype.delete called on non-Map", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(m.mapDelete(engine.Undefined())), nil
		}
		return engine.Boolean(m.mapDelete(args[0])), nil
	}))

	// Map.prototype.clear()
	_ = interp.mapProto.Set("clear", interp.nativeMethod("clear", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.clear called on non-Map", engine.ErrTypeError)
		}
		m.mapClear()
		return engine.Undefined(), nil
	}))

	// Map.prototype.size (getter)
	engine.UpdateAccessor(interp.mapProto, "size", true, interp.nativeMethod("size", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.IntValue(0), fmt.Errorf("%w: Map.prototype.size called on non-Map", engine.ErrTypeError)
		}
		return engine.IntValue(m.mapSize()), nil
	}))

	// Map.prototype.forEach(callback[, thisArg])
	_ = interp.mapProto.Set("forEach", interp.nativeMethod("forEach", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.forEach called on non-Map", engine.ErrTypeError)
		}
		if len(args) == 0 || !isCallable(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: forEach requires a callback", engine.ErrTypeError)
		}
		cb, _ := asCallable(args[0])
		var thisArg engine.Value = engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		// Snapshot keys to avoid mutation-during-iteration issues.
		keys := make([]string, len(m.keys))
		copy(keys, m.keys)
		for _, k := range keys {
			e, ok := m.entries[k]
			if !ok {
				continue
			}
			if _, err := cb.callWith(thisArg, []engine.Value{e.value, e.key, m}); err != nil {
				return engine.Undefined(), err
			}
		}
		return engine.Undefined(), nil
	}))

	// Map.prototype.keys()
	_ = interp.mapProto.Set("keys", interp.nativeMethod("keys", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.keys called on non-Map", engine.ErrTypeError)
		}
		// Access VM via interp — need the current VM. We use a thread-local
		// approach by storing the VM on the interpreter (set in runModule).
		return newMapIteratorStandalone(interp, m, mapIterKeys), nil
	}))

	// Map.prototype.values()
	_ = interp.mapProto.Set("values", interp.nativeMethod("values", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.values called on non-Map", engine.ErrTypeError)
		}
		return newMapIteratorStandalone(interp, m, mapIterValues), nil
	}))

	// Map.prototype.entries()
	_ = interp.mapProto.Set("entries", interp.nativeMethod("entries", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map.prototype.entries called on non-Map", engine.ErrTypeError)
		}
		return newMapIteratorStandalone(interp, m, mapIterEntries), nil
	}))

	// Map.prototype[Symbol.iterator]() — same as entries()
	_ = interp.mapProto.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		m, ok := this.(*MapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Map is not iterable", engine.ErrTypeError)
		}
		return newMapIteratorStandalone(interp, m, mapIterEntries), nil
	}))

	// Map constructor: new Map([iterable])
	mapCtor := interp.makeFunc("Map", func(args []engine.Value) (engine.Value, error) {
		m := NewMapValue(interp)
		if len(args) > 0 && !args[0].IsNull() && !args[0].IsUndefined() {
			if err := populateMapFromIterable(interp, m, args[0]); err != nil {
				return engine.Undefined(), err
			}
		}
		return m, nil
	})
	_ = mapCtor.Set("prototype", interp.mapProto)
	_ = interp.mapProto.Set("constructor", mapCtor)
	_ = interp.globalObj.Set("Map", mapCtor)
	interp.constructors["Map"] = mapCtor
}

func (interp *Interpreter) setupSet() {
	interp.setProto = engine.NewObject()
	engine.SetProto(interp.setProto, interp.objectProto)

	// Set.prototype.add(value)
	_ = interp.setProto.Set("add", interp.nativeMethod("add", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.add called on non-Set", engine.ErrTypeError)
		}
		var val engine.Value = engine.Undefined()
		if len(args) > 0 {
			val = args[0]
		}
		s.setAdd(val)
		return s, nil
	}))

	// Set.prototype.has(value)
	_ = interp.setProto.Set("has", interp.nativeMethod("has", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: Set.prototype.has called on non-Set", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(s.setHas(engine.Undefined())), nil
		}
		return engine.Boolean(s.setHas(args[0])), nil
	}))

	// Set.prototype.delete(value)
	_ = interp.setProto.Set("delete", interp.nativeMethod("delete", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: Set.prototype.delete called on non-Set", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(s.setDelete(engine.Undefined())), nil
		}
		return engine.Boolean(s.setDelete(args[0])), nil
	}))

	// Set.prototype.clear()
	_ = interp.setProto.Set("clear", interp.nativeMethod("clear", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.clear called on non-Set", engine.ErrTypeError)
		}
		s.setClear()
		return engine.Undefined(), nil
	}))

	// Set.prototype.size (getter)
	engine.UpdateAccessor(interp.setProto, "size", true, interp.nativeMethod("size", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.IntValue(0), fmt.Errorf("%w: Set.prototype.size called on non-Set", engine.ErrTypeError)
		}
		return engine.IntValue(s.setSize()), nil
	}))

	// Set.prototype.forEach(callback[, thisArg])
	_ = interp.setProto.Set("forEach", interp.nativeMethod("forEach", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.forEach called on non-Set", engine.ErrTypeError)
		}
		if len(args) == 0 || !isCallable(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: forEach requires a callback", engine.ErrTypeError)
		}
		cb, _ := asCallable(args[0])
		var thisArg engine.Value = engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		keys := make([]string, len(s.keys))
		copy(keys, s.keys)
		for _, k := range keys {
			val, ok := s.values[k]
			if !ok {
				continue
			}
			if _, err := cb.callWith(thisArg, []engine.Value{val, val, s}); err != nil {
				return engine.Undefined(), err
			}
		}
		return engine.Undefined(), nil
	}))

	// Set.prototype.keys() — same as values()
	_ = interp.setProto.Set("keys", interp.nativeMethod("keys", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.keys called on non-Set", engine.ErrTypeError)
		}
		return newSetIteratorStandalone(interp, s, mapIterValues), nil
	}))

	// Set.prototype.values()
	_ = interp.setProto.Set("values", interp.nativeMethod("values", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.values called on non-Set", engine.ErrTypeError)
		}
		return newSetIteratorStandalone(interp, s, mapIterValues), nil
	}))

	// Set.prototype.entries()
	_ = interp.setProto.Set("entries", interp.nativeMethod("entries", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set.prototype.entries called on non-Set", engine.ErrTypeError)
		}
		return newSetIteratorStandalone(interp, s, mapIterEntries), nil
	}))

	// Set.prototype[Symbol.iterator]() — same as values()
	_ = interp.setProto.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s, ok := this.(*SetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Set is not iterable", engine.ErrTypeError)
		}
		return newSetIteratorStandalone(interp, s, mapIterValues), nil
	}))

	// Set constructor: new Set([iterable])
	setCtor := interp.makeFunc("Set", func(args []engine.Value) (engine.Value, error) {
		s := NewSetValue(interp)
		if len(args) > 0 && !args[0].IsNull() && !args[0].IsUndefined() {
			if err := populateSetFromIterable(interp, s, args[0]); err != nil {
				return engine.Undefined(), err
			}
		}
		return s, nil
	})
	_ = setCtor.Set("prototype", interp.setProto)
	_ = interp.setProto.Set("constructor", setCtor)
	_ = interp.globalObj.Set("Set", setCtor)
	interp.constructors["Set"] = setCtor
}

func (interp *Interpreter) setupWeakMap() {
	interp.weakMapProto = engine.NewObject()
	engine.SetProto(interp.weakMapProto, interp.objectProto)

	_ = interp.weakMapProto.Set("get", interp.nativeMethod("get", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakMapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: WeakMap.prototype.get called on non-WeakMap", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return w.wmGet(args[0]), nil
	}))

	_ = interp.weakMapProto.Set("set", interp.nativeMethod("set", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakMapValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: WeakMap.prototype.set called on non-WeakMap", engine.ErrTypeError)
		}
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Undefined(), fmt.Errorf("%w: WeakMap key must be an object", engine.ErrTypeError)
		}
		var val engine.Value = engine.Undefined()
		if len(args) > 1 {
			val = args[1]
		}
		w.wmSet(args[0], val)
		return w, nil
	}))

	_ = interp.weakMapProto.Set("has", interp.nativeMethod("has", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakMapValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: WeakMap.prototype.has called on non-WeakMap", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(w.wmHas(args[0])), nil
	}))

	_ = interp.weakMapProto.Set("delete", interp.nativeMethod("delete", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakMapValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: WeakMap.prototype.delete called on non-WeakMap", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(w.wmDelete(args[0])), nil
	}))

	wmCtor := interp.makeFunc("WeakMap", func(args []engine.Value) (engine.Value, error) {
		w := NewWeakMapValue(interp)
		if len(args) > 0 && !args[0].IsNull() && !args[0].IsUndefined() {
			if err := populateWeakMapFromIterable(interp, w, args[0]); err != nil {
				return engine.Undefined(), err
			}
		}
		return w, nil
	})
	_ = wmCtor.Set("prototype", interp.weakMapProto)
	_ = interp.weakMapProto.Set("constructor", wmCtor)
	_ = interp.globalObj.Set("WeakMap", wmCtor)
	interp.constructors["WeakMap"] = wmCtor
}

func (interp *Interpreter) setupWeakSet() {
	interp.weakSetProto = engine.NewObject()
	engine.SetProto(interp.weakSetProto, interp.objectProto)

	_ = interp.weakSetProto.Set("add", interp.nativeMethod("add", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakSetValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: WeakSet.prototype.add called on non-WeakSet", engine.ErrTypeError)
		}
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Undefined(), fmt.Errorf("%w: WeakSet value must be an object", engine.ErrTypeError)
		}
		w.wsAdd(args[0])
		return w, nil
	}))

	_ = interp.weakSetProto.Set("has", interp.nativeMethod("has", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakSetValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: WeakSet.prototype.has called on non-WeakSet", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(w.wsHas(args[0])), nil
	}))

	_ = interp.weakSetProto.Set("delete", interp.nativeMethod("delete", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		w, ok := this.(*WeakSetValue)
		if !ok {
			return engine.Boolean(false), fmt.Errorf("%w: WeakSet.prototype.delete called on non-WeakSet", engine.ErrTypeError)
		}
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(w.wsDelete(args[0])), nil
	}))

	wsCtor := interp.makeFunc("WeakSet", func(args []engine.Value) (engine.Value, error) {
		w := NewWeakSetValue(interp)
		if len(args) > 0 && !args[0].IsNull() && !args[0].IsUndefined() {
			if err := populateWeakSetFromIterable(interp, w, args[0]); err != nil {
				return engine.Undefined(), err
			}
		}
		return w, nil
	})
	_ = wsCtor.Set("prototype", interp.weakSetProto)
	_ = interp.weakSetProto.Set("constructor", wsCtor)
	_ = interp.globalObj.Set("WeakSet", wsCtor)
	interp.constructors["WeakSet"] = wsCtor
}

// === Iterable population helpers ===========================================

// populateMapFromIterable fills the Map from an iterable yielding [key, value]
// pairs. Uses the iterator protocol via a native helper that calls .next().
func populateMapFromIterable(interp *Interpreter, m *MapValue, iterable engine.Value) error {
	return forEachIterable(interp, iterable, func(item engine.Value) error {
		pair, ok := item.AsObject()
		if !ok {
			return fmt.Errorf("%w: iterator yielded a non-object pair", engine.ErrTypeError)
		}
		key, _ := pair.Get("0")
		val, _ := pair.Get("1")
		m.mapSet(key, val)
		return nil
	})
}

func populateSetFromIterable(interp *Interpreter, s *SetValue, iterable engine.Value) error {
	return forEachIterable(interp, iterable, func(item engine.Value) error {
		s.setAdd(item)
		return nil
	})
}

func populateWeakMapFromIterable(interp *Interpreter, w *WeakMapValue, iterable engine.Value) error {
	return forEachIterable(interp, iterable, func(item engine.Value) error {
		pair, ok := item.AsObject()
		if !ok {
			return fmt.Errorf("%w: iterator yielded a non-object pair", engine.ErrTypeError)
		}
		key, _ := pair.Get("0")
		val, _ := pair.Get("1")
		if !key.IsObject() {
			return fmt.Errorf("%w: WeakMap key must be an object", engine.ErrTypeError)
		}
		w.wmSet(key, val)
		return nil
	})
}

func populateWeakSetFromIterable(interp *Interpreter, w *WeakSetValue, iterable engine.Value) error {
	return forEachIterable(interp, iterable, func(item engine.Value) error {
		if !item.IsObject() {
			return fmt.Errorf("%w: WeakSet value must be an object", engine.ErrTypeError)
		}
		w.wsAdd(item)
		return nil
	})
}

// forEachIterable consumes an iterable via the iterator protocol, calling fn
// for each yielded value. Supports arrays directly for efficiency, then falls
// back to the [Symbol.iterator] + .next() protocol.
func forEachIterable(interp *Interpreter, iterable engine.Value, fn func(engine.Value) error) error {
	// Fast path: array of pairs.
	if arr, ok := iterable.(*engine.ArrayValue); ok {
		for _, item := range arr.Elems() {
			if err := fn(item); err != nil {
				return err
			}
		}
		return nil
	}
	// Fast path: string——字符串是内置可迭代（按码点迭代，与
	// String 迭代器一致）。primitive 不是对象，走不了下面的 AsObject。
	if iterable.Type() == engine.TypeString {
		for _, r := range iterable.String() {
			if err := fn(engine.Str(string(r))); err != nil {
				return err
			}
		}
		return nil
	}
	// Generic path: get [Symbol.iterator] and call .next().
	obj, ok := iterable.AsObject()
	if !ok {
		return fmt.Errorf("%w: value is not iterable", engine.ErrTypeError)
	}
	iterMethod, err := obj.Get(engine.SymbolIterator.SymbolKey())
	if err != nil || iterMethod.IsUndefined() {
		return fmt.Errorf("%w: value is not iterable", engine.ErrTypeError)
	}
	iter, err := callValue(iterMethod, iterable, nil)
	if err != nil {
		return err
	}
	iterObj, ok := iter.AsObject()
	if !ok {
		return fmt.Errorf("%w: iterator is not an object", engine.ErrTypeError)
	}
	nextMethod, err := iterObj.Get("next")
	if err != nil || nextMethod.IsUndefined() {
		return fmt.Errorf("%w: iterator has no next method", engine.ErrTypeError)
	}
	for {
		result, err := callValue(nextMethod, iter, nil)
		if err != nil {
			return err
		}
		resultObj, ok := result.AsObject()
		if !ok {
			return fmt.Errorf("%w: iterator.next() did not return an object", engine.ErrTypeError)
		}
		doneVal, _ := resultObj.Get("done")
		done, _ := doneVal.Bool()
		if done {
			return nil
		}
		val, _ := resultObj.Get("value")
		if err := fn(val); err != nil {
			return err
		}
	}
}

// callValue calls a callable value with the given this and args, used by
// forEachIterable to invoke [Symbol.iterator] and .next() without VM access.
func callValue(callee, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	if cl, ok := callee.(callableValue); ok {
		return cl.callWith(thisVal, args)
	}
	if f, ok := callee.AsFunction(); ok {
		return f.Call(args)
	}
	return engine.Undefined(), fmt.Errorf("%w: value is not callable", engine.ErrTypeError)
}

// === Standalone iterator constructors (no VM dependency) ===================
//
// These mirror newMapIterator/newSetIterator but only require *Interpreter,
// since the prototype methods are invoked via nativeMethod callbacks that do
// not have access to *VM.

func newMapIteratorStandalone(interp *Interpreter, m *MapValue, kind mapIterKind) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, interp.objectProto)
	nextFn := interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		if idx >= len(m.keys) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
			return result, nil
		}
		e := m.entries[m.keys[idx]]
		idx++
		var yielded engine.Value
		switch kind {
		case mapIterKeys:
			yielded = e.key
		case mapIterValues:
			yielded = e.value
		default:
			pair := engine.NewArray([]engine.Value{e.key, e.value})
			engine.SetProto(pair, interp.arrayProto)
			yielded = pair
		}
		_ = result.Set("value", yielded)
		_ = result.Set("done", engine.Boolean(false))
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}

func newSetIteratorStandalone(interp *Interpreter, s *SetValue, kind mapIterKind) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, interp.objectProto)
	nextFn := interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		if idx >= len(s.keys) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
			return result, nil
		}
		val := s.values[s.keys[idx]]
		idx++
		var yielded engine.Value
		if kind == mapIterEntries {
			pair := engine.NewArray([]engine.Value{val, val})
			engine.SetProto(pair, interp.arrayProto)
			yielded = pair
		} else {
			yielded = val
		}
		_ = result.Set("value", yielded)
		_ = result.Set("done", engine.Boolean(false))
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}
