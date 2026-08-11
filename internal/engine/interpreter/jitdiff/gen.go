package jitdiff

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// harnessHead declares the shared value-domain constants and the canonical
// serializer SV. Every generated case records its observable behavior into
// EV: "call" events around top-level invocations, "throw" events in catch
// blocks, and "post" events carrying the observable state of side-effected
// objects. The event log is the comparison oracle across tiers.
//
// The generated hot functions must NOT contain LOG calls (those would change
// the JIT-visible bytecode); side effects inside JIT regions are instead
// observed through the "post" events read after the computation completes.
const harnessHead = `const OBJ_A = {};
const OBJ_B = {};
const SYM1 = Symbol("k1");
const SYM2 = Symbol("k2");
function SV(v) {
  if (v === null) return "null";
  if (v === undefined) return "undefined";
  const t = typeof v;
  if (t === "number") {
    if (v !== v) return "NaN";
    if (v === 0 && 1 / v < 0) return "-0";
    return "n:" + v;
  }
  if (t === "boolean") return "b:" + v;
  if (t === "string") return "s:" + v;
  if (t === "bigint") return "bi:" + String(v);
  if (t === "symbol") return "sym:" + String(v);
  if (v === OBJ_A) return "obj:A";
  if (v === OBJ_B) return "obj:B";
  return "obj:?";
}
const EV = [];
globalThis.JITDIFF_RESULT = "";
function LOG(n, d) {
  EV.push(n + ":" + d);
  globalThis.JITDIFF_RESULT = EV.join("\n");
}
`

// harnessTail serializes the event log into one global string. Separator is
// "\n": generated string values never contain newlines, so the encoding is
// unambiguous.
const harnessTail = `
globalThis.JITDIFF_RESULT = EV.join("\n");
`

// numberLeaves cover the R1-2 Number domain: finite values, -0, NaN,
// ±Infinity, subnormals, huge magnitudes and values beyond 2^32 / 2^53.
var numberLeaves = []string{
	"0", "-0", "1", "-1", "2", "-2", "3", "0.5", "-0.5", "3.5", "-2.25",
	"1e-320", "1e308", "-1e308", "NaN", "Infinity", "-Infinity",
	"2147483648", "-2147483649", "4294967295", "9007199254740993",
}

var stringLeaves = []string{`""`, `"a"`, `"ab"`, `"x"`, `"2"`}
var bigintLeaves = []string{"0n", "1n", "7n", "8n"}

// valueLeaf returns a JS expression for a value from the R1-2 domain.
func (g *Generator) valueLeaf(rng *rand.Rand) string {
	switch rng.Intn(12) {
	case 0, 1, 2, 3, 4:
		return g.numberLeaf(rng)
	case 5:
		if rng.Intn(2) == 0 {
			return "true"
		}
		return "false"
	case 6:
		if rng.Intn(2) == 0 {
			return "null"
		}
		return "undefined"
	case 7:
		return stringLeaves[rng.Intn(len(stringLeaves))]
	case 8:
		return bigintLeaves[rng.Intn(len(bigintLeaves))]
	case 9:
		if rng.Intn(2) == 0 {
			return "OBJ_A"
		}
		return "OBJ_B"
	case 10:
		if rng.Intn(2) == 0 {
			return "SYM1"
		}
		return "SYM2"
	default:
		return g.numberLeaf(rng)
	}
}

// numberLeaf returns a Number from the edge-value table.
func (g *Generator) numberLeaf(rng *rand.Rand) string {
	return numberLeaves[rng.Intn(len(numberLeaves))]
}

func (g *Generator) cmpOp(rng *rand.Rand) string {
	ops := []string{"<", "<=", ">", ">=", "==", "!=", "===", "!=="}
	return ops[rng.Intn(len(ops))]
}

func (g *Generator) logicalOp(rng *rand.Rand) string {
	ops := []string{"&&", "||", "??"}
	return ops[rng.Intn(len(ops))]
}

func (g *Generator) bitOp(rng *rand.Rand) string {
	ops := []string{"&", "|", "^", "<<", ">>", ">>>"}
	return ops[rng.Intn(len(ops))]
}

func (g *Generator) unaryOp(rng *rand.Rand) string {
	ops := []string{"!", "~", "-", "+"}
	return ops[rng.Intn(len(ops))]
}

// genExpr builds a nested expression over the value domain. Every binary
// operand is parenthesized so unary-minus atoms can never sit directly left
// of `**` (which is a JS syntax error) and precedence is fully explicit.
func (g *Generator) genExpr(rng *rand.Rand, depth int) string {
	if depth <= 0 {
		return "(" + g.valueLeaf(rng) + ")"
	}
	switch rng.Intn(10) {
	case 0:
		return "(" + g.genExpr(rng, depth-1) + " + " + g.genExpr(rng, depth-1) + ")"
	case 1:
		return "(" + g.genExpr(rng, depth-1) + " - " + g.genExpr(rng, depth-1) + ")"
	case 2:
		return "(" + g.genExpr(rng, depth-1) + " * " + g.genExpr(rng, depth-1) + ")"
	case 3:
		return "(" + g.genExpr(rng, depth-1) + " / " + g.genExpr(rng, depth-1) + ")"
	case 4:
		return "(" + g.genExpr(rng, depth-1) + " % " + g.genExpr(rng, depth-1) + ")"
	case 5:
		return "(" + g.genExpr(rng, depth-1) + " ** " + g.genExpr(rng, depth-1) + ")"
	case 6:
		return "(" + g.genExpr(rng, depth-1) + " " + g.cmpOp(rng) + " " + g.genExpr(rng, depth-1) + ")"
	case 7:
		return "(" + g.genExpr(rng, depth-1) + " " + g.logicalOp(rng) + " " + g.genExpr(rng, depth-1) + ")"
	case 8:
		return "(" + g.genExpr(rng, depth-1) + " " + g.bitOp(rng) + " " + g.genExpr(rng, depth-1) + ")"
	default:
		return "(" + g.unaryOp(rng) + g.genExpr(rng, depth-1) + ")"
	}
}

