//go:build amd64 && (windows || linux)

package native

// R2-3 gate: the native JIT must stay correct while the Go runtime performs
// concurrent GC, asynchronous preemption and stack growth. These tests publish
// real machine code (Publish(AddF64Kernel()) plus a hand-assembled loop kernel),
// execute it from Go through the fixed trampoline, and verify every result
// bit-exactly (math.Float64bits) while other goroutines hammer runtime.GC(),
// the scheduler (channels + Gosched) and the stack-growth path (deep recursion
// with large frames) at the same time. A frame.Result NaN sentinel is written
// before each call, so a call that did not actually run the machine code
// (stale result) fails the bit-exact check.
//
// Every test snapshots LiveExecutableMemory() before publishing and
// deadline-polls until the accounting returns to that snapshot after all Code
// values are closed, so leaked or double-accounted executable regions fail
// loudly instead of passing silently.
//
// Hazard notes (documented here; production code is out of scope):
//   - Code.Close() is not synchronized with an in-flight Code.Call(). Minimal
//     repro: goroutine A loops over code.Call(frame) while goroutine B calls
//     code.Close() once -> VirtualFree/Munmap of memory that is still being
//     executed (use-after-free of executable memory). These tests only Close
//     after joining all workers. Suggest: document that Close requires a
//     quiesced caller, or reference-count in-flight calls inside Code.
//   - The long loop kernel below keeps a goroutine inside native code for
//     roughly hundreds of microseconds. The runtime cannot async-preempt at
//     the trampoline CALL, so a concurrent runtime.GC() waits for the kernel
//     to return. That is the intended pressure; budgets stay bounded so a
//     kernel that never returns cannot hang the suite forever.

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// requireExecutableBaseline polls (deadline-bounded) until the live executable
// memory accounting matches the snapshot taken before any Code was published.
// Accounting updates are synchronous in Close, so this normally passes on the
// first probe; the poll exists to surface any future accounting regression
// under GC churn without spinning forever.
func requireExecutableBaseline(t *testing.T, baseRegions, baseBytes uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		regions, bytes := LiveExecutableMemory()
		if regions == baseRegions && bytes == baseBytes {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("live executable memory = (%d regions, %d bytes), want baseline (%d, %d)",
				regions, bytes, baseRegions, baseBytes)
		}
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
}

func publishStressKernels(t *testing.T, n int) []*Code {
	t.Helper()
	codes := make([]*Code, 0, n)
	for i := 0; i < n; i++ {
		code, err := Publish(AddF64Kernel())
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		codes = append(codes, code)
	}
	return codes
}

// gcWorker triggers bounded, real stop-the-world/concurrent GC cycles.
func gcWorker(wg *sync.WaitGroup, iterations int) {
	defer wg.Done()
	for i := 0; i < iterations; i++ {
		runtime.GC()
	}
}

// schedWorker hammers the Go scheduler with channel ops and Gosched calls so
// the main goroutine is repeatedly descheduled/re-rescheduled between native
// calls (the default async-preemption path).
func schedWorker(wg *sync.WaitGroup, iterations int) {
	defer wg.Done()
	ch := make(chan int, 1)
	for i := 0; i < iterations; i++ {
		select {
		case ch <- i:
		default:
		}
		runtime.Gosched()
		select {
		case <-ch:
		default:
		}
	}
}

// allocWorker creates bounded heap churn so the GC workers have live garbage
// to collect; the sink keeps every allocation observable.
func allocWorker(wg *sync.WaitGroup, iterations int, sink *atomic.Uint64) {
	defer wg.Done()
	for i := 0; i < iterations; i++ {
		buf := make([]byte, 1<<15)
		buf[0] = byte(i)
		sink.Add(uint64(buf[0]))
		runtime.Gosched()
	}
}

