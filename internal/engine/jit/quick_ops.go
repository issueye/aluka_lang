package jit

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// This file holds the R3-4 / R3-5 primitive operation helpers shared by the
// Quick executor (ir.go) and the trace executor (trace.go).
//
// Design rules:
//   - The helpers are self-contained in this package: the jit package must not
//     depend on the interpreter, and Tier 0 semantics (interpreter/operations.go,
//     interpreter/bigint_ops.go) are never modified. Behavior is mirrored
//     instead, and the jitdiff differential suite proves the tiers agree.
//   - Allocations (String concat results, BigInt results) happen only inside
//     Quick/Trace execution via quickAlloc; Tier 0 never sees the object
//     buffer. The buffer is fixed-size, so exhaustion simply falls back to
//     Tier 0 (the caller returns GuardFailed and the VM replays Tier 0).
//   - Exception conditions (BigInt division by zero, negative shift, BigInt
//     `>>>`, mixed BigInt/Number) are deliberately NOT raised here: the caller
//     returns GuardFailed and Tier 0 re-runs the operation and raises the
//     exact same RangeError / TypeError. Observable behavior is therefore
//     identical across tiers.

// quickAlloc stores a newly allocated engine value (a String concat result or
// a BigInt operation result) in the shared object buffer so a quickValue can
// reference it. It returns false (guard failure -> Tier 0 fallback) when the
// fixed-size buffer is exhausted or the value is nil.
func quickAlloc(objects *[maxQuickSlots]engine.Value, count *int, kind quickKind, v engine.Value) (quickValue, bool) {
	if v == nil || *count >= maxQuickSlots {
		return quickValue{}, false
	}
	ref := uint8(*count)
	objects[*count] = v
	*count++
	truthy, _ := v.Bool()
	return quickValue{kind: kind, ref: ref, b: truthy}, true
}

// quickStringConcat implements R3-4 String `+`: the caller has already guarded
// both operands to quickString. It uses engine.ConcatStrings — the exact Tier 0
// helper — so flat/rope/empty-string semantics stay identical.
func quickStringConcat(l, r quickValue, objects *[maxQuickSlots]engine.Value, count *int) (quickValue, bool) {
	if l.kind != quickString || r.kind != quickString ||
		int(l.ref) >= maxQuickSlots || int(r.ref) >= maxQuickSlots ||
		objects[l.ref] == nil || objects[r.ref] == nil {
		return quickValue{}, false
	}
	return quickAlloc(objects, count, quickString, engine.ConcatStrings(objects[l.ref], objects[r.ref]))
}

// quickStringAnyConcat 支持 String 与任意原始类型（Number/Boolean/Undefined/Null/String）的快速拼接。
func quickStringAnyConcat(str quickValue, other quickValue, strIsLeft bool, objects *[maxQuickSlots]engine.Value, count *int) (quickValue, bool) {
	if str.kind != quickString || int(str.ref) >= maxQuickSlots || objects[str.ref] == nil {
		return quickValue{}, false
	}
	var otherVal engine.Value
	switch {
	case other.isNumber():
		otherVal = engine.Number(other.num)
	case other.kind == quickBoolean:
		otherVal = engine.Boolean(other.b)
	case other.kind == quickUndefined:
		otherVal = engine.Undefined()
	case other.kind == quickNull:
		otherVal = engine.Null()
	case other.kind == quickString:
		if int(other.ref) >= maxQuickSlots || objects[other.ref] == nil {
			return quickValue{}, false
		}
		otherVal = objects[other.ref]
	default:
		return quickValue{}, false
	}

	var res engine.Value
	if strIsLeft {
		res = engine.ConcatStrings(objects[str.ref], otherVal)
	} else {
		res = engine.ConcatStrings(otherVal, objects[str.ref])
	}
	return quickAlloc(objects, count, quickString, res)
}

// quickStringCompare implements R3-4 same-type String relational comparison.
// The order is exactly Tier 0's: compareValues uses
// strings.Compare(l.String(), r.String()) on the flattened values, so this
// helper mirrors it byte-for-byte (UTF-8 byte order on the flattened form,
// which the differential corpus keeps identical to Tier 0 by construction).
func quickStringCompare(l, r quickValue, objects *[maxQuickSlots]engine.Value) (int, bool) {
	if l.kind != quickString || r.kind != quickString ||
		int(l.ref) >= maxQuickSlots || int(r.ref) >= maxQuickSlots ||
		objects[l.ref] == nil || objects[r.ref] == nil {
		return 0, false
	}
	return strings.Compare(objects[l.ref].String(), objects[r.ref].String()), true
}

