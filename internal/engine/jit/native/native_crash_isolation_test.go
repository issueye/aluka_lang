//go:build (windows || linux) && amd64

// R2-6 gate: every piece of illegal machine code must be executable only
// inside a subprocess. These tests re-use the existing subprocess pattern from
// native_crash_test.go: the parent re-executes the current test binary
// (os.Args[0]) with -test.run=^TestNativeCrashIsolationScenarioHelper$ and an
// env var selecting the scenario; the child publishes the scenario bytes and
// calls them, then crashes (or hangs / returns, depending on the scenario).
// The parent classifies the child's death and asserts it is the expected one,
// then keeps running: a legal kernel is published and executed in the parent
// process after every child crash, and LiveExecutableMemory() is asserted to
// return to its snapshot so illegal code cannot pollute RX lifecycle stats.
//
// Outcome classification (classifyChildExit). The child prints an "executing"
// marker to stderr immediately before the call and a "returned" marker if the
// illegal code comes back, and uses sentinel exit codes 3 (startup failure)
// and 42 (in-process watchdog). Classification is marker-based, so no
// platform-specific crash code is hardcoded — Unix signals (ExitCode -1) and
// Windows exception codes (0xC0000005-style) both land in outcomeCrashed as
// long as the child died after printing the executing marker:
//
//	crashOutcome        how the parent detects it
//	------------------  -----------------------------------------------------
//	outcomeCrashed      executing marker present + nonzero exit (any platform)
//	outcomeTimeout      parent context deadline killed the child
//	outcomeChildWatchdog child's own watchdog fired (sentinel exit 42)
//	outcomeStartupFailure child failed before executing (sentinel exit 3, or
//	                      the binary could not be started at all)
//	outcomeUnexpectedSuccess child exited 0, or the illegal code returned and
//	                      the child fell through to t.Fatal (returned marker)
//	outcomeChildTestFailure child exited via the test framework (1/2) without
//	                      ever printing the executing marker
//
// Sentinel exit codes 3 and 42 are produced only by this test's child helper
// and are identical on Windows and Linux; they cannot collide with crash
// codes, so the classification is portable. All crash scenarios also assert
// the crash happened before the child watchdog would have fired.
//
// Scenario list (all deterministic on amd64 for both platforms):
//
//	ud2           0F 0B                  #UD invalid opcode
//	truncated-ud2 0F                    0F escape byte alone; the decoder must
//	                                     fetch past the region end and decodes
//	                                     0F 00 00 = SLDT [RAX] on the zero-filled
//	                                     page tail -> write to the RX page
//	truncated-jmp FF 25 00 00            JMP QWORD PTR [rip+0] with a truncated
//	                                     disp32; missing bytes are zero-filled,
//	                                     target pointer is 0 -> jump to 0
//	jump-out      E9 00 00 00 00         JMP rel32=0 -> target entry+5, one byte
//	                                     past the region; tail decodes
//	                                     00 00 = ADD [RAX], AL -> write to RX page
//	bad-offset    C3 0F 0B @offset 1     RET at 0, UD2 at 1: an in-range call at
//	                                     the wrong offset executes the UD2
//	hang          EB FE                  infinite self-loop, deliberately
//	                                     non-crashing: exercises the watchdog
//	                                     and parent-timeout classification
//	legal-return  AddF64Kernel()         legal kernel that must return normally
//	                                     (exercises unexpected-success path)
//
// Production findings recorded while writing these tests (no production code
// was changed):
//
//  1. Executing at or past the logical end of a published region decodes the
//     zero-filled tail of the page as *valid* instructions (0x00 0x00 ->
//     ADD [RAX], AL) and only faults because the page is RX. Truncated
//     instructions and jumps past the end therefore crash via a write fault,
//     not deterministically (see truncated-ud2 / jump-out). Suggestion: pad
//     published regions with 0xCC (INT3) or 0x0F 0x0B (UD2) so any fetch past
//     the logical end faults deterministically regardless of page contents.
//  2. Code.CallAt conflates "rejected by guard" (nil code/frame, out-of-range
//     offset) with "kernel returned status 1": callers cannot distinguish a
//     guarded call from a kernel that legitimately returns status 1. The
//     out-of-range guard is verified by
//     TestNativeCrashIsolationOutOfRangeOffsetRejectedInProcess. Suggestion:
//     use a separate sentinel (e.g. math.MaxUint64) or an error return.
//  3. A non-crashing, non-returning kernel (EB FE) spins forever on its OS
//     thread: the Go runtime cannot preempt code outside Go text, so the
//     child's in-process watchdog only fires when a spare P is available
//     (the helper forces GOMAXPROCS(2) for that reason) — see the hang
//     scenario. Suggestion: execute native calls on a dedicated locked OS
//     thread, or document that GOMAXPROCS must exceed the number of
//     concurrently running native calls.

