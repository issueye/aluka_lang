package jit

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// Property PIC (R4-3 / R4-4).
//
// R4-3: the guard grows from the original two-way cache to 2-4 stable own
// data shapes. The first two shapes are still admitted freely (the historical
// two-way behavior, so single-observation warmups keep the exact old
// semantics), and the adaptive upper limit admits a third/fourth shape only
// when the site has proven itself stable:
//
//  1. both baseline entries accumulated at least propertyPICStableHits
//     successful guarded loads (the workload is genuinely polymorphic, not a
//     sequence of one-off objects), and
//  2. the candidate shape was observed twice (the first observation falls
//     back so Tier 0 keeps producing the observable result while the site
//     confirms the new shape).
//
// Beyond four shapes the guard stops growing entirely: a miss is a stable
// fallback (no blind admission). After repeated over-cap misses the guard
// cools down (propertyPICCoolDownMisses): it clears the entries and re-learns
// from scratch, so a changed workload is not locked out of the fast path
// forever (the "降温" behavior).
//
// Absorption vs. rejection (R4-3): when a Quick-tier execution fails a guard
// but the same execution absorbed a beyond-baseline shape, the bridge resets
// the site's failure chain (Program.TakePICAbsorptions) — a multi-property
// site learns one new shape per guard without tripping the rejection limit.
// Rejections (accessor / Proxy / prototype / deleted / non-Number) never
// admit, so an unabsorbable site still disables after jitGuardFailureLimit
// consecutive failures, unchanged from the historical behavior.
//
// Accessors, Proxy receivers, prototype-chain properties, deleted properties
// and non-Number values never enter the entries: NumericOwnProperty returns
// false for all of them and loadNumber rejects the access immediately (R4-4
// cache rejection) without walking or mutating the PIC.
//
// R4-4: every load performs exactly one shape probe (NumericOwnProperty
// resolves the name-to-slot mapping once) and the entry scan compares only
// (shapeID, slot) pairs — the guard chain never repeats the shape hash lookup
// or the value-type checks for each entry. Stores resolve the pair the same
// way and then set the slot with a single lookup-free primitive. The counters
// below (hits/adds/rejects/overflows/coolDowns) are surfaced through
// jit.Stats so the hit/admission/rejection behavior is observable.

const (
	// propertyPICMaxEntries is the hard entry capacity of the guard.
	propertyPICMaxEntries = 4
	// propertyPICBaseline is the free-admission limit: the first two shapes
	// are learned unconditionally, matching the historical two-way PIC.
	propertyPICBaseline = 2
	// propertyPICStableHits is the per-baseline-entry hit count that must be
	// reached before a third/fourth shape may be admitted.
	propertyPICStableHits = 3
	// propertyPICCoolDownMisses is the number of over-cap misses after which
	// the guard forgets speculative entries and re-adapts.
	propertyPICCoolDownMisses = 8
)

type propertyGuardEntry struct {
	shapeID uint64
	slot    int
	// hits counts successful guarded loads through this entry. It is the
	// stability signal for the adaptive upper limit (R4-3) and makes the
	// admission decisions explainable.
	hits uint32
}

type propertyGuard struct {
	entries [propertyPICMaxEntries]propertyGuardEntry
	count   uint8
	// snapshot marks the guard copy owned by a native input plan. Native
	// entry guards deliberately keep the historical two-way semantics: the
	// third shape fails and the bridge disables the native tier (existing
	// third-shape cutoff tests pin this behavior), while the portable
	// Quick-tier guards absorb 2-4 stable shapes. snapshots never admit a
	// third shape and never track a pending candidate.
	snapshot bool
	// candidate remembers the last over-limit shape that was not yet stable
	// enough; a second consecutive observation of the same pair admits it.
	candidate    propertyGuardEntry
	candidateSet bool
	// absorptions counts beyond-baseline admissions (3rd/4th shape) since the
	// last TakePICAbsorptions. The bridge uses it to reset the guard-failure
	// chain when a failed execution absorbed a new shape: a multi-site
	// trace/function absorbs one new shape per property guard, and each
	// confirmation would otherwise burn a failure against the rejection limit
	// before every site finished learning. Hard rejects never admit, so a
	// site that can never absorb still hits the failure limit unchanged.
	absorptions uint64
	// fullMisses counts consecutive over-cap misses; at the cool-down
	// threshold the guard forgets speculative entries and re-adapts.
	fullMisses uint8
	// Cumulative diagnostics (R4-4), aggregated through
	// Program.PropertyPICStats / TraceProgram.PropertyPICStats.
	hits      uint64 // successful guarded loads through an entry
	adds      uint64 // admissions beyond the baseline (3rd/4th shape)
	rejects   uint64 // fast rejections (not an own data Number property)
	overflows uint64 // over-cap misses (stable fallback)
	coolDowns uint64 // cool-down resets
}