// tryLog returns the two-statement "call; return" or the "throw" catch block
// used by every generated case so each top-level invocation is logged and any
// exception is recorded with its name and message.
func tryLog(id int, call string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "try { LOG(\"call\", \"%s\"); LOG(\"return\", SV(%s)); } catch (e) { LOG(\"throw\", e.name + \":\" + e.message); }\n", callID(id), call)
	return b.String()
}

func callID(id int) string {
	return "k" + strconv.Itoa(id)
}

// Generator produces cases deterministically from a suite seed.
type Generator struct {
	seed   int64
	params Params
	rng    *rand.Rand
}

// NewGenerator returns a deterministic generator. The same seed and params
// always produce the same case sequence.
func NewGenerator(seed int64, params Params) *Generator {
	params = params.Normalized()
	return &Generator{seed: seed, params: params, rng: rand.New(rand.NewSource(seed))}
}

// Generate produces count cases in a deterministic order. Each case carries
// its own derived seed so a single failing case can be reproduced alone.
func (g *Generator) Generate(count int) []*Case {
	cases := make([]*Case, 0, count)
	for id := 0; id < count; id++ {
		caseSeed := g.rng.Int63()
		cases = append(cases, g.genCase(id, caseSeed))
	}
	return cases
}

func (g *Generator) genCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed))
	kind := AllKinds[rng.Intn(KindCount)]
	switch kind {
	case KindExpr:
		return g.genExprCase(id, seed)
	case KindBranch:
		return g.genBranchCase(id, seed)
	case KindLoop:
		return g.genLoopCase(id, seed)
	case KindStrictEq:
		return g.genStrictEqCase(id, seed)
	case KindLooseEq:
		return g.genLooseEqCase(id, seed)
	case KindPropRead:
		return g.genPropReadCase(id, seed)
	case KindPropWrite:
		return g.genPropWriteCase(id, seed)
	case KindPush:
		return g.genPushCase(id, seed)
	case KindClosure:
		return g.genClosureCase(id, seed)
	case KindCall:
		return g.genCallCase(id, seed)
	case KindGetter:
		return g.genGetterCase(id, seed)
	case KindCallbackThrow:
		return g.genCallbackThrowCase(id, seed)
	case KindProxy:
		return g.genProxyCase(id, seed)
	case KindDeoptPrefix:
		return g.genDeoptPrefixCase(id, seed)
	case KindBigIntDivZero:
		return g.genBigIntDivZeroCase(id, seed)
	case KindGetterSetterThrow:
		return g.genGetterSetterThrowCase(id, seed)
	case KindOOM:
		return g.genOOMCase(id, seed)
	case KindCancel:
		return g.genCancelCase(id, seed)
	case KindGuardMutation:
		return g.genGuardMutationCase(id, seed)
	case KindStringOps:
		return g.genStringOpsCase(id, seed)
	case KindBigIntArith:
		return g.genBigIntArithCase(id, seed)
	case KindBigIntBitwise:
		return g.genBigIntBitwiseCase(id, seed)
	case KindBigIntCompare:
		return g.genBigIntCompareCase(id, seed)
	case KindTernary:
		return g.genTernaryCase(id, seed)
	case KindSwitch:
		return g.genSwitchCase(id, seed)
	case KindShortCircuit:
		return g.genShortCircuitCase(id, seed)
	case KindArrayIndex:
		return g.genArrayIndexCase(id, seed)
	case KindArrayBatch:
		return g.genArrayBatchCase(id, seed)
	case KindArrayCb:
		return g.genArrayCbCase(id, seed)
	case KindNativeMod:
		return g.genNativeModCase(id, seed)
	case KindNativeBitwise:
		return g.genNativeBitwiseCase(id, seed)
	default:
		return g.genSafepointCase(id, seed)
	}
}

// genNativeModCase generates the R4-7 % shapes: an all-Number leaf called
// with number pairs (the amd64 Native tier executes fmod via the x87 FPREM
// loop) and a string-coercion call that must guard back to Tier 0 with the
// identical result.
func (g *Generator) genNativeModCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xA7))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b) { return a %% b; }
`, fn)
	for i := 0; i < 3; i++ {
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, g.numberLeaf(rng), g.numberLeaf(rng))))
	}
	// Negative: string operands coerce to Numbers in Tier 0; the JIT must
	// guard back and reproduce the coercion exactly.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, stringLeaves[rng.Intn(len(stringLeaves))], g.numberLeaf(rng))))
	return g.build(id, KindNativeMod, seed, b.String())
}

// genNativeBitwiseCase generates the R4-7 bitwise shapes: an all-Number leaf
// mixing ^ << | >>> ~ (the amd64 Native tier executes ES ToInt32) and a
// mixed-type call that must guard back to Tier 0.
func (g *Generator) genNativeBitwiseCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xB17))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b, n) { return ((a ^ b) << (n & 31)) | ~(a >>> (n & 31)); }
`, fn)
	for i := 0; i < 3; i++ {
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s)", fn, g.numberLeaf(rng), g.numberLeaf(rng), g.numberLeaf(rng))))
	}
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s)", fn, g.numberLeaf(rng), bigintLeaves[rng.Intn(len(bigintLeaves))], g.numberLeaf(rng))))
	return g.build(id, KindNativeBitwise, seed, b.String())
}

func (g *Generator) loopBound(rng *rand.Rand) int {
	return 2 + rng.Intn(g.params.MaxLoopBound-1)
}

func (g *Generator) build(id int, kind Kind, seed int64, body string) *Case {
	c := &Case{ID: id, Kind: kind, Seed: seed, Params: g.params, Body: body}
	c.Source = harnessHead + "\n" + body + "\n" + harnessTail
	return c
}

