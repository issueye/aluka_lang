package interpreter

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

func logJITBytecode(t *testing.T, functions []*bytecode.FuncTemplate) {
	t.Helper()
	for functionIndex, tmpl := range functions {
		var dump strings.Builder
		fmt.Fprintf(&dump, "function[%d]=%q locals=%d upvalues=%d\n", functionIndex, tmpl.Name, tmpl.NumLocals, len(tmpl.Upvalues))
		for pc := 0; pc < len(tmpl.Code); pc += bytecode.InstrSize {
			op := bytecode.Opcode(tmpl.Code[pc])
			arg := uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3])
			fmt.Fprintf(&dump, "%04d %s %d\n", pc, op, arg)
		}
		t.Log(dump.String())
	}
}

func TestQuickJITCompilesHotNumericLeaf(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 2, Stats: true})
	_, err = vm.Eval(`
		function f(a, b) { return a * b + 2; }
		let r = 0;
		for (let i = 0; i < 5; i++) r = f(i, 4);
		globalThis.jitResult = r;
	`, "jit.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitResult")
	if err != nil || got.String() != "18" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.Compiled != 1 || stats.Executed == 0 || stats.Rejected != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestJITBitwiseCrossTierDifferential(t *testing.T) {
	code := `
		function bitKernel(a, b, n) {
			const traceOnlyMarker = {};
			let x = a;
			for (let i = 0; i < n; i++) {
				x = (~(x ^ b)) + ((x << (i & 7)) | (x >>> (i & 7)));
			}
			return x;
		}
		globalThis.bitResult = bitKernel(2147483648, 305419896, 37);
		globalThis.bitResultNaN = bitKernel(NaN, 7, 13);
		globalThis.bitResultNegZero = bitKernel(-0, 0, 9);
	`
	results := make(map[jit.Mode]map[string]string)
	for _, mode := range []jit.Mode{jit.Off, jit.Quick, jit.Auto} {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 8, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(code, "jit-bitwise.js"); err != nil {
			t.Fatalf("mode=%s: %v", mode, err)
		}
		results[mode] = make(map[string]string)
		for _, name := range []string{"bitResult", "bitResultNaN", "bitResultNegZero"} {
			got, err := vm.Global().Get(name)
			if err != nil {
				t.Fatalf("mode=%s get %s: %v", mode, name, err)
			}
			results[mode][name] = got.String()
		}
		if mode != jit.Off {
			stats := vm.JITStats()
			if stats.Compiled == 0 && stats.TracesCompiled == 0 {
				t.Fatalf("mode=%s did not compile a Quick/trace program: %+v", mode, stats)
			}
		}
	}
	for _, name := range []string{"bitResult", "bitResultNaN", "bitResultNegZero"} {
		if results[jit.Quick][name] != results[jit.Off][name] || results[jit.Auto][name] != results[jit.Off][name] {
			t.Fatalf("bitwise tier mismatch for %s: off=%s quick=%s auto=%s", name,
				results[jit.Off][name], results[jit.Quick][name], results[jit.Auto][name])
		}
	}
}

func TestJITExtendedNumericSyntaxCrossTierDifferential(t *testing.T) {
	source := `
		function pow(a, b) { return a ** b; }
		function not(a) { return !a; }
		function plus(a) { return +a; }
		function trace(a, n) {
			const traceOnlyMarker = {};
			let x = a;
			for (let i = 0; i < n; i++) {
				x = +x;
				x = x ** 1;
				if (!x) x = 2;
			}
			return x;
		}
		globalThis.extPow = pow(2, 10);
		globalThis.extPowNegZero = pow(-0, 3);
		globalThis.extNotNaN = not(NaN);
		globalThis.extNotZero = not(0);
		globalThis.extNotOne = not(1);
		globalThis.extPlusNegZero = plus(-0);
		globalThis.extTraceZero = trace(0, 12);
		globalThis.extTraceValue = trace(3, 12);
	`
	names := []string{
		"extPow", "extPowNegZero", "extNotNaN", "extNotZero", "extNotOne",
		"extPlusNegZero", "extTraceZero", "extTraceValue",
	}
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: 1, BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-extended-numeric.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, len(names))
		for i, name := range names {
			value, err := vm.Global().Get(name)
			if err != nil {
				t.Fatalf("mode=%s get %s: %v", mode, name, err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	auto, autoStats := run(jit.Auto)
	if quickStats.Compiled < 3 || quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("extended syntax did not execute Quick function/trace tiers: %+v", quickStats)
	}
	if autoStats.NativeCompiled == 0 || autoStats.NativeExecuted == 0 || autoStats.TracesCompiled == 0 {
		t.Fatalf("unary plus Native path or Auto trace fallback was not exercised: %+v", autoStats)
	}
	for i, name := range names {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], auto[i]) {
			t.Fatalf("%s mismatch: off=%v quick=%v auto=%v", name, baseline[i], quick[i], auto[i])
		}
	}
}

func TestJITLogicalExpressionsGeneratedCrossTierDifferential(t *testing.T) {
	const expressionCount = 40
	rng := rand.New(rand.NewSource(0x10_61CA1_2026))
	expressions := make([]string, expressionCount)
	var source strings.Builder
	for i := range expressions {
		left := randomJITNumberExpression(rng, 3)
		right := randomJITNumberExpression(rng, 3)
		op := "&&"
		if i%2 != 0 {
			op = "||"
		}
		expressions[i] = fmt.Sprintf("(%s %s %s)", left, op, right)
		fmt.Fprintf(&source, "function jitLogical%d(a,b,c){return %s;}\n", i, expressions[i])
	}
	inputs := [][3]string{
		{"0", "2", "3"}, {"-0", "4", "-5"}, {"NaN", "6", "7"},
		{"Infinity", "-Infinity", "2"}, {"1", "0", "-0"}, {"-3.5", "0.25", "8"},
	}
	for expressionIndex := range expressions {
		for inputIndex, input := range inputs {
			fmt.Fprintf(&source, "globalThis.jitLogicalResult%d_%d=jitLogical%d(%s,%s,%s);\n",
				expressionIndex, inputIndex, expressionIndex, input[0], input[1], input[2])
		}
	}

	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(source.String(), "jit-logical-generated-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 0, len(expressions)*len(inputs))
		for expressionIndex := range expressions {
			for inputIndex := range inputs {
				value, err := vm.Global().Get(fmt.Sprintf("jitLogicalResult%d_%d", expressionIndex, inputIndex))
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, value)
			}
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	wantExecutions := uint64(len(expressions) * len(inputs))
	if quickStats.Compiled != expressionCount || quickStats.Executed != wantExecutions || quickStats.GuardFailures != 0 {
		t.Fatalf("generated logical expressions did not stay in Quick: %+v", quickStats)
	}
	if nativeStats.NativeCompiled != expressionCount || nativeStats.NativeExecuted != wantExecutions ||
		nativeStats.VerifyChecks != wantExecutions || nativeStats.VerifyFailures != 0 || nativeStats.GuardFailures != 0 {
		t.Fatalf("generated logical expressions did not stay in Native: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			expressionIndex, inputIndex := i/len(inputs), i%len(inputs)
			t.Fatalf("expression=%q input=%v off=%v quick=%v native=%v",
				expressions[expressionIndex], inputs[inputIndex], baseline[i], quick[i], native[i])
		}
	}
}

func randomJITNumberExpression(rng *rand.Rand, depth int) string {
	atoms := [...]string{"a", "b", "c", "0", "-0", "1", "-2", "0.5"}
	if depth == 0 {
		return atoms[rng.Intn(len(atoms))]
	}
	switch rng.Intn(7) {
	case 0:
		return fmt.Sprintf("-(%s)", randomJITNumberExpression(rng, depth-1))
	case 1:
		return fmt.Sprintf("+(%s)", randomJITNumberExpression(rng, depth-1))
	case 2:
		return fmt.Sprintf("(%s && %s)", randomJITNumberExpression(rng, depth-1), randomJITNumberExpression(rng, depth-1))
	case 3:
		return fmt.Sprintf("(%s || %s)", randomJITNumberExpression(rng, depth-1), randomJITNumberExpression(rng, depth-1))
	case 4:
		return fmt.Sprintf("(%s ?? %s)", randomJITNumberExpression(rng, depth-1), randomJITNumberExpression(rng, depth-1))
	default:
		operators := [...]string{"+", "-", "*", "/"}
		return fmt.Sprintf("(%s %s %s)", randomJITNumberExpression(rng, depth-1), operators[rng.Intn(len(operators))], randomJITNumberExpression(rng, depth-1))
	}
}

func TestJITLogicalShortCircuitTraceCrossTierDifferential(t *testing.T) {
	source := `
		function logicalTrace(a, b, n) {
			const traceOnlyMarker = {};
			let x = a;
			for (let i = 0; i < n; i++) {
				x = (x && b) || (i + 1);
				b = (b || x) && (i - 2);
			}
			return x + b;
		}
		globalThis.jitLogicalTrace0 = logicalTrace(0, 3, 20);
		globalThis.jitLogicalTrace1 = logicalTrace(-0, 0, 17);
		globalThis.jitLogicalTrace2 = logicalTrace(NaN, 2, 15);
		globalThis.jitLogicalTrace3 = logicalTrace(4, -5, 12);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-logical-trace-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 4)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitLogicalTrace%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 || quickStats.GuardFailures != 0 {
		t.Fatalf("logical trace did not exercise Quick: %+v", quickStats)
	}
	if nativeStats.NativeTracesCompiled == 0 || nativeStats.NativeTracesExecuted == 0 ||
		nativeStats.VerifyChecks == 0 || nativeStats.VerifyFailures != 0 || nativeStats.GuardFailures != 0 {
		t.Fatalf("logical trace did not exercise Native: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case=%d off=%v quick=%v native=%v", i, baseline[i], quick[i], native[i])
		}
	}
}

func TestJITNullishCoalescingCrossTierFallback(t *testing.T) {
	source := `
		function coalesce(a, b) { return a ?? b; }
		globalThis.jitNullish0 = coalesce(0, 7);
		globalThis.jitNullish1 = coalesce(-0, 7);
		globalThis.jitNullish2 = coalesce(NaN, 7);
		globalThis.jitNullish3 = coalesce(4, 7);
		globalThis.jitNullish4 = coalesce(null, 7);
		globalThis.jitNullish5 = coalesce(undefined, 8);
		globalThis.jitNullish6 = coalesce(false, 9);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(source, "jit-nullish-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 7)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitNullish%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.Compiled != 1 || quickStats.Executed != 7 || quickStats.GuardFailures != 0 {
		t.Fatalf("nullish cases did not stay in Quick: %+v", quickStats)
	}
	if nativeStats.NativeCompiled != 1 || nativeStats.NativeExecuted != 4 || nativeStats.Executed != 3 ||
		nativeStats.VerifyChecks != 4 || nativeStats.VerifyFailures != 0 || nativeStats.GuardFailures != 2 ||
		nativeStats.NativeGuardDisabled != 1 || nativeStats.NativeCodeBytes != 0 {
		t.Fatalf("nullish Native fallback did not stabilize: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case=%d off=%v quick=%v native=%v", i, baseline[i], quick[i], native[i])
		}
	}
}

