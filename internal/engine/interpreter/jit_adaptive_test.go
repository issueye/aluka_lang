package interpreter

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// TestAdaptiveThresholdColdStartNoExtraCompiles proves the R5-3 cold-start
// contract: a short-lived script whose functions never reach the static
// threshold must not see any additional compile attempt when the adaptive
// model is enabled. The model only lowers thresholds on positive feedback
// (executions of compiled code), so with zero compiles there is zero
// feedback and the two runs are byte-identical on every compile counter.
func TestAdaptiveThresholdColdStartNoExtraCompiles(t *testing.T) {
	source := `
		function one(n) { let s = 0; for (let i = 0; i < n; i++) s += i; return s; }
		function two(n) { return one(n) + one(n); }
		globalThis.coldStart = two(50) + two(60) + two(70);
	`
	run := func(adaptive bool) jit.Stats {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: jit.Quick, Threshold: 1000, BackedgeThreshold: 10000,
			Stats: true, Adaptive: adaptive,
		})
		if _, err := vm.Eval(source, "jit-adaptive-cold.js"); err != nil {
			t.Fatalf("adaptive=%v: %v", adaptive, err)
		}
		return vm.JITStats()
	}
	base := run(false)
	adaptive := run(true)
	for name, got := range map[string]uint64{
		"Candidates":     adaptive.Candidates,
		"Compiled":       adaptive.Compiled,
		"Rejected":       adaptive.Rejected,
		"TracesCompiled": adaptive.TracesCompiled,
		"TracesRejected": adaptive.TracesRejected,
	} {
		if got != 0 {
			t.Fatalf("cold start produced %s=%d with adaptive on: %+v", name, got, adaptive)
		}
	}
	if adaptive.AdaptiveBenefits != 0 || adaptive.AdaptiveFailures != 0 ||
		adaptive.AdaptiveBoost != 0 || adaptive.AdaptiveCool != 0 {
		t.Fatalf("cold start produced adaptive feedback without compiles: %+v", adaptive)
	}
	// The static run must be identical to the adaptive run: no extra
	// compile attempt anywhere.
	if base.Candidates != adaptive.Candidates || base.Compiled != adaptive.Compiled ||
		base.Rejected != adaptive.Rejected || base.Executed != adaptive.Executed ||
		base.Calls != adaptive.Calls || base.Backedges != adaptive.Backedges {
		t.Fatalf("adaptive cold start diverged from static:\nstatic=%+v\nadaptive=%+v", base, adaptive)
	}
	if got := adaptive.AdaptiveThreshold; got != 1000 {
		t.Fatalf("cold start effective threshold = %d, want 1000", got)
	}
}

// TestAdaptiveThresholdHotPromotesBorderlineFunction proves the R5-3 hot-path
// contract: once a long hotspot compiles and executes many times without
// guard failures (high compile benefit), the feedback loop halves the
// effective threshold so a borderline function that would never compile under
// the static threshold still promotes within the same script. Deterministic:
// every signal is a counter, no wall clock.
func TestAdaptiveThresholdHotPromotesBorderlineFunction(t *testing.T) {
	source := `
		function big(n) { return n * 3 + 1; }
		function border(n) { return n * 2; }
		let acc = 0;
		for (let i = 0; i < 5000; i++) acc += big(i);
		for (let i = 0; i < 3000; i++) acc += border(i);
		globalThis.hotPromote = acc;
	`
	run := func(adaptive bool) (string, jit.Stats) {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: jit.Quick, Threshold: 4000, BackedgeThreshold: 40000,
			Stats: true, Adaptive: adaptive,
		})
		if _, err := vm.Eval(source, "jit-adaptive-hot.js"); err != nil {
			t.Fatalf("adaptive=%v: %v", adaptive, err)
		}
		value, err := vm.Global().Get("hotPromote")
		if err != nil {
			t.Fatal(err)
		}
		return value.String(), vm.JITStats()
	}
	staticResult, staticStats := run(false)
	adaptiveResult, adaptiveStats := run(true)
	if staticResult != adaptiveResult {
		t.Fatalf("results diverge: static=%s adaptive=%s", staticResult, adaptiveResult)
	}
	if staticStats.Compiled != 1 {
		t.Fatalf("static run compiled %d functions, want 1 (borderline must stay cold): %+v",
			staticStats.Compiled, staticStats)
	}
	if adaptiveStats.Compiled != 2 {
		t.Fatalf("adaptive run compiled %d functions, want 2 (borderline promoted by lowered threshold): %+v",
			adaptiveStats.Compiled, adaptiveStats)
	}
	if adaptiveStats.AdaptiveBoost == 0 {
		t.Fatalf("hot loop did not raise the boost level: %+v", adaptiveStats)
	}
	if adaptiveStats.AdaptiveThreshold >= staticStats.AdaptiveThreshold {
		t.Fatalf("effective threshold did not drop: static=%d adaptive=%d",
			staticStats.AdaptiveThreshold, adaptiveStats.AdaptiveThreshold)
	}
	if adaptiveStats.AdaptiveBenefits < 1000 {
		t.Fatalf("expected >= 1000 compiled executions, got %d", adaptiveStats.AdaptiveBenefits)
	}
	// The boosted backedge threshold (40000>>4 = 2500) legitimately promotes
	// the top-level script function and its loops, which the compiler rejects
	// (CALL opcode, non-leaf): a handful of rejection failures is the model
	// cooling correctly, but far below CoolEvery=8 so no cool level may rise.
	if adaptiveStats.AdaptiveCool != 0 {
		t.Fatalf("hot loop raised the cool level: %+v", adaptiveStats)
	}
	if staticStats.AdaptiveBoost != 0 || staticStats.AdaptiveBenefits != 0 {
		t.Fatalf("static run must have no adaptive feedback: %+v", staticStats)
	}
}

