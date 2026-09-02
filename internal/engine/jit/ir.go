// Package jit contains the portable, conservative JIT tiers.
//
// Tier 1 intentionally uses a small typed IR and a Go executor. It is a real
// compilation boundary (bytecode is lowered and verified before execution),
// while keeping the machine-code backend optional and platform-specific.

package jit

import (
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
)

func (v quickValue) nullish() (bool, bool) {
	switch v.kind {
	case quickUndefined, quickNull:
		return true, true
	case quickNumber, quickBoolean, quickObject, quickString, quickBigInt, quickSymbol:
		return false, true
	default:
		return false, false
	}
}

func fromEngine(v engine.Value, objects *[maxQuickSlots]engine.Value, objectCount *int) quickValue {
	if v == nil {
		return quickValue{}
	}
	if v.IsUndefined() {
		return quickValue{kind: quickUndefined}
	}
	if v.IsNull() {
		return quickValue{kind: quickNull}
	}
	if v.Type() == engine.TypeNumber {
		n, _ := v.Float()
		return numberValue(n)
	}
	if v.Type() == engine.TypeBoolean {
		b, _ := v.Bool()
		return booleanValue(b)
	}
	if v.IsObject() {
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		return quickValue{kind: quickObject, ref: uint8(ref)}
	}
	if v.Type() == engine.TypeString || v.Type() == engine.TypeBigInt {
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		truthy, _ := v.Bool()
		kind := quickString
		if v.Type() == engine.TypeBigInt {
			kind = quickBigInt
		}
		return quickValue{kind: kind, ref: uint8(ref), b: truthy}
	}
	if v.Type() == engine.TypeSymbol {
		// R3-1: Symbols join the Quick value domain as opaque references
		// (identity is the only observable property: always truthy, never
		// nullish, strict equality by pointer identity).
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		return quickValue{kind: quickSymbol, ref: uint8(ref)}
	}
	return quickValue{}
}

func (v quickValue) toEngine(objects []engine.Value) engine.Value {
	switch v.kind {
	case quickNumber:
		return engine.Number(v.num)
	case quickBoolean:
		return engine.Boolean(v.b)
	case quickNull:
		return engine.Null()
	case quickUndefined:
		return engine.Undefined()
	case quickObject:
		if int(v.ref) < len(objects) {
			return objects[v.ref]
		}
		return engine.Undefined()
	case quickString, quickBigInt, quickSymbol:
		if int(v.ref) < len(objects) {
			return objects[v.ref]
		}
		return engine.Undefined()
	default:
		return engine.Undefined()
	}
}

func strictQuickEqual(left, right quickValue, values []engine.Value) (bool, bool) {
	if left.kind == quickInvalid || right.kind == quickInvalid || left.kind == quickSelf || right.kind == quickSelf {
		return false, false
	}
	if left.kind != right.kind {
		return false, true
	}
	switch left.kind {
	case quickUndefined, quickNull:
		return true, true
	case quickNumber:
		return left.num == right.num, true
	case quickBoolean:
		return left.b == right.b, true
	case quickString:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref].String() == values[right.ref].String(), true
	case quickBigInt:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		leftInt, leftOK := engine.BigIntValue(values[left.ref])
		rightInt, rightOK := engine.BigIntValue(values[right.ref])
		return leftOK && rightOK && leftInt.Cmp(rightInt) == 0, leftOK && rightOK
	case quickSymbol:
		// R3-2: Symbol strict equality is pure identity — the same symbol
		// object is equal only to itself, never to another symbol (even with
		// the same description) and never to any other type. There is no
		// coercion: different kinds already returned false above.
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref] == values[right.ref], true
	case quickObject:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref] == values[right.ref], true
	default:
		return false, false
	}
}

func floatMod(a, b float64) float64 {
	// math.Mod is kept in a helper so the executor's operation semantics stay
	// explicit and easy to compare with the interpreter.
	return math.Mod(a, b)
}