func TestJITLogicalReferenceValuesStayInQuick(t *testing.T) {
	source := `
		function andValue(a, b) { return a && b; }
		function orValue(a, b) { return a || b; }
		function nullishValue(a, b) { return a ?? b; }
		function notValue(a) { return !a; }

		globalThis.jitRef0 = andValue("", "rhs");
		globalThis.jitRef1 = andValue("left", "rhs");
		globalThis.jitRef2 = andValue(0n, "rhs");
		globalThis.jitRef3 = andValue(2n, "rhs");
		globalThis.jitRef4 = orValue("", "rhs");
		globalThis.jitRef5 = orValue("left", "rhs");
		globalThis.jitRef6 = orValue(0n, "rhs");
		globalThis.jitRef7 = orValue(2n, "rhs");
		globalThis.jitRef8 = nullishValue("", "rhs");
		globalThis.jitRef9 = nullishValue("left", "rhs");
		globalThis.jitRef10 = nullishValue(0n, "rhs");
		globalThis.jitRef11 = nullishValue(2n, "rhs");
		globalThis.jitRef12 = notValue("");
		globalThis.jitRef13 = notValue("left");
		globalThis.jitRef14 = notValue(0n);
		globalThis.jitRef15 = notValue(2n);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(source, "jit-logical-reference-values.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 16)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitRef%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	auto, autoStats := run(jit.Auto)
	if quickStats.Compiled != 4 || quickStats.Executed != 16 || quickStats.GuardFailures != 0 {
		t.Fatalf("reference values did not stay in Quick: %+v", quickStats)
	}
	if autoStats.NativeCompiled != 3 || autoStats.NativeRejected != 1 || autoStats.NativeExecuted != 0 || autoStats.Executed != 16 ||
		autoStats.GuardFailures != 6 || autoStats.NativeGuardDisabled != 3 || autoStats.NativeCodeBytes != 0 ||
		autoStats.VerifyFailures != 0 {
		t.Fatalf("reference values did not stabilize on Quick fallback: %+v", autoStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], auto[i]) {
			t.Fatalf("case=%d off=%v quick=%v auto=%v", i, baseline[i], quick[i], auto[i])
		}
	}
}

func TestJITNullishCoalescingTraceCrossTierFallback(t *testing.T) {
	source := `
		function nullishTrace(maybe, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += maybe ?? i;
			return total;
		}
		globalThis.jitNullishTrace0 = nullishTrace(2, 20);
		globalThis.jitNullishTrace1 = nullishTrace(null, 18);
		globalThis.jitNullishTrace2 = nullishTrace(undefined, 16);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-nullish-trace-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 3)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitNullishTrace%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 || quickStats.GuardFailures != 0 {
		t.Fatalf("nullish trace did not exercise Quick: %+v", quickStats)
	}
	if nativeStats.NativeTracesCompiled == 0 || nativeStats.NativeTracesExecuted == 0 ||
		nativeStats.TracesExecuted == 0 || nativeStats.NativeTraceGuardDisabled != 1 ||
		nativeStats.NativeCodeBytes != 0 || nativeStats.VerifyFailures != 0 {
		t.Fatalf("nullish trace Native fallback did not stabilize: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case=%d off=%v quick=%v native=%v", i, baseline[i], quick[i], native[i])
		}
	}
}

func TestQuickJITUnaryPlusGuardFallsBackForString(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	if _, err := vm.Eval(`
		function plus(value) { return +value; }
		plus(1);
		globalThis.jitPlusString = plus("7") + plus("8") + plus("9");
	`, "jit-unary-plus-guard.js"); err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitPlusString")
	if err != nil || got.String() != "24" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	if stats := vm.JITStats(); stats.Compiled != 1 || stats.Executed != 1 || stats.GuardFailures != 2 || stats.QuickGuardDisabled != 1 {
		t.Fatalf("unary plus did not guard and fall back: %+v", stats)
	}
}

func TestAutoJITUsesNativeLinearKernel(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add(a, b) { return a * b + 2; }
		globalThis.jitNative = add(3, 4);
	`, "jit-native.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitNative")
	if err != nil || got.String() != "14" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 1 {
		t.Fatalf("native kernel was not used: %+v", stats)
	}
}

func TestAutoJITUsesNativeNumericLoop(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Verify: true, Stats: true})
	_, err = vm.Eval(`
		function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += i;
			return total;
		}
		globalThis.jitNativeLoop = sum(1000);
		globalThis.jitNativeNaNLoop = sum(0 / 0);
	`, "jit-native-loop.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitNativeLoop")
	if err != nil || got.String() != "499500" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	nanLoop, err := vm.Global().Get("jitNativeNaNLoop")
	if err != nil || nanLoop.String() != "0" {
		t.Fatalf("NaN loop result=%v err=%v", nanLoop, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 2 || stats.GuardFailures != 0 || stats.VerifyChecks != 2 || stats.VerifyFailures != 0 {
		t.Fatalf("native loop was not used: %+v", stats)
	}
}

func TestAutoJITNativeLoopYieldsAtSafepoints(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, TraceBudget: 7, Verify: true, Stats: true})
	_, err = vm.Eval(`
		function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += i;
			return total;
		}
		globalThis.jitNativeYield = sum(1000);
	`, "jit-native-yield.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitNativeYield")
	if err != nil || got.String() != "499500" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeExecuted != 1 || stats.NativeYields == 0 || stats.VerifyChecks != 1 || stats.VerifyFailures != 0 {
		t.Fatalf("native loop did not yield and resume safely: %+v", stats)
	}
}

func TestAutoJITBackgroundCompilesLargeProgram(t *testing.T) {
	if runtime.GOARCH != "amd64" || runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native background compilation requires the amd64 Windows/Linux backend")
	}
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	source := fmt.Sprintf(`
		globalThis.largeJIT = function(x) { return %s; };
		globalThis.largeJITFirst = globalThis.largeJIT(1);
	`, expression.String())
	if _, err := vm.Eval(source, "jit-background.js"); err != nil {
		t.Fatal(err)
	}
	first, err := vm.Global().Get("largeJITFirst")
	if err != nil || first.String() != "81" {
		t.Fatalf("first result=%v err=%v", first, err)
	}
	stats := vm.JITStats()
	if stats.BackgroundQueued != 1 || stats.Executed != 1 || stats.NativeExecuted != 0 {
		t.Fatalf("large program was not queued after Quick execution: %+v", stats)
	}
	deadline := time.Now().Add(2 * time.Second)
	for vm.jitPending > 0 && time.Now().Before(deadline) {
		runtime.Gosched()
		vm.pollNativeCompiles()
	}
	stats = vm.JITStats()
	if vm.jitPending != 0 || stats.BackgroundCompleted != 1 || stats.NativeCompiled != 1 || stats.BackgroundDiscarded != 0 {
		t.Fatalf("background native compile did not install: pending=%d stats=%+v", vm.jitPending, stats)
	}
	if _, err := vm.Eval(`globalThis.largeJITSecond = globalThis.largeJIT(2);`, "jit-background-second.js"); err != nil {
		t.Fatal(err)
	}
	second, err := vm.Global().Get("largeJITSecond")
	if err != nil || second.String() != "82" {
		t.Fatalf("second result=%v err=%v", second, err)
	}
	if stats := vm.JITStats(); stats.NativeExecuted != 1 || stats.NativeCodeBytes == 0 {
		t.Fatalf("installed background code was not executed: %+v", stats)
	}
}

func TestAutoJITBackgroundCompileDrainsOnReconfigure(t *testing.T) {
	if runtime.GOARCH != "amd64" || runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native background compilation requires the amd64 Windows/Linux backend")
	}
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if _, err := vm.Eval(fmt.Sprintf(`globalThis.drainJIT = function(x) { return %s; }; globalThis.drainJIT(1);`, expression.String()), "jit-background-drain.js"); err != nil {
		t.Fatal(err)
	}
	if stats := vm.JITStats(); stats.BackgroundQueued != 1 {
		t.Fatalf("background compile was not queued: %+v", stats)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	if vm.jitPending != 0 || len(vm.jitStates) != 0 || vm.JITStats().Mode != jit.Off {
		t.Fatalf("background compile was not drained during reconfigure: pending=%d states=%d stats=%+v", vm.jitPending, len(vm.jitStates), vm.JITStats())
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAutoJITNativeCodeCacheEvictsLRU(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, CodeCacheBytes: 100, Stats: true})
	_, err = vm.Eval(`
		function a(x) { return x + 1; }
		function b(x) { return x + 2; }
		function c(x) { return x + 3; }
		function d(x) { return x + 4; }
		globalThis.jitCacheResult = a(1) + b(1) + c(1) + d(1);
	`, "jit-cache.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitCacheResult")
	if err != nil || got.String() != "14" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled == 0 {
		t.Skipf("native backend unavailable: %+v", stats)
	}
	if stats.NativeEvictions == 0 || stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native cache did not enforce budget: %+v", stats)
	}
}

func TestAutoJITNativeCacheSurvivesRepeatedGC(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, CodeCacheBytes: 1024, Stats: true,
	})
	for i := 0; i < 32; i++ {
		source := fmt.Sprintf(`
			function cacheStress%d(x) { return x + %d; }
			globalThis.jitCacheStress%d = cacheStress%d(1);
		`, i, i, i, i)
		if _, err := vm.Eval(source, fmt.Sprintf("jit-cache-stress-%d.js", i)); err != nil {
			t.Fatal(err)
		}
		got, err := vm.Global().Get(fmt.Sprintf("jitCacheStress%d", i))
		if err != nil || got.String() != strconv.Itoa(i+1) {
			t.Fatalf("iteration=%d result=%v err=%v", i, got, err)
		}
		runtime.GC()
	}
	stats := vm.JITStats()
	if stats.NativeCompiled == 0 {
		t.Skipf("native backend unavailable: %+v", stats)
	}
	if stats.NativeEvictions == 0 || stats.NativeCodeBytes > stats.CodeCacheLimit || stats.Errors != 0 {
		t.Fatalf("native cache/GC stress was unstable: %+v", stats)
	}
}

func TestAutoJITNativeTraceCacheEvictsLRU(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		CodeCacheBytes:    512,
		Stats:             true,
	})
	for i := 0; i < 8; i++ {
		source := fmt.Sprintf(`
			function traceCache%d(n) {
				const marker = {};
				let total = 0;
				for (let i = 0; i < n; i++) total += i;
				return total;
			}
			globalThis.jitTraceCache%d = traceCache%d(20);
		`, i, i, i)
		if _, err := vm.Eval(source, fmt.Sprintf("jit-trace-cache-%d.js", i)); err != nil {
			t.Fatal(err)
		}
		got, err := vm.Global().Get(fmt.Sprintf("jitTraceCache%d", i))
		if err != nil || got.String() != "190" {
			t.Fatalf("iteration=%d result=%v err=%v", i, got, err)
		}
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled == 0 {
		t.Skipf("native backend unavailable: %+v", stats)
	}
	if stats.NativeTracesExecuted != 8 || stats.NativeEvictions == 0 || stats.NativeCodeBytes > stats.CodeCacheLimit {
		t.Fatalf("native trace cache did not enforce budget: %+v", stats)
	}
}

func TestQuickJITGuardFallsBackToInterpreter(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add(a, b) { return a + b; }
		add(1, 2);
		globalThis.jitFallback = add("a", "b");
		globalThis.jitMixed = add("a", 1);
	`, "jit-guard.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitFallback")
	if err != nil || got.String() != "ab" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	mixed, err := vm.Global().Get("jitMixed")
	if err != nil || mixed.String() != "a1" {
		t.Fatalf("mixed result=%v err=%v", mixed, err)
	}
	stats := vm.JITStats()
	// R3-4: same-type String `+` now concatenates inside Quick (no guard
	// failure for add("a","b")); a mixed String+Number operand still falls
	// back to Tier 0 for the coercion, so exactly one guard failure remains.
	if stats.Compiled != 1 || stats.GuardFailures != 1 || stats.Executed < 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestJITOffHasNoState(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	// 默认已为 auto：显式切到 off 验证"无 JIT 状态"。
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	_, err = vm.Eval(`function f(a) { return a + 1; } f(1);`, "jit-off.js")
	if err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	if stats.Mode != jit.Off || stats.Calls != 0 || stats.Compiled != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestJITStatsAggregateRejectionReasons(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function text() { return arguments[0]; }
		function less(a, b) { return a < b; }
		globalThis.jitRejectedText = text("x");
		globalThis.jitRejectedNative = less(1, 2);
	`, "jit-rejection-reasons.js")
	if err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	if stats.Rejected != 1 || stats.NativeRejected != 1 {
		t.Fatalf("unexpected rejection totals: %+v", stats)
	}
	want := []jit.RejectionReason{
		{Tier: "native", Reason: "jit: native comparison result escapes", Count: 1},
		{Tier: "quick", Reason: "jit: function is not a leaf candidate", Count: 1},
	}
	if !reflect.DeepEqual(stats.RejectionReasons, want) {
		t.Fatalf("rejection reasons=%+v want=%+v", stats.RejectionReasons, want)
	}
}

func TestJITDumpIR(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Dump: jit.DumpIR, DumpWriter: &dump})
	if _, err := vm.Eval(`function add(a, b) { return a + b; } add(1, 2);`, "jit-dump-ir.js"); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	for _, want := range []string{"JIT dump tier=quick", "load_local 1", "add_f64", "return"} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR dump does not contain %q:\n%s", want, text)
		}
	}
}

func TestJITDumpNativeBytes(t *testing.T) {
	if runtime.GOARCH != "amd64" || runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native dump requires the amd64 Windows/Linux backend")
	}
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Dump: jit.DumpASM, DumpWriter: &dump, Stats: true})
	if _, err := vm.Eval(`function add(a, b) { return a + b; } add(1, 2);`, "jit-dump-native.js"); err != nil {
		t.Fatal(err)
	}
	if stats := vm.JITStats(); stats.NativeCompiled != 1 || stats.NativeExecuted != 1 {
		t.Fatalf("native dump candidate was not executed: %+v", stats)
	}
	text := dump.String()
	if !strings.Contains(text, "JIT dump tier=native bytes=") || !strings.Contains(text, "addsd") {
		t.Fatalf("unexpected native dump:\n%s", text)
	}
}

func TestJITNumericTiersDifferential(t *testing.T) {
	type input struct {
		a, b, c string
		n       int
	}
	inputs := []input{
		{a: "0/0", b: "1", c: "2", n: 4},
		{a: "1/0", b: "-1/0", c: "1", n: 6},
		{a: "-0", b: "-0", c: "-0", n: 0},
		{a: "1", b: "2", c: "0", n: 0},
	}
	rng := rand.New(rand.NewSource(20260811))
	for i := 0; i < 128; i++ {
		format := func(value float64) string {
			return strconv.FormatFloat(value, 'g', -1, 64)
		}
		inputs = append(inputs, input{
			a: format((rng.Float64()*2 - 1) * 1e4),
			b: format((rng.Float64()*2 - 1) * 1e4),
			c: format((rng.Float64()*2 - 1) * 100),
			n: rng.Intn(32),
		})
	}

	var source strings.Builder
	source.WriteString(`
		function kernel(a, b, c, n) {
			let x = a;
			let y = b;
			for (let i = 0; i < n; i++) {
				if (x < y) x = (x + c) * 0.5;
				else y = (y - c) / 1.5;
				let swap = x;
				x = y;
				y = swap;
			}
			return x + y;
		}
	`)
	for i, in := range inputs {
		fmt.Fprintf(&source, "globalThis.jitDiff%d = kernel(%s, %s, %s, %d);\n", i, in.a, in.b, in.c, in.n)
	}

	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(source.String(), "jit-tier-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, len(inputs))
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitDiff%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, offStats := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if offStats.Compiled != 0 || quickStats.Compiled != 1 || quickStats.Executed != uint64(len(inputs)) {
		t.Fatalf("unexpected off/quick stats: off=%+v quick=%+v", offStats, quickStats)
	}
	if nativeStats.NativeCompiled != 1 || nativeStats.NativeExecuted != uint64(len(inputs)) ||
		nativeStats.VerifyChecks != uint64(len(inputs)) || nativeStats.VerifyFailures != 0 {
		t.Fatalf("unexpected native stats: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case %d input=%+v off=%v quick=%v native=%v", i, inputs[i], baseline[i], quick[i], native[i])
		}
	}
}

func TestJITNumericNotEqualTiersDifferential(t *testing.T) {
	source := `
		function neKernel(a, b, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) {
				if (a !== b) total += i + 1;
				else total += i;
				if (a != b) total += 10;
			}
			return total;
		}
		globalThis.jitNe0 = neKernel(1, 1, 20);
		globalThis.jitNe1 = neKernel(1, 2, 20);
		globalThis.jitNe2 = neKernel(NaN, NaN, 20);
		globalThis.jitNe3 = neKernel(-0, 0, 20);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-not-equal-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 4)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitNe%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("not-equal cases did not exercise Quick trace: %+v", quickStats)
	}
	if nativeStats.NativeTracesCompiled == 0 || nativeStats.NativeTracesExecuted == 0 ||
		nativeStats.VerifyChecks == 0 || nativeStats.VerifyFailures != 0 {
		t.Fatalf("not-equal cases did not verify Native trace: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case %d off=%v quick=%v native=%v", i, baseline[i], quick[i], native[i])
		}
	}
}

