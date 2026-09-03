//go:build amd64 && (windows || linux)

package native

// R2-2 W^X deep verification, shared platform-independent portion.
//
// Every test in this suite proves the W^X invariant through the operating
// system's own memory-protection metadata rather than through the package's
// internal bookkeeping:
//
//   - Windows: VirtualQuery on the published entry, plus a full walk of the
//     process virtual address space (execmem_wx_windows_test.go).
//   - Linux: /proc/self/maps parsing of the current process (execmem_wx_linux_test.go).
//
// The platform files implement wxRegionIsRX / wxRegionGone / wxNoRWXPages;
// this file contains the lifecycle tests that use them, the multi-page
// machine-code builder and the bit-exact execution verification (NaN sentinel
// plus math.Float64bits, so a call that did not actually run the generated
// code fails loudly).
//
// Coverage matrix (R2-2):
//   - multi-page code: > 1 page of real machine code (NOP sled + kernel),
//     executed through the sled (TestExecMemWXMultiPageKernelExecutes).
//   - multiple coexisting regions, each independently RX and executable.
//   - failed publish (invalid/empty input) must not allocate or move
//     accounting. Note: Publish has no budget-limit parameter, so the only
//     failure vector reachable through the public API is invalid input;
//     there is no allocator budget to exhaust.
//   - double Close is idempotent; after Close the region is gone from the
//     OS protection metadata (mapping invalidation) and Code is inert.
//   - RWX scan before publishing, while several regions are live, and after
//     all regions are closed: no page in the process may be both writable
//     and executable at any point.
//   - LiveExecutableMemory() must return to the pre-test baseline after every
//     test (regions and bytes), verified by requireExecutableBaseline.
//
// Production code was not modified for these tests; no hooks were needed.

import (
	"math"
	"testing"
)

// paddedKernel returns total bytes of machine code: a NOP (0x90) sled followed
// by AddF64Kernel. Executing from the start runs the sled and then the kernel,
// so the whole multi-page region is real, executed machine code, not padding
// that is skipped. The kernel itself performs Frame.Args[0]+Frame.Args[1]
// (IEEE-754 add, bit-exact versus Go's a+b).
func paddedKernel(total int) []byte {
	kernel := AddF64Kernel()
	if total < len(kernel) {
		total = len(kernel)
	}
	code := make([]byte, total)
	for i := 0; i < total-len(kernel); i++ {
		code[i] = 0x90 // NOP
	}
	copy(code[total-len(kernel):], kernel)
	return code
}

// requireKernelSumBitExact executes the published code with the given operands
// and requires status 0 plus a bit-exact Frame.Result. The NaN sentinel proves
// the machine code actually ran (a stale or unwritten result fails).
func requireKernelSumBitExact(t *testing.T, code *Code, a, b float64) {
	t.Helper()
	frame := &Frame{}
	frame.Result = math.NaN()
	frame.Args[0], frame.Args[1] = a, b
	if status := code.Call(frame); status != 0 {
		t.Fatalf("Call status = %d (operands %v + %v)", status, a, b)
	}
	if got, want := math.Float64bits(frame.Result), math.Float64bits(a+b); got != want {
		t.Fatalf("bit-exact mismatch: got %016x want %016x (%v + %v)", got, want, a, b)
	}
}

func requireRegionRX(t *testing.T, entry uintptr) {
	t.Helper()
	if err := wxRegionIsRX(entry); err != nil {
		t.Fatal(err)
	}
}