// quickBigIntRef returns the *big.Int behind a quickBigInt value (read-only;
// callers must not mutate it).
func quickBigIntRef(v quickValue, objects *[maxQuickSlots]engine.Value) (*big.Int, bool) {
	if v.kind != quickBigInt || int(v.ref) >= maxQuickSlots || objects[v.ref] == nil {
		return nil, false
	}
	return engine.BigIntValue(objects[v.ref])
}

// quickBigIntArith implements R3-5 same-type BigInt arithmetic (+ - * / %).
// Division/modulo by zero returns false so the caller falls back to Tier 0,
// which raises the RangeError ("Division by zero") with identical observable
// behavior.
func quickBigIntArith(l, r quickValue, objects *[maxQuickSlots]engine.Value, count *int, op Op) (quickValue, bool) {
	lb, lok := quickBigIntRef(l, objects)
	rb, rok := quickBigIntRef(r, objects)
	if !lok || !rok {
		return quickValue{}, false
	}
	result := new(big.Int)
	switch op {
	case OpAdd:
		result.Add(lb, rb)
	case OpSub:
		result.Sub(lb, rb)
	case OpMul:
		result.Mul(lb, rb)
	case OpDiv:
		if rb.Sign() == 0 {
			return quickValue{}, false // Tier 0 raises RangeError
		}
		result.Quo(lb, rb)
	case OpMod:
		if rb.Sign() == 0 {
			return quickValue{}, false // Tier 0 raises RangeError
		}
		result.Rem(lb, rb)
	default:
		return quickValue{}, false
	}
	return quickAlloc(objects, count, quickBigInt, engine.BigInt(result))
}

// quickBigIntBitwise implements R3-5 same-type BigInt bitwise operations
// (& | ^ << >> >>>). `>>>` (BigInt has no unsigned right shift) and negative
// shifts return false so the caller falls back to Tier 0, which raises the
// exact TypeError / RangeError the engine specifies.
func quickBigIntBitwise(l, r quickValue, objects *[maxQuickSlots]engine.Value, count *int, op Op) (quickValue, bool) {
	lb, lok := quickBigIntRef(l, objects)
	rb, rok := quickBigIntRef(r, objects)
	if !lok || !rok {
		return quickValue{}, false
	}
	result := new(big.Int)
	switch op {
	case OpBitAnd:
		result.And(lb, rb)
	case OpBitOr:
		result.Or(lb, rb)
	case OpBitXor:
		result.Xor(lb, rb)
	case OpShl:
		if rb.Sign() < 0 {
			return quickValue{}, false // Tier 0 raises RangeError
		}
		result.Lsh(lb, uint(rb.Uint64()))
	case OpShr:
		if rb.Sign() < 0 {
			return quickValue{}, false // Tier 0 raises RangeError
		}
		result.Rsh(lb, uint(rb.Uint64()))
	case OpUShr:
		return quickValue{}, false // Tier 0 raises TypeError (BigInt has no >>>)
	default:
		return quickValue{}, false
	}
	return quickAlloc(objects, count, quickBigInt, engine.BigInt(result))
}

// quickBigIntNeg implements R3-5 unary minus on a BigInt.
func quickBigIntNeg(v quickValue, objects *[maxQuickSlots]engine.Value, count *int) (quickValue, bool) {
	bi, ok := quickBigIntRef(v, objects)
	if !ok {
		return quickValue{}, false
	}
	return quickAlloc(objects, count, quickBigInt, engine.BigInt(new(big.Int).Neg(bi)))
}

// quickBigIntNot implements R3-5 unary bitwise NOT on a BigInt with the
// correct ES semantics (~x = -x-1). Note: Tier 0's OpBitNot does not dispatch
// BigInt and yields Number(-1) for every BigInt input (a Tier 0 bug recorded
// in the R3-5 report); Quick intentionally computes the correct result.
// Differential generators must therefore not route BigInt through `~` until
// Tier 0 is fixed.
func quickBigIntNot(v quickValue, objects *[maxQuickSlots]engine.Value, count *int) (quickValue, bool) {
	bi, ok := quickBigIntRef(v, objects)
	if !ok {
		return quickValue{}, false
	}
	return quickAlloc(objects, count, quickBigInt, engine.BigInt(new(big.Int).Not(bi)))
}

// quickBigIntCompare implements R3-5 same-type BigInt relational comparison,
// mirroring Tier 0's bigintCompare (Cmp on the underlying big.Ints).
func quickBigIntCompare(l, r quickValue, objects *[maxQuickSlots]engine.Value) (int, bool) {
	lb, lok := quickBigIntRef(l, objects)
	rb, rok := quickBigIntRef(r, objects)
	if !lok || !rok {
		return 0, false
	}
	return lb.Cmp(rb), true
}