func (g *Generator) genExprCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE1))
	expr := g.genExpr(rng, g.params.MaxExprDepth)
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b, c) { return %s; }\n", fn, expr)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s)", fn, g.valueLeaf(rng), g.valueLeaf(rng), g.valueLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s)", fn, g.valueLeaf(rng), g.valueLeaf(rng), g.valueLeaf(rng))))
	return g.build(id, KindExpr, seed, b.String())
}

func (g *Generator) genBranchCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE2))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b, n) {
  let r = 0;
  for (let i = 0; i < n; i++) {
    if (a === b) { r += 1; } else if (a > b) { r += 2; } else { r += 3; }
    if (a) { r += 4; }
  }
  return r;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.valueLeaf(rng), g.valueLeaf(rng), n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.valueLeaf(rng), g.valueLeaf(rng), n)))
	return g.build(id, KindBranch, seed, b.String())
}

func (g *Generator) genLoopCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE3))
	fn := callID(id)
	a := g.numberLeaf(rng)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, n) { let s = 0; for (let i = 0; i < n; i++) { s = s + a; } return s; }
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %d)", fn, a, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %d)", fn, g.numberLeaf(rng), n)))
	return g.build(id, KindLoop, seed, b.String())
}

// genStrictEqCase uses controlled value pairs so the R1-2 / R3-2 identity
// semantics (NaN !== NaN, +0 === -0, BigInt value equality, Symbol identity,
// object identity, Symbol never equal to any other type with no coercion) are
// exercised deterministically.
func (g *Generator) genStrictEqCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE4))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b) { return a === b; }\n", fn)
	pairs := [][2]string{
		{"0", "0"}, {"0", "-0"}, {"NaN", "NaN"}, {"1", "2"},
		{`"a"`, `"a"`}, {`"a"`, `"b"`},
		{"7n", "7n"}, {"7n", "8n"}, {"7n", "7"},
		{"true", "true"}, {"true", "1"},
		{"null", "null"}, {"null", "undefined"},
		{"OBJ_A", "OBJ_A"}, {"OBJ_A", "OBJ_B"},
		{"SYM1", "SYM1"}, {"SYM1", "SYM2"},
		// R3-2: a Symbol is never strictly equal to any other type — no
		// coercion, not even against an equal-looking description.
		{"SYM1", `"a"`}, {"SYM1", "7"}, {"SYM1", "7n"}, {"SYM1", "null"},
		{"SYM1", "undefined"}, {"SYM1", "true"}, {"SYM1", "OBJ_A"},
	}
	start := rng.Intn(len(pairs))
	for i := 0; i < 4; i++ {
		pair := pairs[(start+i)%len(pairs)]
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, pair[0], pair[1])))
	}
	return g.build(id, KindStrictEq, seed, b.String())
}

// genLooseEqCase exercises == across the primitive value domain, which R3-3
// executes in Quick: Number/String/BigInt/Boolean/null/undefined/Symbol
// pairs with JS semantics, plus object pairs (and Symbol-vs-primitive) that
// must guard back to Tier 0. Pairs that diverge from Tier 0's looseEquals are
// deliberately absent: "" / whitespace-only / 0x/0o strings vs Number or
// Boolean, and BigInt outside {0n, 1n} vs true (recorded Tier 0 bugs, see the
// package doc); the JIT also guards those inputs at runtime until Tier 0 is
// fixed.
func (g *Generator) genLooseEqCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE5))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b) { return a == b; }\n", fn)
	pairs := [][2]string{
		{"1", "1"}, {"1", `"1"`}, {"0", `"0"`}, {"null", "undefined"},
		{"null", "0"}, {"undefined", "0"}, {"true", "1"}, {"false", "0"},
		{`"a"`, `"a"`}, {`"a"`, `"b"`}, {"7n", "7"}, {"7n", `"7"`},
		{"7n", "7n"}, {"7n", "8n"}, {"8n", "8"}, {"NaN", "NaN"},
		{"0", "-0"}, {"Infinity", "Infinity"}, {"1", "2"},
		{`"2"`, "2"}, {`"a"`, "1"}, {"true", "2"}, {"1n", "true"},
		{"SYM1", "SYM1"}, {"SYM1", "SYM2"}, {"SYM1", "7"}, {"SYM1", `"a"`},
		{"SYM1", "null"}, {"SYM1", "undefined"}, {"SYM1", "true"}, {"SYM1", "7n"},
		// Object operands must guard back to Tier 0 (identity semantics).
		{"OBJ_A", "OBJ_A"}, {"OBJ_A", "OBJ_B"}, {"OBJ_A", "1"}, {"OBJ_A", "null"},
	}
	start := rng.Intn(len(pairs))
	for i := 0; i < 4; i++ {
		pair := pairs[(start+i)%len(pairs)]
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, pair[0], pair[1])))
	}
	return g.build(id, KindLooseEq, seed, b.String())
}