package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const (
	// Sentinel exit codes produced only by the child helper; identical on
	// Windows and Linux (never produced by crash paths: Unix signals and
	// Windows exception codes cannot be 3 or 42).
	nativeCrashStartupExitCode  = 3
	nativeCrashWatchdogExitCode = 42

	nativeCrashScenarioEnv  = "ALUKA_NATIVE_CRASH_SCENARIO"
	nativeCrashWatchdogEnv  = "ALUKA_NATIVE_CRASH_WATCHDOG_MS"
	nativeCrashExecMarker   = "ALUKA_NATIVE_CRASH_EXECUTING "
	nativeCrashReturnMarker = "ALUKA_NATIVE_CRASH_RETURNED status="

	defaultChildWatchdog = 10 * time.Second
	maxDiagOutput        = 2048
)

type crashScenario struct {
	name         string
	code         []byte
	offset       uint64
	expectReturn bool // legal code expected to return normally
}

var crashScenarioList = []crashScenario{
	{name: "ud2", code: []byte{0x0f, 0x0b}},
	{name: "truncated-ud2", code: []byte{0x0f}},
	{name: "truncated-jmp", code: []byte{0xff, 0x25, 0x00, 0x00}},
	{name: "jump-out", code: []byte{0xe9, 0x00, 0x00, 0x00, 0x00}},
	{name: "bad-offset", code: []byte{0xc3, 0x0f, 0x0b}, offset: 1},
	{name: "hang", code: []byte{0xeb, 0xfe}},
	{name: "legal-return", code: AddF64Kernel(), expectReturn: true},
}

var crashScenarioByName = func() map[string]crashScenario {
	m := make(map[string]crashScenario, len(crashScenarioList))
	for _, sc := range crashScenarioList {
		m[sc.name] = sc
	}
	return m
}()

// crashOutcome enumerates the mutually distinguishable child-process results
// the parent is required to tell apart (see the file header).
type crashOutcome int

const (
	outcomeCrashed crashOutcome = iota
	outcomeTimeout
	outcomeChildWatchdog
	outcomeStartupFailure
	outcomeChildTestFailure
	outcomeUnexpectedSuccess
)

func (o crashOutcome) String() string {
	switch o {
	case outcomeCrashed:
		return "crashed"
	case outcomeTimeout:
		return "parent-timeout"
	case outcomeChildWatchdog:
		return "child-watchdog"
	case outcomeStartupFailure:
		return "startup-failure"
	case outcomeChildTestFailure:
		return "child-test-failure"
	case outcomeUnexpectedSuccess:
		return "unexpected-success"
	}
	return "unknown"
}

type childRunResult struct {
	outcome crashOutcome
	elapsed time.Duration
	diag    string
}

// TestNativeCrashIsolationInvalidInstruction: a single-byte invalid opcode
// must crash inside the child only.
func TestNativeCrashIsolationInvalidInstruction(t *testing.T) {
	runCrashScenarioTest(t, "ud2")
}

// TestNativeCrashIsolationTruncatedInstruction: machine code cut in the middle
// of an instruction forces the decoder to fetch past the logical end of the
// published region (see findings 1 in the file header).
func TestNativeCrashIsolationTruncatedInstruction(t *testing.T) {
	runCrashScenarioTest(t, "truncated-ud2")
	runCrashScenarioTest(t, "truncated-jmp")
}

// TestNativeCrashIsolationAbnormalControlFlow: control flow escaping the
// published region, or entering it at the wrong in-range offset, must
// terminate inside the child process.
func TestNativeCrashIsolationAbnormalControlFlow(t *testing.T) {
	runCrashScenarioTest(t, "jump-out")
	runCrashScenarioTest(t, "bad-offset")
}

// TestNativeCrashIsolationHangDetection: a non-crashing, non-returning kernel
// must not be mistaken for a crash — the child's own watchdog fires and exits
// with sentinel 42, which the parent classifies as outcomeChildWatchdog.
func TestNativeCrashIsolationHangDetection(t *testing.T) {
	res := runCrashIsolationChild(t, "hang", 1500*time.Millisecond, 0)
	if res.outcome != outcomeChildWatchdog {
		t.Fatalf("want %v, got %v\n%s", outcomeChildWatchdog, res.outcome, res.diag)
	}
	t.Logf("hang: %s", res.diag)
}

