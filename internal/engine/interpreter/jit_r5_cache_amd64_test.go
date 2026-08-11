//go:build amd64 && (windows || linux)

// R5-5: code-cache LRU weighting (recency + heat) and two-way callee-PIC
// combined billing tests. These tests are white-box (package interpreter) and
// pinned to amd64 because native code installation is platform-gated. They
// must not use t.Parallel: jitnative.LiveExecutableMemory() is process-wide
// and the tests rely on exact RX byte deltas.
//
// Each vm.Eval runs an isolated script scope, so functions that must be
// callable from later evals are installed on globalThis (same template/closure
// identity across evals, so the jitStates are shared).

package interpreter

import (
	"fmt"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// nativeStateUnit is a white-box view of one installed native unit (leaf
// state): its heat (nativeHits) and recency clock (nativeUsed).
type nativeStateUnit struct {
	state *quickJITState
	hits  uint64
	used  uint64
}

func collectNativeStates(v *VM) []nativeStateUnit {
	var units []nativeStateUnit
	for _, st := range v.jitStates {
		if jitStateHasNative(st) {
			units = append(units, nativeStateUnit{state: st, hits: st.nativeHits, used: st.nativeUsed})
		}
	}
	return units
}

// measureLeafSizes returns the per-function native size of the three leaf
// shapes used by the heat test. Machine-code emission is deterministic, so the
// sizes are identical across VMs; the identical shapes also share one size.
func measureLeafSizes(t *testing.T) (a, b, c uint64) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if _, err := vm.Eval(`
		globalThis.hotA = function hotA(x) { return x + 1; };
		globalThis.coldB = function coldB(x) { return x + 1; };
		globalThis.newC = function newC(x) { return x + 1; };
		globalThis.hotA(1); globalThis.coldB(1); globalThis.newC(1);
	`, "r5-heat-size.js"); err != nil {
		t.Fatal(err)
	}
	units := collectNativeStates(vm)
	if len(units) != 3 {
		t.Fatalf("warmup compiled %d native units, want 3", len(units))
	}
	var sizes []uint64
	for _, u := range units {
		sizes = append(sizes, uint64(u.state.program.NativeSize()))
	}
	if sizes[0] == 0 || sizes[0] != sizes[1] || sizes[1] != sizes[2] {
		t.Fatalf("leaf native sizes not equal: %v", sizes)
	}
	return sizes[0], sizes[1], sizes[2]
}