func TestJITStrictEqualityReferenceTraceDifferential(t *testing.T) {
	source := `
		function strictRefKernel(a, b, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) {
				if (a === b) total += 1;
				if (a !== b) total += 2;
			}
			return total;
		}
		const sameObject = {};
		globalThis.jitStrictRef0 = strictRefKernel(4, 4, 12);
		globalThis.jitStrictRef1 = strictRefKernel(4, 5, 12);
		globalThis.jitStrictRef2 = strictRefKernel("same", "same", 12);
		globalThis.jitStrictRef3 = strictRefKernel("left", "right", 12);
		globalThis.jitStrictRef4 = strictRefKernel(7n, 7n, 12);
		globalThis.jitStrictRef5 = strictRefKernel(7n, 8n, 12);
		globalThis.jitStrictRef6 = strictRefKernel(false, false, 12);
		globalThis.jitStrictRef7 = strictRefKernel(null, undefined, 12);
		globalThis.jitStrictRef8 = strictRefKernel(sameObject, sameObject, 12);
		globalThis.jitStrictRef9 = strictRefKernel({}, {}, 12);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-strict-reference-trace.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, 10)
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitStrictRef%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	auto, autoStats := run(jit.Auto)
	if quickStats.TracesCompiled != 1 || quickStats.TracesExecuted == 0 || quickStats.GuardFailures != 0 {
		t.Fatalf("strict reference cases did not stay in Quick trace: %+v", quickStats)
	}
	if autoStats.NativeTracesCompiled != 1 || autoStats.NativeTracesExecuted == 0 ||
		autoStats.TracesExecuted+autoStats.TraceYields == 0 ||
		autoStats.NativeTraceGuardDisabled != 1 || autoStats.NativeCodeBytes != 0 ||
		autoStats.VerifyChecks == 0 || autoStats.VerifyFailures != 0 {
		t.Fatalf("strict reference Native fallback did not stabilize: %+v", autoStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], auto[i]) {
			t.Fatalf("case=%d off=%v quick=%v auto=%v", i, baseline[i], quick[i], auto[i])
		}
	}
}

// TestJITSymbolTruthinessNullishModeledInQuick verifies the R3-1 claim:
// Symbol values are now modeled by Quick (quickSymbol), so truthiness
// (`!`, `if (sym)`, `&&`, `||`, `??`) and nullish tests with Symbol operands
// execute inside the JIT (function and trace levels) instead of guarding back
// to Tier 0. Quick must run the whole scenario with zero guard failures; Auto
// must produce identical results with the Native tier never executing Symbol
// inputs (the number-only ABI rejects at the entry guard; programs containing
// `!` are additionally not native-compilable and stay on Quick).
func TestJITSymbolTruthinessNullishModeledInQuick(t *testing.T) {
	source := `
		const symA = Symbol("a");
		const symB = Symbol("b");
		const objA = {};
		function symTruthyTrace(a, b, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) {
				if (a) total += 1;
				if (a ?? b) total += 2;
				if (!a) total += 4;
			}
			return total;
		}
		function symTruthyLeaf(v, x) {
			let r = 0;
			if (!v) r += 1;
			if (v && x) r += 2;
			if (v || x) r += 4;
			if (v ?? x) r += 8;
			return r;
		}
		globalThis.symTruthyTraceSym = symTruthyTrace(symA, 0, 12);
		globalThis.symTruthyTraceSymUndef = symTruthyTrace(symA, undefined, 12);
		globalThis.symTruthyTraceZeroSym = symTruthyTrace(0, symB, 12);
		globalThis.symTruthyTraceNullSym = symTruthyTrace(null, symB, 12);
		globalThis.symTruthyLeafSymZero = symTruthyLeaf(symA, 0);
		globalThis.symTruthyLeafSymThree = symTruthyLeaf(symA, 3);
		globalThis.symTruthyLeafZeroSym = symTruthyLeaf(0, symB);
		globalThis.symTruthyLeafNullSym = symTruthyLeaf(null, symB);
		globalThis.symTruthyLeafObjSym = symTruthyLeaf(objA, symB);
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: 1, BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-symbol-truthy.js"); err != nil {
			t.Fatal(err)
		}
		names := []string{
			"symTruthyTraceSym", "symTruthyTraceSymUndef", "symTruthyTraceZeroSym", "symTruthyTraceNullSym",
			"symTruthyLeafSymZero", "symTruthyLeafSymThree", "symTruthyLeafZeroSym",
			"symTruthyLeafNullSym", "symTruthyLeafObjSym",
		}
		values := make([]engine.Value, len(names))
		for i, name := range names {
			value, err := vm.Global().Get(name)
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	want := map[string]string{
		"symTruthyTraceSym":      "36", // truthy + nullish-kept x 12
		"symTruthyTraceSymUndef": "36",
		"symTruthyTraceZeroSym":  "48", // !0 only x 12
		"symTruthyTraceNullSym":  "72", // (?? result truthy + !null) x 12
		"symTruthyLeafSymZero":   "12", // || + ?? keep the Symbol
		"symTruthyLeafSymThree":  "14", // && + || + ??
		"symTruthyLeafZeroSym":   "5",  // !0 + || keeps Symbol
		"symTruthyLeafNullSym":   "13", // !null + || + ??
		"symTruthyLeafObjSym":    "14", // && + || + ?? with object operand
	}
	names := []string{
		"symTruthyTraceSym", "symTruthyTraceSymUndef", "symTruthyTraceZeroSym", "symTruthyTraceNullSym",
		"symTruthyLeafSymZero", "symTruthyLeafSymThree", "symTruthyLeafZeroSym",
		"symTruthyLeafNullSym", "symTruthyLeafObjSym",
	}
	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	auto, autoStats := run(jit.Auto)
	for i, name := range names {
		if got := baseline[i].String(); got != want[name] {
			t.Fatalf("%s = %s, want %s", name, got, want[name])
		}
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], auto[i]) {
			t.Fatalf("%s off=%v quick=%v auto=%v", name, baseline[i], quick[i], auto[i])
		}
	}
	// Quick: Symbol is modeled — compiled functions and traces execute without
	// any guard fallback to Tier 0.
	if quickStats.Compiled == 0 || quickStats.TracesCompiled == 0 ||
		quickStats.Executed == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("quick did not model Symbol truthiness/nullish: %+v", quickStats)
	}
	if quickStats.GuardFailures != 0 {
		t.Fatalf("quick guard-failed on modeled Symbol values: %+v", quickStats)
	}
	// Auto: Native never executes Symbol inputs and the results stay identical;
	// the `!`-bearing programs are not native-compilable, so Auto stabilizes on
	// Quick with no runtime guard failures.
	if autoStats.NativeExecuted+autoStats.NativeTracesExecuted != 0 {
		t.Fatalf("auto executed Native with Symbol inputs (ABI leak): %+v", autoStats)
	}
	if autoStats.GuardFailures != 0 || autoStats.VerifyFailures != 0 {
		t.Fatalf("auto guard/verify failures on Symbol truthiness paths: %+v", autoStats)
	}
	if autoStats.Executed == 0 || autoStats.TracesExecuted == 0 {
		t.Fatalf("auto did not execute Symbol truthiness in Quick: %+v", autoStats)
	}
}