// genPropReadCase exercises the R4-3 property PIC: the first two calls warm up
// two shapes, the middle rotation proves the baseline is stable (each of the
// first two shapes is observed at least three times), then the third and
// fourth shapes arrive twice each and are absorbed by the extended guard.
// The final four-shape rotation keeps every shape hot. All reads are pure, so
// the event log is identical across tiers no matter how the guard decides.
func (g *Generator) genPropReadCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE6))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a + o.b; } return s; }
const O = { a: %s, b: %s };
const O2 = { a: %s, b: %s, c: 1 };
const O3 = { a: %s, b: %s, c: 2, d: 3 };
const O4 = { a: %s, b: %s, d: 4, e: 5, f: 6 };
`, fn, g.numberLeaf(rng), g.numberLeaf(rng), g.numberLeaf(rng), g.numberLeaf(rng),
		g.numberLeaf(rng), g.numberLeaf(rng), g.numberLeaf(rng), g.numberLeaf(rng))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	// Stable two-shape rotation: each baseline shape accumulates >= 3 hits.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	// Third and fourth shapes arrive twice each (confirm + absorb).
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O3, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O3, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O4, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O4, %d)", fn, n)))
	// Four-shape rotation keeps every absorbed shape hot.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O3, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O4, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(O.a) + \",\" + SV(O.b));\n"))
	return g.build(id, KindPropRead, seed, b.String())
}

// genPropWriteCase exercises the R4-3 property-write PIC: a stable two-shape
// rotation is followed by a third shape observed twice, then a rotation back.
// Writes stay inside the JIT function; the observable state is read only in
// the post event, so every tier must commit the identical values.
func (g *Generator) genPropWriteCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE7))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { for (let i = 0; i < n; i++) { o.a = i; } return o.a; }
const O = { a: 0 };
const O2 = { a: 0, b: 1 };
const O3 = { a: 0, c: 2 };
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O3, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O3, %d)", fn, n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(O.a) + \",\" + SV(O2.a) + \",\" + SV(O3.a));\n"))
	return g.build(id, KindPropWrite, seed, b.String())
}

func (g *Generator) genPushCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE8))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(arr, start, end) { for (let i = start; i < end; i++) arr.push(i); return arr.length; }
const A = [];
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(A, 0, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(A.length) + \":\" + SV(A[%d]));\n", n-1))
	return g.build(id, KindPush, seed, b.String())
}

// genClosureCase is the R4-2 generative shape. Besides the single-upvalue
// increment kernel it now covers: a closure with multiple numeric upvalues
// read and written in order (`() => { a++; b += a; return b; }`), a read-only
// capture (`() => a + b`), and an in-frame (non-escaping) closure created
// inside the loop function. Each shape must run on the closure fast path in
// Quick/Auto and fall back to Tier 0 identically when the captured cells are
// non-numeric.
func (g *Generator) genClosureCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE9))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function make%s() { let n = 0; return () => ++n; }
function run%s(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const C%s = make%s();
`, fn, fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("run%s(C%s, %d)", fn, fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(C%s()));\n", fn))
	// R4-2: multi-upvalue read/write closure (own loop template so the fast
	// path is exercised per shape, not shadowed by the increment kernel).
	fmt.Fprintf(&b, `function makeM%s() { let a = %s; let b = %s; return () => { a++; b += a; return b; }; }
function runM%s(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const CM%s = makeM%s();
`, fn, g.numberLeaf(rng), g.numberLeaf(rng), fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("runM%s(CM%s, %d)", fn, fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(CM%s()));\n", fn))
	// R4-2: read-only capture (own loop template).
	fmt.Fprintf(&b, `function makeR%s() { let a = %s; let b = %s; return () => a + b; }
function runR%s(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const CR%s = makeR%s();
`, fn, g.numberLeaf(rng), g.numberLeaf(rng), fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("runR%s(CR%s, %d)", fn, fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(CR%s()));\n", fn))
	// R4-2: in-frame (non-escaping) closure.
	fmt.Fprintf(&b, `function runF%s(end) { let acc = %s; const inc = () => ++acc; let sum = 0; for (let i = 0; i < end; i++) sum += inc(); return sum; }
`, fn, g.numberLeaf(rng))
	b.WriteString(tryLog(id, fmt.Sprintf("runF%s(%d)", fn, n)))
	return g.build(id, KindClosure, seed, b.String())
}

func (g *Generator) genCallCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xEA))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	// R4-1: zero-argument leaf called through a single-upvalue wrapper.
	fmt.Fprintf(&b, `function zero%s() { return %s; }
function c0%s() { return zero%s(); }
`, fn, g.numberLeaf(rng), fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("c0%s()", fn)))
	// 1-arg leaf (R1-2 shape).
	fmt.Fprintf(&b, `function leaf%s(x) { return x + %s; }
function run%s(n) { let s = 0; for (let i = 0; i < n; i++) s += leaf%s(i); return s; }
`, fn, g.numberLeaf(rng), fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("run%s(%d)", fn, n)))
	// R4-1: four-argument leaf with two call sites in one body (argument
	// order and multi-site inlining).
	fmt.Fprintf(&b, `function leaf4%s(a, b, c, d) { return (a + b) * (c - d) + %s; }
function run4%s(n) { let s = 0; for (let i = 0; i < n; i++) { s += leaf4%s(i, i + 1, 10, 3); s += leaf4%s(i + 1, i, 5, 2); } return s; }
`, fn, g.numberLeaf(rng), fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("run4%s(%d)", fn, n)))
	// R4-1: boolean-returning leaf feeding a branch.
	fmt.Fprintf(&b, `function leafB%s(x) { return x > %s; }
function runB%s(n) { let c = 0; for (let i = 0; i < n; i++) { if (leafB%s(i)) c++; } return c; }
`, fn, g.numberLeaf(rng), fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("runB%s(%d)", fn, n)))
	// A non-inlineable callee (string concat) must fall back cleanly.
	fmt.Fprintf(&b, `function leafS%s(x) { return "s" + x; }
function runS%s(n) { let s = 0; for (let i = 0; i < n; i++) s += leafS%s(i); return s; }
`, fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("runS%s(%d)", fn, n)))
	return g.build(id, KindCall, seed, b.String())
}

func (g *Generator) genGetterCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { let s = 0; for (let i = 0; i < n; i++) s += o.v; return s; }
const O = { _v: 3, get v() { LOG("get", SV(this._v)); return this._v; }, set v(x) { LOG("set", SV(x)); this._v = x; } };
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, 4)", fn)))
	b.WriteString("O.v = 5;\n")
	b.WriteString("LOG(\"post\", SV(O._v));\n")
	// A throwing getter exercises the getter-throw fallback path.
	b.WriteString(fmt.Sprintf(`const O2 = { get v() { throw new RangeError("boom-get"); } };
`))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, 3)", fn)))
	return g.build(id, KindGetter, seed, b.String())
}

