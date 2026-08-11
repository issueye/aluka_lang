package interpreter

import (
	"fmt"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// runExceptionTier evaluates source in one tier and returns the values of the
// given globals plus the JIT stats. Threshold 1 + BackedgeThreshold 1 make
// traces compile on the first backedge so the exception-exit path is actually
// exercised (not just Tier 0 throwing).
func runExceptionTier(t *testing.T, mode jit.Mode, source string, globals []string) (map[string]string, jit.Stats) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, Stats: true})
	if _, err := vm.Eval(source, "deopt-exception.js"); err != nil {
		t.Fatalf("mode=%s: %v", mode, err)
	}
	values := make(map[string]string, len(globals))
	for _, name := range globals {
		value, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("mode=%s get %s: %v", mode, name, err)
		}
		values[name] = value.String()
	}
	return values, vm.JITStats()
}

// checkExceptionScenario runs a scenario in off/quick/auto, asserts the
// results match, and requires that the JIT actually executed the trace
// (the pending-exception state went through the deopt recovery path).
func checkExceptionScenario(t *testing.T, source string, globals []string, want map[string]string) {
	t.Helper()
	off, _ := runExceptionTier(t, jit.Off, source, globals)
	quick, quickStats := runExceptionTier(t, jit.Quick, source, globals)
	auto, autoStats := runExceptionTier(t, jit.Auto, source, globals)
	for _, name := range globals {
		if off[name] != want[name] {
			t.Fatalf("%s off = %q, want %q", name, off[name], want[name])
		}
		if quick[name] != off[name] || auto[name] != off[name] {
			t.Fatalf("%s: off=%q quick=%q auto=%q", name, off[name], quick[name], auto[name])
		}
	}
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("quick did not execute the trace (exception path not exercised): %+v", quickStats)
	}
	if autoStats.TracesCompiled == 0 || autoStats.TracesExecuted == 0 {
		t.Fatalf("auto did not execute the trace (exception path not exercised): %+v", autoStats)
	}
}

// TestDeoptExceptionExitNumericThrow covers a numeric throw inside a trace:
// the exception exit carries the original value, the committed locals survive
// (s == 2 before the throw), and execution continues after the catch.
func TestDeoptExceptionExitNumericThrow(t *testing.T) {
	source := `
function k1(n) {
  let s = 0;
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw s * 10;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "caught:" + typeof e + ":" + e;
  }
  return s;
}
globalThis.E1 = k1(100);
`
	checkExceptionScenario(t, source, []string{"E1", "ER"}, map[string]string{
		"E1": "1002", // 2 (committed prefix) + 1000 (catch)
		"ER": "caught:number:20",
	})
}

// TestDeoptExceptionExitStringThrow covers a string thrown through the trace
// (the string is a local, so the trace can still compile and carry it).
func TestDeoptExceptionExitStringThrow(t *testing.T) {
	source := `
function k2(n) {
  let s = 0;
  const msg = "boom";
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw msg;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "caught:" + typeof e + ":" + e;
  }
  return s;
}
globalThis.E1 = k2(100);
`
	checkExceptionScenario(t, source, []string{"E1", "ER"}, map[string]string{
		"E1": "1002",
		"ER": "caught:string:boom",
	})
}

// TestDeoptExceptionExitObjectIdentity covers an object thrown through the
// trace: the catch parameter must be the very same object (identity kept).
func TestDeoptExceptionExitObjectIdentity(t *testing.T) {
	source := `
function k3(n) {
  let s = 0;
  const errObj = { code: 42 };
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw errObj;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "same:" + (e === errObj) + ":code:" + e.code;
  }
  return s;
}
globalThis.E1 = k3(100);
`
	checkExceptionScenario(t, source, []string{"E1", "ER"}, map[string]string{
		"E1": "1002",
		"ER": "same:true:code:42",
	})
}

// TestDeoptExceptionExitFinallyRethrow covers throw -> finally -> rethrow ->
// outer catch: the finally block runs exactly once and the original thrown
// value reaches the outer catch unchanged.
func TestDeoptExceptionExitFinallyRethrow(t *testing.T) {
	source := `
function k4(n) {
  let s = 0;
  let f = 0;
  try {
    try {
      for (let i = 0; i < n; i++) {
        if (i < 2) { s += 1; continue; }
        throw s;
      }
    } finally {
      f += 100;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "caught:" + e + ":f:" + f;
  }
  return s;
}
globalThis.E1 = k4(100);
`
	checkExceptionScenario(t, source, []string{"E1", "ER"}, map[string]string{
		"E1": "1002",
		"ER": "caught:2:f:100",
	})
}

