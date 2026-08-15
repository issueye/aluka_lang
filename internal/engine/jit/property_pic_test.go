package jit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// picObject builds a plain object with the given key/value pairs, so each
// distinct key set yields a distinct shape (the R4-3 test shapes).

func picObject(t *testing.T, pairs ...interface{}) engine.Object {
	t.Helper()
	values := make([]engine.Value, 0, len(pairs))
	for _, item := range pairs {
		switch v := item.(type) {
		case string:
			values = append(values, engine.Str(v))
		case float64:
			values = append(values, engine.Number(v))
		case int:
			values = append(values, engine.Number(float64(v)))
		case engine.Value:
			values = append(values, v)
		default:
			t.Fatalf("unsupported pair element %T", item)
		}
	}
	return engine.NewObjectFromPairs(values)
}

// TestPropertyGuardAbsorbsFourStableShapes is the R4-3 guard-level positive
// evidence: the first two shapes are learned freely (baseline), a third and a
// fourth shape are admitted only after a stable baseline (>= 3 hits per
// baseline entry) and a second consecutive observation, and all four shapes
// then hit the fast path. The first observation of each new shape falls back
// (overflows), which the bridge reports as one guard failure per new shape.
func TestPropertyGuardAbsorbsFourStableShapes(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	s3 := picObject(t, "a", 5, "d", 6, "e", 7)
	s4 := picObject(t, "a", 7, "f", 8, "g", 9, "h", 10)

	var g propertyGuard
	load := func(o engine.Object, want float64, okWant bool) {
		t.Helper()
		n, ok := g.loadNumber(o, "a")
		if ok != okWant || ok && n != want {
			t.Fatalf("loadNumber = (%v, %v), want (%v, %v)", n, ok, want, okWant)
		}
	}
	// Baseline: two shapes admitted freely.
	load(s1, 1, true)
	load(s2, 3, true)
	// Stability: each baseline entry needs >= propertyPICStableHits hits (the
	// first observation of each shape is the admission, not a hit, so each
	// baseline shape is observed four times in total).
	load(s1, 1, true)
	load(s2, 3, true)
	load(s1, 1, true)
	load(s2, 3, true)
	load(s1, 1, true)
	load(s2, 3, true)
	if g.count != 2 {
		t.Fatalf("baseline count = %d, want 2", g.count)
	}
	// Third shape: first observation falls back, second is admitted.
	load(s3, 0, false)
	load(s3, 5, true)
	if g.count != 3 {
		t.Fatalf("count after third shape = %d, want 3", g.count)
	}
	// Fourth shape: same two-step admission.
	load(s4, 0, false)
	load(s4, 7, true)
	if g.count != 4 {
		t.Fatalf("count after fourth shape = %d, want 4", g.count)
	}
	// All four shapes stay on the fast path.
	load(s1, 1, true)
	load(s2, 3, true)
	load(s3, 5, true)
	load(s4, 7, true)
	// A fifth shape is never admitted (hard cap).
	load(s4, 7, true)
	if g.count != 4 {
		t.Fatalf("hard cap violated: count = %d", g.count)
	}
	hits, adds, rejects, overflows, coolDowns := g.takeStats()
	if adds != 2 || overflows != 2 || rejects != 0 || coolDowns != 0 {
		t.Fatalf("stats = (hits=%d adds=%d rejects=%d overflows=%d cools=%d), want (adds=2 overflows=2 rejects=0 cools=0)",
			hits, adds, rejects, overflows, coolDowns)
	}
	if hits == 0 {
		t.Fatal("no hits recorded")
	}
}

// TestPropertyGuardRequiresStableBaseline proves the adaptive upper limit:
// when the baseline shapes were each observed only once, the third shape is
// never admitted even when observed repeatedly (the R1-6 third-shape cutoff
// tests depend on exactly this behavior).
func TestPropertyGuardRequiresStableBaseline(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	s3 := picObject(t, "a", 5, "d", 6, "e", 7)

	var g propertyGuard
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a")
	for i := 0; i < 4; i++ {
		if _, ok := g.loadNumber(s3, "a"); ok {
			t.Fatalf("observation %d of an unstable third shape was admitted", i+1)
		}
	}
	if g.count != 2 || g.candidateSet == false {
		t.Fatalf("unstable site mutated: count=%d candidate=%v", g.count, g.candidateSet)
	}
	_, adds, _, overflows, _ := g.takeStats()
	if adds != 0 || overflows != 4 {
		t.Fatalf("unstable site stats = adds=%d overflows=%d, want 0/4", adds, overflows)
	}
}