func (g *Generator) genCallbackThrowCase(id int, seed int64) *Case {
	var b strings.Builder
	b.WriteString(`const A = [1, 2, 3, 4, 5];
`)
	b.WriteString(tryLog(id, `A.map(function(x) { if (x === 3) throw new TypeError("cb-throw"); return x * 2; }).length`))
	// A pure numeric arrow still hits the NativeCallback fast path.
	b.WriteString(tryLog(id, `A.map(x => x * 2).length`))
	b.WriteString(tryLog(id, `A.filter(x => x % 2 === 0).length`))
	return g.build(id, KindCallbackThrow, seed, b.String())
}

func (g *Generator) genProxyCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xED))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `const P = new Proxy({ a: %s }, { get(t, k) { LOG("pget", String(k)); return t[k]; } });
function %s(o, n) { let s = 0; for (let i = 0; i < n; i++) s += o.a; return s; }
`, g.numberLeaf(rng), fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(P, 3)", fn)))
	return g.build(id, KindProxy, seed, b.String())
}

// genDeoptPrefixCase runs a property-write loop long enough that the small
// trace budget forces repeated safepoint yields; the final accumulated result
// proves no write was lost or duplicated across deopt boundaries.
func (g *Generator) genDeoptPrefixCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(n) {
  let s = 0;
  const o = { a: 0 };
  for (let i = 0; i < n; i++) { o.a = i; s += o.a; }
  return s + o.a;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(96)", fn)))
	return g.build(id, KindDeoptPrefix, seed, b.String())
}

// genBigIntDivZeroCase covers BigInt division by zero (RangeError) and
// BigInt/Number mixing (TypeError). Both throw at a deterministic point after
// the JIT guards back to Tier 0, so the exception prefix is identical across
// tiers.
func (g *Generator) genBigIntDivZeroCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b) { return a / b; }\n", fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(1n, 0n)", fn)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(7n, 1n)", fn)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(1n, 0)", fn)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(0n, 0n)", fn)))
	return g.build(id, KindBigIntDivZero, seed, b.String())
}

// genGetterSetterThrowCase covers getter/setter throws with a committed side
// effect before the throw: the prefix (setter write, earlier getter reads)
// must be committed exactly once and no post-throw side effect may run.
func (g *Generator) genGetterSetterThrowCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { let s = 0; for (let i = 0; i < n; i++) s += o.v; return s; }
const O = { _v: 0, get v() { LOG("get", SV(this._v)); if (this._v === 1) throw new RangeError("boom-get"); return this._v; }, set v(x) { LOG("set", SV(x)); this._v = x; } };
O.v = 1;
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, 3)", fn)))
	b.WriteString("LOG(\"post\", SV(O._v));\n")
	// A throwing setter: the write must not land (post value stays 0).
	b.WriteString(`const O2 = { _v: 0, set v(x) { LOG("set", SV(x)); throw new TypeError("boom-set"); } };
`)
	b.WriteString(tryLog(id, "O2.v = 9"))
	b.WriteString("LOG(\"post2\", SV(O2._v));\n")
	return g.build(id, KindGetterSetterThrow, seed, b.String())
}

// genOOMCase covers an OOM interruption observed at the same loop-backedge
// safepoint contract in every tier.
func (g *Generator) genOOMCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(n) { let s = 0; for (let i = 0; i < n; i++) { s += i; } return s; }
try { LOG("call", "%s"); LOG("return", SV(%s(1000000))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", "caught");
`, fn, callID(id), fn)
	c := g.build(id, KindOOM, seed, b.String())
	c.Hook = &RunHook{OOMBytes: 1 << 40, TriggerOOM: 1}
	return c
}

// genCancelCase covers an embedding cancellation at the first safepoint poll.
func (g *Generator) genCancelCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(n) { let s = 0; for (let i = 0; i < n; i++) { s += i; } return s; }
try { LOG("call", "%s"); LOG("return", SV(%s(1000000))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", "caught");
`, fn, callID(id), fn)
	c := g.build(id, KindCancel, seed, b.String())
	c.Hook = &RunHook{CancelAfter: 1, CancelErr: "embedding canceled"}
	return c
}

// genSafepointCase covers a long loop that yields repeatedly under a small
// trace budget and is then interrupted at a later safepoint poll; the
// committed prefix must survive and the post-throw statement must not run.
func (g *Generator) genSafepointCase(id int, seed int64) *Case {
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(n, o) { for (let i = 0; i < n; i++) { o.last = i; o.count++; } return o.last; }
const INTERRUPT_STATE = { last: -1, count: 0 };
try { LOG("call", "%s"); LOG("return", SV(%s(1000000, INTERRUPT_STATE))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(INTERRUPT_STATE.count > 0 && INTERRUPT_STATE.count === INTERRUPT_STATE.last + 1));
`, fn, callID(id), fn)
	c := g.build(id, KindSafepoint, seed, b.String())
	c.Params.TraceBudget = 1
	c.Hook = &RunHook{CancelAfter: 5, CancelErr: "safepoint interrupted"}
	return c
}

// guardMutationKind enumerates the R1-6 mutation families. Each mutation is
// embedded in the case source at a deterministic call boundary: the case
// first warms up the JIT, then mutates the guarded input, then calls again so
// the guard fires inside the JIT and Tier 0 resumes with the mutated state.
type guardMutationKind int

const (
	mutShapeThird       guardMutationKind = iota // 1st / 2nd / 3rd property shape
	mutTypeChange                                // Number -> String / BigInt / nullish / object
	mutCalleeSwap                                // bound callee identity replacement
	mutMethodSwap                                // trivial method target replacement
	mutMethodToAccessor                          // own method -> accessor
	mutPrototypeMethod                           // own method removed, prototype method
	mutPushReceiver                              // push replaced / receiver non-array
	mutUpvalueChange                             // closure upvalue type / identity change
	guardMutationCount
)

// guardMutationTemplates returns the eight R1-6 mutation bodies. fn is the
// hot-function name; n is the loop bound. Bodies are shared by the fixed
// cases (fixed.go) and the random generator so both exercise the same shapes.
func guardMutationTemplates(fn string, n int) []string {
	return []string{
		// 1st/2nd/3rd shape: the two-way PIC absorbs S1 and S2, the third
		// shape must fall back and the first shape must keep working.
		fmt.Sprintf(`function %[1]s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a; } return s; }
