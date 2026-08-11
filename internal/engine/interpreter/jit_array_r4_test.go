package interpreter

// R4-5 / R4-6 array fast path evidence suite (§9.3 六类证据):
//
//	1. positive hit: stats prove the Quick trace tier entered the target
//	   arrayIndex / arrayBatch path;
//	2. negative: holes, sparse arrays, prototype indexes, Proxy receivers,
//	   mixed-type elements and unsafe numbers never execute the fast path;
//	3. meltdown: consecutive guard failures disable the specialized trace and
//	   Tier 0 resumes stably;
//	4. safepoint: an embedding cancellation keeps the committed chunk prefix
//	   (invariant: sum == i*(i-1)/2, length == last element + 1);
//	5. verify: Auto+Verify runs of the same shapes report successful checks
//	   with zero failures (the verify-mismatch recovery mechanism itself is
//	   covered by jit.TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch);
//	6. benchmarks: bench/matrix_test.go BenchmarkArrayIndexRead /
//	   BenchmarkArrayBatchWrite / BenchmarkArrayCbNumeric (专项).
//
// All array paths are Quick-only by design: the machine-code ABI does not
// receive Go array pointers, so the guard and commit stay on the Go side.

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

const arrayIndexReadSource = `
	function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
`

func arrayIndexReadVM(t *testing.T, mode jit.Mode) *VM {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vm.Close() })
	vm.ConfigureJIT(jit.Config{
		Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	return vm
}

// 1. positive: the packed Number read loop must enter the arrayIndex trace.
func TestJITArrayIndexReadTraceRange(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm := arrayIndexReadVM(t, mode)
			_, err := vm.Eval(`
				function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
				const array = [10, 20, 30, 40, 50, 60, 70, 80];
				globalThis.jitIndexSum = readSum(array, 8);
				globalThis.jitIndexLength = array.length;
			`, "jit-array-index-read.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitIndexSum":    "360",
				"jitIndexLength": "8",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.ArrayIndexSites != 1 || stats.TracesExecuted == 0 || stats.TracesCompiled == 0 {
				t.Fatalf("packed Number read loop did not enter the arrayIndex trace: %+v", stats)
			}
		})
	}
}