// TestJITLRUHeatProtectsHotUnitOverRecency proves the R5-5 heat weighting:
// a cold unit is evicted before a hot one even when the hot unit was used
// longer ago (pure clock LRU would evict the hot unit). Once every installed
// unit is hot, eviction proceeds by recency inside the hot class, which is
// observable through NativeHotEvictions.
func TestJITLRUHeatProtectsHotUnitOverRecency(t *testing.T) {
	sizeA, sizeB, sizeC := measureLeafSizes(t)
	budget := sizeA + sizeB + sizeC - 1 // fits any two units, never three

	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, CodeCacheBytes: budget, Stats: true})
	if _, err := vm.Eval(`
		globalThis.hotA = function hotA(x) { return x + 1; };
		globalThis.coldB = function coldB(x) { return x + 1; };
		globalThis.newC = function newC(x) { return x + 1; };
	`, "r5-heat-defs.js"); err != nil {
		t.Fatal(err)
	}
	// hotA: install touch + 10 executions -> 11 touches (hot).
	for i := 0; i < 10; i++ {
		if _, err := vm.Eval("globalThis.hotA(1);", "r5-heat-a.js"); err != nil {
			t.Fatal(err)
		}
	}
	// coldB: install touch + 1 execution -> 2 touches (cold).
	if _, err := vm.Eval("globalThis.coldB(1);", "r5-heat-b.js"); err != nil {
		t.Fatal(err)
	}
	units := collectNativeStates(vm)
	if len(units) != 2 {
		t.Fatalf("native units after A+B = %d, want 2", len(units))
	}
	if !(units[0].hits >= jitHotHeatThreshold && units[1].hits < jitHotHeatThreshold ||
		units[1].hits >= jitHotHeatThreshold && units[0].hits < jitHotHeatThreshold) {
		t.Fatalf("want exactly one hot and one cold native unit, got %+v", units)
	}
	if bytesAB := vm.jitNativeBytes; bytesAB > budget {
		t.Fatalf("cache exceeded budget after A+B: %d > %d", bytesAB, budget)
	}

	// newC's install must evict (A+B+C do not fit). Pure clock LRU would
	// evict hotA (least recently used); the R5-5 weight must instead evict
	// coldB and keep hotA despite its recency.
	if _, err := vm.Eval("globalThis.newC(1);", "r5-heat-c.js"); err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	if stats.NativeEvictions != 1 {
		t.Fatalf("evictions = %d, want exactly 1", stats.NativeEvictions)
	}
	if stats.NativeHotEvictions != 0 {
		t.Fatalf("hot evictions = %d, want 0 (a cold victim existed)", stats.NativeHotEvictions)
	}
	units = collectNativeStates(vm)
	if len(units) != 2 {
		t.Fatalf("native units after C = %d, want 2 (A and C)", len(units))
	}
	oldest := units[0]
	if units[1].used < oldest.used {
		oldest = units[1]
	}
	if oldest.hits < jitHotHeatThreshold {
		t.Fatalf("oldest surviving unit has hits=%d, want >= %d (heat protected): %+v",
			oldest.hits, jitHotHeatThreshold, units)
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native cache exceeded budget: %d > %d", stats.NativeCodeBytes, stats.CodeCacheLimit)
	}
	if vm.jitNativeBytes != sizeA+sizeC {
		t.Fatalf("combined cache bytes = %d, want %d (A+C)", vm.jitNativeBytes, sizeA+sizeC)
	}
	if _, bytes := jitnative.LiveExecutableMemory(); bytes != baseBytes+vm.jitNativeBytes {
		t.Fatalf("live RX bytes = %d, want baseline %d + cache %d", bytes, baseBytes, vm.jitNativeBytes)
	}

	// Phase 2: once every installed unit is hot, the next install must evict
	// the least-recently-used hot unit (heat protection no longer applies).
	for i := 0; i < 4; i++ {
		if _, err := vm.Eval("globalThis.newC(1);", "r5-heat-c2.js"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := vm.Eval(`
		globalThis.hotD = function hotD(x) { return x + 1; };
		globalThis.hotD(1); globalThis.hotD(1); globalThis.hotD(1);
	`, "r5-heat-d.js"); err != nil {
		t.Fatal(err)
	}
	stats = vm.JITStats()
	if stats.NativeEvictions != 2 {
		t.Fatalf("evictions = %d, want 2", stats.NativeEvictions)
	}
	if stats.NativeHotEvictions != 1 {
		t.Fatalf("hot evictions = %d, want 1 (all installed units were hot)", stats.NativeHotEvictions)
	}
	units = collectNativeStates(vm)
	if len(units) != 2 {
		t.Fatalf("native units after D = %d, want 2 (C and D)", len(units))
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native cache exceeded budget after D: %d > %d", stats.NativeCodeBytes, stats.CodeCacheLimit)
	}

	// All four functions still produce correct results (evicted units run
	// Quick; the surviving units keep executing natively).
	for _, name := range []string{"hotA", "coldB", "newC", "hotD"} {
		if _, err := vm.Eval(fmt.Sprintf("globalThis.r5r = globalThis.%s(1);", name), "r5-heat-r.js"); err != nil {
			t.Fatal(err)
		}
		got, err := vm.Global().Get("r5r")
		if err != nil || got.String() != "2" {
			t.Fatalf("%s result=%v err=%v", name, got, err)
		}
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("RX not released on close: regions=%d/%d bytes=%d/%d", regions, baseRegions, bytes, baseBytes)
	}
}

// measurePICAndFillerSizes returns the combined native byte count of one
// two-way callee-PIC unit (primary + alternate inlined native versions) and
// the native size of a single filler leaf, measured in a warmup VM.
func measurePICAndFillerSizes(t *testing.T) (combined, filler uint64) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if _, err := vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		let target = add1;
		function wrapper(x) { return target(x) * 2; }
		globalThis.wrapper = wrapper;
		globalThis.check = function() {
			let r1 = wrapper(20);
			target = add1;
			let r2 = wrapper(20);
			return r1 * 1000 + r2;
		};
		wrapper(20);
		target = add10;
		wrapper(20);
	`, "r5-pic-size.js"); err != nil {
		t.Fatal(err)
	}
	var picState *quickJITState
	for _, st := range vm.jitStates {
		if st != nil && st.altProgram != nil && jitStateHasNative(st) {
			picState = st
		}
	}
	if picState == nil {
		t.Fatal("warmup produced no native callee PIC unit")
	}
	combined = uint64(picState.program.NativeSize()) + uint64(picState.altProgram.NativeSize())
	before := vm.jitNativeBytes
	if _, err := vm.Eval("function fill(x) { return x + 1; } fill(1);", "r5-pic-fill-size.js"); err != nil {
		t.Fatal(err)
	}
	if vm.jitNativeBytes <= before {
		t.Fatal("warmup filler did not install native code")
	}
	filler = vm.jitNativeBytes - before
	return combined, filler
}

// TestJITLRUCombinedPICBillingFreesBothVersions proves the R5-5 combined
// billing claim: the primary and the alternate callee-PIC native versions are
// one LRU unit billed at their summed byte count, and one eviction releases
// both RX regions while keeping the live byte total under the budget.
func TestJITLRUCombinedPICBillingFreesBothVersions(t *testing.T) {
	combined, filler := measurePICAndFillerSizes(t)
	if combined == 0 || filler == 0 {
		t.Fatalf("combined=%d filler=%d", combined, filler)
	}

	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	budget := combined + 2*filler // fits the PIC unit + two fillers, not three
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, CodeCacheBytes: budget, Stats: true})
	if _, err := vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		let target = add1;
		function wrapper(x) { return target(x) * 2; }
		globalThis.wrapper = wrapper;
		globalThis.check = function() {
			let r1 = wrapper(20);
			target = add1;
			let r2 = wrapper(20);
			return r1 * 1000 + r2;
		};
		wrapper(20);
		target = add10;
		wrapper(20);
	`, "r5-pic.js"); err != nil {
		t.Fatal(err)
	}
	var picState *quickJITState
	for _, st := range vm.jitStates {
		if st != nil && st.altProgram != nil && jitStateHasNative(st) {
			picState = st
		}
	}
	if picState == nil {
		t.Fatal("no native callee PIC unit")
	}
	// Combined billing: both native versions are one cache unit.
	wantCombined := uint64(picState.program.NativeSize()) + uint64(picState.altProgram.NativeSize())
	if vm.jitNativeBytes != wantCombined {
		t.Fatalf("combined PIC billing: cache bytes=%d want=%d", vm.jitNativeBytes, wantCombined)
	}
	if stats := vm.JITStats(); stats.NativeCodeBytes != wantCombined {
		t.Fatalf("stats native bytes=%d want=%d", stats.NativeCodeBytes, wantCombined)
	}
	if _, bytes := jitnative.LiveExecutableMemory(); bytes != baseBytes+wantCombined {
		t.Fatalf("live RX bytes = %d, want baseline %d + %d", bytes, baseBytes, wantCombined)
	}

	// Fill the cache with hot fillers; the next install must evict the oldest
	// hot unit — the PIC unit — freeing BOTH native versions at once.
	for k := 0; k < 3; k++ {
		if _, err := vm.Eval(fmt.Sprintf(`
			function fill%d(x) { return x + 1; }
			fill%d(1); fill%d(1); fill%d(1); fill%d(1);
		`, k, k, k, k, k), "r5-pic-fill.js"); err != nil {
			t.Fatal(err)
		}
	}
	stats := vm.JITStats()
	if stats.NativeEvictions != 1 {
		t.Fatalf("evictions = %d, want exactly 1 (the PIC unit): %+v", stats.NativeEvictions, stats)
	}
	if stats.NativeHotEvictions == 0 {
		t.Fatalf("hot evictions = 0, want >= 1 (every unit was hot): %+v", stats)
	}
	if picState.program.HasNative() || picState.altProgram.HasNative() {
		t.Fatal("eviction did not free both callee-PIC native versions")
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native cache exceeded budget: %d > %d", stats.NativeCodeBytes, stats.CodeCacheLimit)
	}
	if vm.jitNativeBytes > budget {
		t.Fatalf("cache bytes %d > budget %d", vm.jitNativeBytes, budget)
	}
	if _, bytes := jitnative.LiveExecutableMemory(); bytes != baseBytes+vm.jitNativeBytes {
		t.Fatalf("live RX bytes = %d, want baseline %d + cache %d", bytes, baseBytes, vm.jitNativeBytes)
	}

	// Results stay correct after eviction (Quick fallback for the PIC unit):
	// 60 with target=add10, 42 after target=add1 -> 60042.
	if _, err := vm.Eval("globalThis.r5pic = globalThis.check();", "r5-pic-r.js"); err != nil {
		t.Fatal(err)
	}
	if got, err := vm.Global().Get("r5pic"); err != nil || got.String() != "60042" {
		t.Fatalf("check result=%v err=%v want=60042", got, err)
	}

	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("RX not released on close: regions=%d/%d bytes=%d/%d", regions, baseRegions, bytes, baseBytes)
	}
}

