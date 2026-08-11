// R5-6: TraceBudget joint calibration — latency (per-slice execution time),
// throughput (total executed iterations) and safepoint response (cancellation
// / OOM observed within a bounded number of polls and bounded wall time at any
// TraceBudget). These tests run on every platform: Auto degrades to the
// budgeted Quick executor where native code is unsupported, and the response
// bound holds in both tiers (each executor polls between budgeted slices).

package interpreter

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// r5LongLoopSource is the canonical long numeric loop used by the budget
// tests: it compiles to a budgeted leaf/trace in Auto (Quick on unsupported
// platforms), so every safepoint poll happens between budgeted slices.
const r5LongLoopSource = `
	function longLoop(n) {
		let s = 0;
		for (let i = 0; i < n; i++) s += i;
		return s;
	}
`

// TestJITCancelResponseBoundedAcrossBudgets proves the R5-6 cancellation
// claim: at any TraceBudget, a long loop observes cancellation within at most
// cancelAfter+2 safepoint polls (deterministic) and within a wall-clock bound
// derived from the budgeted slice length.
func TestJITCancelResponseBoundedAcrossBudgets(t *testing.T) {
	const iterations = 1 << 22
	for _, budget := range []uint32{1, 64, 65536, 1 << 20} {
		for _, cancelAfter := range []int{1, 3} {
			t.Run(fmt.Sprintf("budget=%d/cancelAfter=%d", budget, cancelAfter), func(t *testing.T) {
				vm, err := NewVM()
				if err != nil {
					t.Fatal(err)
				}
				defer vm.Close()
				cancelErr := errors.New("r5 cancel")
				polls := 0
				vm.ConfigureJIT(jit.Config{
					Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
					TraceBudget: budget, Stats: true, InterpreterSafepoints: true,
					Safepoint: func() error {
						polls++
						if polls >= cancelAfter {
							return cancelErr
						}
						return nil
					},
				})
				start := time.Now()
				_, err = vm.Eval(fmt.Sprintf("%s\nglobalThis.r5cancel = longLoop(%d);", r5LongLoopSource, iterations),
					"r5-cancel.js")
				elapsed := time.Since(start)
				if err == nil || !strings.Contains(err.Error(), "r5 cancel") {
					t.Fatalf("cancellation did not propagate: err=%v", err)
				}
				// Deterministic poll bound: the cancelAfter-th poll returns the
				// error, and no executor polls more often than once per slice.
				if polls < cancelAfter || polls > cancelAfter+2 {
					t.Fatalf("response polls=%d want in [%d,%d]", polls, cancelAfter, cancelAfter+2)
				}
				// Wall-clock bound: at most cancelAfter+2 slices, each slice at
				// most `budget` iterations of `s += i` (~1µs/iteration is a very
				// loose ceiling), plus fixed overhead for compile and dispatch.
				bound := time.Duration(budget)*time.Microsecond*time.Duration(cancelAfter+2) + 5*time.Second
				if elapsed > bound {
					t.Fatalf("cancel response %v exceeds bound %v (budget=%d)", elapsed, bound, budget)
				}
			})
		}
	}
}

// TestJITOOMResponseBoundedAcrossBudgets proves the R5-6 OOM claim: once the
// OOM flag is set, a long loop at any TraceBudget surfaces the OOM error at
// the next safepoint poll (bounded by one budgeted slice).
func TestJITOOMResponseBoundedAcrossBudgets(t *testing.T) {
	engine.ResetOOMState()
	oldStrikes := engine.OOMStrikeLimitForTest()
	engine.SetOOMStrikeLimitForTest(1000)
	defer func() {
		engine.StopMemoryWatchdog()
		engine.SetMemoryLimit(0)
		engine.ResetOOMState()
		engine.SetOOMStrikeLimitForTest(oldStrikes)
	}()
	// Enable the VM OOM path (oomEnabled mirrors MemoryLimitBytes at NewVM)
	// with a generous limit so the watchdog never forces a process exit.
	engine.SetMemoryLimit(1 << 30)
	const iterations = 1 << 22
	for _, budget := range []uint32{1, 65536, 1 << 20} {
		t.Run(fmt.Sprintf("budget=%d", budget), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
				TraceBudget: budget, Stats: true,
			})
			engine.TriggerOOMForTest()
			start := time.Now()
			_, err = vm.Eval(fmt.Sprintf("%s\nglobalThis.r5oom = longLoop(%d);", r5LongLoopSource, iterations),
				"r5-oom.js")
			elapsed := time.Since(start)
			if err == nil || !strings.Contains(err.Error(), "out of memory") {
				t.Fatalf("OOM did not propagate: err=%v", err)
			}
			// Bound: one observed slice (at most `budget` iterations) plus
			// compile/dispatch overhead.
			bound := time.Duration(budget)*time.Microsecond*3 + 5*time.Second
			if elapsed > bound {
				t.Fatalf("OOM response %v exceeds bound %v (budget=%d)", elapsed, bound, budget)
			}
		})
	}
}