// TestJITSymbolStrictEqualityAcrossTiers is the R3-2 differential for
// `===` / `!==` with Symbol operands: same-symbol identity is equal,
// different symbols (even with the same description) are not, and a Symbol is
// never equal to any other type (no coercion). String, BigInt, Number, object
// and nullish semantics must not regress. The leaf and trace shapes are
// Native-compilable, so Auto exercises the number-only Native ABI: Symbol
// inputs fail the entry guard and the site stabilizes on Quick after at most
// jitGuardFailureLimit (2) failures.
func TestJITSymbolStrictEqualityAcrossTiers(t *testing.T) {
	source := `
		const symA = Symbol("a");
		const symB = Symbol("a");
		const objA = {};
		const objB = {};
		function symStrictLeaf(a, b) {
			let r = 0;
			if (a === b) r += 1;
			if (a !== b) r += 2;
			return r;
		}
		function symStrictTrace(a, b, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) {
				if (a === b) total += 1;
				if (a !== b) total += 2;
			}
			return total;
		}
		function strictEq(a, b) { return a === b; }
		function strictNe(a, b) { return a !== b; }
		globalThis.leafSameSym = symStrictLeaf(symA, symA);
		globalThis.leafDiffSym = symStrictLeaf(symA, symB);
		globalThis.leafSymString = symStrictLeaf(symA, "a");
		globalThis.leafSymNumber = symStrictLeaf(symA, 1);
		globalThis.leafSymBigInt = symStrictLeaf(symA, 1n);
		globalThis.leafSymBool = symStrictLeaf(symA, true);
		globalThis.leafSymNull = symStrictLeaf(symA, null);
		globalThis.leafSymUndef = symStrictLeaf(symA, undefined);
		globalThis.leafSymObject = symStrictLeaf(symA, objA);
		globalThis.leafBigIntValue = symStrictLeaf(7n, 7n);
		globalThis.leafStringValue = symStrictLeaf("a", "a");
		globalThis.leafNumberValue = symStrictLeaf(0, -0);
		globalThis.leafNumberNaN = symStrictLeaf(NaN, NaN);
		globalThis.leafObjectIdentity = symStrictLeaf(objA, objA);
		globalThis.leafDifferentObject = symStrictLeaf(objA, objB);
		globalThis.traceSameSym = symStrictTrace(symA, symA, 12);
		globalThis.traceDiffSym = symStrictTrace(symA, symB, 12);
		globalThis.traceSymString = symStrictTrace(symA, "a", 12);
		globalThis.traceBigIntValue = symStrictTrace(7n, 7n, 12);
		globalThis.traceStringValue = symStrictTrace("a", "a", 12);
		globalThis.traceObjectIdentity = symStrictTrace(objA, objA, 12);
		globalThis.traceDifferentObject = symStrictTrace(objA, objB, 12);
		globalThis.eqSameSym = strictEq(symA, symA);
		globalThis.eqDiffSym = strictEq(symA, symB);
		globalThis.eqSymString = strictEq(symA, "a");
		globalThis.eqSymBigInt = strictEq(symA, 1n);
		globalThis.eqSymNull = strictEq(symA, null);
		globalThis.eqSymUndef = strictEq(symA, undefined);
		globalThis.eqSymObject = strictEq(symA, objA);
		globalThis.eqNumberValue = strictEq(0, -0);
		globalThis.eqNumberNaN = strictEq(NaN, NaN);
		globalThis.neSameSym = strictNe(symA, symA);
		globalThis.neDiffSym = strictNe(symA, symB);
		globalThis.neSymString = strictNe(symA, "a");
	`
	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode: mode, Threshold: 1, BackedgeThreshold: 2,
			TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
		})
		if _, err := vm.Eval(source, "jit-symbol-strict.js"); err != nil {
			t.Fatal(err)
		}
		names := []string{
			"leafSameSym", "leafDiffSym", "leafSymString", "leafSymNumber", "leafSymBigInt",
			"leafSymBool", "leafSymNull", "leafSymUndef", "leafSymObject",
			"leafBigIntValue", "leafStringValue", "leafNumberValue", "leafNumberNaN",
			"leafObjectIdentity", "leafDifferentObject",
			"traceSameSym", "traceDiffSym", "traceSymString", "traceBigIntValue",
			"traceStringValue", "traceObjectIdentity", "traceDifferentObject",
			"eqSameSym", "eqDiffSym", "eqSymString", "eqSymBigInt", "eqSymNull",
			"eqSymUndef", "eqSymObject", "eqNumberValue", "eqNumberNaN",
			"neSameSym", "neDiffSym", "neSymString",
		}
		values := make([]engine.Value, len(names))
		for i, name := range names {
			value, err := vm.Global().Get(name)
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}
	want := map[string]string{
		"leafSameSym": "1", "leafDiffSym": "2", "leafSymString": "2", "leafSymNumber": "2",
		"leafSymBigInt": "2", "leafSymBool": "2", "leafSymNull": "2", "leafSymUndef": "2",
		"leafSymObject": "2", "leafBigIntValue": "1", "leafStringValue": "1", "leafNumberValue": "1",
		"leafNumberNaN": "2", "leafObjectIdentity": "1", "leafDifferentObject": "2",
		"traceSameSym": "12", "traceDiffSym": "24", "traceSymString": "24", "traceBigIntValue": "12",
		"traceStringValue": "12", "traceObjectIdentity": "12", "traceDifferentObject": "24",
		"eqSameSym": "true", "eqDiffSym": "false", "eqSymString": "false", "eqSymBigInt": "false",
		"eqSymNull": "false", "eqSymUndef": "false", "eqSymObject": "false",
		"eqNumberValue": "true", "eqNumberNaN": "false",
		"neSameSym": "false", "neDiffSym": "true", "neSymString": "true",
	}
	names := []string{
		"leafSameSym", "leafDiffSym", "leafSymString", "leafSymNumber", "leafSymBigInt",
		"leafSymBool", "leafSymNull", "leafSymUndef", "leafSymObject",
		"leafBigIntValue", "leafStringValue", "leafNumberValue", "leafNumberNaN",
		"leafObjectIdentity", "leafDifferentObject",
		"traceSameSym", "traceDiffSym", "traceSymString", "traceBigIntValue",
		"traceStringValue", "traceObjectIdentity", "traceDifferentObject",
		"eqSameSym", "eqDiffSym", "eqSymString", "eqSymBigInt", "eqSymNull",
		"eqSymUndef", "eqSymObject", "eqNumberValue", "eqNumberNaN",
		"neSameSym", "neDiffSym", "neSymString",
	}
	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	auto, autoStats := run(jit.Auto)
	for i, name := range names {
		if got := baseline[i].String(); got != want[name] {
			t.Fatalf("%s = %s, want %s", name, got, want[name])
		}
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], auto[i]) {
			t.Fatalf("%s off=%v quick=%v auto=%v", name, baseline[i], quick[i], auto[i])
		}
	}
	// Quick: strict equality with Symbol operands executes in-JIT (function
	// and trace) with zero guard failures; value/identity semantics intact.
	if quickStats.Compiled == 0 || quickStats.TracesCompiled == 0 ||
		quickStats.Executed == 0 || quickStats.TracesExecuted == 0 ||
		quickStats.GuardFailures != 0 {
		t.Fatalf("quick did not execute Symbol strict equality in-JIT: %+v", quickStats)
	}
	// Auto: Native compiles the leaf and the trace, but the number-only ABI
	// rejects Symbol inputs at the entry guard; after at most two failures per
	// site it stabilizes on Quick with identical results and no verify error.
	if autoStats.NativeCompiled == 0 || autoStats.NativeTracesCompiled == 0 {
		t.Fatalf("auto did not compile Native for Symbol strict programs: %+v", autoStats)
	}
	if autoStats.NativeExecuted != 0 || autoStats.NativeTracesExecuted != 0 {
		t.Fatalf("auto executed Native with Symbol inputs (ABI leak): %+v", autoStats)
	}
	if autoStats.NativeGuardDisabled == 0 || autoStats.NativeTraceGuardDisabled == 0 {
		t.Fatalf("auto did not disable Native after Symbol entry-guard failures: %+v", autoStats)
	}
	if autoStats.GuardFailures < 4 {
		t.Fatalf("auto entry-guard failures %d, want at least 4 (2 leaf + 2 trace): %+v", autoStats.GuardFailures, autoStats)
	}
	if autoStats.Executed == 0 || autoStats.TracesExecuted == 0 {
		t.Fatalf("auto did not fall back to Quick for Symbol strict equality: %+v", autoStats)
	}
	if autoStats.VerifyFailures != 0 {
		t.Fatalf("auto verify must never run on Native-rejected Symbol paths: %+v", autoStats)
	}
}

func TestJITTraceTiersGeneratedDifferential(t *testing.T) {
	type input struct {
		start string
		n     int
		mode  int
		stop  int
	}
	inputs := []input{
		{start: "0/0", n: 5, mode: 0, stop: 0},
		{start: "1/0", n: 4, mode: 1, stop: 2},
		{start: "-1/0", n: 3, mode: 0, stop: 5},
		{start: "-0", n: 0, mode: 1, stop: 0},
	}
	rng := rand.New(rand.NewSource(0xA1_2026_0811))
	for i := 0; i < 256; i++ {
		inputs = append(inputs, input{
			start: strconv.FormatFloat((rng.Float64()*2-1)*1e6, 'g', -1, 64),
			n:     rng.Intn(24),
			mode:  rng.Intn(2),
			stop:  rng.Intn(6),
		})
	}

	var source strings.Builder
	source.WriteString(`
		function traceKernel(start, n, mode, stop) {
			const marker = {};
			let total = start;
			outer:
			for (let i = 0; i < n; i++) {
				for (let j = 0; j < 6; j++) {
					total = (total + j) * 0.5;
					if (mode === 1) {
						if (j === stop) continue outer;
					}
				}
				total += 7;
			}
			return total;
		}
	`)
	for i, in := range inputs {
		fmt.Fprintf(&source, "globalThis.jitTraceDiff%d = traceKernel(%s, %d, %d, %d);\n", i, in.start, in.n, in.mode, in.stop)
	}

	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode:              mode,
			Threshold:         ^uint32(0),
			BackedgeThreshold: 2,
			TraceBudget:       3,
			Verify:            mode == jit.Auto,
			Stats:             true,
		})
		if _, err := vm.Eval(source.String(), "jit-trace-generated-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, len(inputs))
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitTraceDiff%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("generated cases did not exercise Quick traces: %+v", quickStats)
	}
	if nativeStats.NativeTracesCompiled == 0 || nativeStats.NativeTracesExecuted == 0 ||
		nativeStats.VerifyChecks == 0 || nativeStats.VerifyFailures != 0 {
		t.Fatalf("generated cases did not verify Native traces: %+v", nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case %d input=%+v off=%v quick=%v native=%v", i, inputs[i], baseline[i], quick[i], native[i])
		}
	}
}

func TestJITTraceReturnsIntoTryCatch(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
			})
			_, err = vm.Eval(`
				function sumThenThrow(n) {
					let total = 0;
					for (let i = 0; i < n; i++) total += i;
					if (n > 0) throw new Error("jit boom");
					return total;
				}
				try { sumThenThrow(41); }
				catch (error) {
					globalThis.jitCaughtMessage = error.message;
					globalThis.jitCaughtType = error instanceof Error;
				}
			`, "jit-trace-try-catch.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitCaughtMessage": "jit boom",
				"jitCaughtType":    "true",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.TracesCompiled == 0 || stats.TracesExecuted+stats.TraceYields+
				stats.NativeTracesExecuted+stats.NativeTraceYields == 0 {
				t.Fatalf("try/catch did not execute compiled trace: %+v", stats)
			}
		})
	}
}

func TestJITTraceGuardFailurePreservesThrowAndCatch(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Verify: mode == jit.Auto, Stats: true,
			})
			_, err = vm.Eval(`
				function readLoop(o, n) {
					const traceOnlyMarker = {};
					let total = 0;
					for (let i = 0; i < n; i++) total += o.value;
					return total;
				}
				const target = { value: 2 };
				globalThis.jitWarmRead = readLoop(target, 20);
				let reads = 0;
				Object.defineProperty(target, "value", {
					configurable: true,
					get() { reads++; if (reads === 1) return 3; throw new Error("property boom"); }
				});
				try { readLoop(target, 4); }
				catch (error) {
					globalThis.jitGuardCaughtMessage = error.message;
					globalThis.jitGuardCaughtReads = reads;
				}
			`, "jit-trace-guard-throw.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitWarmRead":           "40",
				"jitGuardCaughtMessage": "property boom",
				"jitGuardCaughtReads":   "2",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.TracesCompiled == 0 || stats.GuardFailures == 0 {
				t.Fatalf("guard failure was not observed before throw: %+v", stats)
			}
		})
	}
}