// stressGrowStack recurses depth times with a 4 KiB frame, forcing repeated Go
// stack growth while the caller runs native code. The sink keeps the frame
// observable so the compiler cannot elide it. Returns the recursion depth so
// callers can prove the growth really happened.
//
//go:noinline
func stressGrowStack(depth int, sink *atomic.Uint64) int {
	var buf [4096]byte
	buf[0] = byte(depth)
	sink.Add(uint64(buf[0]))
	if depth <= 1 {
		return int(buf[0])
	}
	return stressGrowStack(depth-1, sink) + 1
}

// loopAddKernel returns machine code equivalent to:
//
//	x := frame.Args[0]
//	for i := 0; i < int(frame.Budget); i++ {
//		x += frame.Args[2]
//	}
//	frame.Result = x
//	return 0
//
// Encodings match the production emitter conventions in native_emit_amd64.go
// (movsd [R10+disp] / addsd / disp32 base-relative addressing). It keeps the
// CPU inside native code for ~hundreds of microseconds per call, giving
// concurrent GC and preemption a real window in which to interfere.
func loopAddKernel() []byte {
	return []byte{
		0xF2, 0x41, 0x0F, 0x10, 0x02, // MOVSD XMM0, [R10]         Args[0]
		0xF2, 0x41, 0x0F, 0x10, 0x4A, 0x10, // MOVSD XMM1, [R10+0x10]    Args[2]
		0x49, 0x8B, 0x82, 0x50, 0x01, 0x00, 0x00, // MOV RAX, [R10+0x150]      Budget
		0xF2, 0x0F, 0x58, 0xC1, // LOOP: ADDSD XMM0, XMM1
		0x48, 0xFF, 0xC8, // DEC RAX
		0x75, 0xF7, // JNZ LOOP (back to ADDSD)
		0xF2, 0x41, 0x0F, 0x11, 0x42, 0x40, // MOVSD [R10+0x40], XMM0    Result
		0x31, 0xC0, // XOR EAX, EAX
		0xC3, // RET
	}
}