// 2. negative: hole / sparse / prototype-index / mixed-type / Proxy receivers
// must never execute the packed read path; results stay identical to Tier 0.
func TestJITArrayIndexTraceRejectsUnsafeShapes(t *testing.T) {
	vm := arrayIndexReadVM(t, jit.Quick)
	_, err := vm.Eval(`
		function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
		// hole: undefined element in the packed range. Note: this engine's
		// Tier 0 binAdd treats number+undefined as number+0 (documented
		// deviation from ES; the trace must match it by falling back).
		const holey = [1, 2, undefined, 4, 5, 6];
		globalThis.jitIndexHole = readSum(holey, 6);
		// sparse: new Array(6) leaves undefined holes.
		const sparse = new Array(6);
		sparse[1] = 10; sparse[3] = 30;
		globalThis.jitIndexSparse = readSum(sparse, 6);
		// out-of-range index: bound beyond the element storage. Tier 0
		// resolves these through the objectValue chain (undefined here), and
		// the trace must hand the tail back instead of exiting the loop early.
		const protoArray = [1, 2, 3, 4];
		Array.prototype[5] = 50;
		globalThis.jitIndexProto = readSum(protoArray, 7);
		// mixed types: a string element flips the accumulation to concat.
		const mixed = [1, 2, "x", 4, 5, 6];
		globalThis.jitIndexMixed = readSum(mixed, 6);
		// Proxy receiver never matches the ArrayValue guard.
		const proxy = new Proxy([1, 2, 3, 4], {});
		globalThis.jitIndexProxy = readSum(proxy, 4);
		// negative index start falls back (own property path in Tier 0).
		const neg = [1, 2, 3];
		globalThis.jitIndexNeg = readSum(neg, 1);
		globalThis.jitIndexNegKey = neg[-1];
		neg[-1] = 7;
		globalThis.jitIndexNegKey2 = neg[-1];
	`, "jit-array-index-negative.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitIndexHole":    "NaN",
		"jitIndexSparse":  "NaN",
		"jitIndexProto":   "NaN",
		"jitIndexMixed":   "3x456",
		"jitIndexProxy":   "10",
		"jitIndexNeg":     "1",
		"jitIndexNegKey":  "undefined",
		"jitIndexNegKey2": "7",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	// The holey/mixed/proto/neg shapes may still create a site (the matcher
	// only checks receiver + loop numbers), but the element or length guard
	// must fail before any bulk read commits.
	if stats.ArrayIndexSites == 0 || stats.GuardFailures == 0 {
		t.Fatalf("negative array shapes did not exercise the read guards: %+v", stats)
	}
	// A Proxy receiver never enters the specialization at all.
	proxyVM := arrayIndexReadVM(t, jit.Auto)
	if _, err := proxyVM.Eval(`
		function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
		const proxy = new Proxy([1, 2, 3, 4, 5, 6, 7, 8], {});
		for (let i = 0; i < 4; i++) globalThis.jitProxyRead = readSum(proxy, 8);
	`, "jit-array-index-proxy.js"); err != nil {
		t.Fatal(err)
	}
	if stats := proxyVM.JITStats(); stats.ArrayIndexSites != 0 {
		t.Fatalf("proxy receiver entered the arrayIndex specialization: %+v", stats)
	}
}

// 3. meltdown: after the element type changes, repeated guard failures disable
// the specialized trace; Tier 0 keeps producing the exact results.
func TestJITArrayIndexTraceMeltdownOnTypeChange(t *testing.T) {
	vm := arrayIndexReadVM(t, jit.Quick)
	_, err := vm.Eval(`
		function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
		const array = [1, 2, 3, 4, 5, 6, 7, 8];
		globalThis.jitIndexMelt1 = readSum(array, 8);
		array[2] = "str";
		globalThis.jitIndexMelt2 = readSum(array, 8);
		globalThis.jitIndexMelt3 = readSum(array, 8);
		array[2] = 3;
		globalThis.jitIndexMelt4 = readSum(array, 8);
	`, "jit-array-index-meltdown.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitIndexMelt1": "36",
		"jitIndexMelt2": "3str45678",
		"jitIndexMelt3": "3str45678",
		"jitIndexMelt4": "36",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.ArrayIndexSites != 1 || stats.GuardFailures == 0 || stats.TraceGuardDisabled == 0 {
		t.Fatalf("type change did not melt the arrayIndex trace down to Tier 0: %+v", stats)
	}
}

// 4. safepoint: an embedding cancellation interrupts a packed read loop; the
// committed prefix stays exact (s == i*(i-1)/2) in every tier.
func TestJITArrayIndexTraceSafepointKeepsCompletedPrefix(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Stats: true,
				Safepoint: func() error {
					polls++
					if polls == 2 {
						return errors.New("stop array index")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				const array = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99];
				let s = 0;
				try {
					for (let i = 0; i < 100; i++) s += array[i];
				} catch (error) {
					globalThis.jitIndexInterrupt = error.message;
				}
				globalThis.jitIndexPartialSum = s;
			`, "jit-array-index-interrupt.js")
			if err != nil {
				t.Fatal(err)
			}
			message, _ := vm.Global().Get("jitIndexInterrupt")
			sumValue, _ := vm.Global().Get("jitIndexPartialSum")
			sum, ok := sumValue.Float()
			if message.String() != "stop array index" || !ok || math.IsNaN(sum) {
				t.Fatalf("message=%v sum=%v", message, sumValue)
			}
			// Every committed iteration added exactly its own index once, so
			// the partial sum is k*(k-1)/2 for the committed count k.
			k := 0
			for k < 100 && float64(k)*(float64(k)-1)/2 != sum {
				k++
			}
			if k == 0 || k >= 100 {
				t.Fatalf("partial sum %v is not a committed 0..k-1 prefix", sum)
			}
			stats := vm.JITStats()
			if stats.ArrayIndexSites != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 {
				t.Fatalf("unexpected interrupted array index stats: %+v", stats)
			}
		})
	}
}

// 5. verify: Auto+Verify runs the array shapes beside verified native traces;
// the verify machinery must not be disturbed and reports zero failures.
func TestJITArrayIndexBatchVerifyCoexists(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
		TraceBudget: 3, Verify: true, Stats: true,
	})
	_, err = vm.Eval(`
		function readSum(array, n) { let s = 0; for (let i = 0; i < n; i++) s += array[i]; return s; }
		function writeBatch(array, n) { for (let i = 0; i < n; i++) array[i] = i; return array.length; }
		function propLoop(o, n) { for (let i = 0; i < n; i++) o.a = i; return o.a; }
		const array = [1, 2, 3, 4, 5, 6, 7, 8];
		const out = [];
		globalThis.jitVerifySum = readSum(array, 8);
		globalThis.jitVerifyLen = writeBatch(out, 8);
		const o = { a: 0 };
		globalThis.jitVerifyProp = propLoop(o, 8);
	`, "jit-array-verify.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitVerifySum":  "36",
		"jitVerifyLen":  "8",
		"jitVerifyProp": "7",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.VerifyChecks == 0 || stats.VerifyFailures != 0 || stats.ArrayIndexSites == 0 || stats.ArrayBatchSites == 0 {
		t.Fatalf("verify coexistence failed: %+v", stats)
	}
}

// 1. positive: all three canonical batch write forms enter the arrayBatch
// trace (W1 a[i]=i, W2 a[j]=i; j++, W3 a[j++]=i) and sync length once.
func TestJITArrayBatchWriteTraceRange(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Stats: true,
			})
			_, err = vm.Eval(`
				function writeA(array, n) { for (let i = 0; i < n; i++) array[i] = i; return array.length; }
				function writeJ(array, n) { let j = 2; for (let i = 0; i < n; i++) { array[j] = i; j++; } return array.length; }
				function writeJPost(array, n) { let j = 1; for (let i = 0; i < n; i++) array[j++] = i; return array.length; }
				const A = [];
				globalThis.jitBatchLenA = writeA(A, 20);
				globalThis.jitBatchFirstA = A[0];
				globalThis.jitBatchLastA = A[19];
				const B = [9, 9];
				globalThis.jitBatchLenB = writeJ(B, 6);
				globalThis.jitBatchKeyB = B[2] + ":" + B[7];
				globalThis.jitBatchHoleB = B[0];
				const C = [];
				globalThis.jitBatchLenC = writeJPost(C, 5);
				globalThis.jitBatchKeyC = C[1] + ":" + C[5];
			`, "jit-array-batch-write.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitBatchLenA":   "20",
				"jitBatchFirstA": "0",
				"jitBatchLastA":  "19",
				"jitBatchLenB":   "8",
				"jitBatchKeyB":   "0:5",
				"jitBatchHoleB":  "9",
				"jitBatchLenC":   "6",
				"jitBatchKeyC":   "0:4",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.ArrayBatchSites != 3 || stats.TracesExecuted == 0 {
				t.Fatalf("batch write loops did not enter the arrayBatch trace: %+v", stats)
			}
		})
	}
}

// 2. negative: unsafe keys/values and Proxy receivers never batch-write; the
// writes land through Tier 0 (own properties for fractional/negative keys).
func TestJITArrayBatchWriteTraceRejectsUnsafeShapes(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Quick, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		function writeA(array, n) { for (let i = 0; i < n; i++) array[i] = i; return array.length; }
		// fractional key start: array["0.5"] own property path in Tier 0.
		function writeFrac(array, n) { let j = 0.5; for (let i = 0; i < n; i++) { array[j] = i; j++; } return array.length; }
		// negative key start: array["-1"] own property path in Tier 0.
		function writeNeg(array, n) { let j = -1; for (let i = 0; i < n; i++) { array[j] = i; j++; } return array.length; }
		const F = [];
		globalThis.jitBatchFrac = writeFrac(F, 3);
		globalThis.jitBatchFracKey = F[0.5] + ":" + F[1.5] + ":" + F.length;
		const N = [];
		globalThis.jitBatchNeg = writeNeg(N, 3);
		globalThis.jitBatchNegKey = N[-1] + ":" + N[0] + ":" + N.length;
		const P = new Proxy([], {});
		globalThis.jitBatchProxy = writeA(P, 4);
		globalThis.jitBatchProxyLen = P.length;
	`, "jit-array-batch-negative.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitBatchFrac":     "0",
		"jitBatchFracKey":  "0:1:0",
		"jitBatchNeg":      "2",
		"jitBatchNegKey":   "0:1:2",
		"jitBatchProxy":    "4",
		"jitBatchProxyLen": "4",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	// Fractional keys reject the shape at match time (no site), negative keys
	// start outside the packed range; the value checks above prove the writes
	// landed through Tier 0. Execution-time guard failures are covered by the
	// meltdown test and the unsafe-number unit guards below.
	if stats.ArrayBatchSites > 1 {
		t.Fatalf("unsafe batch shapes created more sites than the single integral-key form: %+v", stats)
	}
	// Proxy receiver must never create a batch site.
	proxyVM, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer proxyVM.Close()
	proxyVM.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	if _, err := proxyVM.Eval(`
		function writeA(array, n) { for (let i = 0; i < n; i++) array[i] = i; return array.length; }
		const proxy = new Proxy([], {});
		for (let k = 0; k < 3; k++) writeA(proxy, 8);
	`, "jit-array-batch-proxy.js"); err != nil {
		t.Fatal(err)
	}
	if stats := proxyVM.JITStats(); stats.ArrayBatchSites != 0 {
		t.Fatalf("proxy receiver entered the arrayBatch specialization: %+v", stats)
	}
}

// unsafe-number unit guards for the batch executor (mirrors the arrayPush
// unsafe-number test).
func TestJITArrayBatchTraceRejectsUnsafeNumbers(t *testing.T) {
	trace := &arrayBatchWriteTraceState{indexLocal: 0, keyLocal: 1, valueLocal: 2, boundLocal: 3, boundIsLocal: true}
	cases := []struct {
		name  string
		index *float64
		key   *float64
		value *float64
		bound *float64
	}{
		{name: "nan index", index: fptr(math.NaN())},
		{name: "inf index", index: fptr(math.Inf(1))},
		{name: "negative index", index: fptr(-1)},
		{name: "fractional index", index: fptr(0.5)},
		{name: "nan bound", bound: fptr(math.NaN())},
		{name: "inf bound", bound: fptr(math.Inf(1))},
		{name: "negative bound", bound: fptr(-1)},
		{name: "fractional bound", bound: fptr(1.5)},
		{name: "nan key", key: fptr(math.NaN())},
		{name: "negative key", key: fptr(-1)},
		{name: "fractional key", key: fptr(2.5)},
		{name: "huge key", key: fptr(1 << 53)},
		{name: "nan value", value: fptr(math.NaN())},
		{name: "inf value", value: fptr(math.Inf(1))},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			index, key, value, bound := 1.0, 1.0, 1.0, 10.0
			if tt.index != nil {
				index = *tt.index
			}
			if tt.key != nil {
				key = *tt.key
			}
			if tt.value != nil {
				value = *tt.value
			}
			if tt.bound != nil {
				bound = *tt.bound
			}
			locals := []engine.Value{
				engine.Number(index), engine.Number(key),
				engine.Number(value), engine.Number(bound),
			}
			if _, _, _, _, ok := trace.arrayBatchNumbers(locals); ok {
				t.Fatalf("unsafe batch range accepted: index=%v key=%v value=%v bound=%v", index, key, value, bound)
			}
		})
	}
}

func fptr(v float64) *float64 { return &v }

// 3. meltdown: a receiver swap to a non-array disables the batch trace after
// two guard failures; Tier 0 writes keep working.
func TestJITArrayBatchWriteTraceMeltdownOnReceiverChange(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Quick, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		function writeA(array, n) { for (let i = 0; i < n; i++) array[i] = i; return array.length; }
		const A = [];
		globalThis.jitBatchMelt1 = writeA(A, 8);
		const fake = { 0: 0, length: 1 };
		globalThis.jitBatchMelt2 = writeA(fake, 3);
		globalThis.jitBatchMelt3 = writeA(fake, 3);
		globalThis.jitBatchMelt4 = writeA(A, 3);
	`, "jit-array-batch-meltdown.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitBatchMelt1": "8",
		// fake is a plain object: Tier 0 never syncs its length (no
		// ArrayValue), so it stays 1 across both fallback calls.
		"jitBatchMelt2": "1",
		"jitBatchMelt3": "1",
		// A already has 8 elements; writing 0..2 does not extend it.
		"jitBatchMelt4": "8",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.ArrayBatchSites != 1 || stats.GuardFailures == 0 || stats.TraceGuardDisabled == 0 {
		t.Fatalf("receiver change did not melt the arrayBatch trace down: %+v", stats)
	}
}

// 4. safepoint: an embedding cancellation interrupts a batch write loop; the
// committed prefix stays exact (length === last element + 1).
func TestJITArrayBatchWriteTraceSafepointKeepsCompletedPrefix(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Stats: true,
				Safepoint: func() error {
					polls++
					if polls == 2 {
						return errors.New("stop array batch")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				const array = [];
				try {
					for (let i = 0; i < 100; i++) array[i] = i;
				} catch (error) {
					globalThis.jitBatchInterrupt = error.message;
				}
				globalThis.jitBatchPartialLength = array.length;
				globalThis.jitBatchPartialLast = array[array.length - 1];
			`, "jit-array-batch-interrupt.js")
			if err != nil {
				t.Fatal(err)
			}
			message, _ := vm.Global().Get("jitBatchInterrupt")
			lengthValue, _ := vm.Global().Get("jitBatchPartialLength")
			lastValue, _ := vm.Global().Get("jitBatchPartialLast")
			length, ok := lengthValue.Int()
			if message.String() != "stop array batch" || !ok || length <= 0 || length >= 100 || lastValue.String() != strconv.Itoa(length-1) {
				t.Fatalf("message=%v length=%v last=%v", message, lengthValue, lastValue)
			}
			stats := vm.JITStats()
			if stats.ArrayBatchSites != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 {
				t.Fatalf("unexpected interrupted array batch stats: %+v", stats)
			}
		})
	}
}