// TestDeoptExceptionExitNestedCatch covers nested try/catch: the inner catch
// consumes the trace-thrown value and the outer handler never fires.
func TestDeoptExceptionExitNestedCatch(t *testing.T) {
	source := `
function k5(n) {
  let s = 0;
  let outer = 0;
  try {
    try {
      for (let i = 0; i < n; i++) {
        if (i < 2) { s += 1; continue; }
        throw 7;
      }
    } catch (e) {
      s += 100;
      globalThis.ER = "inner:" + e;
    }
    outer += 1;
  } catch (e) {
    outer += 1000;
  }
  return s + outer;
}
globalThis.E1 = k5(100);
`
	checkExceptionScenario(t, source, []string{"E1", "ER"}, map[string]string{
		"E1": "103", // s = 2 + 100, outer = 1
		"ER": "inner:7",
	})
}

// TestDeoptExceptionExitAutoFallsBackToQuick verifies that Auto refuses to
// publish the exception-exit trace as Native and stably runs it in Quick.
func TestDeoptExceptionExitAutoFallsBackToQuick(t *testing.T) {
	source := `
function k6(n) {
  let s = 0;
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw 99;
    }
  } catch (e) {
    s += 1000;
  }
  return s;
}
globalThis.E1 = k6(100);
`
	off, _ := runExceptionTier(t, jit.Off, source, []string{"E1"})
	auto, autoStats := runExceptionTier(t, jit.Auto, source, []string{"E1"})
	if auto["E1"] != off["E1"] {
		t.Fatalf("auto=%q off=%q", auto["E1"], off["E1"])
	}
	if autoStats.NativeTracesCompiled != 0 {
		t.Fatalf("exception-exit trace must not compile natively: %+v", autoStats)
	}
	if autoStats.NativeTracesRejected == 0 {
		t.Fatalf("native must reject the exception-exit trace: %+v", autoStats)
	}
	if autoStats.TracesCompiled == 0 || autoStats.TracesExecuted == 0 {
		t.Fatalf("auto must run the trace in Quick: %+v", autoStats)
	}
}

// TestDeoptExceptionExitGuardFailureCatch covers the guard-failure path: the
// trace falls back before a throwing operation and Tier 0 re-throws into the
// same catch; the exception type, message and side-effect prefix match. (The
// trace compiles but guards fail at the accessor, so this scenario asserts
// guard failures instead of trace executions.)
func TestDeoptExceptionExitGuardFailureCatch(t *testing.T) {
	source := `
function k7(n) {
  let s = 0;
  const o = { get v() { throw new RangeError("boom-get"); } };
  try {
    for (let i = 0; i < n; i++) {
      if (i < 3) { s += 1; continue; }
      s += o.v;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "caught:" + e.name + ":" + e.message;
  }
  return s;
}
globalThis.E1 = k7(100);
`
	off, _ := runExceptionTier(t, jit.Off, source, []string{"E1", "ER"})
	quick, quickStats := runExceptionTier(t, jit.Quick, source, []string{"E1", "ER"})
	auto, autoStats := runExceptionTier(t, jit.Auto, source, []string{"E1", "ER"})
	for name, want := range map[string]string{"E1": "1003", "ER": "caught:RangeError:boom-get"} {
		if off[name] != want {
			t.Fatalf("%s off = %q, want %q", name, off[name], want)
		}
		if quick[name] != off[name] || auto[name] != off[name] {
			t.Fatalf("%s: off=%q quick=%q auto=%q", name, off[name], quick[name], auto[name])
		}
	}
	if quickStats.TracesCompiled == 0 || quickStats.GuardFailures == 0 {
		t.Fatalf("quick should compile the trace and guard-fail at the accessor: %+v", quickStats)
	}
	if autoStats.TracesCompiled == 0 || autoStats.GuardFailures == 0 {
		t.Fatalf("auto should compile the trace and guard-fail at the accessor: %+v", autoStats)
	}
}

// TestDeoptExceptionExitStatsDescribesExceptionExit verifies the deopt
// statistics record the exception exit with its resume PC.
func TestDeoptExceptionExitStatsDescribesExceptionExit(t *testing.T) {
	source := `
function k8(n) {
  let s = 0;
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw 5;
    }
  } catch (e) { s += 1000; }
  return s;
}
globalThis.E1 = k8(100);
`
	_, stats := runExceptionTier(t, jit.Quick, source, []string{"E1"})
	if stats.TracesExecuted == 0 {
		t.Fatalf("trace not executed: %+v", stats)
	}
	found := false
	for _, deopt := range stats.DeoptExits {
		if deopt.Count > 0 {
			found = true
			if deopt.ResumePC <= 0 {
				t.Fatalf("exception exit resume PC not recorded: %+v", deopt)
			}
		}
	}
	if !found {
		t.Fatalf("no deopt recorded for the exception exit: %+v", stats)
	}
	_ = fmt.Sprint
	_ = engine.Undefined
}