// TestNativeCrashIsolationParentTimeoutDetection: with the child watchdog
// disabled, a hung child is finally killed by the parent's own context
// deadline; the parent must classify that as outcomeTimeout, never as a crash.
func TestNativeCrashIsolationParentTimeoutDetection(t *testing.T) {
	res := runCrashIsolationChild(t, "hang", 0, 5*time.Second)
	if res.outcome != outcomeTimeout {
		t.Fatalf("want %v, got %v\n%s", outcomeTimeout, res.outcome, res.diag)
	}
	t.Logf("parent-timeout: %s", res.diag)
	// The parent process is still healthy after the hung child was reaped.
	verifyLegalKernel(t)
}

// TestNativeCrashIsolationStartupFailureDetection: a child that fails before
// executing any code (unknown scenario sentinel exit 3, or a binary that
// cannot be launched at all) must be classified as outcomeStartupFailure.
func TestNativeCrashIsolationStartupFailureDetection(t *testing.T) {
	res := runCrashIsolationChild(t, "no-such-scenario", 0, 0)
	if res.outcome != outcomeStartupFailure {
		t.Fatalf("want %v, got %v\n%s", outcomeStartupFailure, res.outcome, res.diag)
	}
	t.Logf("unknown-scenario: %s", res.diag)

	out, err := exec.Command("aluka-native-crash-no-such-binary-xyz").CombinedOutput()
	outcome, diag := classifyChildExit("unlaunchable-binary", err, nil, out, 0)
	if outcome != outcomeStartupFailure {
		t.Fatalf("want %v, got %v\n%s", outcomeStartupFailure, outcome, diag)
	}
}

// TestNativeCrashIsolationUnexpectedSuccessDetection: legal code in the child
// returns normally and the child exits 0; the parent must classify that as
// outcomeUnexpectedSuccess, never as a crash.
func TestNativeCrashIsolationUnexpectedSuccessDetection(t *testing.T) {
	res := runCrashIsolationChild(t, "legal-return", 0, 0)
	if res.outcome != outcomeUnexpectedSuccess {
		t.Fatalf("want %v, got %v\n%s", outcomeUnexpectedSuccess, res.outcome, res.diag)
	}
	t.Logf("legal-return: %s", res.diag)
}

// TestNativeCrashIsolationRecoversAfterChildCrash: after a child dies from
// illegal code, this parent test process must still publish and execute legal
// native code with bit-exact results, and the RX lifecycle counters must be
// back at their snapshot.
func TestNativeCrashIsolationRecoversAfterChildCrash(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	verifyLegalKernel(t)
	res := runCrashIsolationChild(t, "ud2", defaultChildWatchdog, 0)
	if res.outcome != outcomeCrashed {
		t.Fatalf("ud2: want %v, got %v\n%s", outcomeCrashed, res.outcome, res.diag)
	}
	verifyLegalKernel(t)
	assertLiveMemoryBaseline(t, baseRegions, baseBytes)
	t.Logf("recover-after-crash: %s", res.diag)
}

// TestNativeCrashIsolationPublishReleaseBaseline: illegal machine code may be
// published in the parent (publishing never executes) and must leave the
// LiveExecutableMemory accounting exactly at its baseline after Close, for
// every scenario.
func TestNativeCrashIsolationPublishReleaseBaseline(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	for _, sc := range crashScenarioList {
		code, err := Publish(sc.code)
		if err != nil {
			t.Fatalf("scenario %q: publish: %v", sc.name, err)
		}
		regions, bytes := LiveExecutableMemory()
		if regions != baseRegions+1 || bytes != baseBytes+uint64(len(sc.code)) {
			t.Fatalf("scenario %q: accounting=(%d,%d), want (%d,%d)",
				sc.name, regions, bytes, baseRegions+1, baseBytes+uint64(len(sc.code)))
		}
		if err := code.Close(); err != nil {
			t.Fatalf("scenario %q: close: %v", sc.name, err)
		}
		assertLiveMemoryBaseline(t, baseRegions, baseBytes)
	}
}