// === R4-6: map/filter/reduce compiler/guard-proven purity paths ============
//
// The callback purity is proven by the compiler (NativeCallback is only
// emitted for closure-free single-expression arrows), and the runtime guards
// prove the exact pattern plus the all-Number inputs; any failure falls back
// to the full per-element call chain with unchanged JS semantics.

const arrayCbSource = `
	function cbKernel(array) {
		let out = [];
		out.push(array.map(x => x + 1).join(","));
		out.push(array.map(x => 3 * x).join(","));
		out.push(array.map(x => x / 2).join(","));
		out.push(array.map(x => x ** 2).join(","));
		out.push(array.map(x => x).join(","));
		out.push(array.filter(x => x % 2 !== 0).join(","));
		out.push(array.filter(x => x < 3).join(","));
		out.push(array.filter(x => x % 2 === 0).join(","));
		out.push(array.reduce((acc, x) => acc + x, 0));
		out.push(array.reduce((acc, x) => acc - x, 0));
		out.push(array.reduce((acc, x) => acc * 2, 1));
		out.push(array.reduce((acc, x) => x + acc, 0));
		out.push(array.reduce((acc, x) => acc / 10, 100));
		return out.join("|");
	}
	const A = [1, 2, 3, 4, 5, 6];
	globalThis.jitCbPure = cbKernel(A);
`