// TestJITStatsAggregatesExplainTierHealth proves the R5-7 aggregates: leaf
// and trace shapes both produce a nonzero post-compile execution volume
// (Executions), a compile benefit (Executions per compiled site), trace
// deopts (Deopts counts semantic trace exits) and internally consistent
// guard/deopt/eviction rates — the inputs --jit-stats uses to explain whether
// a site should be promoted or demoted.
func TestJITStatsAggregatesExplainTierHealth(t *testing.T) {
	// Phase A: native leaf shape (Threshold=1 compiles leaves on first call).
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if _, err := vm.Eval(`
		globalThis.add1 = function add1(x) { return x + 1; };
		globalThis.add1(1); globalThis.add1(2); globalThis.add1(3);
		globalThis.add1(4); globalThis.add1(5);
	`, "r5-agg-leaf.js"); err != nil {
		vm.Close()
		t.Fatal(err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 5 {
		t.Fatalf("leaf phase did not run 5 native executions: %+v", stats)
	}
	if stats.Executions != 5 || stats.CompileBenefit != 5 {
		t.Fatalf("leaf aggregates: executions=%d benefit=%d want 5/5", stats.Executions, stats.CompileBenefit)
	}

	// Phase B: traced loop shape. The object literal makes the leaf compile
	// reject, so the backedges build the trace instead (soak-proven shape);
	// every trace semantic exit is a deopt.
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 1, Stats: true})
	if _, err := vm.Eval(`
		globalThis.add1 = function add1(x) { return x + 1; };
		globalThis.add1(1); globalThis.add1(2); globalThis.add1(3);
		globalThis.add1(4); globalThis.add1(5);
		globalThis.loop = function loop(n) {
			const marker = {};
			let s = 0;
			for (let i = 0; i < n; i++) s += i;
			return s;
		};
		globalThis.loop(1000);
		globalThis.loop(2000);
	`, "r5-agg-trace.js"); err != nil {
		vm.Close()
		t.Fatal(err)
	}
	stats = vm.JITStats()
	if stats.TracesCompiled+stats.NativeTracesCompiled == 0 {
		t.Fatalf("trace phase compiled no trace: %+v", stats)
	}
	if stats.TracesExecuted+stats.NativeTracesExecuted == 0 {
		t.Fatalf("trace phase executed no trace: %+v", stats)
	}
	if stats.Deopts == 0 {
		t.Fatalf("no deopts recorded for traced loop: %+v", stats)
	}
	expectedExecutions := stats.Executed + stats.NativeExecuted +
		stats.TracesExecuted + stats.NativeTracesExecuted +
		stats.TraceYields + stats.NativeYields + stats.NativeTraceYields
	if stats.Executions != expectedExecutions {
		t.Fatalf("executions=%d want derived %d", stats.Executions, expectedExecutions)
	}
	if stats.CompileBenefit == 0 {
		t.Fatalf("compile benefit is zero: %+v", stats)
	}
	if stats.NativeEvictions > stats.NativeCompiled+stats.NativeTracesCompiled {
		t.Fatalf("eviction rate > 100%%: %+v", stats)
	}
	vm.Close()
}

// TestJITTraceBudgetCalibration measures the R5-6 latency/throughput
// trade-off of TraceBudget and logs the data table. Assertions are loose
// (CI-safe): the default budget 65536 must keep single-slice latency below
// 100ms and throughput within 10x of the largest budget, which justifies
// keeping the default unchanged instead of hardcoding a benchmark special
// case.
func TestJITTraceBudgetCalibration(t *testing.T) {
	const iterations = 1 << 24
	expected := fmt.Sprint(uint64(1<<24) * (uint64(1<<24) - 1) / 2)
	type row struct {
		budget     uint32
		elapsed    time.Duration
		slices     uint64
		perSlice   time.Duration
		throughput float64 // iterations per second
	}
	var rows []row
	for _, budget := range []uint32{16, 1024, 65536, 1 << 20} {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{
			Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
			TraceBudget: budget, Stats: true,
		})
		start := time.Now()
		if _, err := vm.Eval(fmt.Sprintf("%s\nglobalThis.r5cal = longLoop(%d);", r5LongLoopSource, iterations),
			"r5-cal.js"); err != nil {
			vm.Close()
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		got, err := vm.Global().Get("r5cal")
		vm.Close()
		if err != nil || got.String() != expected {
			t.Fatalf("budget=%d result=%v err=%v want=%s", budget, got, err, expected)
		}
		slices := uint64(math.Ceil(float64(iterations) / float64(budget)))
		perSlice := elapsed / time.Duration(slices)
		throughput := float64(iterations) / elapsed.Seconds()
		rows = append(rows, row{budget: budget, elapsed: elapsed, slices: slices, perSlice: perSlice, throughput: throughput})
		t.Logf("TraceBudget=%d elapsed=%v slices=%d perSlice=%v throughput=%.0f iter/s",
			budget, elapsed, slices, perSlice, throughput)
	}
	if len(rows) != 4 {
		t.Fatal("calibration rows missing")
	}
	def := rows[2] // TraceBudget = 65536 (the normalized default)
	if def.perSlice > 100*time.Millisecond {
		t.Fatalf("default TraceBudget slice latency %v exceeds 100ms", def.perSlice)
	}
	maxThroughput := 0.0
	for _, r := range rows {
		if r.throughput > maxThroughput {
			maxThroughput = r.throughput
		}
	}
	if maxThroughput == 0 || def.throughput < maxThroughput/10 {
		t.Fatalf("default TraceBudget throughput %.0f iter/s below 10x of best %.0f",
			def.throughput, maxThroughput)
	}
}