// TestNativeCrashIsolationOutOfRangeOffsetRejectedInProcess: a wrong offset at
// or beyond the region end is rejected by the Go-side guard without executing
// anything (status 1, untouched frame), so the only way to execute a wrong
// offset is in the child (bad-offset scenario).
func TestNativeCrashIsolationOutOfRangeOffsetRejectedInProcess(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, off := range []uint64{uint64(code.Size()), uint64(code.Size()) + 1, 1 << 40} {
		frame := &Frame{}
		frame.Args[0], frame.Args[1] = 1.25, 2.5
		if status := code.CallAt(off, frame); status != 1 {
			t.Fatalf("CallAt(%d): status=%d, want guard status 1", off, status)
		}
		if frame.Result != 0 {
			t.Fatalf("CallAt(%d): frame mutated to %v; guard executed code", off, frame.Result)
		}
	}
	frame := &Frame{}
	frame.Args[0], frame.Args[1] = 1.25, 2.5
	if status := code.Call(frame); status != 0 || math.Float64bits(frame.Result) != math.Float64bits(3.75) {
		t.Fatalf("legal call after guard rejections: status=%d result=%v", status, frame.Result)
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
	assertLiveMemoryBaseline(t, baseRegions, baseBytes)
}

// runCrashScenarioTest runs one crash scenario in a child, asserts the crash
// happened (and happened before the child watchdog could fire), then proves
// the parent still executes legal native code and keeps RX accounting at its
// baseline.
func runCrashScenarioTest(t *testing.T, scenario string) {
	t.Helper()
	baseRegions, baseBytes := LiveExecutableMemory()
	res := runCrashIsolationChild(t, scenario, defaultChildWatchdog, 0)
	if res.outcome != outcomeCrashed {
		t.Fatalf("scenario %q: want %v, got %v\n%s", scenario, outcomeCrashed, res.outcome, res.diag)
	}
	if res.elapsed >= defaultChildWatchdog {
		t.Fatalf("scenario %q: crash took %s; a crash must precede the %s child watchdog",
			scenario, res.elapsed, defaultChildWatchdog)
	}
	t.Logf("scenario %q: %s", scenario, res.diag)
	verifyLegalKernel(t)
	assertLiveMemoryBaseline(t, baseRegions, baseBytes)
}

// verifyLegalKernel publishes the legal AddF64Kernel, executes it bit-exactly,
// and checks the out-of-range guard, proving the parent process is still
// fully functional.
func verifyLegalKernel(t *testing.T) {
	t.Helper()
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatalf("publish legal kernel: %v", err)
	}
	defer code.Close()
	frame := &Frame{}
	frame.Args[0], frame.Args[1] = 1.25, 2.5
	if status := code.Call(frame); status != 0 {
		t.Fatalf("legal kernel status=%d", status)
	}
	if math.Float64bits(frame.Result) != math.Float64bits(3.75) {
		t.Fatalf("legal kernel result=%v, want 3.75", frame.Result)
	}
	guard := &Frame{}
	guard.Args[0], guard.Args[1] = 9, 9
	if status := code.CallAt(uint64(code.Size())+1, guard); status != 1 {
		t.Fatalf("out-of-range CallAt status=%d, want guard status 1", status)
	}
	if guard.Result != 0 {
		t.Fatalf("out-of-range CallAt mutated the frame to %v", guard.Result)
	}
}

func assertLiveMemoryBaseline(t *testing.T, baseRegions, baseBytes uint64) {
	t.Helper()
	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("LiveExecutableMemory=(%d,%d), want baseline (%d,%d)", regions, bytes, baseRegions, baseBytes)
	}
}