func TestJITPropertyWriteTiersGeneratedDifferential(t *testing.T) {
	type input struct {
		start string
		step  string
		n     int
	}
	inputs := []input{
		{start: "0/0", step: "1", n: 5},
		{start: "1/0", step: "-1/0", n: 4},
		{start: "-0", step: "-0", n: 7},
		{start: "1", step: "2", n: 0},
	}
	rng := rand.New(rand.NewSource(0x5E7_2026_0811))
	for i := 0; i < 128; i++ {
		inputs = append(inputs, input{
			start: strconv.FormatFloat((rng.Float64()*2-1)*1e4, 'g', -1, 64),
			step:  strconv.FormatFloat((rng.Float64()*2-1)*100, 'g', -1, 64),
			n:     rng.Intn(40),
		})
	}

	var source strings.Builder
	source.WriteString(`
		function propertyWriteKernel(start, step, n) {
			const marker = {};
			const o = { value: start };
			let checksum = 0;
			for (let i = 0; i < n; i++) {
				o.value = (o.value + step) * 0.5;
				checksum += o.value;
			}
			return checksum + o.value;
		}
	`)
	for i, in := range inputs {
		fmt.Fprintf(&source, "globalThis.jitPropWriteDiff%d = propertyWriteKernel(%s, %s, %d);\n", i, in.start, in.step, in.n)
	}

	run := func(mode jit.Mode) ([]engine.Value, jit.Stats) {
		t.Helper()
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		vm.ConfigureJIT(jit.Config{
			Mode:              mode,
			Threshold:         ^uint32(0),
			BackedgeThreshold: 2,
			TraceBudget:       3,
			Stats:             true,
		})
		if _, err := vm.Eval(source.String(), "jit-property-write-generated-differential.js"); err != nil {
			t.Fatal(err)
		}
		values := make([]engine.Value, len(inputs))
		for i := range values {
			value, err := vm.Global().Get(fmt.Sprintf("jitPropWriteDiff%d", i))
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values, vm.JITStats()
	}

	baseline, _ := run(jit.Off)
	quick, quickStats := run(jit.Quick)
	native, nativeStats := run(jit.Auto)
	if quickStats.TracesExecuted == 0 || nativeStats.NativeTracesExecuted == 0 || nativeStats.NativeTraceYields == 0 {
		t.Fatalf("property-write differential did not exercise all tiers: quick=%+v native=%+v", quickStats, nativeStats)
	}
	for i := range baseline {
		if !sameJITValue(baseline[i], quick[i]) || !sameJITValue(baseline[i], native[i]) {
			t.Fatalf("case %d input=%+v off=%v quick=%v native=%v", i, inputs[i], baseline[i], quick[i], native[i])
		}
	}
}

func TestJITBypassesWhenInstructionMetricsAreEnabled(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	vm.insnsEnabled = true
	_, err = vm.Eval(`function add(a, b) { return a + b; } globalThis.jitMetricsSafe = add(1, 2);`, "jit-metrics-safe.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitMetricsSafe")
	if err != nil || got.String() != "3" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	if stats := vm.JITStats(); stats.Compiled != 0 || stats.NativeCompiled != 0 || stats.Executed != 0 {
		t.Fatalf("JIT was not bypassed for exact instruction metrics: %+v", stats)
	}
}

func TestJITTraceGuardedNoopCallDeoptsOnCalleeChange(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		TraceBudget:       3,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function run(fn, n) {
			let total = 0;
			for (let i = 0; i < n; i++) { fn(); total++; }
			return total;
		}
		function noop() {}
		let side = 0;
		function effect() { side += 10; }
		globalThis.jitNoopRun = run(noop, 20);
		globalThis.jitEffectRun = run(effect, 4);
		globalThis.jitNoopSide = side;
	`, "jit-trace-guarded-noop.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitNoopRun":   "20",
		"jitEffectRun": "4",
		"jitNoopSide":  "40",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.TracesCompiled != 1 || stats.NoopCallSites != 1 || stats.NativeTracesExecuted == 0 || stats.GuardFailures == 0 {
		t.Fatalf("unexpected guarded noop stats: %+v", stats)
	}
}

func TestJITTraceGuardedTrivialMethodGetter(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		TraceBudget:       3,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function methodLoop(o, n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += o.get();
			return total;
		}
		const target = { v: 7, get() { return this.v; } };
		globalThis.jitMethodGetter = methodLoop(target, 20);
		target.get = function() { return 3; };
		globalThis.jitMethodGetterChanged = methodLoop(target, 4);
	`, "jit-trace-method-getter.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitMethodGetter")
	if err != nil || got.String() != "140" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	changed, err := vm.Global().Get("jitMethodGetterChanged")
	if err != nil || changed.String() != "12" {
		t.Fatalf("changed result=%v err=%v", changed, err)
	}
	stats := vm.JITStats()
	if stats.MethodCallSites != 1 || stats.NativeTracesExecuted == 0 || stats.GuardFailures == 0 {
		for key, state := range vm.jitTraces {
			t.Logf("trace key=%s pc=%d state=%+v", key.tmpl.Name, key.backedgePC, state)
			if state != nil && state.program != nil {
				t.Logf("trace IR:\n%s", state.program.DumpIR())
			}
		}
		t.Fatalf("unexpected method trace stats: %+v", stats)
	}
}

func TestJITTraceGuardedArrayPushRange(t *testing.T) {
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
				function appendRange(array, start, end) {
					for (let i = start; i < end; i++) array.push(i);
					return array.length;
				}
				const array = [];
				globalThis.jitArrayPushLength = appendRange(array, 0, 20);
				globalThis.jitArrayPushFirst = array[0];
				globalThis.jitArrayPushLast = array[19];
				let calls = 0;
				array.push = function(_) { calls++; return this.length; };
				globalThis.jitArrayPushChangedLength = appendRange(array, 20, 24);
				globalThis.jitArrayPushChangedCalls = calls;
			`, "jit-trace-array-push.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitArrayPushLength":        "20",
				"jitArrayPushFirst":         "0",
				"jitArrayPushLast":          "19",
				"jitArrayPushChangedLength": "20",
				"jitArrayPushChangedCalls":  "4",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.ArrayPushSites != 1 || stats.TracesExecuted == 0 || stats.ArrayPushYields == 0 || stats.GuardFailures == 0 {
				t.Fatalf("unexpected guarded array push stats: %+v", stats)
			}
		})
	}
}

func TestJITArrayPushTraceRejectsProxy(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		const array = [];
		const proxy = new Proxy(array, {});
		for (let i = 0; i < 8; i++) proxy.push(i);
		globalThis.jitProxyPushLength = array.length;
	`, "jit-trace-array-push-proxy.js")
	if err != nil {
		t.Fatal(err)
	}
	if stats := vm.JITStats(); stats.ArrayPushSites != 0 {
		t.Fatalf("proxy entered array push specialization: %+v", stats)
	}
}

func TestJITArrayPushTraceSafepointKeepsCompletedPrefix(t *testing.T) {
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
						return errors.New("stop array push")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				const array = [];
				try {
					for (let i = 0; i < 100; i++) array.push(i);
				} catch (error) {
					globalThis.jitArrayPushInterrupt = error.message;
				}
				globalThis.jitArrayPushPartialLength = array.length;
				globalThis.jitArrayPushPartialLast = array[array.length - 1];
			`, "jit-trace-array-push-interrupt.js")
			if err != nil {
				t.Fatal(err)
			}
			message, _ := vm.Global().Get("jitArrayPushInterrupt")
			lengthValue, _ := vm.Global().Get("jitArrayPushPartialLength")
			lastValue, _ := vm.Global().Get("jitArrayPushPartialLast")
			length, ok := lengthValue.Int()
			if message.String() != "stop array push" || !ok || length <= 0 || length >= 100 || lastValue.String() != strconv.Itoa(length-1) {
				t.Fatalf("message=%v length=%v last=%v", message, lengthValue, lastValue)
			}
			stats := vm.JITStats()
			if stats.ArrayPushSites != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 || stats.ArrayPushYields != 1 {
				t.Fatalf("unexpected interrupted array push stats: %+v", stats)
			}
		})
	}
}

func TestJITArrayPushTraceRejectsUnsafeNumbers(t *testing.T) {
	trace := &arrayPushTraceState{indexLocal: 0, boundLocal: 1, boundIsLocal: true}
	tests := []struct {
		name  string
		index float64
		bound float64
	}{
		{name: "nan index", index: math.NaN(), bound: 10},
		{name: "infinite index", index: math.Inf(1), bound: 10},
		{name: "negative index", index: -1, bound: 10},
		{name: "fractional index", index: 0.5, bound: 10},
		{name: "nan bound", index: 0, bound: math.NaN()},
		{name: "infinite bound", index: 0, bound: math.Inf(1)},
		{name: "negative bound", index: 0, bound: -1},
		{name: "fractional bound", index: 0, bound: 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := trace.arrayPushNumbers([]engine.Value{engine.Number(tt.index), engine.Number(tt.bound)}); ok {
				t.Fatalf("unsafe range accepted: index=%v bound=%v", tt.index, tt.bound)
			}
		})
	}
}

func TestJITTraceGuardedClosureIncrementUpvalue(t *testing.T) {
	const source = `
		function make() { let n = 0; return () => ++n; }
		function run(fn, end) {
			let sum = 0;
			for (let i = 0; i < end; i++) sum += fn();
			return sum;
		}
		const increment = make();
		globalThis.jitClosureIncrementSum = run(increment, 20);
		globalThis.jitClosureIncrementNext = increment();
	`
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			mod, err := vm.Compile(source, "jit-trace-closure-increment.js")
			if err != nil {
				t.Fatal(err)
			}
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Stats: true,
			})
			if _, err = vm.RunModule(mod); err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"jitClosureIncrementSum":  "210",
				"jitClosureIncrementNext": "21",
			} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.ClosureUpvalueSites != 1 || stats.TracesExecuted == 0 || stats.ClosureUpvalueYields == 0 {
				logJITBytecode(t, mod.Functions)
				t.Fatalf("unexpected closure upvalue stats: %+v", stats)
			}
		})
	}
}

func TestJITClosureIncrementTraceDeoptsOnCalleeChange(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		function make() { let n = 0; return () => ++n; }
		function run(fn, end) {
			let sum = 0;
			for (let i = 0; i < end; i++) sum += fn();
			return sum;
		}
		const increment = make();
		let side = 0;
		function effect() { side += 10; return 2; }
		globalThis.jitClosureFirst = run(increment, 20);
		globalThis.jitClosureChanged = run(effect, 4);
		globalThis.jitClosureChangedSide = side;
	`, "jit-trace-closure-changed.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitClosureFirst":       "210",
		"jitClosureChanged":     "8",
		"jitClosureChangedSide": "40",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.ClosureUpvalueSites != 1 || stats.GuardFailures == 0 {
		t.Fatalf("closure callee change did not deopt: %+v", stats)
	}
}

