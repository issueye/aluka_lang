package jitdiff

import (
	"fmt"
	"testing"
)

// TestR47FixedCasesHitTargetTier proves the R4-7 fixed cases are not Tier 0
// sleight of hand: the % and bitwise cases must execute in the amd64 Native
// tier in Auto, and the ** case must stay in Quick (native pow requires
// libm). The R4-8 case asserts the safepoint-prefix event log instead.
func TestR47FixedCasesHitTargetTier(t *testing.T) {
	for _, c := range FixedCases() {
		if c.ID != -71 && c.ID != -72 && c.ID != -73 && c.ID != -74 {
			continue
		}
		c.applySource()
		t.Run(fmt.Sprintf("%02d", -c.ID), func(t *testing.T) {
			results, err := RunCase(c, c.Params)
			if err != nil {
				if mismatch, ok := err.(*Mismatch); ok {
					t.Fatalf("cross-tier mismatch: %v", mismatch)
				}
				t.Fatalf("infrastructure error: %v", err)
			}
			auto := results[2].Stats
			switch c.ID {
			case -71, -72:
				// The leaf must have been compiled and executed natively.
				if auto.NativeCompiled == 0 || auto.NativeExecuted == 0 {
					t.Fatalf("case %d did not execute natively in Auto: %+v", c.ID, auto)
				}
			case -73:
				// Cancellation must interrupt identically in every tier; the
				// committed % prefix is proven by the equal event logs.
				if auto.NativeTracesCompiled == 0 {
					t.Fatalf("case %d did not compile the mod trace: %+v", c.ID, auto)
				}
			case -74:
				// pow must stay in Quick: no native code, Quick executions.
				if auto.NativeCompiled != 0 || auto.NativeExecuted != 0 || auto.Executed == 0 {
					t.Fatalf("case %d pow must stay in Quick: %+v", c.ID, auto)
				}
			}
		})
	}
}

// TestGeneratedCorpusIncludesR47Kinds proves the R4-7 generator emits the
// nativeMod / nativeBitwise kinds in the random corpus.
func TestGeneratedCorpusIncludesR47Kinds(t *testing.T) {
	g := NewGenerator(prSeed, prParams())
	kinds := make(map[Kind]int)
	for _, c := range g.Generate(600) {
		kinds[c.Kind]++
	}
	for _, k := range []Kind{KindNativeMod, KindNativeBitwise} {
		if kinds[k] == 0 {
			t.Fatalf("generator produced no %s cases in 600 samples", k)
		}
	}
}