const S1 = { a: 1, b: 2 };
const S2 = { a: 3, c: 4 };
const S3 = { a: 5, d: 6, e: 7 };
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(S1, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(S2, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "%[1]s3"); LOG("return", SV(%[1]s(S3, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "%[1]s4"); LOG("return", SV(%[1]s(S1, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Number property -> String / BigInt / nullish / object: the type
		// guard must fire before the JIT touches the new value, and the
		// BigInt mix must throw the same TypeError in every tier.
		fmt.Sprintf(`function %[1]s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a; } return s; }
const T = { a: 3 };
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(T, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
T.a = "str";
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(T, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
T.a = 7n;
try { LOG("call", "%[1]s3"); LOG("return", SV(%[1]s(T, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
T.a = null;
try { LOG("call", "%[1]s4"); LOG("return", SV(%[1]s(T, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
T.a = OBJ_A;
try { LOG("call", "%[1]s5"); LOG("return", SV(%[1]s(T, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Bound callee identity: leafA warms up, leafB is the second PIC
		// target, leafC is the third target which must disable the callee
		// guard; the first callee must keep returning the right value.
		fmt.Sprintf(`function make%[1]s(fn) { return function(n) { let s = 0; for (let i = 0; i < n; i++) { s += fn(i); } return s; }; }
function leafA%[1]s(x) { return x + 1; }
function leafB%[1]s(x) { return x * 10; }
function leafC%[1]s(x) { return x - 5; }
const R%[1]s = make%[1]s(leafA%[1]s);
try { LOG("call", "%[1]s1"); LOG("return", SV(R%[1]s(%[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
const R%[1]sb = make%[1]s(leafB%[1]s);
try { LOG("call", "%[1]s2"); LOG("return", SV(R%[1]sb(%[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
const R%[1]sc = make%[1]s(leafC%[1]s);
try { LOG("call", "%[1]s3"); LOG("return", SV(R%[1]sc(%[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "%[1]s4"); LOG("return", SV(R%[1]s(%[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Trivial method target: the guarded `return this._a` method is
		// replaced by another function; the identity guard must fire before
		// the replacement runs in Tier 0.
		fmt.Sprintf(`function %[1]s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.getV(); } return s; }
const M = { _a: 2, getV() { return this._a; } };
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(M, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
M.getV = function() { return 99; };
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(M, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Own method -> accessor. The accessor must never run inside the JIT:
		// the method guard fails at the first trace iteration after the swap
		// and the getter runs exactly once per Tier 0 iteration (the gget
		// events count them; any JIT-side getter call would change the log).
		fmt.Sprintf(`function %[1]s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.getV(); } return s; }
const A = { _a: 2, getV() { return this._a; } };
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(A, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
Object.defineProperty(A, "getV", { get: function() { LOG("gget", "x"); return function() { return 50; }; } });
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(A, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Own method -> prototype method: deleting the own method exposes the
		// prototype one; the guard fires and Tier 0 resolves through the
		// prototype chain.
		fmt.Sprintf(`const PROTO%[1]s = { getV() { return 7; } };
function %[1]s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.getV(); } return s; }
const P = { _a: 2, getV() { return this._a; } };
Object.setPrototypeOf(P, PROTO%[1]s);
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(P, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
delete P.getV;
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(P, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`, fn, n),
		// Array push receiver: first the push method is replaced, then the
		// receiver becomes a non-array. Each Tier 0 push must run exactly
		// once (the LOG events prove no duplicate append and no JIT-side
		// push before the guard fails).
		fmt.Sprintf(`function %[1]s(arr, n) { for (let i = 0; i < n; i++) arr.push(i); return arr.length; }
const B = [];
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(B, %[2]d))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
B.push = function(v) { LOG("push", SV(v)); Array.prototype.push.call(this, v * 100); };
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(B, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
const N = { push: function() { LOG("nopush", "x"); } };
try { LOG("call", "%[1]s3"); LOG("return", SV(%[1]s(N, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(B.length) + ":" + SV(B[0]) + ":" + SV(B[1]));
`, fn, n),
		// Closure upvalue: first the upvalue becomes a non-Number, then a
		// different closure instance of the same template runs. Both must
		// fall back to Tier 0 with identical observable results.
		fmt.Sprintf(`let U = 0;
const INC%[1]s = () => ++U;
function %[1]s(n, fn) { let sum = 0; for (let i = 0; i < n; i++) { sum += fn(); } return sum; }
try { LOG("call", "%[1]s1"); LOG("return", SV(%[1]s(%[2]d, INC%[1]s))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
U = "str";
try { LOG("call", "%[1]s2"); LOG("return", SV(%[1]s(2, INC%[1]s))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
const INC%[1]s2 = () => ++U;
try { LOG("call", "%[1]s3"); LOG("return", SV(%[1]s(2, INC%[1]s2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(U));
`, fn, n),
	}
}

// genGuardMutationCase builds a warmup / mutation / post-mutation case from a
// randomly chosen mutation family. The mutation is embedded in the source at
// a deterministic call boundary, so the seed, the source and the mutation
// schedule travel together in the artifact and replay exactly.
func (g *Generator) genGuardMutationCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF1))
	fn := callID(id)
	n := g.loopBound(rng)
	kind := guardMutationKind(rng.Intn(int(guardMutationCount)))
	body := guardMutationTemplates(fn, n)[kind]
	return g.build(id, KindGuardMutation, seed, body)
}

func (g *Generator) stringLeaf(rng *rand.Rand) string {
	return stringLeaves[rng.Intn(len(stringLeaves))]
}

func (g *Generator) bigintLeaf(rng *rand.Rand) string {
	return bigintLeaves[rng.Intn(len(bigintLeaves))]
}

// genStringOpsCase is the R3-4 generative shape: a hot function that
// concatenates Strings (same-type, executed in Quick) and compares them
// relationally, plus a mixed String+Number call whose coercion must fall back
// to Tier 0 with identical observable behavior. The hot body uses only
// parameters (String literals would reject the leaf compile), so String
// results flow through Return and the SV serializer.
func (g *Generator) genStringOpsCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF2))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b, n) {
  let s = a;
  for (let i = 0; i < n; i++) { s = s + b; }
  if (s < a) { return s; }
  return b;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.stringLeaf(rng), g.stringLeaf(rng), n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.stringLeaf(rng), g.stringLeaf(rng), n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.stringLeaf(rng), g.numberLeaf(rng), n)))
	return g.build(id, KindStringOps, seed, b.String())
}

// genBigIntArithCase is the R3-5 generative shape: same-type BigInt + - * /
// % with unary minus, executed in Quick, plus a mixed BigInt+Number call that
// must throw the identical TypeError in every tier.
func (g *Generator) genBigIntArithCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF3))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b, c) {
  let s = -a;
  for (let i = 0; i < c; i++) { s = s + b; }
  return (s * b - a) / b;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng), n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng), n)))
	// b == 0n -> RangeError (Division by zero) after a Quick-computed prefix.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(7n, 0n, %d)", fn, 2)))
	// Mixed BigInt + Number -> TypeError, identical in every tier.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.numberLeaf(rng), n)))
	return g.build(id, KindBigIntArith, seed, b.String())
}

// genBigIntBitwiseCase is the R3-5 generative shape: same-type BigInt
// & | ^ << >> executed in Quick, plus the fallback exceptions (negative shift
// RangeError, mixed TypeError). BigInt `>>>` (TypeError) and unary `~` are
// deliberately not generated here (see the package doc for the `~` Tier 0
// bug); `>>>` is covered by the fixed corpus.
func (g *Generator) genBigIntBitwiseCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF4))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b, c) {
  let s = a;
  for (let i = 0; i < c; i++) { s = (s ^ b) & b; }
  return (s | b) << b;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng), n)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng), n)))
	// Negative shift -> RangeError (BigInt negative shift) in every tier.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(1n, -1n, %d)", fn, 1)))
	// Mixed BigInt + Number -> TypeError in every tier.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %d)", fn, g.bigintLeaf(rng), g.numberLeaf(rng), n)))
	return g.build(id, KindBigIntBitwise, seed, b.String())
}

