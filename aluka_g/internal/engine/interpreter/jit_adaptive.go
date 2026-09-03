// 自适应阈值与编译预算：热度计数、收益/失败反馈、编译准入。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

type jitHotCount struct {
	calls     uint32
	backedges uint32
}

// jitAdaptiveState is the R5-3 feedback loop: counter-based, no wall clock.
// Every execution of a compiled function/trace is a benefit event; every
// guard failure / deopt / rejected compile is a failure event. After
// AdaptiveBoostEvery consecutive benefits the boost level rises (effective
// threshold halves, promoting borderline functions eagerly); after
// AdaptiveCoolEvery consecutive failures the cool level rises (effective
// threshold doubles, cooling down a VM whose compiles yield nothing). Levels
// are capped at jit.MaxAdaptiveBoost / jit.MaxAdaptiveCool.
type jitAdaptiveState struct {
	boostLevel uint8
	coolLevel  uint8
	benefits   uint64
	failures   uint64
	sinceBoost uint32
	sinceCool  uint32
}

// jitHotHeatThreshold is the R5-5 heat boundary for LRU eviction: a native
// unit is "hot" once it has been touched this many times (install counts as
// one touch, every native execution adds one). Cold units are evicted before
// hot ones; among units of the same heat class the least-recently-used
// (smallest nativeUsed clock) unit is evicted, so recency ordering is
// preserved inside each class.
const jitHotHeatThreshold = 4

// effectiveThreshold applies the R5-3 feedback loop to a static threshold:
// effective = static >> boost with the cool level shifting back (static <<
// cool). The result is clamped to [1, saturated] so no feedback level can
// overflow or zero the threshold. With Adaptive disabled the static value is
// returned unchanged.
func (v *VM) effectiveThreshold(static uint32) uint32 {
	if !v.jitConfig.Adaptive {
		return static
	}
	a := &v.jitAdaptive
	if a.boostLevel >= a.coolLevel {
		shift := uint(a.boostLevel - a.coolLevel)
		if shift >= 31 {
			return 1
		}
		if t := static >> shift; t >= 1 {
			return t
		}
		return 1
	}
	shift := uint(a.coolLevel - a.boostLevel)
	if shift >= 20 || static > ^uint32(0)>>shift {
		return ^uint32(0)
	}
	return static << shift
}

func (v *VM) callThreshold() uint32 {
	return v.effectiveThreshold(v.jitConfig.Threshold)
}

func (v *VM) backedgeThreshold() uint32 {
	return v.effectiveThreshold(v.jitConfig.BackedgeThreshold)
}

// noteAdaptiveBenefit records one execution of a compiled function or trace.
// AdaptiveBoostEvery consecutive benefits raise the boost level (lowering the
// effective threshold by one half per level), so long hotspots with low deopt
// rates promote borderline functions more eagerly.
func (v *VM) noteAdaptiveBenefit() {
	if !v.jitConfig.Adaptive {
		return
	}
	a := &v.jitAdaptive
	a.benefits++
	a.sinceBoost++
	every := v.jitConfig.AdaptiveBoostEvery
	if every == 0 {
		every = 64
	}
	if a.sinceBoost >= every && a.boostLevel < jit.MaxAdaptiveBoost {
		a.boostLevel++
		a.sinceBoost = 0
	}
}

// noteAdaptiveFailure records one guard failure / deopt / rejected compile.
// AdaptiveCoolEvery consecutive failures raise the cool level (doubling the
// effective threshold per level), cooling down VMs whose compiles yield no
// benefit.
func (v *VM) noteAdaptiveFailure() {
	if !v.jitConfig.Adaptive {
		return
	}
	a := &v.jitAdaptive
	a.failures++
	a.sinceCool++
	every := v.jitConfig.AdaptiveCoolEvery
	if every == 0 {
		every = 8
	}
	if a.sinceCool >= every && a.coolLevel < jit.MaxAdaptiveCool {
		a.coolLevel++
		a.sinceCool = 0
	}
}

// compileAdmitted reports whether a new compile may start under the R5-4
// cumulative compile-time budget. A zero budget is unlimited (default).
// Denied admissions are observable via jit.Stats.BudgetDenied.
func (v *VM) compileAdmitted() bool {
	limit := v.jitConfig.CompileBudgetNanos
	if limit != 0 && v.jitBudgetSpent >= limit {
		if v.jitConfig.Stats {
			v.jitStats.BudgetDenied++
		}
		return false
	}
	return true
}

// spendCompileBudget accounts measured compile time against the R5-4 budget.
// It is called at every site that measures a compile, whether or not a limit
// is configured, so jit.Stats.BudgetSpent is always observable.
func (v *VM) spendCompileBudget(nanos uint64) {
	v.jitBudgetSpent += nanos
}