// TestAdaptiveThresholdCoolDownRaisesThreshold proves the R5-3 cooling
// contract: compiles whose programs immediately deopt (guard failures) are
// wasted compilation; AdaptiveCoolEvery consecutive failures double the
// effective threshold, so a function that would compile under the static
// threshold stays on Tier 0 afterwards. Deterministic: 8 functions each
// produce exactly one benefit (the threshold call) and one guard failure
// (the string call), and the cool level rises to 1 exactly at the 8th
// failure — all before the borderline function runs.
func TestAdaptiveThresholdCoolDownRaisesThreshold(t *testing.T) {
	var body strings.Builder
	body.WriteString("let acc = 0;\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&body, "function f%d(x) { return x + 1; }\n", i)
	}
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&body, "for (let j = 0; j < 1000; j++) acc += f%d(1);\n", i)
	}
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&body, "acc += f%d({});\n", i)
	}
	body.WriteString("function g(n) { return n * 2; }\n")
	body.WriteString("for (let i = 0; i < 1500; i++) acc += g(i);\n")
	body.WriteString("globalThis.coolDown = typeof acc;\n")
	source := body.String()

	run := func(adaptive bool) (string, jit.Stats) {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: jit.Quick, Threshold: 1000, BackedgeThreshold: 10000,
			Stats: true, Adaptive: adaptive,
		})
		if _, err := vm.Eval(source, "jit-adaptive-cool.js"); err != nil {
			t.Fatalf("adaptive=%v: %v", adaptive, err)
		}
		value, err := vm.Global().Get("coolDown")
		if err != nil {
			t.Fatal(err)
		}
		return value.String(), vm.JITStats()
	}
	staticResult, staticStats := run(false)
	adaptiveResult, adaptiveStats := run(true)
	if staticResult != adaptiveResult {
		t.Fatalf("results diverge: static=%s adaptive=%s", staticResult, adaptiveResult)
	}
	if staticStats.Compiled != 9 {
		t.Fatalf("static run compiled %d functions, want 9 (g must compile at 1000): %+v",
			staticStats.Compiled, staticStats)
	}
	if adaptiveStats.Compiled != 8 {
		t.Fatalf("adaptive run compiled %d functions, want 8 (g must stay cold at effective threshold 2000): %+v",
			adaptiveStats.Compiled, adaptiveStats)
	}
	if adaptiveStats.AdaptiveCool != 1 {
		t.Fatalf("8 wasted compiles must raise the cool level to 1, got %d: %+v",
			adaptiveStats.AdaptiveCool, adaptiveStats)
	}
	if adaptiveStats.AdaptiveThreshold != 2000 {
		t.Fatalf("effective threshold = %d, want 2000 (1000 << 1)", adaptiveStats.AdaptiveThreshold)
	}
	if adaptiveStats.AdaptiveFailures < 8 {
		t.Fatalf("expected >= 8 failure events, got %d", adaptiveStats.AdaptiveFailures)
	}
	if staticStats.AdaptiveCool != 0 || staticStats.AdaptiveFailures != 0 {
		t.Fatalf("static run must have no adaptive feedback: %+v", staticStats)
	}
}

// TestCompileBudgetDeniesNewCompiles proves the R5-4 time budget: a 1ns
// per-VM cumulative compile budget admits the first compile and denies every
// later one (BudgetDenied), while interpretation keeps producing the correct
// result and the VM closes cleanly. The budget fields are observable through
// JITStats. The storm functions carry 80-term bodies so every compile takes
// well over one clock tick (the budget limit), making the denial pattern
// deterministic instead of relying on sub-tick measurement luck.
func TestCompileBudgetDeniesNewCompiles(t *testing.T) {
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	var body strings.Builder
	body.WriteString("let acc = 0;\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, "function b%d(x) { return %s; }\n", i, expression.String())
	}
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, "acc += b%d(3);\n", i)
	}
	body.WriteString("globalThis.budgetStorm = acc;\n")
	source := body.String()

	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
		CompileBudgetNanos: 1, Stats: true,
	})
	if _, err := vm.Eval(source, "jit-budget-time.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("budgetStorm")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.String(), "3320"; got != want {
		t.Fatalf("result=%s want=%s", got, want)
	}
	stats := vm.JITStats()
	if stats.BudgetDenied == 0 {
		t.Fatalf("1ns budget must deny later compiles: %+v", stats)
	}
	if stats.BudgetSpent == 0 {
		t.Fatalf("budget spent must be measured: %+v", stats)
	}
	if stats.CompileBudgetNanos != 1 {
		t.Fatalf("configured budget not observable: %+v", stats)
	}
	if stats.Candidates == 0 || stats.Compiled == 0 {
		t.Fatalf("first compile must have been admitted: %+v", stats)
	}
	// The interpreter kept advancing: every function call completed.
	if stats.Calls == 0 {
		t.Fatalf("no calls executed under budget: %+v", stats)
	}
}