// genBigIntCompareCase is the R3-5 generative shape: all six same-type BigInt
// comparisons in one kernel (executed in Quick), a mixed BigInt/Number call
// (relational comparisons across types are legal in Tier 0), and a String
// call through the same kernel (String relational comparisons in Quick).
func (g *Generator) genBigIntCompareCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF5))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(a, b) {
  let r = 0;
  if (a < b) r += 1;
  if (a <= b) r += 2;
  if (a > b) r += 4;
  if (a >= b) r += 8;
  if (a === b) r += 16;
  if (a !== b) r += 32;
  return r;
}
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, g.bigintLeaf(rng), g.bigintLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, g.bigintLeaf(rng), g.numberLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, g.stringLeaf(rng), g.stringLeaf(rng))))
	return g.build(id, KindBigIntCompare, seed, b.String())
}

// genArrayIndexCase (R4-5) generates the packed Number read loop
// `s += array[i]` over all-Number arrays (Quick trace hit) plus the mandatory
// fallbacks: bound beyond the element storage, mixed-type elements, holes,
// sparse arrays, a prototype index and a Proxy receiver. Tier 0 is the only
// semantic oracle in every shape.
func (g *Generator) genArrayIndexCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF6))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(array, end) { let s = 0; for (let i = 0; i < end; i++) s += array[i]; return s; }
const AI1 = [1, 2, 3, 4, 5, 6, 7, 8];
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI1, 8)", fn)))
	// bound beyond the element storage: the packed prefix runs, the tail must
	// hand back to Tier 0 (prototype-chain reads) with identical results.
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI1, %d)", fn, 2+n)))
	// mixed-type element: the chunk guard fails before any local is touched.
	b.WriteString(`const AI2 = [1, "x", 3, 4, 5, 6, 7, 8];
`)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI2, 8)", fn)))
	// hole (undefined element): guard fail, Tier 0 semantics preserved.
	b.WriteString(`const AI3 = [1, 2, 3, 4, 5, 6, 7, 8];
AI3[4] = undefined;
`)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI3, 8)", fn)))
	// sparse array: new Array(6) leaves undefined holes.
	b.WriteString(`const AI4 = new Array(6);
AI4[1] = 10; AI4[3] = 30;
`)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI4, 6)", fn)))
	// Proxy receiver never matches the ArrayValue guard.
	b.WriteString(`const AI5 = new Proxy([1, 2, 3, 4, 5, 6, 7, 8], {});
`)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AI5, 8)", fn)))
	return g.build(id, KindArrayIndex, seed, b.String())
}

