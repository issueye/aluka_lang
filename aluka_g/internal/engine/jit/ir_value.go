// Quick 值表示：类型化 quickValue、数值/布尔构造与真值判定。

package jit

import (
	"math"
)

type quickKind uint8

const (
	quickInvalid quickKind = iota
	quickUndefined
	quickNull
	quickNumber
	quickBoolean
	quickSelf
	quickObject
	quickString
	quickBigInt
	quickSymbol
)

type quickValue struct {
	num  float64
	kind quickKind
	ref  uint8
	b    bool
}

const quickUint32Range = 4294967296.0

func quickUint32(n float64) uint32 {
	if math.IsNaN(n) || math.IsInf(n, 0) || n == 0 {
		return 0
	}
	n = math.Mod(math.Trunc(n), quickUint32Range)
	if n < 0 {
		n += quickUint32Range
	}
	return uint32(n)
}

func quickInt32(n float64) int32 { return int32(quickUint32(n)) }

func numberValue(n float64) quickValue { return quickValue{kind: quickNumber, num: n} }

func booleanValue(b bool) quickValue { return quickValue{kind: quickBoolean, b: b} }

func (v quickValue) isNumber() bool { return v.kind == quickNumber }

func (v quickValue) truthy() (bool, bool) {
	switch v.kind {
	case quickBoolean:
		return v.b, true
	case quickNumber:
		return v.num != 0 && !math.IsNaN(v.num), true
	case quickUndefined, quickNull:
		return false, true
	case quickObject:
		return true, true
	case quickString, quickBigInt:
		return v.b, true
	case quickSymbol:
		// Symbols are always truthy (no falsy Symbol exists).
		return true, true
	default:
		return false, false
	}
}