func TestJITClosureIncrementTraceRejectsAliasedUpvalue(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		function aliasRun(end) {
			let sum = 0;
			const increment = () => ++sum;
			for (let i = 0; i < end; i++) sum += increment();
			return sum;
		}
		globalThis.jitClosureAlias = aliasRun(4);
	`, "jit-trace-closure-alias.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitClosureAlias")
	if err != nil || got.String() != "15" {
		t.Fatalf("aliased closure result=%v err=%v", got, err)
	}
	if stats := vm.JITStats(); stats.ClosureUpvalueSites != 0 {
		t.Fatalf("aliased upvalue entered specialization: %+v", stats)
	}
}

func TestJITClosureIncrementTraceRejectsNonNumberUpvalue(t *testing.T) {
	uv := &upvalue{closed: engine.Str("1")}
	target := &vmClosure{upvalues: []*upvalue{uv}}
	trace := &closureIncrementTraceState{
		calleeLocal: 0, indexLocal: 1, boundLocal: 2, sumLocal: 3,
		target: target, upvalues: []*upvalue{uv},
		plan: &closurePlan{
			writes: []closureWrite{{slot: 0, expr: closureExpr{kind: closureExprBin, op: bytecode.OpAdd,
				left: &closureExpr{kind: closureExprUpvalue, slot: 0}, right: &closureExpr{kind: closureExprConst, value: 1}}}},
			result: closureExpr{kind: closureExprUpvalue, slot: 0},
		},
	}
	vm := &VM{jitConfig: jit.Config{TraceBudget: 3}}
	locals := []engine.Value{target, engine.Number(0), engine.Number(10), engine.Number(0)}
	_, reason, err := vm.executeClosureIncrementTrace(trace, locals)
	if err != nil || reason != jit.GuardFailed {
		t.Fatalf("non-number upvalue reason=%v err=%v", reason, err)
	}
	if uv.closed.String() != "1" || locals[1].String() != "0" || locals[3].String() != "0" {
		t.Fatalf("guard failure mutated state: upvalue=%v index=%v sum=%v", uv.closed, locals[1], locals[3])
	}
}

func TestJITClosureIncrementTraceSafepointCommitsUpvalue(t *testing.T) {
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
						return errors.New("stop closure increment")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				function make() { let n = 0; return () => ++n; }
				function run(fn, end) {
					let sum = 0;
					for (let i = 0; i < end; i++) sum += fn();
					return sum;
				}
				const increment = make();
				try { run(increment, 100); }
				catch (error) { globalThis.jitClosureInterrupt = error.message; }
				globalThis.jitClosureAfterInterrupt = increment();
			`, "jit-trace-closure-interrupt.js")
			if err != nil {
				t.Fatal(err)
			}
			message, _ := vm.Global().Get("jitClosureInterrupt")
			nextValue, _ := vm.Global().Get("jitClosureAfterInterrupt")
			next, ok := nextValue.Int()
			if message.String() != "stop closure increment" || !ok || next <= 1 || next >= 101 {
				t.Fatalf("message=%v next=%v", message, nextValue)
			}
			stats := vm.JITStats()
			if stats.ClosureUpvalueSites != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 ||
				stats.ClosureUpvalueYields != 1 {
				t.Fatalf("unexpected interrupted closure stats: %+v", stats)
			}
		})
	}
}

func TestNativeCallbackNumericFastPathsPreserveFallback(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	_, err = vm.Eval(`
		globalThis.jitFastMap = [1, 2, 3].map(x => x * 2).join(",");
		globalThis.jitFallbackMap = ["2", 3].map(x => x * 2).join(",");
		globalThis.jitFastFilter = [1, 2, 3, 4].filter(x => x % 2 === 0).join(",");
		globalThis.jitFallbackFilter = ["1", 2].filter(x => x % 2 === 0).join(",");
		globalThis.jitFastReduce = [1, 2, 3].reduce((acc, x) => acc + x, 0);
		globalThis.jitFallbackReduce = [1, 2].reduce((acc, x) => acc + x, "");
		const edgeMap = [NaN, Infinity, -0].map(x => x * 2);
		globalThis.jitFastMapEdges = edgeMap[0] !== edgeMap[0] && edgeMap[1] === Infinity && Object.is(edgeMap[2], -0);
		const edgeFilter = [NaN, Infinity, -0, 3].filter(x => x % 2 === 0);
		globalThis.jitFastFilterEdges = edgeFilter.length === 1 && Object.is(edgeFilter[0], -0);
		const edgeReduce = [-0, -0].reduce((acc, x) => acc + x, -0);
		globalThis.jitFastReduceEdges = Object.is(edgeReduce, -0);
		globalThis.jitNativeDivideEdges = Object.is([-0].map(x => 1 / x)[0], -Infinity);
	`, "jit-native-callback-fast.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitFastMap":           "2,4,6",
		"jitFallbackMap":       "4,6",
		"jitFastFilter":        "2,4",
		"jitFallbackFilter":    "2",
		"jitFastReduce":        "6",
		"jitFallbackReduce":    "12",
		"jitFastMapEdges":      "true",
		"jitFastFilterEdges":   "true",
		"jitFastReduceEdges":   "true",
		"jitNativeDivideEdges": "true",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
}

func TestJITSafepointInterruptsQuickAndNativeFunctionLoops(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode:              mode,
				Threshold:         ^uint32(0),
				BackedgeThreshold: 2,
				TraceBudget:       3,
				Stats:             true,
				Safepoint: func() error {
					polls++
					if polls == 2 {
						return errors.New("function loop interrupted")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				function sum(n) {
					let total = 0;
					for (let i = 0; i < n; i++) total += i;
					return total;
				}
				let message = "";
				try { sum(1000000); } catch (e) { message = e.message; }
				globalThis.jitSafepointMessage = message;
			`, "jit-safepoint-function.js")
			if err != nil {
				t.Fatal(err)
			}
			message, err := vm.Global().Get("jitSafepointMessage")
			if err != nil || message.String() != "function loop interrupted" {
				t.Fatalf("message=%v err=%v", message, err)
			}
			stats := vm.JITStats()
			if stats.Compiled != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 || stats.Errors != 0 {
				t.Fatalf("unexpected safepoint stats: %+v", stats)
			}
			if mode == jit.Auto && (stats.NativeCompiled != 1 || stats.NativeYields < 2) {
				t.Fatalf("native function did not yield through safepoints: %+v", stats)
			}
		})
	}
}

func TestJITSafepointInterruptsQuickAndNativePropertyTraces(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode:              mode,
				Threshold:         ^uint32(0),
				BackedgeThreshold: 2,
				TraceBudget:       1,
				Stats:             true,
				Safepoint: func() error {
					polls++
					if polls == 2 {
						return errors.New("property trace interrupted")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				function write(o, n) {
					const traceOnlyMarker = {};
					for (let i = 0; i < n; i++) o.a = i;
				}
				const target = { a: -1 };
				let message = "";
				try { write(target, 1000000); } catch (e) { message = e.message; }
				globalThis.jitTraceSafepointMessage = message;
				globalThis.jitTraceSafepointValue = target.a;
			`, "jit-safepoint-property-trace.js")
			if err != nil {
				t.Fatal(err)
			}
			message, err := vm.Global().Get("jitTraceSafepointMessage")
			if err != nil || message.String() != "property trace interrupted" {
				t.Fatalf("message=%v err=%v", message, err)
			}
			value, err := vm.Global().Get("jitTraceSafepointValue")
			lastCompleted, number := value.Float()
			if err != nil || !number || lastCompleted < 0 || lastCompleted >= 999999 {
				t.Fatalf("property=%v err=%v, want a committed partial result", value, err)
			}
			stats := vm.JITStats()
			if stats.TracesCompiled != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 || stats.Errors != 0 {
				t.Fatalf("unexpected trace safepoint stats: %+v", stats)
			}
			if mode == jit.Auto && (stats.NativeTracesCompiled != 1 || stats.NativeTraceYields < 2) {
				t.Fatalf("native trace did not yield through safepoints: %+v", stats)
			}
		})
	}
}

func TestQuickJITSafepointInterruptsRecursion(t *testing.T) {
	polls := 0
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Quick, Threshold: 1, TraceBudget: 5, Stats: true,
		Safepoint: func() error {
			polls++
			if polls == 2 {
				return errors.New("recursion interrupted")
			}
			return nil
		},
	})
	_, err = vm.Eval(`
		function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
		let message = "";
		try { fib(30); } catch (e) { message = e.message; }
		globalThis.jitRecursiveSafepointMessage = message;
	`, "jit-safepoint-recursion.js")
	if err != nil {
		t.Fatal(err)
	}
	message, err := vm.Global().Get("jitRecursiveSafepointMessage")
	if err != nil || message.String() != "recursion interrupted" {
		t.Fatalf("message=%v err=%v", message, err)
	}
	stats := vm.JITStats()
	if stats.Compiled != 1 || stats.SafepointPolls != 2 || stats.Interruptions != 1 || stats.Errors != 0 {
		t.Fatalf("unexpected recursive safepoint stats: %+v", stats)
	}
}

func TestNativeJITSafepointPropagatesOOMRangeError(t *testing.T) {
	engine.ResetOOMState()
	defer engine.ResetOOMState()
	triggered := false
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.oomEnabled = true
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2, TraceBudget: 1, Stats: true,
		Safepoint: func() error {
			if !triggered {
				triggered = true
				engine.TriggerOOMForTest()
			}
			return nil
		},
	})
	_, err = vm.Eval(`
		function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += i;
			return total;
		}
		let message = "";
		try { sum(1000000); } catch (e) { message = e.name + ":" + e.message; }
		globalThis.jitOOMSafepointMessage = message;
	`, "jit-safepoint-oom.js")
	if err != nil {
		t.Fatal(err)
	}
	message, err := vm.Global().Get("jitOOMSafepointMessage")
	if err != nil || !strings.HasPrefix(message.String(), "RangeError:JavaScript heap out of memory") {
		t.Fatalf("message=%v err=%v", message, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeYields < 2 || stats.SafepointPolls != 2 ||
		stats.Interruptions != 1 || stats.Errors != 0 || engine.OOMTriggered() {
		t.Fatalf("unexpected OOM safepoint stats: %+v oom=%t", stats, engine.OOMTriggered())
	}
}

func TestQuickJITSelfRecursive(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 2, Stats: true})
	_, err = vm.Eval(`
		function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
		globalThis.jitFib = fib(12);
	`, "jit-fib.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitFib")
	if err != nil || got.String() != "144" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.Compiled != 1 || stats.Executed == 0 || stats.GuardFailures != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestQuickJITSelfIdentityGuard(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function original(n) { return n < 1 ? 1 : alias(n - 1) + 1; }
		let alias = original;
		original(2);
		alias = function() { return 40; };
		globalThis.jitIdentity = original(2);
	`, "jit-identity.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitIdentity")
	if err != nil || got.String() != "41" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	if vm.JITStats().GuardFailures == 0 {
		t.Fatalf("identity guard did not fail: %+v", vm.JITStats())
	}
}

func TestQuickJITMonomorphicCalleeGuard(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		function add100(x) { return x + 100; }
		let target = add1;
		function wrapper(x) { return target(x) * 2; }
		globalThis.jitCallee1 = wrapper(3);
		target = add10;
		globalThis.jitCallee2 = wrapper(3);
		globalThis.jitCallee3 = wrapper(3);
		target = add100;
		globalThis.jitCallee4 = wrapper(3);
	`, "jit-callee.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitCallee1": "8", "jitCallee2": "26", "jitCallee3": "26", "jitCallee4": "206"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 2 || stats.CalleeInlined != 2 || stats.CalleeExecuted != 3 ||
		stats.CalleePICAdds != 1 || stats.CalleePICHits != 1 || stats.CalleeGuardFailures != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestQuickJITCalleeGuardDisablesAfterRepeatedThirdTarget(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		function add100(x) { return x + 100; }
		function add1000(x) { return x + 1000; }
		let target = add1;
		function wrapper(x) { return target(x) * 2; }
		wrapper(1);
		target = add10; wrapper(1);
		target = add100; wrapper(1);
		target = add100; wrapper(1);
		target = add1000;
		globalThis.jitThirdTarget = wrapper(1);
	`, "jit-callee-third-target.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitThirdTarget")
	if err != nil || got.String() != "2002" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 2 || stats.CalleeGuardFailures != 2 || stats.CalleeExecuted != 2 || stats.CalleeGuardDisabled != 1 {
		t.Fatalf("callee third-target stats: %+v", stats)
	}
	var found bool
	for _, state := range vm.jitStates {
		if state != nil && state.calleeDisabled {
			found = true
			if state.rejected {
				t.Fatalf("callee guard rejected unrelated Quick state: %+v", state)
			}
		}
	}
	if !found {
		t.Fatalf("callee guard was not disabled after repeated third-target failures: %+v", stats)
	}
}

func TestAutoJITNativeInlinesMonomorphicCallee(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Verify: true, Stats: true})
	_, err = vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		let target = add1;
		function wrapper(x) { return target(x) * 2; }
		globalThis.jitNativeInline = wrapper(20);
		target = add10;
		globalThis.jitNativeInlineAlt = wrapper(20);
		target = add1;
		globalThis.jitNativeInlineAgain = wrapper(20);
	`, "jit-native-inline.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitNativeInline": "42", "jitNativeInlineAlt": "60", "jitNativeInlineAgain": "42"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 2 || stats.CalleeInlined != 2 || stats.CalleePICAdds != 1 ||
		stats.NativeCompiled != 2 || stats.NativeExecuted != 3 || stats.Executed != 0 ||
		stats.VerifyChecks != 3 || stats.VerifyFailures != 0 {
		t.Fatalf("callee was not inlined into native code: %+v", stats)
	}
	var picState *quickJITState
	for _, state := range vm.jitStates {
		if state != nil && state.altProgram != nil {
			picState = state
			break
		}
	}
	if picState == nil || !picState.program.HasNative() || !picState.altProgram.HasNative() {
		t.Fatalf("both callee PIC versions are not native: %+v", stats)
	}
	wantBytes := uint64(picState.program.NativeSize() + picState.altProgram.NativeSize())
	if stats.NativeCodeBytes != wantBytes {
		t.Fatalf("native PIC cache bytes=%d want=%d", stats.NativeCodeBytes, wantBytes)
	}
	primary, alternate := picState.program, picState.altProgram
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	if primary.HasNative() || alternate.HasNative() || vm.jitNativeBytes != 0 {
		t.Fatal("reconfigure did not release both native callee PIC versions")
	}
}

func TestAutoJITNativeInlinePreservesArgumentOrder(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function subtract(a, b) { return a - b; }
		let target = subtract;
		function wrapper(x) { return target(x + 1, x * 2); }
		globalThis.jitInlineArgs = wrapper(20);
	`, "jit-native-inline-args.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitInlineArgs")
	if err != nil || got.String() != "-19" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.CalleeInlined != 1 || stats.NativeExecuted != 1 {
		t.Fatalf("multi-argument callee was not inlined: %+v", stats)
	}
}

func TestQuickJITGuardedCalleeFallsBackWhenNotInlineable(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function abs(x) { return x < 0 ? -x : x; }
		let target = abs;
		function wrapper(x) { return target(x) + 1; }
		globalThis.jitDirectCallee = wrapper(-5);
	`, "jit-direct-callee.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitDirectCallee")
	if err != nil || got.String() != "6" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 1 || stats.CalleeInlined != 0 || stats.CalleeExecuted != 1 {
		t.Fatalf("guarded direct callee path was not used: %+v", stats)
	}
}