// TestPropertyGuardSnapshotStaysTwoWay pins the native input-plan guard
// semantics (R4-3): snapshot guards keep the historical two-way PIC, so a
// third shape always misses and is never admitted or tracked as a candidate.
func TestPropertyGuardSnapshotStaysTwoWay(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	s3 := picObject(t, "a", 5, "d", 6, "e", 7)

	var g propertyGuard
	g.snapshot = true
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a")
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a")
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a") // baseline is stable, yet...
	for i := 0; i < 3; i++ {
		if _, ok := g.loadNumber(s3, "a"); ok {
			t.Fatalf("snapshot guard admitted a third shape (observation %d)", i+1)
		}
	}
	if g.count != 2 {
		t.Fatalf("snapshot guard count = %d, want 2", g.count)
	}
	_, adds, _, overflows, _ := g.takeStats()
	if adds != 0 || overflows != 3 {
		t.Fatalf("snapshot stats = adds=%d overflows=%d, want 0/3", adds, overflows)
	}
}

// TestPropertyGuardRejectsNonDataShapes is the R4-4 cache-rejection evidence:
// accessor values, non-Number values, deleted properties and non-plain
// receivers are rejected in one probe without touching the entries or the
// admission counters.
func TestPropertyGuardRejectsNonDataShapes(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	var g propertyGuard
	if n, ok := g.loadNumber(s1, "a"); !ok || n != 1 {
		t.Fatalf("warmup load = (%v, %v)", n, ok)
	}
	// Type change: the same shape now holds a String in slot "a".
	if err := s1.Set("a", engine.Str("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.loadNumber(s1, "a"); ok {
		t.Fatal("non-Number value was not rejected")
	}
	// Deleted property (same shape, object-level deleted map).
	if err := s1.Set("a", engine.Number(9)); err != nil {
		t.Fatal(err)
	}
	if !s1.Delete("a") {
		t.Fatal("delete failed")
	}
	if _, ok := g.loadNumber(s1, "a"); ok {
		t.Fatal("deleted property was not rejected")
	}
	// Accessor property.
	accessor := picObject(t, "a", engine.NewAccessor(nil, nil))
	if _, ok := g.loadNumber(accessor, "a"); ok {
		t.Fatal("accessor property was not rejected")
	}
	// Missing property on a plain object.
	if _, ok := g.loadNumber(s1, "nope"); ok {
		t.Fatal("missing property was not rejected")
	}
	if g.count != 1 {
		t.Fatalf("rejections mutated the entries: count=%d", g.count)
	}
	_, _, rejects, overflows, _ := g.takeStats()
	if rejects == 0 || overflows != 0 {
		t.Fatalf("reject stats = rejects=%d overflows=%d", rejects, overflows)
	}
	// The stored value is untouched by any rejection.
	value, err := s1.Get("a")
	if err != nil || value == nil || !value.IsUndefined() {
		t.Fatalf("deleted slot value = %v err=%v, want undefined", value, err)
	}
}

// TestPropertyGuardStoreUsesResolvedPair is the R4-4 store-path evidence: the
// store resolves the (shape, slot) pair once and writes through the entries
// without repeating the shape hash lookup; non-entry shapes and rejects never
// store.
func TestPropertyGuardStoreUsesResolvedPair(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	var g propertyGuard
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a")
	if !g.storeNumber(s1, "a", 42) {
		t.Fatal("store into an entry shape failed")
	}
	if n, ok := g.loadNumber(s1, "a"); !ok || n != 42 {
		t.Fatalf("stored value not visible: (%v, %v)", n, ok)
	}
	// A shape outside the entries must not store.
	if g.storeNumber(picObject(t, "a", 5, "d", 6, "e", 7), "a", 1) {
		t.Fatal("store into a non-entry shape succeeded")
	}
	if g.storeNumber(s1, "nope", 1) {
		t.Fatal("store of a missing property succeeded")
	}
	if n, ok := g.loadNumber(s2, "a"); !ok || n != 3 {
		t.Fatalf("other entry corrupted: (%v, %v)", n, ok)
	}
}

// TestPropertyGuardCoolDownReadapts is the R4-3 "降温" evidence: after
// repeated over-cap misses the guard forgets the speculative entries, resets
// the baseline counters and re-adapts to a changed workload.
func TestPropertyGuardCoolDownReadapts(t *testing.T) {
	var g propertyGuard
	shapes := make([]engine.Object, 5)
	for i := 0; i < 5; i++ {
		// Distinct key names -> distinct shapes.
		shapes[i] = picObject(t, "a", i+1, "p"+string(rune('a'+i)), i, "q"+string(rune('a'+i)), i*7)
	}
	// Baseline: two shapes, stable (admission + 3 hits each).
	for i := 0; i < 4; i++ {
		g.loadNumber(shapes[0], "a")
		g.loadNumber(shapes[1], "a")
	}
	// Absorb third and fourth shapes (two observations each).
	g.loadNumber(shapes[2], "a")
	g.loadNumber(shapes[2], "a")
	g.loadNumber(shapes[3], "a")
	g.loadNumber(shapes[3], "a")
	if g.count != 4 {
		t.Fatalf("count = %d, want 4", g.count)
	}
	// A fifth shape misses repeatedly; after the cool-down threshold the
	// guard clears its entries so the changed workload can re-adapt.
	for i := 0; i < propertyPICCoolDownMisses; i++ {
		if _, ok := g.loadNumber(shapes[4], "a"); ok {
			t.Fatalf("fifth shape admitted at miss %d", i+1)
		}
	}
	if g.count != 0 {
		t.Fatalf("count after cool-down = %d, want 0", g.count)
	}
	_, _, _, _, coolDowns := g.takeStats()
	if coolDowns != 1 {
		t.Fatalf("cool-downs = %d, want 1", coolDowns)
	}
	// The changed workload re-adapts from scratch: the first two new shapes
	// are free admissions again, and every shape hits afterwards.
	g.loadNumber(shapes[4], "a")
	g.loadNumber(shapes[4], "a")
	g.loadNumber(shapes[4], "a")
	if n, ok := g.loadNumber(shapes[4], "a"); !ok || n != 5 {
		t.Fatalf("re-adapted load = (%v, %v), want 5", n, ok)
	}
}

// TestNativePropertyVerifyMismatchRestoresQuickResult is the §9.3 class-5
// evidence for the property PIC: a manually injected native-code mismatch
// (addsd -> subsd inside the property-write loop) is caught by the verify
// protocol, the Native tier is not trusted, and the Quick-executed expected
// results (locals + property values) are restored exactly.
func TestNativePropertyVerifyMismatchRestoresQuickResult(t *testing.T) {
	tmpl := sideEffectTraceTemplate()
	trace, err := CompileTraceWithGuards(tmpl, 0, 64, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.CompileNativeForDump(); err != nil {
		t.Skipf("native compile unavailable: %v", err)
	}
	if !trace.HasNative() {
		t.Fatal("native trace was not installed")
	}
	// Corrupt one addsd (F2 0F 58) into subsd (F2 0F 5C) so the machine code
	// computes s - o.x instead of s + o.x. The instruction length is
	// unchanged, so the rest of the stream decodes identically.
	bytes := trace.program.nativeCode.DebugBytes()
	if len(bytes) == 0 {
		t.Fatal("native code has no debug bytes")
	}
	corrupted := false
	for i := 0; i+2 < len(bytes); i++ {
		if bytes[i] == 0xF2 && bytes[i+1] == 0x0F && bytes[i+2] == 0x58 {
			bytes[i+2] = 0x5C
			corrupted = true
			break
		}
	}
	if !corrupted {
		t.Fatal("no addsd instruction found to corrupt")
	}
	code, err := jitnative.Publish(bytes, true)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	old := trace.program.nativeCode
	trace.program.nativeCode = code

	obj := picObject(t, "x", 10)
	locals := []engine.Value{
		engine.Undefined(), engine.Number(0), engine.Number(6), obj, engine.Number(0),
	}
	expected := append([]engine.Value(nil), locals...)
	expectedExit, expectedReason, expectedErr := trace.ExecuteBudgetDetailed(expected, 0)
	if expectedErr != nil || expectedReason != Executed {
		t.Fatalf("expected Quick run: reason=%v err=%v", expectedReason, expectedErr)
	}
	exit, reason, _, verifyChecked, verifyMatched, err := trace.ExecuteNativeBudgetVerifiedWithSafepoint(locals, 65536, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyChecked {
		t.Fatal("verify protocol did not run a check")
	}
	if verifyMatched {
		t.Fatal("corrupted native code verified as matching")
	}
	if reason != Executed {
		t.Fatalf("recovery reason = %v, want Executed", reason)
	}
	if !SameDeoptExit(exit, expectedExit) {
		t.Fatalf("recovery exit = %+v, want %+v", exit, expectedExit)
	}
	for i := range expected {
		same := locals[i] == expected[i] || func() bool {
			w, ok2 := expected[i].Float()
			if !ok2 {
				return false
			}
			f, ok1 := locals[i].Float()
			return ok1 && f == w
		}()
		if !same {
			t.Fatalf("local %d after recovery = %v, want %v (Quick result not restored)", i, locals[i], expected[i])
		}
	}
	// The loop wrote o.x = i for i in [0, n), so the committed property value
	// must be n-1 = 5 — the Quick-computed value, not the corrupted native one.
	value, err := obj.Get("x")
	if err != nil || value == nil {
		t.Fatalf("object property after recovery = %v err=%v", value, err)
	}
	if got, _ := value.Float(); got != 5 {
		t.Fatalf("property after recovery = %v, want 5 (Quick result not restored)", got)
	}
	trace.program.nativeCode = old
}

// TestPropertyGuardAbsorptionsResetTakeZero pins the R4-3 absorption
// take-and-zero contract: beyond-baseline admissions are reported exactly
// once by TakePICAbsorptions, baseline admissions are not counted, and the
// snapshot (native input-plan) guards never contribute.
func TestPropertyGuardAbsorptionsResetTakeZero(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	s3 := picObject(t, "a", 5, "d", 6, "e", 7)
	program := &Program{propertyGuards: make([]propertyGuard, 2)}
	program.propertyGuards[1].snapshot = true
	// Baseline: two shapes admitted freely and hit enough times to be stable.
	program.propertyGuards[0].loadNumber(s1, "a")
	program.propertyGuards[0].loadNumber(s2, "a")
	program.propertyGuards[0].loadNumber(s1, "a")
	program.propertyGuards[0].loadNumber(s2, "a")
	program.propertyGuards[0].loadNumber(s1, "a")
	program.propertyGuards[0].loadNumber(s2, "a")
	program.propertyGuards[0].loadNumber(s1, "a")
	program.propertyGuards[0].loadNumber(s2, "a")
	// Baseline admissions are free and are not absorption events.
	if absorbed := program.TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("absorptions after baseline = %d, want 0", absorbed)
	}
	// Third shape: first observation misses, the confirmation admits it.
	program.propertyGuards[0].loadNumber(s3, "a")
	if absorbed := program.TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("absorptions after one miss = %d, want 0", absorbed)
	}
	program.propertyGuards[0].loadNumber(s3, "a")
	if absorbed := program.TakePICAbsorptions(); absorbed != 1 {
		t.Fatalf("absorptions after confirmation = %d, want 1", absorbed)
	}
	// A second take reports nothing: the counters were reset.
	if absorbed := program.TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("absorptions after take = %d, want 0", absorbed)
	}
	// Snapshot guards (native input plans) never absorb.
	program.propertyGuards[1].loadNumber(s1, "a")
	program.propertyGuards[1].loadNumber(s2, "a")
	program.propertyGuards[1].loadNumber(s3, "a")
	program.propertyGuards[1].loadNumber(s3, "a")
	if absorbed := program.TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("snapshot guard absorbed = %d, want 0", absorbed)
	}
	// Nil receivers are safe.
	if absorbed := (*Program)(nil).TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("nil program absorptions = %d", absorbed)
	}
	if absorbed := (*TraceProgram)(nil).TakePICAbsorptions(); absorbed != 0 {
		t.Fatalf("nil trace absorptions = %d", absorbed)
	}
}

