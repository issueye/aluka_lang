package jit

import "testing"

// TestConfigBudgetDefaultsPreserveLegacyBehavior proves the R5-3/R5-4
// conservative defaults: a zero-value new-field config normalizes exactly to
// the legacy behavior (static thresholds, adaptive off, unlimited budget and
// queue, one background compile worker).
func TestConfigBudgetDefaultsPreserveLegacyBehavior(t *testing.T) {
	c := Config{}.Normalized()
	if c.Threshold != 1000 || c.BackedgeThreshold != 10000 {
		t.Fatalf("legacy thresholds changed: %+v", c)
	}
	if c.Adaptive {
		t.Fatalf("adaptive must default to off: %+v", c)
	}
	if c.CompileBudgetNanos != 0 || c.CompileQueueLimit != 0 {
		t.Fatalf("budget/queue must default to unlimited: %+v", c)
	}
	if c.CompileWorkers != 1 {
		t.Fatalf("CompileWorkers must normalize to 1: %+v", c)
	}
	if c.AdaptiveBoostEvery != 0 || c.AdaptiveCoolEvery != 0 {
		t.Fatalf("adaptive windows must stay zero while adaptive is off: %+v", c)
	}
}

// TestConfigAdaptiveAndWorkersNormalization proves the R5-3/R5-4 defaults when
// the features are enabled: adaptive feedback windows and an explicit worker
// count survive Normalized, and explicit values are preserved.
func TestConfigAdaptiveAndWorkersNormalization(t *testing.T) {
	c := (Config{Adaptive: true, CompileWorkers: 4}).Normalized()
	if !c.Adaptive {
		t.Fatal("adaptive flag lost")
	}
	if c.AdaptiveBoostEvery != 64 || c.AdaptiveCoolEvery != 8 {
		t.Fatalf("adaptive window defaults wrong: %+v", c)
	}
	if c.CompileWorkers != 4 {
		t.Fatalf("explicit CompileWorkers lost: %+v", c)
	}
	c2 := (Config{Adaptive: true, AdaptiveBoostEvery: 32, AdaptiveCoolEvery: 4, CompileWorkers: 2}).Normalized()
	if c2.AdaptiveBoostEvery != 32 || c2.AdaptiveCoolEvery != 4 || c2.CompileWorkers != 2 {
		t.Fatalf("explicit values lost: %+v", c2)
	}
	if MaxAdaptiveBoost != 4 || MaxAdaptiveCool != 4 {
		t.Fatalf("adaptive level caps changed: boost=%d cool=%d", MaxAdaptiveBoost, MaxAdaptiveCool)
	}
}