// genArrayBatchCase (R4-6) generates the safe batch write forms
// `array[i] = i` and `array[j] = i; j++` (length synced once per chunk) plus
// the fallbacks: fractional key start (own-property path), Proxy receiver and
// a pre-filled array that is overwritten and extended.
func (g *Generator) genArrayBatchCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF7))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(array, end) { for (let i = 0; i < end; i++) array[i] = i; return array.length; }
const AB1 = [];
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AB1, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(AB1.length) + \":\" + SV(AB1[%d]));\n", n-1))
	// separate key counter starting above the current length: hole filling
	// above the previous storage plus overwrite of existing elements.
	fmt.Fprintf(&b, `function %sJ(array, end) { let j = 2; for (let i = 0; i < end; i++) { array[j] = i; j++; } return array.length; }
const AB2 = [7, 7];
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%sJ(AB2, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post2\", SV(AB2.length) + \":\" + SV(AB2[2]) + \":\" + SV(AB2[%d]));\n", 1+n))
	// fractional key start: never matches the safe-integer guard; the writes
	// land as own properties in Tier 0.
	fmt.Fprintf(&b, `function %sF(array, end) { let j = 0.5; for (let i = 0; i < end; i++) { array[j] = i; j++; } return array.length; }
const AB3 = [];
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%sF(AB3, %d)", fn, n)))
	b.WriteString("LOG(\"post3\", SV(AB3[0.5]) + \":\" + SV(AB3.length));\n")
	// Proxy receiver never enters the batch specialization.
	b.WriteString(`const AB4 = new Proxy([], {});
`)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(AB4, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post4\", SV(AB4.length));\n"))
	return g.build(id, KindArrayBatch, seed, b.String())
}

// genArrayCbCase (R4-6) generates the compiler/guard-proven numeric callback
// purity paths: pure arrows over all-Number arrays execute in the Go fast
// path (map/filter/reduce with extended patterns), while mixed elements,
// holes, block-body (impure) callbacks and Proxy receivers fall back to the
// full call chain. The differential proves identical observable results.
func (g *Generator) genArrayCbCase(id int, seed int64) *Case {
	var b strings.Builder
	b.WriteString(`const AC1 = [1, 2, 3, 4, 5, 6];
`)
	b.WriteString(tryLog(id, `AC1.map(x => x * 2).join(",")`))
	b.WriteString(tryLog(id, `AC1.map(x => x + 1).join(",")`))
	b.WriteString(tryLog(id, `AC1.filter(x => x % 2 === 0).join(",")`))
	b.WriteString(tryLog(id, `AC1.filter(x => x >= 4).join(",")`))
	b.WriteString(tryLog(id, `AC1.reduce((acc, x) => acc + x, 0)`))
	b.WriteString(tryLog(id, `AC1.reduce((acc, x) => acc * 10, 1)`))
	// mixed elements: input guard fails, full per-element call.
	b.WriteString(`const AC2 = [1, "x", 3, 4];
`)
	b.WriteString(tryLog(id, `AC2.map(x => x * 2).join(",")`))
	// hole: input guard fails, full call reproduces Tier 0's hole behavior.
	b.WriteString(`const AC3 = [1, 2, 3, 4];
AC3[1] = undefined;
`)
	b.WriteString(tryLog(id, `AC3.map(x => x * 2).join(",")`))
	// impure block-body callback: the compiler refuses NativeCallback, so the
	// full call chain runs with its side effect exactly once per element.
	b.WriteString(`let ACSIDE = 0;
`)
	b.WriteString(tryLog(id, `AC1.map(function(x) { ACSIDE += x; return x * 2; }).join(",")`))
	b.WriteString(tryLog(id, `ACSIDE`))
	// Proxy receiver: full call chain through the proxy traps.
	b.WriteString(`const AC4 = new Proxy([1, 2, 3], {});
`)
	b.WriteString(tryLog(id, `AC4.map(x => x * 2).join(",")`))
	return g.build(id, KindArrayCb, seed, b.String())
}

// genTernaryCase (R3-6) generates `a ? b : c` leaves whose test covers the
// truthiness domain (numbers, booleans, strings, bigints, nullish) and whose
// branches cover the value domain. Falsy tests must take the alternate path
// identically in every tier.
func (g *Generator) genTernaryCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF2))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b, c) { return a ? b : c; }\n", fn)
	for i := 0; i < 3; i++ {
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s)", fn, g.valueLeaf(rng), g.valueLeaf(rng), g.valueLeaf(rng))))
	}
	return g.build(id, KindTernary, seed, b.String())
}

// genSwitchCase (R3-6) generates an integer switch and a string switch leaf
// (strict-equality jump chains) whose discriminant draws from the whole value
// domain; non-matching discriminants must take the default body identically
// in every tier.
func (g *Generator) genSwitchCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF3))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(x) { switch (x) { case 1: return 10; case 2: return 20; default: return 30; } }
function %ss(x) { switch (x) { case "a": return 1; case "b": return 2; default: return 3; } }
`, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s)", fn, g.valueLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(%s)", fn, g.valueLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%ss(%s)", fn, g.valueLeaf(rng))))
	b.WriteString(tryLog(id, fmt.Sprintf("%ss(%s)", fn, g.valueLeaf(rng))))
	return g.build(id, KindSwitch, seed, b.String())
}

// genShortCircuitCase (R3-6) generates multi-level `a && b || c && d` keep
// chains; the short-circuit paths must preserve the left operand value
// (numbers, strings, bigints, objects) identically in every tier.
func (g *Generator) genShortCircuitCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xF4))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b, c, d) { return a && b || c && d; }\n", fn)
	for i := 0; i < 3; i++ {
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s, %s, %s)", fn, g.valueLeaf(rng), g.valueLeaf(rng), g.valueLeaf(rng), g.valueLeaf(rng))))
	}
	return g.build(id, KindShortCircuit, seed, b.String())
}