func TestQuickJITCalleeGuardIsolatesClosureInstances(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		function make(fn) { return function(x) { return fn(x) + 1; }; }
		const first = make(add1);
		const second = make(add10);
		globalThis.jitClosure1 = first(2);
		globalThis.jitClosure2 = second(2);
	`, "jit-callee-instances.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitClosure1": "4", "jitClosure2": "13"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 2 || stats.CalleePICAdds != 1 || stats.CalleeExecuted != 2 || stats.CalleeGuardFailures != 0 {
		t.Fatalf("closure instances shared an unguarded target: %+v", stats)
	}
}

func TestQuickJITCompilesNumericLoopFromBackedge(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1000, BackedgeThreshold: 3, Stats: true})
	_, err = vm.Eval(`
		function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total = total + i;
			return total;
		}
		globalThis.jitLoop = sum(100);
	`, "jit-loop.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitLoop")
	if err != nil || got.String() != "4950" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.Backedges < 3 || stats.Compiled != 1 || stats.Executed == 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestAutoJITRunsNativeLoopFromBackedge(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 3,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += i;
			return total;
		}
		globalThis.jitNativeBackedge = sum(1000);
	`, "jit-native-backedge.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitNativeBackedge")
	if err != nil || got.String() != "499500" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 1 || stats.Executed != 0 {
		t.Fatalf("native loop backedge was not used: %+v", stats)
	}
}

func TestQuickJITSkipsTrivialThisPropertyGetter(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		const o = { v: 7, get() { return this.v; } };
		globalThis.jitProp1 = o.get();
		o.extra = 1;
		globalThis.jitProp2 = o.get();
		o.v = "changed";
		globalThis.jitProp3 = o.get();
	`, "jit-prop.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitProp1": "7", "jitProp2": "7", "jitProp3": "changed"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.Compiled != 0 || stats.Rejected != 1 || stats.Executed != 0 || len(stats.RejectionReasons) != 1 ||
		stats.RejectionReasons[0].Reason != "jit: cost model rejects trivial this-property getter" {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestAutoJITNativeNumericPropertyGuard(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function read(o) { return o.v + 1; }
		const o = { v: 7 };
		globalThis.jitNativeProp1 = read(o);
		o.v = 9;
		globalThis.jitNativeProp2 = read(o);
		o.extra = 1;
		globalThis.jitNativeProp3 = read(o);
		o.v = "changed";
		globalThis.jitNativeProp4 = read(o);
	`, "jit-native-property.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitNativeProp1": "8",
		"jitNativeProp2": "10",
		"jitNativeProp3": "10",
		"jitNativeProp4": "changed1",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 3 || stats.Executed != 0 || stats.GuardFailures != 2 {
		t.Fatalf("unexpected native property stats: %+v", stats)
	}
}

func TestAutoJITNativePropertyGuardDisablesOnlyNativeAfterThirdShape(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function read(o) { return o.v + 1; }
		const first = { v: 1 };
		const second = { prefix: 0, v: 2 };
		const third = { prefix: 0, extra: 0, v: 3 };
		read(first);
		read(second);
		read(third);
		read(third);
		globalThis.jitThirdShape = read(third);
	`, "jit-property-third-shape.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitThirdShape")
	if err != nil || got.String() != "4" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 2 || stats.NativeCodeBytes != 0 || stats.GuardFailures < 2 || stats.NativeGuardDisabled != 1 {
		t.Fatalf("native third-shape fallback stats: %+v", stats)
	}
	var found bool
	for _, state := range vm.jitStates {
		if state != nil && state.nativeDisabled {
			found = true
			if state.rejected {
				t.Fatalf("third shape rejected Quick state: %+v", state)
			}
		}
	}
	if !found {
		t.Fatalf("native guard was not disabled after repeated third-shape failures: %+v", stats)
	}
}

func TestAutoJITNativePropertyLoop(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function sumProps(o, n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += o.a + o.b + o.c;
			return total;
		}
		const o = { a: 1, b: 2, c: 3 };
		globalThis.jitNativePropLoop1 = sumProps(o, 100);
		o.a = 2;
		globalThis.jitNativePropLoop2 = sumProps(o, 100);
		o.extra = 1;
		globalThis.jitNativePropLoop3 = sumProps(o, 10);
		const p = { prefix: 0, a: 3, b: 2, c: 1 };
		globalThis.jitNativePropLoop4 = sumProps(p, 10);
	`, "jit-native-property-loop.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitNativePropLoop1": "600",
		"jitNativePropLoop2": "700",
		"jitNativePropLoop3": "70",
		"jitNativePropLoop4": "60",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 3 || stats.Executed != 1 || stats.GuardFailures != 1 {
		t.Fatalf("unexpected native property-loop stats: %+v", stats)
	}
}

func TestAutoJITVerifiesNativePropertyLoop(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Verify: true, Stats: true})
	_, err = vm.Eval(`
		function sumProps(o, n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += o.a + o.b + o.c;
			return total;
		}
		const o = { a: 1, b: 2, c: 3 };
		globalThis.jitVerifiedPropLoop1 = sumProps(o, 100);
		o.a = 2;
		globalThis.jitVerifiedPropLoop2 = sumProps(o, 100);
	`, "jit-verify-native-property-loop.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitVerifiedPropLoop1": "600",
		"jitVerifiedPropLoop2": "700",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 2 || stats.VerifyChecks != 2 || stats.VerifyFailures != 0 {
		t.Fatalf("native property-loop verification failed: %+v", stats)
	}
}

func TestAutoJITNativePropertyGuardDoesNotInvokeAccessor(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Verify: true, Stats: true})
	_, err = vm.Eval(`
		let getterCalls = 0;
		const o = { get v() { getterCalls++; return 7; } };
		function read(obj) { return obj.v + 1; }
		globalThis.jitAccessorResult = read(o);
		globalThis.jitAccessorCalls = getterCalls;
	`, "jit-native-accessor.js")
	if err != nil {
		t.Fatal(err)
	}
	result, resultErr := vm.Global().Get("jitAccessorResult")
	calls, callsErr := vm.Global().Get("jitAccessorCalls")
	if resultErr != nil || callsErr != nil || result.String() != "8" || calls.String() != "1" {
		t.Fatalf("result=%v resultErr=%v calls=%v callsErr=%v", result, resultErr, calls, callsErr)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled != 1 || stats.NativeExecuted != 0 || stats.GuardFailures != 2 || stats.VerifyChecks != 0 || stats.VerifyFailures != 0 {
		t.Fatalf("accessor was not conservatively guarded: %+v", stats)
	}
}

func TestQuickJITCompilesPropertyLoopTrace(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Quick,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 3,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function sumProps(o, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += o.a + o.b + o.c;
			return total;
		}
		const o = { a: 1, b: 2, c: 3 };
		globalThis.jitTrace1 = sumProps(o, 100);
		o.extra = 4;
		globalThis.jitTrace2 = sumProps(o, 10);
		const p = { prefix: 0, a: 3, b: 2, c: 1 };
		globalThis.jitTrace3 = sumProps(p, 10);
	`, "jit-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"jitTrace1": "600", "jitTrace2": "60", "jitTrace3": "60"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.TracesCompiled != 1 || stats.TracesExecuted != 2 || stats.GuardFailures != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestQuickJITTraceGuardDisablesAfterRepeatedTypeFailure(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Quick, Threshold: ^uint32(0), BackedgeThreshold: 2, Stats: true,
	})
	_, err = vm.Eval(`
		function sumValue(o, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += o.value;
			return total;
		}
		sumValue({ value: 2 }, 12);
		const bad = { value: "x" };
		sumValue(bad, 3);
		sumValue(bad, 3);
		globalThis.jitTraceDisabledResult = sumValue(bad, 3);
	`, "jit-trace-disable.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitTraceDisabledResult")
	if err != nil || got.String() != "0xxx" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.TracesCompiled != 1 || stats.GuardFailures != 2 || stats.TraceGuardDisabled != 1 {
		t.Fatalf("Quick trace guard did not stabilize: %+v", stats)
	}
}

func TestAutoJITNativeTraceGuardDisablesOnlyNativeAfterThirdShape(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2, Stats: true,
	})
	_, err = vm.Eval(`
		function sumValue(o, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += o.value;
			return total;
		}
		sumValue({ value: 1 }, 12);
		sumValue({ prefix: 0, value: 2 }, 12);
		const third = { prefix: 0, extra: 0, value: 3 };
		sumValue(third, 12);
		sumValue(third, 12);
		globalThis.jitNativeTraceDisabledResult = sumValue(third, 12);
	`, "jit-native-trace-disable.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitNativeTraceDisabledResult")
	if err != nil || got.String() != "36" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 2 || stats.NativeTraceGuardDisabled != 1 ||
		stats.NativeCodeBytes != 0 || stats.TracesExecuted < 3 {
		t.Fatalf("Native trace guard did not preserve Quick fallback: %+v", stats)
	}
	var found bool
	for _, state := range vm.jitTraces {
		if state != nil && state.nativeDisabled {
			found = true
			if state.rejected {
				t.Fatalf("Native trace guard rejected Quick trace: %+v", state)
			}
		}
	}
	if !found {
		t.Fatalf("Native trace state was not disabled: %+v", stats)
	}
}

func TestAutoJITNativeTraceGuardsLocalObjectProperty(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		Verify:            true,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function sumAlias(o, n) {
			const marker = {};
			const alias = o;
			let total = 0;
			for (let i = 0; i < n; i++) total += alias.value;
			return total;
		}
		const first = { value: 2 };
		globalThis.jitNativeTraceProp1 = sumAlias(first, 20);
		first.extra = 1;
		globalThis.jitNativeTraceProp2 = sumAlias(first, 20);
		const third = { prefix: 0, value: 3 };
		globalThis.jitNativeTraceProp3 = sumAlias(third, 10);
	`, "jit-native-trace-property.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitNativeTraceProp1": "40",
		"jitNativeTraceProp2": "40",
		"jitNativeTraceProp3": "30",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 2 || stats.VerifyChecks != 2 ||
		stats.VerifyFailures != 0 || stats.GuardFailures == 0 {
		t.Fatalf("unexpected native property-trace stats: %+v", stats)
	}
}