// loadNumber resolves a Number-valued own data property through the PIC.
// R4-4: one shape probe + a (shapeID, slot) entry scan; a reject returns
// without touching the entries. The returned number is the current property
// value; ok=false means the caller must fall back (Tier 0 / other tier).
func (g *propertyGuard) loadNumber(object engine.Value, name string) (float64, bool) {
	number, shapeID, slot, ok := engine.NumericOwnProperty(object, name)
	if !ok {
		// R4-4 cache rejection: Proxy / accessor / prototype / deleted /
		// non-Number / non-plain-object shapes are rejected in one primitive
		// call without probing or mutating the PIC.
		g.rejects++
		return 0, false
	}
	for i := uint8(0); i < g.count; i++ {
		entry := &g.entries[i]
		if entry.shapeID == shapeID && entry.slot == slot {
			if entry.hits < ^uint32(0) {
				entry.hits++
			}
			g.hits++
			return number, true
		}
	}
	if g.count < propertyPICBaseline {
		g.admit(shapeID, slot)
		return number, true
	}
	if !g.snapshot && g.count < propertyPICMaxEntries && g.stable() &&
		g.candidateSet && g.candidate.shapeID == shapeID && g.candidate.slot == slot {
		// Second consecutive observation of a shape that arrived after a
		// stable two-shape baseline: the site is truly polymorphic, admit it.
		g.candidateSet = false
		g.admit(shapeID, slot)
		return number, true
	}
	// Over the adaptive limit: remember the candidate (the most recent
	// unconfirmed shape) and fall back instead of blindly growing the cache.
	// Snapshot (native input-plan) guards skip the candidate entirely: for
	// them every third-shape miss is a plain two-way cache miss.
	if !g.snapshot {
		g.candidate, g.candidateSet = propertyGuardEntry{shapeID: shapeID, slot: slot}, true
	}
	g.overflows++
	g.fullMisses++
	if g.fullMisses >= propertyPICCoolDownMisses {
		g.coolDown()
	}
	return 0, false
}

// storeNumber writes a Number-valued own data property through the PIC.
// R4-4: one shape probe, an entry scan and a single lookup-free guarded set;
// the property name-to-slot mapping is never resolved more than once.
func (g *propertyGuard) storeNumber(object engine.Value, name string, number float64) bool {
	_, shapeID, slot, ok := engine.NumericOwnProperty(object, name)
	if !ok {
		return false
	}
	for i := uint8(0); i < g.count; i++ {
		entry := &g.entries[i]
		if entry.shapeID == shapeID && entry.slot == slot {
			return engine.GuardedSetNumericOwnPropertySlot(object, name, entry.shapeID, entry.slot, number)
		}
	}
	return false
}

// stable reports whether the baseline entries have accumulated enough hits to
// prove the site is a stable polymorphic workload (R4-3 adaptive upper limit).
func (g *propertyGuard) stable() bool {
	if g.count < propertyPICBaseline {
		return true
	}
	return g.entries[0].hits >= propertyPICStableHits && g.entries[1].hits >= propertyPICStableHits
}

// admit appends a (shapeID, slot) pair to the entries. Admissions beyond the
// baseline (third/fourth shape) are counted in adds and absorptions.
func (g *propertyGuard) admit(shapeID uint64, slot int) {
	if g.count >= propertyPICMaxEntries {
		return
	}
	g.entries[g.count] = propertyGuardEntry{shapeID: shapeID, slot: slot}
	g.count++
	g.fullMisses = 0
	if g.count > propertyPICBaseline {
		g.adds++
		g.absorptions++
	}
}

// coolDown clears the entries so a changed workload can re-learn from
// scratch: the first two new shapes are free admissions again and further
// growth re-requires a stable baseline (R4-3 "降温").
func (g *propertyGuard) coolDown() {
	g.entries = [propertyPICMaxEntries]propertyGuardEntry{}
	g.count = 0
	g.candidateSet = false
	g.fullMisses = 0
	g.coolDowns++
}

