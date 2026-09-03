//go:build amd64 && (windows || linux)

// R2-5: deterministic interleaving tests for ConfigureJIT, pending background
// native compiles, native code installation, and VM.Close.
//
// These tests are white-box (package interpreter): they read v.jitPending,
// v.jitGeneration, v.jitStates, v.jitNativeBytes and drive v.pollNativeCompiles
// directly, exactly like the pre-existing jit_test.go / jit_lifetime tests.
//
// No t.Parallel here. The RX counters behind
// jitnative.LiveExecutableMemory are package-global atomics shared by every
// test in this package, and ConfigureJIT mutates VM-global maps; running in
// parallel with jit_lifetime_amd64_test.go or jit_test.go would make the
// baseline assertions racy. All tests rely on the Go test harness running
// package tests sequentially.
//
// Determinism strategy: every interleaving is pinned by synchronization
// primitives or observable VM state, never by sleeps:
//   - "result published, not installed": wait on v.jitCompileWG (the worker
//     goroutine sends its result to the buffered channel before calling
//     Done(), so after Wait the result is deterministically in the channel,
//     and only a pollNativeCompiles call — which we deliberately do not make —
//     could install it).
//   - "reconfigure / close before completion": ConfigureJIT and Close drain
//     v.jitPending results synchronously (closeJIT blocks on the channel until
//     the worker publishes), so the assertions hold in either interleaving;
//     every such call runs under a 5s deadline so a production drain bug fails
//     the test instead of hanging it.
//   - "old result not installed": asserted via NativeCompiled staying 0,
//     v.jitNativeBytes == 0, no state in v.jitStates carrying HasNative(), and
//     the RX baseline being restored (closeJIT frees the published clone).
//     Because Code.Close() decrements the global counters exactly once per
//     published region, a double-free would push the counters BELOW the
//     baseline — the equality assertion catches leaks and double-frees alike.

package interpreter

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// reconfigureBigExpr builds `x + 1 + 1 + ...` with terms additions. Each add
// emits one OpLoadLocal/OpPushConst/OpAdd triple, so terms=80 already yields
// well over 128 IR instructions and forces the Auto background-queue path
// (queueNativeCompile is only used when len(program.Code) >= 128).
func reconfigureBigExpr(terms int) string {
	var b strings.Builder
	b.WriteString("x")
	for i := 0; i < terms; i++ {
		b.WriteString(" + 1")
	}
	return b.String()
}

// reconfigureQueueBig defines fn(x) = bigExpr(terms) and calls it once with
// threshold 1, which must leave exactly one background native compile queued.
func reconfigureQueueBig(t *testing.T, vm *VM, fnName string, terms int) {
	t.Helper()
	src := fmt.Sprintf(
		`globalThis.%[1]s = function(x) { return %[2]s; }; globalThis.%[1]s(1);`,
		fnName, reconfigureBigExpr(terms))
	if _, err := vm.Eval(src, "r2-5-queue.js"); err != nil {
		t.Fatalf("queue eval: %v", err)
	}
	stats := vm.JITStats()
	if stats.BackgroundQueued != 1 || vm.jitPending != 1 {
		t.Fatalf("background compile was not queued: pending=%d stats=%+v", vm.jitPending, stats)
	}
}

// reconfigureCall calls fn(value) and returns the result as a string.
func reconfigureCall(t *testing.T, vm *VM, fnName string, value int) string {
	t.Helper()
	src := fmt.Sprintf(`globalThis.%sResult = globalThis.%s(%d);`, fnName, fnName, value)
	if _, err := vm.Eval(src, "r2-5-call.js"); err != nil {
		t.Fatalf("call eval: %v", err)
	}
	got, err := vm.Global().Get(fnName + "Result")
	if err != nil {
		t.Fatalf("result lookup: %v", err)
	}
	return got.String()
}

// reconfigureWaitCompile blocks until every queued background compile has
// published its result (deterministic: the worker sends to the buffered
// channel before WaitGroup.Done), under a 5s deadline.
func reconfigureWaitCompile(t *testing.T, vm *VM) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		vm.jitCompileWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background compile did not finish within 5s deadline")
	}
}

// reconfigureConfigure calls ConfigureJIT under a 5s deadline. ConfigureJIT
// synchronously drains pending results (blocking until in-flight compiles
// publish), so a stuck drain fails the test instead of hanging it.
func reconfigureConfigure(t *testing.T, vm *VM, config jit.Config) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		vm.ConfigureJIT(config)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ConfigureJIT did not return within 5s deadline (pending compile drain stuck?)")
	}
}