// TestCompileBudgetQueueStormKeepsInterpreterMoving proves the R5-4 queue
// limit: with background compiles deterministically blocked at the start
// hook, the queue admits exactly CompileQueueLimit jobs and denies every
// further admission (QueueDenied), the interpreter still completes the
// script, and Close drains the pinned compiles without hanging. The queue
// depth never exceeds the limit (QueueDepthMax).
func TestCompileBudgetQueueStormKeepsInterpreterMoving(t *testing.T) {
	var body strings.Builder
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	body.WriteString("let acc = 0;\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, "function s%d(x) { return %s; }\n", i, expression.String())
	}
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, "acc += s%d(1);\n", i)
	}
	body.WriteString("globalThis.queueStorm = acc;\n")
	source := body.String()

	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	const queueLimit = 4
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
		CompileQueueLimit: queueLimit, CompileWorkers: queueLimit, Stats: true,
	})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer release()
	vm.jitCompileStartHook = func() { <-releaseCh }
	if _, err := vm.Eval(source, "jit-budget-queue.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("queueStorm")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.String(), "3240"; got != want {
		t.Fatalf("result=%s want=%s (interpreter must keep advancing under the storm)", got, want)
	}
	stats := vm.JITStats()
	if stats.QueueDenied != 40-queueLimit {
		t.Fatalf("QueueDenied=%d want=%d: %+v", stats.QueueDenied, 40-queueLimit, stats)
	}
	if stats.BackgroundQueued != queueLimit {
		t.Fatalf("BackgroundQueued=%d want=%d: %+v", stats.BackgroundQueued, queueLimit, stats)
	}
	if stats.QueueDepthMax != queueLimit {
		t.Fatalf("QueueDepthMax=%d want=%d (queue must never exceed the limit): %+v",
			stats.QueueDepthMax, queueLimit, stats)
	}
	if stats.QueueDepth != queueLimit {
		t.Fatalf("QueueDepth=%d want=%d (compiles still pinned at the hook): %+v",
			stats.QueueDepth, queueLimit, stats)
	}
	// Release the blocked compiles; Close must drain them without hanging.
	release()
	vm.jitCompileStartHook = nil
	closeDone := make(chan error, 1)
	go func() { closeDone <- vm.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung while draining the pinned background compiles")
	}
}

// TestCompileWorkersExplicitConcurrency proves the R5-4 concurrency cap: at
// most CompileWorkers background compiles run at once even when more jobs
// are queued (observed through a counting start hook), and the default (0)
// normalizes to a single worker.
func TestCompileWorkersExplicitConcurrency(t *testing.T) {
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	source := func(count int) string {
		var body strings.Builder
		body.WriteString("let acc = 0;\n")
		for i := 0; i < count; i++ {
			fmt.Fprintf(&body, "function w%d(x) { return %s; }\n", i, expression.String())
		}
		for i := 0; i < count; i++ {
			fmt.Fprintf(&body, "acc += w%d(1);\n", i)
		}
		body.WriteString("globalThis.workerResult = acc;\n")
		return body.String()
	}
	run := func(t *testing.T, workers, jobs, wantMax int) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{
			Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
			CompileQueueLimit: jobs, CompileWorkers: workers, Stats: true,
		})
		releaseCh := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
		defer release()
		var mu sync.Mutex
		active, maxActive := 0, 0
		vm.jitCompileStartHook = func() {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			<-releaseCh
			mu.Lock()
			active--
			mu.Unlock()
		}
		if _, err := vm.Eval(source(jobs), "jit-workers.js"); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			reached := maxActive
			mu.Unlock()
			if reached >= wantMax {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("workers=%d jobs=%d: max concurrent compiles %d never reached %d",
					workers, jobs, reached, wantMax)
			}
			time.Sleep(time.Millisecond)
		}
		mu.Lock()
		gotMax := maxActive
		mu.Unlock()
		if gotMax != wantMax {
			t.Fatalf("workers=%d jobs=%d: max concurrent compiles = %d, want %d",
				workers, jobs, gotMax, wantMax)
		}
		stats := vm.JITStats()
		if got := stats.CompileWorkers; workers == 0 && got != 1 {
			t.Fatalf("default CompileWorkers normalized to %d, want 1", got)
		} else if workers != 0 && got != uint64(workers) {
			t.Fatalf("CompileWorkers=%d not observable: %+v", workers, stats)
		}
		release()
		vm.jitCompileStartHook = nil
		if err := vm.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("DefaultSingleWorker", func(t *testing.T) { run(t, 0, 3, 1) })
	t.Run("ExplicitTwoWorkers", func(t *testing.T) { run(t, 2, 4, 2) })
}