// runCrashIsolationChild spawns the helper child for one scenario and returns
// the classified outcome, elapsed time and a bounded diagnostic. childWatchdog
// <= 0 disables the child's in-process watchdog; parentTimeout <= 0 defaults
// to childWatchdog + 15s (min 10s).
func runCrashIsolationChild(t *testing.T, scenario string, childWatchdog, parentTimeout time.Duration) childRunResult {
	t.Helper()
	if parentTimeout <= 0 {
		parentTimeout = childWatchdog + 15*time.Second
		if parentTimeout < 10*time.Second {
			parentTimeout = 10 * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeCrashIsolationScenarioHelper$")
	cmd.Env = append(os.Environ(),
		nativeCrashHelperEnv+"=1",
		nativeCrashScenarioEnv+"="+scenario,
		nativeCrashWatchdogEnv+"="+strconv.FormatInt(childWatchdog.Milliseconds(), 10),
		"GOTRACEBACK=none",
	)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	outcome, diag := classifyChildExit(scenario, err, ctx.Err(), out, elapsed)
	return childRunResult{outcome: outcome, elapsed: elapsed, diag: diag}
}

// classifyChildExit maps a child process result to an outcome plus a bounded
// diagnostic. It intentionally hardcodes no platform-specific crash code:
// Windows exception codes and Unix signals both land in outcomeCrashed via the
// child's executing marker (see the file header for the full matrix).
func classifyChildExit(scenario string, err error, ctxErr error, out []byte, elapsed time.Duration) (crashOutcome, string) {
	diag := func(o crashOutcome, detail string) (crashOutcome, string) {
		return o, fmt.Sprintf("scenario=%q outcome=%s elapsed=%s detail=%s\n%s",
			scenario, o, elapsed.Truncate(time.Millisecond), detail, boundedOutput(out, maxDiagOutput))
	}
	if ctxErr != nil {
		return diag(outcomeTimeout, fmt.Sprintf("parent watchdog killed the child: %v", ctxErr))
	}
	if err == nil {
		return diag(outcomeUnexpectedSuccess, "child exited 0")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return diag(outcomeStartupFailure, fmt.Sprintf("child could not be started: %v", err))
	}
	code := ee.ExitCode()
	state := ee.ProcessState.String()
	switch code {
	case nativeCrashWatchdogExitCode:
		return diag(outcomeChildWatchdog, "child's in-process watchdog fired; the code did not crash")
	case nativeCrashStartupExitCode:
		return diag(outcomeStartupFailure, "child reported a startup failure before executing")
	}
	if bytes.Contains(out, []byte(nativeCrashReturnMarker)) {
		return diag(outcomeUnexpectedSuccess, "illegal code returned without crashing; child exited via t.Fatal")
	}
	if bytes.Contains(out, []byte(nativeCrashExecMarker)) {
		return diag(outcomeCrashed, fmt.Sprintf("child died while executing illegal code: %s (exit code %d)", state, code))
	}
	switch code {
	case 1, 2:
		return diag(outcomeChildTestFailure, fmt.Sprintf("child exited via the test framework without executing: %s (exit code %d)", state, code))
	default:
		return diag(outcomeChildTestFailure, fmt.Sprintf("child exited without executing: %s (exit code %d)", state, code))
	}
}

// boundedOutput limits diagnostic output to the head and tail of the child's
// combined output, so a crashing (or logging) child cannot flood the parent.
func boundedOutput(out []byte, limit int) string {
	if len(out) <= limit {
		return string(out)
	}
	half := limit / 2
	return string(out[:half]) + fmt.Sprintf("\n... [%d bytes omitted] ...\n", len(out)-limit) + string(out[len(out)-half:])
}

// TestNativeCrashIsolationScenarioHelper is the child entry point. It is
// re-executed by every parent test above via os.Args[0] with the scenario in
// the environment; without the helper env var it does nothing. All illegal
// machine code therefore executes only here, inside the child process.
func TestNativeCrashIsolationScenarioHelper(t *testing.T) {
	if os.Getenv(nativeCrashHelperEnv) != "1" {
		return
	}
	// The watchdog goroutine must stay schedulable even while a kernel spins
	// in a JIT loop (finding 3 in the file header).
	runtime.GOMAXPROCS(2)

	scenario := os.Getenv(nativeCrashScenarioEnv)
	sc, ok := crashScenarioByName[scenario]
	if !ok {
		fmt.Fprintf(os.Stderr, "native crash isolation helper: unknown scenario %q\n", scenario)
		os.Exit(nativeCrashStartupExitCode)
	}

	watchdog := defaultChildWatchdog
	if v := os.Getenv(nativeCrashWatchdogEnv); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms < 0 {
			fmt.Fprintf(os.Stderr, "native crash isolation helper: bad watchdog %q\n", v)
			os.Exit(nativeCrashStartupExitCode)
		}
		watchdog = time.Duration(ms) * time.Millisecond
	}
	if watchdog > 0 {
		time.AfterFunc(watchdog, func() {
			fmt.Fprintf(os.Stderr, "native crash isolation helper: watchdog fired after %s (scenario %q did not crash)\n", watchdog, sc.name)
			os.Exit(nativeCrashWatchdogExitCode)
		})
	}

	fmt.Fprintf(os.Stderr, "native crash isolation helper: scenario %q starting\n", sc.name)
	code, err := Publish(sc.code)
	if err != nil {
		t.Fatalf("scenario %q: publish: %v", sc.name, err)
	}
	defer code.Close()
	if code.Size() != len(sc.code) {
		t.Fatalf("scenario %q: published size %d, want %d", sc.name, code.Size(), len(sc.code))
	}

	os.Stderr.WriteString(nativeCrashExecMarker + sc.name + "\n")
	status := code.CallAt(sc.offset, &Frame{})
	if sc.expectReturn {
		if status != 0 {
			t.Fatalf("scenario %q: legal code returned status %d", sc.name, status)
		}
		return
	}
	os.Stderr.WriteString(fmt.Sprintf("%s%d\n", nativeCrashReturnMarker, status))
	t.Fatalf("scenario %q: illegal code returned status=%d without crashing", sc.name, status)
}