func runCbVM(t *testing.T, mode jit.Mode) (*VM, string) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vm.Close() })
	vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 3, Stats: true})
	if _, err := vm.Eval(arrayCbSource, "jit-array-cb.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("jitCbPure")
	if err != nil {
		t.Fatal(err)
	}
	return vm, value.String()
}

// 1. positive: every extended purity pattern executes in the Go fast path
// with results identical to Tier 0 in every tier. The O-6 NativeCallback path
// is a Tier 0 interpreter optimization (mode-independent), so the hit counter
// proves the compiler/guard proof fired in every tier while falls stay zero.
func TestJITNumericCallbackPurityDifferential(t *testing.T) {
	results := make(map[jit.Mode]string)
	stats := make(map[jit.Mode]jit.Stats)
	for _, mode := range []jit.Mode{jit.Off, jit.Quick, jit.Auto} {
		vm, got := runCbVM(t, mode)
		results[mode] = got
		stats[mode] = vm.JITStats()
	}
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		if results[mode] != results[jit.Off] {
			t.Fatalf("tier %s result differs from Tier 0:\noff=%q\n%s=%q", mode, results[jit.Off], mode, results[mode])
		}
	}
	for _, mode := range []jit.Mode{jit.Off, jit.Quick, jit.Auto} {
		if stats[mode].NumericCallbackHits == 0 || stats[mode].NumericCallbackFalls != 0 {
			t.Fatalf("tier %s purity path counts: hits=%d falls=%d (want hits>0, falls==0)", mode,
				stats[mode].NumericCallbackHits, stats[mode].NumericCallbackFalls)
		}
	}
}