// takeStats returns the guard's cumulative counters (R4-4 diagnostics).
func (g *propertyGuard) takeStats() (hits, adds, rejects, overflows, coolDowns uint64) {
	return g.hits, g.adds, g.rejects, g.overflows, g.coolDowns
}

// TakePICAbsorptions returns the number of beyond-baseline shape admissions
// across this program's function-level guards since the last call, and resets
// the per-guard counters. The bridge calls it when a Quick-tier execution
// fails a guard: an absorption inside that execution proves the site is
// absorbing polymorphism, so the failure chain is reset and the site is not
// rejected while its remaining guards finish learning the new shape. Native
// input-plan guards are snapshots and never absorb, so they never contribute.
func (p *Program) TakePICAbsorptions() uint64 {
	if p == nil {
		return 0
	}
	var total uint64
	for i := range p.propertyGuards {
		total += p.propertyGuards[i].absorptions
		p.propertyGuards[i].absorptions = 0
	}
	return total
}

// PropertyPICStats returns the property-PIC counters accumulated by this
// program's function-level guards and, when installed, its native input-plan
// guards (R4-4 diagnostics). Counters are cumulative; repeated reads return
// the same totals. Verification executions run against guard copies and are
// not counted.
func (p *Program) PropertyPICStats() (hits, adds, rejects, overflows, coolDowns uint64) {
	if p == nil {
		return 0, 0, 0, 0, 0
	}
	for i := range p.propertyGuards {
		h, a, r, o, c := p.propertyGuards[i].takeStats()
		hits, adds, rejects, overflows, coolDowns = hits+h, adds+a, rejects+r, overflows+o, coolDowns+c
	}
	if p.nativePlan != nil {
		for i := range p.nativePlan.properties {
			h, a, r, o, c := p.nativePlan.properties[i].guard.takeStats()
			hits, adds, rejects, overflows, coolDowns = hits+h, adds+a, rejects+r, overflows+o, coolDowns+c
		}
	}
	return hits, adds, rejects, overflows, coolDowns
}

// NativePlanPropertyPICStats returns the property-PIC counters accumulated by
// the native input-plan guards only (R4-4 diagnostics). The bridge folds
// these into the VM stats when a native program is dropped, because the plan
// (and its counters) is released with the machine code.
func (p *Program) NativePlanPropertyPICStats() (hits, adds, rejects, overflows, coolDowns uint64) {
	if p == nil || p.nativePlan == nil {
		return 0, 0, 0, 0, 0
	}
	for i := range p.nativePlan.properties {
		h, a, r, o, c := p.nativePlan.properties[i].guard.takeStats()
		hits, adds, rejects, overflows, coolDowns = hits+h, adds+a, rejects+r, overflows+o, coolDowns+c
	}
	return hits, adds, rejects, overflows, coolDowns
}

// PropertyPICEntries returns the total number of cached (shape, slot) entries
// across this program's function-level guards (R4-4 diagnostics).
func (p *Program) PropertyPICEntries() int {
	if p == nil {
		return 0
	}
	total := 0
	for i := range p.propertyGuards {
		total += int(p.propertyGuards[i].count)
	}
	return total
}

// PropertyPICEntries returns the total number of cached (shape, slot) entries
// across this trace program's guards (R4-4 diagnostics).
func (t *TraceProgram) PropertyPICEntries() int {
	if t == nil || t.program == nil {
		return 0
	}
	return t.program.PropertyPICEntries()
}

// NativePlanPropertyPICStats returns the property-PIC counters accumulated by
// this trace program's native input-plan guards only (R4-4 diagnostics).
func (t *TraceProgram) NativePlanPropertyPICStats() (hits, adds, rejects, overflows, coolDowns uint64) {
	if t == nil || t.program == nil {
		return 0, 0, 0, 0, 0
	}
	return t.program.NativePlanPropertyPICStats()
}

// PropertyPICStats returns the property-PIC counters accumulated by this trace
// program's guards (R4-4 diagnostics); see Program.PropertyPICStats.
func (t *TraceProgram) PropertyPICStats() (hits, adds, rejects, overflows, coolDowns uint64) {
	if t == nil || t.program == nil {
		return 0, 0, 0, 0, 0
	}
	return t.program.PropertyPICStats()
}

// TakePICAbsorptions returns the number of beyond-baseline shape admissions
// across this trace program's guards since the last call and resets the
// counters; see Program.TakePICAbsorptions.
func (t *TraceProgram) TakePICAbsorptions() uint64 {
	if t == nil || t.program == nil {
		return 0
	}
	return t.program.TakePICAbsorptions()
}