// reconfigureClose calls VM.Close under a 5s deadline (same drain rationale).
func reconfigureClose(t *testing.T, vm *VM) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- vm.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("VM.Close did not return within 5s deadline (pending compile drain stuck?)")
	}
}

// reconfigurePoll drains published background results via the same call path
// the interpreter uses (tryQuickCall/tryQuickFrame), with a 5s deadline.
func reconfigurePoll(t *testing.T, vm *VM) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for vm.jitPending > 0 && time.Now().Before(deadline) {
		runtime.Gosched()
		vm.pollNativeCompiles()
	}
	if vm.jitPending != 0 {
		t.Fatalf("pollNativeCompiles did not drain within 5s deadline: pending=%d", vm.jitPending)
	}
}

// reconfigureAssertBaseline verifies the package-global RX counters returned
// to the values captured before the VM was created. Any published region that
// leaked keeps the counters above baseline; any double-free of a published
// region pushes them below baseline; both fail here.
func reconfigureAssertBaseline(t *testing.T, where string, baseRegions, baseBytes uint64) {
	t.Helper()
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("%s: live executable memory not at baseline: live=(%d,%d) baseline=(%d,%d) (leak or double-free)",
			where, regions, bytes, baseRegions, baseBytes)
	}
}

// reconfigureAssertNoNative checks that no native code is installed anywhere
// in this generation: no accounted bytes, no NativeCompiled stats, and no
// state in jitStates/jitTraces holding published code.
func reconfigureAssertNoNative(t *testing.T, vm *VM, where string) {
	t.Helper()
	stats := vm.JITStats()
	if vm.jitNativeBytes != 0 || stats.NativeCompiled != 0 || stats.NativeExecuted != 0 {
		t.Fatalf("%s: native code installed: nativeBytes=%d stats=%+v", where, vm.jitNativeBytes, stats)
	}
	for tmpl, state := range vm.jitStates {
		if jitStateHasNative(state) {
			t.Fatalf("%s: jitStates[%p] holds native code", where, tmpl)
		}
	}
	for key, state := range vm.jitTraces {
		if state != nil && state.program != nil && state.program.HasNative() {
			t.Fatalf("%s: jitTraces[%+v] holds native code", where, key)
		}
	}
}