// 2. negative: mixed types, holes, string-constant callbacks, impure
// callbacks and Proxy receivers all fall back to the full call chain with
// identical observable results.
func TestJITNumericCallbackRejectsImpureShapes(t *testing.T) {
	source := `
		function sideEffect(x) { return x * 2; }
		let side = 0;
		const mixed = [1, "x", 3];
		const holey = [1, 2, undefined, 4];
		const strCb = [1, 2, 3];
		const impure = [1, 2, 3];
		const proxy = new Proxy([1, 2, 3], {});
		const nonArrow = [1, 2, 3];
		const nonArrowFn = function(x) { return x * 2; };
		globalThis.jitCbMixed = mixed.map(x => x * 2).join(",");
		globalThis.jitCbHole = holey.map(x => x * 2).join(",");
		globalThis.jitCbStr = strCb.map(x => x + "s").join(",");
		globalThis.jitCbImpure = impure.map(function(x) { side += x; return x * 2; }).join(",");
		globalThis.jitCbSide = side;
		globalThis.jitCbProxy = proxy.map(x => x * 2).join(",");
		globalThis.jitCbNonArrow = nonArrow.map(nonArrowFn).join(",");
		globalThis.jitCbFilterMixed = mixed.filter(x => x % 2 === 0).join(",");
		globalThis.jitCbReduceMixed = mixed.reduce((acc, x) => acc + x, 0);
		globalThis.jitCbReduceStr = strCb.reduce((acc, x) => acc + x, "s");
	`
	run := func(mode jit.Mode) (string, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Stats: true})
		if _, err := vm.Eval(source, "jit-array-cb-negative.js"); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		for _, name := range []string{
			"jitCbMixed", "jitCbHole", "jitCbStr", "jitCbImpure", "jitCbSide",
			"jitCbProxy", "jitCbNonArrow", "jitCbFilterMixed", "jitCbReduceMixed", "jitCbReduceStr",
		} {
			value, err := vm.Global().Get(name)
			if err != nil {
				t.Fatal(err)
			}
			out.WriteString(value.String())
			out.WriteByte('|')
		}
		return out.String(), vm.JITStats()
	}
	off, offStats := run(jit.Off)
	if offStats.NumericCallbackHits != 0 {
		t.Fatalf("off must not hit the numeric callback path: %+v", offStats)
	}
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		got, stats := run(mode)
		if got != off {
			t.Fatalf("tier %s negative results differ from Tier 0:\noff=%q\n%s=%q", mode, off, mode, got)
		}
		if stats.NumericCallbackHits != 0 || stats.NumericCallbackFalls == 0 {
			t.Fatalf("tier %s must fall back for every impure shape: %+v", mode, stats)
		}
	}
}