// TestPropertyPICStatsAggregatesGuards verifies Program.PropertyPICStats
// aggregates function-level guards and native plan guards and is cumulative.
func TestPropertyPICStatsAggregatesGuards(t *testing.T) {
	s1 := picObject(t, "a", 1, "b", 2)
	s2 := picObject(t, "a", 3, "c", 4)
	program := &Program{propertyGuards: make([]propertyGuard, 1)}
	g := &program.propertyGuards[0]
	g.loadNumber(s1, "a")
	g.loadNumber(s2, "a")
	g.loadNumber(s1, "a")
	g.loadNumber(s1, "a")
	hits, adds, rejects, overflows, cools := program.PropertyPICStats()
	if hits == 0 || adds != 0 || rejects != 0 || overflows != 0 || cools != 0 {
		t.Fatalf("aggregate = %d/%d/%d/%d/%d", hits, adds, rejects, overflows, cools)
	}
	// Cumulative: a second read reports the same totals.
	hits2, _, _, _, _ := program.PropertyPICStats()
	if hits2 != hits {
		t.Fatalf("stats not cumulative: %d then %d", hits, hits2)
	}
	// Nil plan is safe.
	if h, a, r, o, c := (*Program)(nil).PropertyPICStats(); h != 0 || a != 0 || r != 0 || o != 0 || c != 0 {
		t.Fatalf("nil program stats = %d/%d/%d/%d/%d", h, a, r, o, c)
	}
}