// TestJITLRUPressure1KB is the compact standalone 1KB-budget multi-function
// pressure test: repeated installs keep evicting, the native byte total never
// exceeds the budget, single-execution units stay cold (heat protection does
// not interfere with the historical clock LRU), and Close releases all RX.
func TestJITLRUPressure1KB(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, CodeCacheBytes: 1024, Stats: true})
	for k := 0; k < 64; k++ {
		source := fmt.Sprintf("function lruK%d(x) { return x + %d; } globalThis.lruK%d = lruK%d(1);", k, k, k, k)
		if _, err := vm.Eval(source, "r5-lru-1kb.js"); err != nil {
			t.Fatal(err)
		}
		got, err := vm.Global().Get(fmt.Sprintf("lruK%d", k))
		if err != nil || got.String() != fmt.Sprint(k+1) {
			t.Fatalf("function=%d result=%v err=%v", k, got, err)
		}
		if stats := vm.JITStats(); stats.NativeCodeBytes > stats.CodeCacheLimit {
			t.Fatalf("native cache exceeded budget at install %d: %+v", k, stats)
		}
	}
	stats := vm.JITStats()
	if stats.NativeEvictions == 0 || stats.NativeCompiled == 0 {
		t.Fatalf("expected evictions and compiles under 1KB pressure: %+v", stats)
	}
	if stats.NativeHotEvictions != 0 {
		t.Fatalf("single-execution units must stay cold: hotEvictions=%d", stats.NativeHotEvictions)
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native cache exceeded budget: %d > %d", stats.NativeCodeBytes, stats.CodeCacheLimit)
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("RX not released on close: regions=%d/%d bytes=%d/%d", regions, baseRegions, bytes, baseBytes)
	}
}