func TestAutoJITNativePropertyWriteTrace(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		TraceBudget:       3,
		Verify:            true,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function setLoop(o, n) {
			const marker = {};
			for (let i = 0; i < n; i++) o.a = i;
			return o.a;
		}
		const first = { a: 0 };
		globalThis.jitNativeSet1 = setLoop(first, 20);
		first.extra = 1;
		globalThis.jitNativeSet2 = setLoop(first, 10);
		const third = { prefix: 0, a: 0 };
		globalThis.jitNativeSet3 = setLoop(third, 7);
		third.a = "changed";
		globalThis.jitNativeSet4 = setLoop(third, 5);
	`, "jit-native-trace-property-write.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitNativeSet1": "19",
		"jitNativeSet2": "9",
		"jitNativeSet3": "6",
		"jitNativeSet4": "4",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 2 || stats.TracesExecuted == 0 ||
		stats.GuardFailures < 2 || stats.NativeTraceGuardDisabled != 1 || stats.NativeCodeBytes != 0 ||
		stats.VerifyChecks != 2 || stats.VerifyFailures != 0 {
		t.Fatalf("unexpected native property-write stats: %+v", stats)
	}
}

func TestJITPropertyWriteTracePreservesSetterAndProxy(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			vm.ConfigureJIT(jit.Config{
				Mode:              mode,
				Threshold:         ^uint32(0),
				BackedgeThreshold: 2,
				Stats:             true,
			})
			_, err = vm.Eval(`
				function write(o, n) {
					const marker = {};
					for (let i = 0; i < n; i++) o.a = i;
				}
				let setterCalls = 0;
				const accessor = { set a(v) { setterCalls++; } };
				write(accessor, 9);
				let proxyCalls = 0;
				const proxy = new Proxy({ a: 0 }, {
					set(target, key, value) { proxyCalls++; target[key] = value; return true; }
				});
				write(proxy, 8);
				globalThis.jitSetterCalls = setterCalls;
				globalThis.jitProxyCalls = proxyCalls;
			`, "jit-property-write-effects.js")
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"jitSetterCalls": "9", "jitProxyCalls": "8"} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			if stats := vm.JITStats(); stats.TracesCompiled != 1 || stats.TracesExecuted != 0 || stats.GuardFailures < 2 {
				t.Fatalf("effectful property writes were not guarded: %+v", stats)
			}
		})
	}
}

func TestAutoJITPropertyWriteTraceRejectsAliasedNativeInputs(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		TraceBudget:       3,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function sumWrites(o, n) {
			const marker = {};
			const alias = o;
			let total = 0;
			for (let i = 0; i < n; i++) {
				o.a = o.a + 1;
				total += alias.a;
			}
			return total;
		}
		globalThis.jitAliasedWrite = sumWrites({ a: 0 }, 20);
	`, "jit-property-write-alias.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitAliasedWrite")
	if err != nil || got.String() != "210" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 0 || stats.TracesExecuted == 0 || stats.GuardFailures == 0 {
		t.Fatalf("aliased native property inputs were not rejected: %+v", stats)
	}
}

func TestQuickJITTraceYieldsAtSafepointBudget(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Quick,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		TraceBudget:       4,
		Stats:             true,
	})
	_, err = vm.Eval(`
		function sumProps(o, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += o.value;
			return total;
		}
		globalThis.jitYieldResult = sumProps({ value: 2 }, 41);
	`, "jit-yield.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("jitYieldResult")
	if err != nil || got.String() != "82" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.TracesCompiled != 1 || stats.TraceYields == 0 || stats.GuardFailures != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestJITTraceRestoresTwoDistinctDeoptExits(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config jit.Config
		native bool
	}{
		{name: "quick", config: jit.Config{Mode: jit.Quick, Threshold: ^uint32(0), BackedgeThreshold: 2, Stats: true}},
		{name: "native", config: jit.Config{Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2, TraceBudget: 1, Verify: true, Stats: true}, native: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stats := runTwoExitTrace(t, tt.config)
			byBackedge := make(map[int][]jit.DeoptStat)
			for _, exit := range stats.DeoptExits {
				byBackedge[exit.BackedgePC] = append(byBackedge[exit.BackedgePC], exit)
			}
			found := false
			for _, exits := range byBackedge {
				if len(exits) == 2 && exits[0].ExitID == 0 && exits[1].ExitID == 1 &&
					exits[0].ResumePC != exits[1].ResumePC && exits[0].Count > 0 && exits[1].Count > 0 {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("no trace executed two distinct exits: %+v", stats)
			}
			if tt.native && (stats.NativeTracesCompiled == 0 || stats.NativeTracesExecuted == 0 ||
				stats.NativeTraceYields == 0 || stats.VerifyChecks == 0 || stats.VerifyFailures != 0) {
				t.Fatalf("native multi-exit trace was not verified: %+v", stats)
			}
		})
	}
}

func TestJITTraceRestoresOperandStackIntoVM(t *testing.T) {
	var code []byte
	emit := func(op bytecode.Opcode, operand uint32) {
		code = append(code, byte(op), byte(operand>>16), byte(operand>>8), byte(operand))
	}
	emit(bytecode.OpLoadLocal, 1)
	emit(bytecode.OpJmpFalseKeep, 16)
	emit(bytecode.OpPushInt, 7)
	emit(bytecode.OpStoreLocal, 2)
	emit(bytecode.OpJmp, (1<<24)-20)
	emit(bytecode.OpReturnUndef, 0)
	emit(bytecode.OpStoreLocal, 2)
	emit(bytecode.OpReturnUndef, 0)
	tmpl := &bytecode.FuncTemplate{
		Name: "stackExit", NumParams: 2, NumLocals: 3, ArgumentsSlot: 3,
		NoArgumentsObject: true, Code: code,
	}
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 1,
				TraceBudget: 1, Verify: mode == jit.Auto, Stats: true,
			})
			negativeZero := math.Copysign(0, -1)
			vm.stack = []engine.Value{engine.Undefined(), engine.Number(negativeZero), engine.Number(0)}
			frame := &vmFrame{tmpl: tmpl, base: 0}
			exitPC, ok, err := vm.tryQuickTrace(frame, 0, 16)
			if err != nil || !ok || exitPC != 24 || len(vm.stack) != tmpl.NumLocals+1 {
				t.Fatalf("exitPC=%d ok=%v err=%v stack=%v", exitPC, ok, err, vm.stack)
			}
			restored, _ := vm.stack[len(vm.stack)-1].Float()
			if math.Float64bits(restored) != math.Float64bits(negativeZero) {
				t.Fatalf("restored=%v bits=%x want -0 bits=%x", restored, math.Float64bits(restored), math.Float64bits(negativeZero))
			}
			vm.stack[2] = vm.pop()
			stored, _ := vm.stack[2].Float()
			if math.Float64bits(stored) != math.Float64bits(negativeZero) {
				t.Fatalf("resumed store lost -0: %v bits=%x", stored, math.Float64bits(stored))
			}
			stats := vm.JITStats()
			if stats.TracesCompiled != 1 || mode == jit.Quick && stats.TracesExecuted != 1 ||
				mode == jit.Auto && (stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 1 ||
					stats.VerifyChecks != 1 || stats.VerifyFailures != 0) {
				t.Fatalf("unexpected stats: %+v", stats)
			}
		})
	}
}

func TestJITTraceDeoptStackPreservesSideEffectBeforeThrow(t *testing.T) {
	var code []byte
	emit := func(op bytecode.Opcode, operand uint32) {
		code = append(code, byte(op), byte(operand>>16), byte(operand>>8), byte(operand))
	}
	emit(bytecode.OpLoadLocal, 1)
	emit(bytecode.OpJmpFalseKeep, 16)
	emit(bytecode.OpPushInt, 7)
	emit(bytecode.OpStoreLocal, 2)
	emit(bytecode.OpJmp, (1<<24)-20)
	emit(bytecode.OpReturnUndef, 0)
	emit(bytecode.OpStoreLocal, 2)
	emit(bytecode.OpLoadGlobal, 0)
	emit(bytecode.OpCall, 0)
	emit(bytecode.OpReturnUndef, 0)
	tmpl := &bytecode.FuncTemplate{
		Name: "stackExitThrow", NumParams: 2, NumLocals: 3, ArgumentsSlot: 3,
		NoArgumentsObject: true, Code: code, Constants: []engine.Value{engine.Str("jitThrowOnce")},
	}

	negativeZero := math.Copysign(0, -1)
	for _, valueCase := range []struct {
		name  string
		value engine.Value
	}{
		{name: "negative-zero", value: engine.Number(negativeZero)},
		{name: "empty-string", value: engine.Str("")},
		{name: "zero-bigint", value: engine.BigIntZero()},
	} {
		for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
			t.Run(valueCase.name+"/"+mode.String(), func(t *testing.T) {
				vm, err := NewVM()
				if err != nil {
					t.Fatal(err)
				}
				defer vm.Close()
				vm.ConfigureJIT(jit.Config{
					Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 1,
					TraceBudget: 1, Verify: mode == jit.Auto, Stats: true,
				})

				calls := 0
				if err := vm.RegisterFunc("jitThrowOnce", func([]engine.Value) (engine.Value, error) {
					calls++
					return engine.Undefined(), errors.New("deopt side effect boom")
				}); err != nil {
					t.Fatal(err)
				}

				vm.stack = []engine.Value{engine.Undefined(), valueCase.value, engine.Number(9)}
				vm.frames = []vmFrame{{tmpl: tmpl, base: 0}}
				frame := vm.cur()
				exitPC, ok, err := vm.tryQuickTrace(frame, 0, 16)
				if err != nil || !ok || exitPC != 24 || len(vm.stack) != tmpl.NumLocals+1 {
					t.Fatalf("exitPC=%d ok=%v err=%v stack=%v", exitPC, ok, err, vm.stack)
				}
				frame.pc = exitPC
				if _, err := vm.run(); err == nil || !strings.Contains(err.Error(), "deopt side effect boom") {
					t.Fatalf("run err=%v", err)
				}
				if calls != 1 {
					t.Fatalf("side effect calls=%d want=1", calls)
				}
				if stored := vm.stack[2]; !sameJITValue(stored, valueCase.value) {
					t.Fatalf("stored=%v type=%s want=%v type=%s", stored, stored.Type(), valueCase.value, valueCase.value.Type())
				}

				stats := vm.JITStats()
				if stats.TracesCompiled != 1 || stats.VerifyFailures != 0 || mode == jit.Quick && stats.TracesExecuted != 1 ||
					mode == jit.Auto && valueCase.value.Type() == engine.TypeNumber &&
						(stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 1 || stats.VerifyChecks != 1) ||
					mode == jit.Auto && valueCase.value.Type() != engine.TypeNumber &&
						(stats.NativeTracesCompiled != 1 || stats.NativeTracesExecuted != 0 || stats.TracesExecuted != 1 || stats.GuardFailures != 1) {
					t.Fatalf("unexpected stats: %+v", stats)
				}
			})
		}
	}
}

func runTwoExitTrace(t *testing.T, config jit.Config) jit.Stats {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(config)
	_, err = vm.Eval(`
		function route(n, mode) {
			const traceOnlyMarker = {};
			let total = 0;
			outer:
			for (let i = 0; i < n; i++) {
				for (let j = 0; j < 4; j++) {
					total += j;
					if (mode === 1) {
						if (j === 1) continue outer;
					}
				}
				total += 100;
			}
			return total;
		}
		globalThis.jitMultiExitContinue = route(5, 1);
		globalThis.jitMultiExitNormal = route(2, 0);
	`, "jit-multi-exit-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"jitMultiExitContinue": "5",
		"jitMultiExitNormal":   "212",
	} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}

	return vm.JITStats()
}