// TestAutoJITReconfigureQuickDiscardsQueuedBackgroundCompile: Auto queues a
// background compile of a >=128-instruction program, then ConfigureJIT(Quick)
// runs before the result could ever be installed. The published (or still
// in-flight) result must be drained and freed, never adopted; subsequent calls
// in Quick mode must not grow NativeCompiled or reserve RX bytes.
func TestAutoJITReconfigureQuickDiscardsQueuedBackgroundCompile(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	reconfigureQueueBig(t, vm, "r2q", 300)

	// Switch to Quick while the background compile is pending. ConfigureJIT
	// drains synchronously: whether the compile already published or not, its
	// result must be closed (RX freed) and never installed.
	reconfigureConfigure(t, vm, jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	if stats := vm.JITStats(); stats.Mode != jit.Quick {
		t.Fatalf("mode after reconfigure: %v", stats.Mode)
	}
	if vm.jitPending != 0 || len(vm.jitStates) != 0 || vm.jitNativeBytes != 0 {
		t.Fatalf("reconfigure did not drain: pending=%d states=%d nativeBytes=%d",
			vm.jitPending, len(vm.jitStates), vm.jitNativeBytes)
	}
	reconfigureAssertBaseline(t, "after reconfigure to Quick", baseRegions, baseBytes)

	// Old-generation result must never appear: further calls only compile and
	// execute Quick tier-1 code in the new generation.
	for _, input := range []int{2, 5, 9} {
		if got := reconfigureCall(t, vm, "r2q", input); got != fmt.Sprint(input+300) {
			t.Fatalf("Quick round input=%d result=%s", input, got)
		}
		reconfigureAssertNoNative(t, vm, "Quick round after call")
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	reconfigureAssertBaseline(t, "after Close", baseRegions, baseBytes)
}

// TestAutoJITReconfigureOffDiscardsQueuedBackgroundCompile: same drain
// contract, but the target mode is Off; the new generation must stay
// completely JIT-free (no states, no stats, no RX) while results stay correct.
func TestAutoJITReconfigureOffDiscardsQueuedBackgroundCompile(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	reconfigureQueueBig(t, vm, "r2o", 300)

	reconfigureConfigure(t, vm, jit.Config{Mode: jit.Off})
	if stats := vm.JITStats(); stats.Mode != jit.Off {
		t.Fatalf("mode after reconfigure: %v", stats.Mode)
	}
	if vm.jitPending != 0 || len(vm.jitStates) != 0 || vm.jitNativeBytes != 0 {
		t.Fatalf("reconfigure did not drain: pending=%d states=%d nativeBytes=%d",
			vm.jitPending, len(vm.jitStates), vm.jitNativeBytes)
	}
	reconfigureAssertBaseline(t, "after reconfigure to Off", baseRegions, baseBytes)

	for _, input := range []int{3, 7} {
		if got := reconfigureCall(t, vm, "r2o", input); got != fmt.Sprint(input+300) {
			t.Fatalf("Off round input=%d result=%s", input, got)
		}
	}
	stats := vm.JITStats()
	if stats.Calls != 0 || stats.Compiled != 0 || stats.NativeCompiled != 0 || stats.Executed != 0 {
		t.Fatalf("Off mode recorded JIT activity: %+v", stats)
	}
	if len(vm.jitStates) != 0 || vm.jitNativeBytes != 0 {
		t.Fatalf("Off mode created JIT state: states=%d nativeBytes=%d", len(vm.jitStates), vm.jitNativeBytes)
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	reconfigureAssertBaseline(t, "after Close", baseRegions, baseBytes)
}

// TestJITReconfigureRotationAutoQuickOffKeepsResultsCorrect: repeated
// Auto -> Quick -> Off -> Auto rotation. Each round re-profiles the same
// closure (per-closure jitGeneration is refreshed by tryQuickCall), results
// stay correct in every mode, and each generation starts from a clean,
// drained, RX-empty state. The second Auto round additionally proves a fresh
// generation can queue, install, and execute background native code again.
func TestJITReconfigureRotationAutoQuickOffKeepsResultsCorrect(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	const terms = 200

	// Round 1: Auto, background compile + install + native execution.
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	generation := vm.jitGeneration // NewVM starts at 1; this ConfigureJIT made it 2.
	reconfigureQueueBig(t, vm, "r2r", terms)
	reconfigureWaitCompile(t, vm)
	reconfigurePoll(t, vm)
	stats := vm.JITStats()
	if stats.BackgroundCompleted != 1 || stats.BackgroundDiscarded != 0 || stats.NativeCompiled != 1 || vm.jitNativeBytes == 0 {
		t.Fatalf("round Auto: background result not installed: pending=%d stats=%+v nativeBytes=%d",
			vm.jitPending, stats, vm.jitNativeBytes)
	}
	if got := reconfigureCall(t, vm, "r2r", 4); got != fmt.Sprint(4+terms) {
		t.Fatalf("round Auto result=%s", got)
	}
	if stats := vm.JITStats(); stats.NativeExecuted != 1 {
		t.Fatalf("round Auto: installed code not executed: %+v", stats)
	}

	// Round 2: Quick. Generation increments, everything drains, native is
	// released; the same function compiles and runs tier-1 only.
	reconfigureConfigure(t, vm, jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	if vm.jitGeneration != generation+1 {
		t.Fatalf("generation after 1st reconfigure: %d want %d", vm.jitGeneration, generation+1)
	}
	generation = vm.jitGeneration
	if vm.jitPending != 0 || vm.jitNativeBytes != 0 || len(vm.jitStates) != 0 {
		t.Fatalf("round Quick: state not drained: pending=%d nativeBytes=%d states=%d",
			vm.jitPending, vm.jitNativeBytes, len(vm.jitStates))
	}
	reconfigureAssertBaseline(t, "round Quick after reconfigure", baseRegions, baseBytes)
	if got := reconfigureCall(t, vm, "r2r", 8); got != fmt.Sprint(8+terms) {
		t.Fatalf("round Quick result=%s", got)
	}
	stats = vm.JITStats()
	if stats.NativeCompiled != 0 || stats.NativeExecuted != 0 || stats.Executed != 1 || vm.jitNativeBytes != 0 {
		t.Fatalf("round Quick: unexpected JIT behavior: %+v nativeBytes=%d", stats, vm.jitNativeBytes)
	}

	// Round 3: Off. No JIT state at all, plain interpreter execution.
	reconfigureConfigure(t, vm, jit.Config{Mode: jit.Off})
	if vm.jitGeneration != generation+1 {
		t.Fatalf("generation after 2nd reconfigure: %d want %d", vm.jitGeneration, generation+1)
	}
	generation = vm.jitGeneration
	if got := reconfigureCall(t, vm, "r2r", 16); got != fmt.Sprint(16+terms) {
		t.Fatalf("round Off result=%s", got)
	}
	if len(vm.jitStates) != 0 || vm.jitNativeBytes != 0 {
		t.Fatalf("round Off: JIT state leaked: states=%d nativeBytes=%d", len(vm.jitStates), vm.jitNativeBytes)
	}

	// Round 4: Auto again. The new generation must compile, queue, install and
	// execute background native code from scratch.
	reconfigureConfigure(t, vm, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if vm.jitGeneration != generation+1 {
		t.Fatalf("generation after 3rd reconfigure: %d want %d", vm.jitGeneration, generation+1)
	}
	reconfigureQueueBig(t, vm, "r2r", terms)
	reconfigureWaitCompile(t, vm)
	reconfigurePoll(t, vm)
	if got := reconfigureCall(t, vm, "r2r", 32); got != fmt.Sprint(32+terms) {
		t.Fatalf("round Auto-2 result=%s", got)
	}
	stats = vm.JITStats()
	if stats.BackgroundCompleted != 1 || stats.NativeCompiled != 1 || stats.NativeExecuted != 1 || vm.jitNativeBytes == 0 {
		t.Fatalf("round Auto-2: new generation did not install/execute: %+v nativeBytes=%d", stats, vm.jitNativeBytes)
	}

	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	reconfigureAssertBaseline(t, "after rotation Close", baseRegions, baseBytes)
}

// TestAutoJITReconfigureCloseBeforePendingCompileCompletes blocks the compiler
// at its start hook, then waits until Close has entered closeJIT. This pins the
// in-flight drain path without relying on compiler speed or sleeps.
func TestAutoJITReconfigureCloseBeforePendingCompileCompletes(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	compileStarted := make(chan struct{})
	releaseCompile := make(chan struct{})
	closeStarted := make(chan struct{})
	vm.jitCompileStartHook = func() {
		close(compileStarted)
		<-releaseCompile
	}
	vm.jitCloseStartHook = func() { close(closeStarted) }
	reconfigureQueueBig(t, vm, "r2c", 2000)
	select {
	case <-compileStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("background compile did not reach the start barrier")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- vm.Close() }()
	select {
	case <-closeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not enter closeJIT")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while background compile was blocked: %v", err)
	default:
	}
	close(releaseCompile)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not drain the released background compile")
	}
	vm.jitCompileStartHook = nil
	vm.jitCloseStartHook = nil
	if vm.jitPending != 0 {
		t.Fatalf("Close left pending compiles: pending=%d", vm.jitPending)
	}
	if len(vm.jitCompileDone) != 0 {
		t.Fatalf("Close left results in compile channel: %d", len(vm.jitCompileDone))
	}
	reconfigureAssertBaseline(t, "after Close", baseRegions, baseBytes)

	// Post-Close: JIT state must be inert — polling is a no-op and a second
	// Close must not panic, double-free, or move the counters.
	vm.pollNativeCompiles()
	if vm.jitPending != 0 {
		t.Fatalf("poll after Close mutated state: pending=%d", vm.jitPending)
	}
	reconfigureAssertBaseline(t, "after post-Close poll", baseRegions, baseBytes)
	reconfigureClose(t, vm)
	reconfigureAssertBaseline(t, "after second Close", baseRegions, baseBytes)
}

// TestJITReconfigureResultPublishedBeforeInstall pins the tightest window:
// the background compile has fully finished (result deterministically in the
// buffered channel — the worker sends before WaitGroup.Done) but no
// pollNativeCompiles has run, so nothing is installed. Reconfiguring or
// closing at this exact point must discard the published result.
func TestJITReconfigureResultPublishedBeforeInstall(t *testing.T) {
	t.Run("ReconfigureDiscardsAndNewGenerationWorks", func(t *testing.T) {
		baseRegions, baseBytes := jitnative.LiveExecutableMemory()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
		reconfigureQueueBig(t, vm, "r2p", 300)
		reconfigureWaitCompile(t, vm)

		// Deterministic interleaving point: result published, not installed.
		if vm.jitPending != 1 {
			t.Fatalf("expected exactly one published-but-uninstalled result, pending=%d", vm.jitPending)
		}
		regions, bytes := jitnative.LiveExecutableMemory()
		if regions <= baseRegions || bytes <= baseBytes {
			t.Fatalf("published compile did not allocate RX: live=(%d,%d) baseline=(%d,%d)", regions, bytes, baseRegions, baseBytes)
		}
		reconfigureAssertNoNative(t, vm, "published result must not be installed")

		// Reconfigure (Auto again) at this point: the published clone must be
		// drained and freed, never adopted into the new generation.
		reconfigureConfigure(t, vm, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
		if vm.jitPending != 0 || len(vm.jitStates) != 0 || vm.jitNativeBytes != 0 {
			t.Fatalf("reconfigure did not discard published result: pending=%d states=%d nativeBytes=%d",
				vm.jitPending, len(vm.jitStates), vm.jitNativeBytes)
		}
		reconfigureAssertBaseline(t, "after reconfigure at published point", baseRegions, baseBytes)

		// Re-enabling Auto works in the new generation: fresh queue -> install
		// -> native execution, with correct results.
		reconfigureQueueBig(t, vm, "r2p", 300)
		reconfigureWaitCompile(t, vm)
		reconfigurePoll(t, vm)
		if got := reconfigureCall(t, vm, "r2p", 11); got != fmt.Sprint(11+300) {
			t.Fatalf("new-generation result=%s", got)
		}
		stats := vm.JITStats()
		if stats.BackgroundCompleted != 1 || stats.NativeCompiled != 1 || stats.NativeExecuted != 1 || vm.jitNativeBytes == 0 {
			t.Fatalf("new generation did not compile/install/execute: %+v nativeBytes=%d", stats, vm.jitNativeBytes)
		}
		if err := vm.Close(); err != nil {
			t.Fatal(err)
		}
		reconfigureAssertBaseline(t, "after Close", baseRegions, baseBytes)
	})

	t.Run("CloseReleasesPublishedNotInstalled", func(t *testing.T) {
		baseRegions, baseBytes := jitnative.LiveExecutableMemory()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
		reconfigureQueueBig(t, vm, "r2q2", 300)
		reconfigureWaitCompile(t, vm)

		if vm.jitPending != 1 {
			t.Fatalf("expected exactly one published-but-uninstalled result, pending=%d", vm.jitPending)
		}
		regions, bytes := jitnative.LiveExecutableMemory()
		if regions <= baseRegions || bytes <= baseBytes {
			t.Fatalf("published compile did not allocate RX: live=(%d,%d) baseline=(%d,%d)", regions, bytes, baseRegions, baseBytes)
		}
		reconfigureAssertNoNative(t, vm, "published result must not be installed")

		reconfigureClose(t, vm)
		if vm.jitPending != 0 || len(vm.jitCompileDone) != 0 {
			t.Fatalf("Close left published result: pending=%d channel=%d", vm.jitPending, len(vm.jitCompileDone))
		}
		reconfigureAssertBaseline(t, "after Close at published point", baseRegions, baseBytes)
	})
}

// TestJITReconfigurePollDiscardsRejectedBackgroundResult drives the
// pollNativeCompiles discard branch deterministically: between queue and
// completion the state is marked rejected (as a concurrent guard-disable
// would), so when the worker publishes, the poll must close the program and
// count BackgroundDiscarded instead of installing it.
//
// Note on the generation-mismatch branch: it is not reachable in
// single-threaded VM usage — ConfigureJIT/Close always drain the old channel
// synchronously (closeJIT blocks on the receive until in-flight workers
// publish, and workers send before WaitGroup.Done), so a result with a stale
// generation can never be observed by pollNativeCompiles. It is defensive
// code; the reachable stale-result paths are rejected/nil-program, exercised
// here.
func TestJITReconfigurePollDiscardsRejectedBackgroundResult(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	reconfigureQueueBig(t, vm, "r2d", 300)

	if len(vm.jitStates) != 1 {
		t.Fatalf("expected one profiled template, got %d", len(vm.jitStates))
	}
	for _, state := range vm.jitStates {
		state.rejected = true // simulate a concurrent guard-disable before completion
	}

	reconfigureWaitCompile(t, vm)
	if vm.jitPending != 1 {
		t.Fatalf("expected one published result, pending=%d", vm.jitPending)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions <= baseRegions || bytes <= baseBytes {
		t.Fatalf("published compile did not allocate RX: live=(%d,%d) baseline=(%d,%d)", regions, bytes, baseRegions, baseBytes)
	}

	vm.pollNativeCompiles()
	stats := vm.JITStats()
	if vm.jitPending != 0 || vm.jitNativeBytes != 0 || stats.NativeCompiled != 0 || stats.NativeExecuted != 0 {
		t.Fatalf("rejected result was installed: pending=%d nativeBytes=%d stats=%+v", vm.jitPending, vm.jitNativeBytes, stats)
	}
	if stats.BackgroundDiscarded != 1 || stats.BackgroundCompleted != 0 {
		t.Fatalf("rejected result was not counted as discarded: %+v", stats)
	}
	reconfigureAssertBaseline(t, "after discard poll", baseRegions, baseBytes)

	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	reconfigureAssertBaseline(t, "after Close", baseRegions, baseBytes)
}
