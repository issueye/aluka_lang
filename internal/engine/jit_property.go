package engine

// OwnDataProperty returns an own data property from a plain object without
// walking the prototype chain or invoking accessors, Proxy traps, or other
// user code. JIT identity guards use this primitive so speculative profiling
// and failed guards remain unobservable.
func OwnDataProperty(value Value, key string) (Value, bool) {
	obj, isObject := value.(*objectValue)
	if !isObject || obj.shape == nil || obj.deleted != nil && obj.deleted[key] {
		return nil, false
	}
	idx, exists := obj.shape.lookup(key)
	if !exists || idx < 0 || idx >= len(obj.slots) {
		return nil, false
	}
	property := obj.slots[idx]
	if property == nil {
		return nil, false
	}
	if _, isAccessor := property.(*AccessorValue); isAccessor {
		return nil, false
	}
	return property, true
}

// NumericOwnProperty returns a Number-valued own data property without
// invoking accessors or walking the prototype chain. It is the only property
// primitive exposed to the portable JIT tier; callers must guard the returned
// shape and slot before reusing a cached access.
func NumericOwnProperty(value Value, key string) (number float64, shapeID uint64, slot int, ok bool) {
	obj, isObject := value.(*objectValue)
	if !isObject || obj.shape == nil || obj.deleted != nil && obj.deleted[key] {
		return 0, 0, 0, false
	}
	idx, exists := obj.shape.lookup(key)
	if !exists || idx < 0 || idx >= len(obj.slots) {
		return 0, 0, 0, false
	}
	property := obj.slots[idx]
	if property == nil || property.Type() != TypeNumber {
		return 0, 0, 0, false
	}
	number, _ = property.Float()
	return number, obj.shape.id, idx, true
}

// GuardedNumericOwnProperty is the monomorphic fast path after a caller has
// cached shapeID and slot with NumericOwnProperty.
func GuardedNumericOwnProperty(value Value, key string, shapeID uint64, slot int) (float64, bool) {
	obj, isObject := value.(*objectValue)
	if !isObject || obj.shape == nil || obj.shape.id != shapeID || slot < 0 || slot >= len(obj.slots) ||
		obj.deleted != nil && obj.deleted[key] {
		return 0, false
	}
	if currentSlot, exists := obj.shape.lookup(key); !exists || currentSlot != slot {
		return 0, false
	}
	property := obj.slots[slot]
	if property == nil || property.Type() != TypeNumber {
		return 0, false
	}
	number, _ := property.Float()
	return number, true
}

// GuardedSetNumericOwnProperty updates an existing Number-valued own data
// property. It never adds a property, walks prototypes, or invokes accessors.
func GuardedSetNumericOwnProperty(value Value, key string, shapeID uint64, slot int, number float64) bool {
	obj, isObject := value.(*objectValue)
	if !isObject || obj.shape == nil || obj.shape.id != shapeID || slot < 0 || slot >= len(obj.slots) ||
		obj.deleted != nil && obj.deleted[key] {
		return false
	}
	if currentSlot, exists := obj.shape.lookup(key); !exists || currentSlot != slot {
		return false
	}
	property := obj.slots[slot]
	if property == nil || property.Type() != TypeNumber {
		return false
	}
	obj.slots[slot] = Number(number)
	return true
}

// GuardedSetNumericOwnPropertySlot is the R4-4 store-side companion for the
// property PIC: it sets an existing Number-valued own data property whose
// (shapeID, slot) pair the caller already resolved with NumericOwnProperty in
// the same guard transaction. No user code (accessor, Proxy trap, prototype
// walk) can run between the probe and this store, so the shape/slot pair
// cannot go stale; the deleted map is still re-checked defensively, matching
// GuardedSetNumericOwnProperty without repeating the name-to-slot hash lookup.
func GuardedSetNumericOwnPropertySlot(value Value, key string, shapeID uint64, slot int, number float64) bool {
	obj, isObject := value.(*objectValue)
	if !isObject || obj.shape == nil || obj.shape.id != shapeID || slot < 0 || slot >= len(obj.slots) ||
		obj.deleted != nil && obj.deleted[key] {
		return false
	}
	property := obj.slots[slot]
	if property == nil || property.Type() != TypeNumber {
		return false
	}
	obj.slots[slot] = Number(number)
	return true
}

// GuardedMethodLookup resolves a method along a plain object-value prototype
// chain without invoking accessors, Proxy traps or any other user code. It is
// the only method primitive exposed to the portable JIT tier: the caller must
// still assert the returned value's identity before calling it. ok is false
// when the receiver is not a plain object (Proxy, function, embedded types)
// or when the chain leaves the plain-object world, so unsafe receivers always
// fall back instead of running user code during profiling or guard checks.
func GuardedMethodLookup(obj Object, key string) (Value, bool) {
	cur, isPlain := obj.(*objectValue)
	if !isPlain {
		return nil, false
	}
	for cur != nil {
		if v, ok := cur.getSlot(key); ok {
			return v, true
		}
		if cur.proto == nil {
			return nil, false
		}
		next, ok := cur.proto.(*objectValue)
		if !ok {
			// A non-plain prototype (function, Proxy, embedded type) could run
			// user code on Get; the JIT must not touch it.
			return nil, false
		}
		cur = next
	}
	return nil, false
}