// quickRelational maps a comparison result to a relational operator outcome,
// exactly like Tier 0's compareBool does for non-NaN comparisons.
func quickRelational(cmp int, op Op) bool {
	switch op {
	case OpLt:
		return cmp < 0
	case OpLe:
		return cmp <= 0
	case OpGt:
		return cmp > 0
	case OpGe:
		return cmp >= 0
	}
	return false
}

// quickLooseEqual implements R3-3 JS `==` / `!=` on the primitive quickValue
// domain (Number, String, BigInt, Boolean, null, undefined, Symbol) through
// the shared engine.LooseEquals helper. Object / self / invalid operands
// always guard-fail: object identity and every object coercion stay in Tier 0
// (the JIT never copies ToPrimitive).
//
// Known Tier 0 divergence guard: the interpreter's looseEquals (a) parses
// String operands with Value.Float() (strconv.ParseFloat), which rejects ""
// and 0x/0o-prefixed strings that the engine's ToNumber rules accept, and
// (b) computes BigInt vs Boolean with the formula (l==0n) != b, which is wrong
// for BigInt outside {0n, 1n} with b == true (7n == true must be false).
// Until those Tier 0 bugs are fixed (recorded in the R3-3 report), the exact
// diverging pairs guard-fail so Tier 0 computes the identical observable
// result and the differential suite stays zero-diff.
func quickLooseEqual(l, r quickValue, values []engine.Value) (bool, bool) {
	if l.kind == quickInvalid || r.kind == quickInvalid ||
		l.kind == quickSelf || r.kind == quickSelf ||
		l.kind == quickObject || r.kind == quickObject {
		return false, false
	}
	if tier0LooseEqDiverges(l, r, values) {
		return false, false
	}
	lv, lok := quickToEngineValue(l, values)
	rv, rok := quickToEngineValue(r, values)
	if !lok || !rok {
		return false, false
	}
	return engine.LooseEquals(lv, rv), true
}

// quickToEngineValue converts a primitive quickValue back to its engine.Value
// form for the shared equality helper. Object/self values are not converted
// (callers guard them away first).
func quickToEngineValue(v quickValue, values []engine.Value) (engine.Value, bool) {
	switch v.kind {
	case quickUndefined:
		return engine.Undefined(), true
	case quickNull:
		return engine.Null(), true
	case quickNumber:
		return engine.Number(v.num), true
	case quickBoolean:
		return engine.Boolean(v.b), true
	case quickString, quickBigInt, quickSymbol:
		if int(v.ref) >= len(values) || values[v.ref] == nil {
			return nil, false
		}
		return values[v.ref], true
	default:
		return nil, false
	}
}

// tier0LooseEqDiverges reports the exact operand pairs where the shared
// helper's JS-correct result differs from Tier 0's looseEquals (see
// quickLooseEqual). It must stay in sync with the interpreter so the
// differential suite remains zero-diff until the Tier 0 bugs are fixed.
func tier0LooseEqDiverges(l, r quickValue, values []engine.Value) bool {
	// (a) String vs Number/Boolean: Tier 0 parses the String with
	// Value.Float() (ParseFloat after TrimSpace); the helper uses the engine
	// ToNumber rules. Results disagree exactly when the ToNumber parse
	// succeeds but ParseFloat fails: "" and whitespace-only strings (-> 0)
	// and 0x/0X/0o/0O-prefixed strings.
	for _, pair := range [2][2]quickValue{{l, r}, {r, l}} {
		str, other := pair[0], pair[1]
		if str.kind != quickString || (other.kind != quickNumber && other.kind != quickBoolean) {
			continue
		}
		if int(str.ref) >= len(values) || values[str.ref] == nil {
			continue
		}
		s := values[str.ref].String()
		if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			continue // Tier 0's parse succeeds with the same numeric value
		}
		return !math.IsNaN(engine.StringToNumber(s))
	}
	// (b) BigInt vs Boolean true with a BigInt outside {0n, 1n}: Tier 0's
	// formula (l==0n) != b answers true, JS answers false (ToNumber(true)==1;
	// for 1n vs true both tiers agree, so the guard is exactly the divergent
	// set BigInt != 0n && BigInt != 1n && b == true).
	for _, pair := range [2][2]quickValue{{l, r}, {r, l}} {
		bi, b := pair[0], pair[1]
		if bi.kind != quickBigInt || b.kind != quickBoolean || !b.b {
			continue
		}
		if int(bi.ref) >= len(values) || values[bi.ref] == nil {
			continue
		}
		value, ok := engine.BigIntValue(values[bi.ref])
		if !ok || value == nil {
			continue
		}
		return value.Sign() != 0 && value.Cmp(big.NewInt(1)) != 0
	}
	return false
}
