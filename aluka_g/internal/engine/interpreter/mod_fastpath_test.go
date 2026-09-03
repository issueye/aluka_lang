package interpreter

import (
	"math"
	"testing"
)

func TestModFastParity(t *testing.T) {
	vals := []float64{
		0, -0.0, 1, -1, 2, -2, 3, 7, -7, 100, -100,
		1 << 52, -(1 << 52), (1 << 53) - 1, -(1<<53)+1,
		1 << 53, -(1 << 53), 1<<53 + 2, 1e15, -1e15, 1e16,
		0.5, -0.5, 1.5, 2.5, -2.5,
		math.Inf(1), math.Inf(-1), math.NaN(),
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64,
		9007199254740993, // 2^53+1（不可表示精确整数）
	}
	for _, a := range vals {
		for _, b := range vals {
			want := math.Mod(a, b)
			got := want
			if a2, ok1 := fastInt64(a); ok1 {
				if b2, ok2 := fastInt64(b); ok2 && b2 != 0 {
					m := a2 % b2
					if m == 0 && a2 < 0 {
						got = math.Copysign(0, -1)
					} else {
						got = float64(m)
					}
				}
			}
			if math.IsNaN(want) && math.IsNaN(got) {
				continue
			}
			if want != got || math.Signbit(want) != math.Signbit(got) {
				t.Fatalf("a=%v(%x) b=%v(%x): fmod=%v(%x) fast=%v(%x)", a, math.Float64bits(a), b, math.Float64bits(b), want, math.Float64bits(want), got, math.Float64bits(got))
			}
		}
	}
}
