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
	default:
		return g.genSafepointCase(id, seed)
	}
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

// genStrictEqCase uses controlled value pairs so the R1-2 identity semantics
// (NaN !== NaN, +0 === -0, BigInt value equality, Symbol identity, object
// identity) are exercised deterministically.
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
	}
	start := rng.Intn(len(pairs))
	for i := 0; i < 4; i++ {
		pair := pairs[(start+i)%len(pairs)]
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, pair[0], pair[1])))
	}
	return g.build(id, KindStrictEq, seed, b.String())
}

// genLooseEqCase exercises == / !=, which the JIT only handles for Number
// operands; all other combinations must guard back to Tier 0.
func (g *Generator) genLooseEqCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE5))
	fn := callID(id)
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(a, b) { return a == b; }\n", fn)
	pairs := [][2]string{
		{"1", "1"}, {"1", `"1"`}, {"0", `""`}, {"null", "undefined"},
		{"true", "1"}, {`"a"`, `"a"`}, {"7n", "7"}, {"NaN", "NaN"},
		{"0", "-0"}, {"1", "2"}, {`"a"`, `"b"`}, {"7n", "7n"},
	}
	start := rng.Intn(len(pairs))
	for i := 0; i < 4; i++ {
		pair := pairs[(start+i)%len(pairs)]
		b.WriteString(tryLog(id, fmt.Sprintf("%s(%s, %s)", fn, pair[0], pair[1])))
	}
	return g.build(id, KindLooseEq, seed, b.String())
}

func (g *Generator) genPropReadCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE6))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a + o.b; } return s; }
const O = { a: %s, b: %s };
`, fn, g.numberLeaf(rng), g.numberLeaf(rng))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	// A second object with a different shape exercises the shape guard path.
	b.WriteString(fmt.Sprintf("const O2 = { a: %s, b: %s, c: 1 };\n", g.numberLeaf(rng), g.numberLeaf(rng)))
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O2, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(O.a) + \",\" + SV(O.b));\n"))
	return g.build(id, KindPropRead, seed, b.String())
}

func (g *Generator) genPropWriteCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE7))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function %s(o, n) { for (let i = 0; i < n; i++) { o.a = i; } return o.a; }
const O = { a: 0 };
`, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("%s(O, %d)", fn, n)))
	b.WriteString("LOG(\"post\", SV(O.a));\n")
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

func (g *Generator) genClosureCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xE9))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function make%s() { let n = 0; return () => ++n; }
function run%s(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const C = make%s();
`, fn, fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("run%s(C, %d)", fn, n)))
	b.WriteString(fmt.Sprintf("LOG(\"post\", SV(C()));\n"))
	return g.build(id, KindClosure, seed, b.String())
}

func (g *Generator) genCallCase(id int, seed int64) *Case {
	rng := rand.New(rand.NewSource(seed ^ 0xEA))
	fn := callID(id)
	n := g.loopBound(rng)
	var b strings.Builder
	fmt.Fprintf(&b, `function leaf%s(x) { return x + %s; }
function run%s(n) { let s = 0; for (let i = 0; i < n; i++) s += leaf%s(i); return s; }
`, fn, g.numberLeaf(rng), fn, fn)
	b.WriteString(tryLog(id, fmt.Sprintf("run%s(%d)", fn, n)))
	// A non-inlineable callee (string concat) must fall back cleanly.
	b.WriteString(fmt.Sprintf(`function leafS%s(x) { return "s" + x; }
function runS%s(n) { let s = 0; for (let i = 0; i < n; i++) s += leafS%s(i); return s; }
`, fn, fn, fn))
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