func requireRegionGone(t *testing.T, entry uintptr) {
	t.Helper()
	gone, err := wxRegionGone(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !gone {
		t.Fatalf("region at %#x is still visible in OS protection metadata after Close", entry)
	}
}

func requireNoRWXPages(t *testing.T) {
	t.Helper()
	if err := wxNoRWXPages(); err != nil {
		t.Fatal(err)
	}
}

// TestExecMemWXMultiPageKernelExecutes publishes more than one page of real
// machine code, verifies the OS reports it as RX (readable + executable, not
// writable), verifies no RWX page exists anywhere in the process, executes the
// code through the sled with bit-exact results, and closes back to baseline.
func TestExecMemWXMultiPageKernelExecutes(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(paddedKernel(3*4096 + 64))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	if size := code.Size(); size < 2*4096 {
		t.Fatalf("published code size = %d, want more than one page", size)
	}
	requireRegionRX(t, code.Entry())
	requireNoRWXPages(t)

	// Operands chosen to be exactly representable so the IEEE-754 add is
	// bit-identical between Go and the generated code.
	requireKernelSumBitExact(t, code, 1.25, 2.5)
	requireKernelSumBitExact(t, code, -0.5, 0.125)
	requireKernelSumBitExact(t, code, math.MaxFloat64, -math.MaxFloat64)
}

// TestExecMemWXMultipleCoexistingRegions keeps several published regions alive
// at the same time: each must be RX in OS metadata, each must execute with
// bit-exact results, accounting must count all of them, and no RWX page may
// appear anywhere in the process while they are live.
func TestExecMemWXMultipleCoexistingRegions(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	const n = 3
	codes := make([]*Code, n)
	for i := range codes {
		c, err := Publish(paddedKernel(4096 + 32*(i+1)))
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		codes[i] = c
	}
	defer func() {
		for _, c := range codes {
			_ = c.Close()
		}
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions+n || bytes < baseBytes {
		t.Fatalf("live accounting = (%d regions, %d bytes), want regions = %d", regions, bytes, baseRegions+n)
	}
	for i, c := range codes {
		requireRegionRX(t, c.Entry())
		requireKernelSumBitExact(t, c, float64(i), 0.25)
	}
	requireNoRWXPages(t)
}

// TestExecMemWXFailedPublishInvalidInput proves rejected publishes never
// allocate executable memory and never move the accounting counters. The only
// failure vector the public API exposes is invalid input (nil/empty code):
// Publish has no budget/limit parameter.
func TestExecMemWXFailedPublishInvalidInput(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	for _, input := range [][]byte{nil, {}, []byte("")} {
		code, err := Publish(input)
		if err == nil {
			if code != nil {
				_ = code.Close()
			}
			t.Fatalf("Publish(%v) succeeded, want error", input)
		}
	}
	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("failed Publish changed accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
	requireNoRWXPages(t)
}

// TestExecMemWXDoubleCloseAndMappingInvalidation proves Close is idempotent,
// that after Close the region disappears from the OS protection metadata
// (mapping invalidation), that a closed Code is inert (Entry 0, Call refused),
// and that accounting returns to baseline.
func TestExecMemWXDoubleCloseAndMappingInvalidation(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	entry := code.Entry()
	requireRegionRX(t, entry)
	requireKernelSumBitExact(t, code, 1, 2)

	if err := code.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	requireRegionGone(t, entry)
	requireNoRWXPages(t)

	// Double close must be a no-op (accounting is idempotent by design).
	if err := code.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	requireRegionGone(t, entry)
	requireNoRWXPages(t)

	if code.Entry() != 0 {
		t.Fatalf("Entry() = %#x after Close, want 0", code.Entry())
	}
	if status := code.Call(&Frame{}); status == 0 {
		t.Fatal("Call on closed Code returned status 0; closed code must not execute")
	}
	requireExecutableBaseline(t, baseRegions, baseBytes)
}

// TestExecMemWXNoRWXPagesAcrossLifecycle scans the whole process for pages
// that are simultaneously writable and executable before publishing, while a
// mix of single-page and multi-page regions is live, and after everything is
// closed. At no point may a RWX page exist.
func TestExecMemWXNoRWXPagesAcrossLifecycle(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	requireNoRWXPages(t) // baseline: nothing published yet

	sizes := []int{64, 4096 + 8, 2*4096 + 17, 3*4096 + 255}
	codes := make([]*Code, 0, len(sizes))
	for _, size := range sizes {
		c, err := Publish(paddedKernel(size))
		if err != nil {
			t.Fatalf("publish size %d: %v", size, err)
		}
		codes = append(codes, c)
		requireRegionRX(t, c.Entry())
	}
	defer func() {
		for _, c := range codes {
			_ = c.Close()
		}
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	requireNoRWXPages(t) // several regions live, none may be RWX
	for _, c := range codes {
		requireKernelSumBitExact(t, c, 0.5, 0.5)
	}
	for _, c := range codes {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	requireNoRWXPages(t) // everything released, no RWX residue
	requireExecutableBaseline(t, baseRegions, baseBytes)
}