// 3. meltdown: the element type flipping between calls switches the path per
// call (each invocation re-proves inputs, so the mixed call falls back and
// the numeric call hits again).
func TestJITNumericCallbackMeltdownOnTypeChange(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		const arr = [1, 2, 3, 4, 5, 6];
		globalThis.jitCbMelt1 = arr.map(x => x * 2).join(",");
		arr[2] = "str";
		globalThis.jitCbMelt2 = arr.map(x => x * 2).join(",");
		arr[2] = 3;
		globalThis.jitCbMelt3 = arr.map(x => x * 2).join(",");
	`, "jit-array-cb-meltdown.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitCbMelt1": "2,4,6,8,10,12",
		// "str" * 2 coerces to 0 in this engine's Tier 0 (Float() ok flag is
		// ignored; documented deviation from ES, which yields NaN). The fast
		// path falls back, so the result must match Tier 0 exactly.
		"jitCbMelt2": "2,4,0,8,10,12",
		"jitCbMelt3": "2,4,6,8,10,12",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NumericCallbackHits != 2 || stats.NumericCallbackFalls != 1 {
		t.Fatalf("type change did not flip the purity path per call: %+v", stats)
	}
}

// Regression: the loop bound must not alias a local the loop body or tail
// writes (sum for reads, key for batch writes). Such shapes must never match
// the matchers — the chunked range would diverge from Tier 0's per-iteration
// semantics.
func TestJITArrayTraceRejectsBoundAliasing(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: ^uint32(0), BackedgeThreshold: 1, TraceBudget: 3, Stats: true})
	_, err = vm.Eval(`
		// bound aliases the sum: 0 < 0 is false, the loop never runs; but the
		// shape must not be specialized either.
		function readAlias(a) { let s = 0; for (let i = 0; i < s; i++) s += a[i]; return s; }
		globalThis.jitAliasRead = readAlias([1, 2, 3]);
		// bound aliases the key counter: j starts 0, the loop never runs; the
		// shape must not be specialized (with j > 0 it would diverge).
		function writeAlias(a) { let j = 0; for (let i = 0; i < j; i++) { a[j] = i; j++; } return a.length; }
		globalThis.jitAliasWrite = writeAlias([1, 2, 3]);
	`, "jit-array-alias.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitAliasRead": "0", "jitAliasWrite": "3"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.ArrayIndexSites != 0 || stats.ArrayBatchSites != 0 {
		t.Fatalf("bound-aliasing shapes entered a specialization: %+v", stats)
	}
}