// TestNativeJITGCAndSchedStress publishes four AddF64Kernel instances and runs
// them round-robin while concurrent goroutines perform runtime.GC(), scheduler
// contention and heap churn. Every call must return status 0 and a bit-exact
// sum, proving the published machine code really executed.
func TestNativeJITGCAndSchedStress(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	codes := publishStressKernels(t, 4)
	defer func() {
		for _, code := range codes {
			_ = code.Close()
		}
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	var sink atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(3)
	go gcWorker(&wg, 200)
	go schedWorker(&wg, 100000)
	go allocWorker(&wg, 2000, &sink)

	frame := &Frame{}
	const iterations = 150000
	for i := 0; i < iterations; i++ {
		code := codes[i%len(codes)]
		frame.Args[0] = float64(i) * 0.25
		frame.Args[1] = 1.5
		if status := code.Call(frame); status != 0 {
			t.Fatalf("iter %d: status = %d", i, status)
		}
		want := math.Float64bits(frame.Args[0] + frame.Args[1])
		if got := math.Float64bits(frame.Result); got != want {
			t.Fatalf("iter %d: bit-exact mismatch: result=%016x want=%016x", i, got, want)
		}
		if i%512 == 0 {
			runtime.Gosched()
		}
	}
	wg.Wait()
}

// TestNativeJITLongNativeWindowGCAndStackGrowth publishes a loop kernel that
// keeps the calling goroutine inside native code for hundreds of microseconds
// per call. Concurrent goroutines run GC cycles and bounded deep recursion
// with 4 KiB frames (forcing repeated stack growth through the runtime's
// async-preemption machinery). A NaN sentinel plus bit-exact budget-derived
// results prove the kernel itself ran and computed the expected value.
func TestNativeJITLongNativeWindowGCAndStackGrowth(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(loopAddKernel())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	frame := &Frame{}
	frame.Args[2] = 0.5 // per-iteration increment, exact in binary

	// Sanity: prove the kernel loops once and overwrites the NaN sentinel.
	frame.Budget = 1
	frame.Result = math.NaN()
	if status := code.Call(frame); status != 0 {
		t.Fatalf("sanity status = %d", status)
	}
	if got, want := math.Float64bits(frame.Result), math.Float64bits(0.5); got != want {
		t.Fatalf("sanity result bits = %016x, want %016x", got, want)
	}

	var sink atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(3)
	go gcWorker(&wg, 120)
	go schedWorker(&wg, 50000)
	go func() {
		defer wg.Done()
		for r := 0; r < 4; r++ {
			if n := stressGrowStack(1024, &sink); n != 1024 {
				t.Errorf("stack growth worker: depth=%d, want 1024", n)
			}
		}
	}()

	const calls = 40
	const maxBudget = 300000
	for i := 0; i < calls; i++ {
		frame.Args[0] = 0
		frame.Budget = uint64(maxBudget * (i%5 + 1) / 5)
		frame.Result = math.NaN()
		if status := code.Call(frame); status != 0 {
			t.Fatalf("call %d: status = %d", i, status)
		}
		// With Args[0]=0 and an exact 0.5 increment, every partial sum is
		// exactly representable, so the kernel result must equal Budget*0.5
		// bit-for-bit regardless of rounding order.
		want := math.Float64bits(float64(frame.Budget) * 0.5)
		if got := math.Float64bits(frame.Result); got != want {
			t.Fatalf("call %d: result bits=%016x want=%016x (budget=%d)", i, got, want, frame.Budget)
		}
	}
	wg.Wait()
}

// TestNativeJITStackGrowthDuringNativeCalls runs short AddF64Kernel calls in a
// tight loop while a worker goroutine repeatedly recurses 1536 deep with 4 KiB
// frames (millions of bytes of stack growth) and another worker runs GC. This
// exercises the default async-preemption stack-growth path alongside native
// execution; any corruption of register/FPU state or the frame shows up as a
// bit-exact mismatch.
func TestNativeJITStackGrowthDuringNativeCalls(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	codes := publishStressKernels(t, 2)
	defer func() {
		for _, code := range codes {
			_ = code.Close()
		}
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	var sink atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for r := 0; r < 6; r++ {
			if n := stressGrowStack(1536, &sink); n != 1536 {
				t.Errorf("stack growth worker: depth=%d, want 1536", n)
			}
			runtime.Gosched()
		}
	}()
	go gcWorker(&wg, 300)

	frame := &Frame{}
	const iterations = 120000
	for i := 0; i < iterations; i++ {
		code := codes[i&1]
		frame.Args[0] = float64(i) * 0.125
		frame.Args[1] = 2.25
		if status := code.Call(frame); status != 0 {
			t.Fatalf("iter %d: status = %d", i, status)
		}
		want := math.Float64bits(frame.Args[0] + frame.Args[1])
		if got := math.Float64bits(frame.Result); got != want {
			t.Fatalf("iter %d: bit-exact mismatch: result=%016x want=%016x", i, got, want)
		}
	}
	wg.Wait()
}

// TestNativeJITAccountingReturnsToBaselineUnderGC closes published code while
// a GC worker runs, then deadline-polls until LiveExecutableMemory() returns
// to the pre-test snapshot. Double-closing a Code must not corrupt the
// counters (Close is accounting-idempotent by design).
func TestNativeJITAccountingReturnsToBaselineUnderGC(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	const n = 16
	codes := publishStressKernels(t, n)

	var wg sync.WaitGroup
	wg.Add(1)
	go gcWorker(&wg, 60)
	for i := 0; i < n; i++ {
		if i%3 == 0 {
			runtime.Gosched()
		}
		if err := codes[i].Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
	wg.Wait()
	requireExecutableBaseline(t, baseRegions, baseBytes)

	if err := codes[0].Close(); err != nil {
		t.Fatal(err)
	}
	requireExecutableBaseline(t, baseRegions, baseBytes)
}
