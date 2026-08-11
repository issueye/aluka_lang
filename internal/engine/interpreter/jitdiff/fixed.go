package jitdiff

import "strings"

// FixedCases returns deterministic, hand-shaped cases covering the event-log
// categories the framework must verify: property write, array append, upvalue
// write, function call, getter/setter, callback throw, try/catch and safepoint
// yield (deopt prefix). Each case has a known expected event log (the off-mode
// oracle) so the tests can assert exact behavior in addition to cross-tier
// equality. IDs are negative so they never collide with generated cases.
func FixedCases() []*Case {
	params := Params{Seed: 0, MaxExprDepth: 3, MaxLoopBound: 24, TraceBudget: 3}
	safepointParams := params
	safepointParams.TraceBudget = 1
	return []*Case{
		{
			ID: -1, Kind: KindPropWrite, Seed: 101, Params: params,
			Expected: "call:kF\nreturn:n:5\npost:n:5",
			Body: `function kF(o, n) { for (let i = 0; i < n; i++) { o.a = i; } return o.a; }
const P = { a: 0 };
try { LOG("call", "kF"); LOG("return", SV(kF(P, 6))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(P.a));
`,
		},
		{
			ID: -2, Kind: KindPush, Seed: 102, Params: params,
			Expected: "call:kA\nreturn:n:5\npost:n:5:n:4",
			Body: `function kA(arr, start, end) { for (let i = start; i < end; i++) arr.push(i); return arr.length; }
const A = [];
try { LOG("call", "kA"); LOG("return", SV(kA(A, 0, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(A.length) + ":" + SV(A[4]));
`,
		},
		{
			ID: -3, Kind: KindClosure, Seed: 103, Params: params,
			Expected: "call:runC\nreturn:n:10\npost:n:5",
			Body: `function makeC() { let n = 0; return () => ++n; }
function runC(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const C = makeC();
try { LOG("call", "runC"); LOG("return", SV(runC(C, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(C()));
`,
		},
		{
			ID: -4, Kind: KindCall, Seed: 104, Params: params,
			Expected: "call:runF\nreturn:n:10",
			Body: `function leafF(x) { return x + 1; }
function runF(n) { let s = 0; for (let i = 0; i < n; i++) s += leafF(i); return s; }
try { LOG("call", "runF"); LOG("return", SV(runF(4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			ID: -5, Kind: KindGetter, Seed: 105, Params: params,
			Expected: "call:kG\nget:n:3\nget:n:3\nget:n:3\nreturn:n:9\nset:n:5\npost:n:5",
			Body: `function kG(o, n) { let s = 0; for (let i = 0; i < n; i++) s += o.v; return s; }
const G = { _v: 3, get v() { LOG("get", SV(this._v)); return this._v; }, set v(x) { LOG("set", SV(x)); this._v = x; } };
try { LOG("call", "kG"); LOG("return", SV(kG(G, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
G.v = 5;
LOG("post", SV(G._v));
`,
		},
		{
			ID: -6, Kind: KindCallbackThrow, Seed: 106, Params: params,
			Expected: "call:map\nthrow:TypeError:cb-throw",
			Body: `const CB = [1, 2, 3];
try { LOG("call", "map"); LOG("return", SV(CB.map(function(x) { if (x === 2) throw new TypeError("cb-throw"); return x; }).length)); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			ID: -7, Kind: KindExpr, Seed: 107, Params: params,
			Expected: "call:kT1\nreturn:n:6\ncall:kT2\nthrow:RangeError:neg",
			Body: `function kT(x) { if (x < 0) { throw new RangeError("neg"); } return x * 2; }
try { LOG("call", "kT1"); LOG("return", SV(kT(3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT2"); LOG("return", SV(kT(-1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// Long property-write loop with a small trace budget: every yield
			// commits the completed prefix; the final sum must prove that no
			// write was lost or repeated across deopt boundaries.
			ID: -8, Kind: KindDeoptPrefix, Seed: 108, Params: params,
			Expected: "call:kD\nreturn:n:135",
			Body: `function kD(n) {
  let s = 0;
  const o = { a: 0 };
  for (let i = 0; i < n; i++) { o.a = i; s += o.a; }
  return s + o.a;
}
try { LOG("call", "kD"); LOG("return", SV(kD(16))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// Symbol identity: === must compare identity and never coerce.
			ID: -9, Kind: KindStrictEq, Seed: 109, Params: params,
			Expected: "call:kS1\nreturn:b:true\ncall:kS2\nreturn:b:false",
			Body: `function kS(a, b) { return a === b; }
const S1 = Symbol("z1");
const S2 = Symbol("z2");
try { LOG("call", "kS1"); LOG("return", SV(kS(S1, S1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS2"); LOG("return", SV(kS(S1, S2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// BigInt + Number must throw the engine TypeError in every tier.
			ID: -10, Kind: KindLooseEq, Seed: 110, Params: params,
			Expected: "call:kB\nthrow:TypeError:cannot mix BigInt and other types, use explicit conversions",
			Body: `function kB(x) { return 7n + x; }
try { LOG("call", "kB"); LOG("return", SV(kB(1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// Loose equality on non-number operands must guard back to Tier 0.
			ID: -11, Kind: KindLooseEq, Seed: 111, Params: params,
			Expected: "call:kL1\nreturn:b:true\ncall:kL2\nreturn:b:false",
			Body: `function kL(a, b) { return a == b; }
try { LOG("call", "kL1"); LOG("return", SV(kL("2", 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kL2"); LOG("return", SV(kL("a", "b"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R1-3: BigInt division by zero throws a deterministic
			// RangeError at the same guard-fallback point in every tier.
			ID: -12, Kind: KindBigIntDivZero, Seed: 112, Params: params,
			Expected: "call:kZ\nthrow:RangeError:Division by zero\ncall:kZ\nreturn:bi:7\ncall:kZ\nthrow:TypeError:cannot mix BigInt and other types, use explicit conversions",
			Body: `function kZ(a, b) { return a / b; }
try { LOG("call", "kZ"); LOG("return", SV(kZ(1n, 0n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kZ"); LOG("return", SV(kZ(7n, 1n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kZ"); LOG("return", SV(kZ(1n, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R1-3: getter throws after a committed prefix; the setter write
			// before the throw lands exactly once and the throwing setter's
			// write never lands.
			ID: -13, Kind: KindGetterSetterThrow, Seed: 113, Params: params,
			Expected: "set:n:1\ncall:kT\nget:n:1\nthrow:RangeError:boom-get\npost:n:1\ncall:kU\nset:n:9\nthrow:TypeError:boom-set\npost2:n:0",
			Body: `function kT(o, n) { let s = 0; for (let i = 0; i < n; i++) s += o.v; return s; }
const O = { _v: 0, get v() { LOG("get", SV(this._v)); if (this._v === 1) throw new RangeError("boom-get"); return this._v; }, set v(x) { LOG("set", SV(x)); this._v = x; } };
O.v = 1;
try { LOG("call", "kT"); LOG("return", SV(kT(O, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(O._v));
const O2 = { _v: 0, set v(x) { LOG("set", SV(x)); throw new TypeError("boom-set"); } };
try { LOG("call", "kU"); LOG("return", SV((O2.v = 9))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post2", SV(O2._v));
`,
		},
		{
			// R1-3: OOM is triggered by the first loop safepoint in every tier,
			// enters the same catch block at the next check, and preserves the
			// event prefix.
			ID: -14, Kind: KindOOM, Seed: 114, Params: params,
			Hook:     &RunHook{OOMBytes: 1 << 40, TriggerOOM: 1},
			Expected: "call:kO\nthrow:RangeError:JavaScript heap out of memory (limit 1099511627776 bytes)\npost:caught",
			Body: `function kO(n) { let s = 0; for (let i = 0; i < n; i++) { s += i; } return s; }
try { LOG("call", "kO"); LOG("return", SV(kO(1000000))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", "caught");
`,
		},
		{
			// R1-3: embedding cancellation at the first safepoint poll
			// remains a distinct Error and enters the same catch block.
			ID: -15, Kind: KindCancel, Seed: 115, Params: params,
			Hook:     &RunHook{CancelAfter: 1, CancelErr: "embedding canceled"},
			Expected: "call:kV\nthrow:Error:embedding canceled\npost:caught",
			Body: `function kV(n) { let s = 0; for (let i = 0; i < n; i++) { s += i; } return s; }
try { LOG("call", "kV"); LOG("return", SV(kV(1000000))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", "caught");
`,
		},
		{
			// R1-3: safepoint interruption after several yields under a tiny
			// trace budget (TraceBudget=1), preserving committed writes.
			ID: -16, Kind: KindSafepoint, Seed: 116, Params: safepointParams,
			Hook:     &RunHook{CancelAfter: 5, CancelErr: "safepoint interrupted"},
			Expected: "call:kW\nthrow:Error:safepoint interrupted\npost:b:true",
			Body: `function kW(n, o) { for (let i = 0; i < n; i++) { o.last = i; o.count++; } return o.last; }
const INTERRUPT_STATE = { last: -1, count: 0 };
try { LOG("call", "kW"); LOG("return", SV(kW(1000000, INTERRUPT_STATE))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(INTERRUPT_STATE.count > 0 && INTERRUPT_STATE.count === INTERRUPT_STATE.last + 1));
`,
		},
		{
			// R1-4: a property-write loop commits its prefix, then a type
			// change makes the next guard fail. The guard must fire before
			// any partial write of the failing iteration, and the whole loop
			// falls back to Tier 0 with the exact same observable state.
			ID: -17, Kind: KindPropWrite, Seed: 117, Params: params,
			Expected: "call:kx\nreturn:n:4\ncall:ky\nreturn:n:2\npost:n:2",
			Body: `function kx(o, n) { for (let i = 0; i < n; i++) { o.a = i; } return o.a; }
const O = { a: 0 };
try { LOG("call", "kx"); LOG("return", SV(kx(O, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
O.a = "str";
try { LOG("call", "ky"); LOG("return", SV(kx(O, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(O.a));
`,
		},
		{
			// R1-4: a throw inside the loop's trace (after two committed
			// iterations) exits through the exception exit. The pending
			// exception (the original numeric value) reaches the catch, the
			// committed local prefix survives, and execution continues after
			// the catch. Identical observable state in all three tiers.
			ID: -18, Kind: KindLoop, Seed: 118, Params: params,
			Expected: "call:ky\ncatch:number:20\nreturn:n:1002",
			Body: `function ky(n) {
  let s = 0;
  try {
    for (let i = 0; i < n; i++) {
      if (i < 2) { s += 1; continue; }
      throw s * 10;
    }
  } catch (e) {
    s += 1000;
    LOG("catch", typeof e + ":" + e);
  }
  return s;
}
try { LOG("call", "ky"); LOG("return", SV(ky(100))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R1-5: after warmup the noop callee is replaced by a
			// late-throwing callee. The second invocation enters the trace
			// (the throw is delayed past the first backedge), the callee
			// identity guard fires before the call, the pending property
			// write of that iteration is discarded (no partial write), Tier 0
			// replays the iteration, and the user call throws into the same
			// catch. The warmup prefix is committed exactly once.
			ID: -19, Kind: KindCall, Seed: 119, Params: params,
			Expected: "call:kY1\nreturn:n:4\ncall:kY2\nthrow:Error:call-boom\npost:n:1",
			Body: `function NOOP() {}
let THROW_COUNT = 0;
function LATE_THROWER() { THROW_COUNT++; if (THROW_COUNT >= 2) throw new Error("call-boom"); }
function kY(n, o, cb) { for (let i = 0; i < n; i++) { o.a = i; cb(); } return o.a; }
const O = { a: -1 };
try { LOG("call", "kY1"); LOG("return", SV(kY(5, O, NOOP))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kY2"); LOG("return", SV(kY(3, O, LATE_THROWER))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(O.a));
`,
		},
		{
			// R1-5: an embedding cancellation interrupts a push loop mid-JIT.
			// Every committed chunk must land exactly once: the invariant
			// A.length === A[A.length-1] + 1 proves consecutive elements with
			// no duplicate append and no lost append, in every tier.
			ID: -20, Kind: KindPush, Seed: 120, Params: safepointParams,
			Hook:     &RunHook{CancelAfter: 7, CancelErr: "cancel-append"},
			Expected: "call:kA\nthrow:Error:cancel-append\npost:b:true",
			Body: `function kA(arr, n) { for (let i = 0; i < n; i++) arr.push(i); return arr.length; }
const A = [];
try { LOG("call", "kA"); LOG("return", SV(kA(A, 1000000))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(A.length > 0 && A.length === A[A.length - 1] + 1));
`,
		},
		{
			// R1-5: an embedding cancellation interrupts a numeric-upvalue
			// closure loop. The upvalue and the sum are written back
			// atomically per committed chunk, so sum === N*(N+1)/2 (every
			// committed iteration contributed exactly once) must hold after
			// the interruption in every tier.
			ID: -21, Kind: KindClosure, Seed: 121, Params: safepointParams,
			Hook:     &RunHook{CancelAfter: 7, CancelErr: "cancel-upvalue"},
			Expected: "throw:Error:cancel-upvalue\npost:b:true",
			Body: `let N = 0;
const INC = () => ++N;
function kR(n, fn) {
  let sum = 0;
  try {
    for (let i = 0; i < n; i++) { sum += fn(); }
  } catch (e) { LOG("throw", e.name + ":" + e.message); }
  return sum;
}
const S1 = kR(1000000, INC);
LOG("post", SV(N > 0 && S1 === N * (N + 1) / 2));
`,
		},
		{
			// R1-5: a property write followed by an in-trace throw. The
			// two-phase commit runs before the pending exception reaches the
			// catch, so the catch observes the committed write (o.a = 2),
			// the finally block runs, and the returned value proves the
			// commit-before-throw ordering in every tier.
			ID: -22, Kind: KindLoop, Seed: 122, Params: params,
			Expected: "call:kF\ncatch:number:200\nfinally:n:1\nreturn:n:2\npost:n:2",
			Body: `function kF(n, o) {
  let f = 0;
  try {
    for (let i = 0; i < n; i++) {
      o.a = i;
      if (i < 2) { f += 1; continue; }
      throw i * 100;
    }
  } catch (e) {
    LOG("catch", typeof e + ":" + e);
  } finally {
    f = 1;
    LOG("finally", SV(f));
  }
  return o.a;
}
const O = { a: -1 };
try { LOG("call", "kF"); LOG("return", SV(kF(100, O))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(O.a));
`,
		},
		{
			// R1-5: OOM interrupts a property-write loop after committed
			// chunks. The invariant count === last + 1 proves the committed
			// prefix is complete (no duplicated or lost write) when the OOM
			// unwinds into the same catch in every tier.
			ID: -23, Kind: KindPropWrite, Seed: 123, Params: params,
			Hook:     &RunHook{OOMBytes: 1 << 40, TriggerOOM: 1},
			Expected: "call:kO2\nthrow:RangeError:JavaScript heap out of memory (limit 1099511627776 bytes)\npost:b:true",
			Body: `function kO2(n, o) { for (let i = 0; i < n; i++) { o.last = i; o.count = o.count + 1; } return o.last; }
const O2 = { last: -1, count: 0 };
try { LOG("call", "kO2"); LOG("return", SV(kO2(1000000, O2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(O2.count > 0 && O2.count === O2.last + 1));
`,
		},
		{
			// R1-6: 1st/2nd/3rd property shape. The two-way PIC absorbs S1 and
			// S2; the third shape must fall back to Tier 0 and the first shape
			// must keep working afterwards.
			ID: -24, Kind: KindGuardMutation, Seed: 124, Params: params,
			Expected: "call:kS1\nreturn:n:4\ncall:kS2\nreturn:n:12\ncall:kS3\nreturn:n:20\ncall:kS4\nreturn:n:4",
			Body:     guardMutationTemplates("kS", 4)[mutShapeThird],
		},
		{
			// R1-6: Number property -> String / BigInt / nullish / object.
			// The type guard must fire before the JIT touches the new value,
			// the BigInt mix must throw the same TypeError in every tier, and
			// the nullish/object cases resume in Tier 0.
			ID: -25, Kind: KindGuardMutation, Seed: 125, Params: params,
			Expected: "call:kT1\nreturn:n:12\ncall:kT2\nreturn:s:0strstr\ncall:kT3\nthrow:TypeError:cannot mix BigInt and other types, use explicit conversions\ncall:kT4\nreturn:n:0\ncall:kT5\nreturn:n:0",
			Body:     guardMutationTemplates("kT", 4)[mutTypeChange],
		},
		{
			// R1-6: bound callee identity. leafA warms up the callee guard,
			// leafB is the second PIC target, leafC is the third target which
			// disables the callee specialization; the first callee must keep
			// returning the right value.
			ID: -26, Kind: KindGuardMutation, Seed: 126, Params: params,
			Expected: "call:kC1\nreturn:n:21\ncall:kC2\nreturn:n:150\ncall:kC3\nreturn:n:-15\ncall:kC4\nreturn:n:21",
			Body:     guardMutationTemplates("kC", 6)[mutCalleeSwap],
		},
		{
			// R1-6: trivial method target replacement. The guarded
			// `return this._a` method is swapped for another function; the
			// identity guard must fire before the replacement runs in Tier 0.
			ID: -27, Kind: KindGuardMutation, Seed: 127, Params: params,
			Expected: "call:kM1\nreturn:n:8\ncall:kM2\nreturn:n:297",
			Body:     guardMutationTemplates("kM", 4)[mutMethodSwap],
		},
		{
			// R1-6: own method -> accessor. The accessor must never run inside
			// the JIT: the method guard fails at the first trace iteration
			// after the swap and the getter runs exactly once per Tier 0
			// iteration (the three gget events prove no JIT-side getter call
			// and no duplicated or lost getter invocation).
			ID: -28, Kind: KindGuardMutation, Seed: 128, Params: params,
			Expected: "call:kA1\nreturn:n:8\ncall:kA2\ngget:x\ngget:x\ngget:x\nreturn:n:150",
			Body:     guardMutationTemplates("kA", 4)[mutMethodToAccessor],
		},
		{
			// R1-6: own method -> prototype method. Deleting the own method
			// exposes the prototype one; the guard fires and Tier 0 resolves
			// through the prototype chain.
			ID: -29, Kind: KindGuardMutation, Seed: 129, Params: params,
			Expected: "call:kP1\nreturn:n:8\ncall:kP2\nreturn:n:21",
			Body:     guardMutationTemplates("kP", 4)[mutPrototypeMethod],
		},
		{
			// R1-6: array push receiver. First the push method is replaced,
			// then the receiver becomes a non-array. Each Tier 0 push must run
			// exactly once (the push/nopush events prove no duplicate append
			// and no JIT-side push before the guard fails).
			ID: -30, Kind: KindGuardMutation, Seed: 130, Params: params,
			Expected: "call:kB1\nreturn:n:4\ncall:kB2\npush:n:0\npush:n:1\npush:n:2\nreturn:n:7\ncall:kB3\nnopush:x\nnopush:x\nreturn:undefined\npost:n:7:n:0:n:1",
			Body:     guardMutationTemplates("kB", 4)[mutPushReceiver],
		},
		{
			// R1-6: closure upvalue. First the upvalue becomes a non-Number,
			// then a different closure instance of the same template runs.
			// Both must fall back to Tier 0 with identical observable results.
			ID: -31, Kind: KindGuardMutation, Seed: 131, Params: params,
			Expected: "call:kU1\nreturn:n:10\ncall:kU2\nreturn:NaN\ncall:kU3\nreturn:NaN\npost:NaN",
			Body:     guardMutationTemplates("kU", 4)[mutUpvalueChange],
		},
		{
			// R3-1: Symbol truthiness and nullish behavior across `!`, `&&`,
			// `||` and `??`: a Symbol is always truthy, never nullish, and the
			// short-circuit operators keep the Symbol value itself.
			ID: -32, Kind: KindBranch, Seed: 132, Params: params,
			Expected: "call:kA1\nreturn:n:12\ncall:kA2\nreturn:n:14\ncall:kA3\nreturn:n:12\ncall:kA4\nreturn:n:5\ncall:kA5\nreturn:n:13\ncall:kA6\nreturn:n:14",
			Body: `function kT(v, x) {
  let r = 0;
  if (!v) r += 1;
  if (v && x) r += 2;
  if (v || x) r += 4;
  if (v ?? x) r += 8;
  return r;
}
try { LOG("call", "kA1"); LOG("return", SV(kT(SYM1, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kA2"); LOG("return", SV(kT(SYM1, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kA3"); LOG("return", SV(kT(SYM1, undefined))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kA4"); LOG("return", SV(kT(0, SYM2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kA5"); LOG("return", SV(kT(null, SYM2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kA6"); LOG("return", SV(kT(OBJ_A, SYM1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-2: Symbol strict equality is identity with no coercion: the
			// same Symbol is equal only to itself, never to another Symbol or
			// to any other type; !== inverts; String/BigInt/Number/object
			// value semantics do not regress.
			ID: -33, Kind: KindStrictEq, Seed: 133, Params: params,
			Expected: "call:kS1\nreturn:b:true\ncall:kS2\nreturn:b:false\ncall:kS3\nreturn:b:false\ncall:kS4\nreturn:b:false\ncall:kS5\nreturn:b:false\ncall:kS6\nreturn:b:false\ncall:kS7\nreturn:b:false\ncall:kS8\nreturn:b:false\ncall:kS9\nreturn:b:false\ncall:kN1\nreturn:b:false\ncall:kN2\nreturn:b:true\ncall:kS10\nreturn:b:true\ncall:kS11\nreturn:b:true\ncall:kS12\nreturn:b:true\ncall:kS13\nreturn:b:true",
			Body: `function kS(a, b) { return a === b; }
function kN(a, b) { return a !== b; }
try { LOG("call", "kS1"); LOG("return", SV(kS(SYM1, SYM1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS2"); LOG("return", SV(kS(SYM1, SYM2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS3"); LOG("return", SV(kS(SYM1, "a"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS4"); LOG("return", SV(kS(SYM1, 7))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS5"); LOG("return", SV(kS(SYM1, 7n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS6"); LOG("return", SV(kS(SYM1, null))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS7"); LOG("return", SV(kS(SYM1, undefined))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS8"); LOG("return", SV(kS(SYM1, true))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS9"); LOG("return", SV(kS(SYM1, OBJ_A))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN1"); LOG("return", SV(kN(SYM1, SYM1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN2"); LOG("return", SV(kN(SYM1, SYM2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS10"); LOG("return", SV(kS("a", "a"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS11"); LOG("return", SV(kS(7n, 7n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS12"); LOG("return", SV(kS(0, -0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS13"); LOG("return", SV(kS(OBJ_A, OBJ_A))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-1/R3-2: Symbol truthiness, nullish and strict equality inside
			// a traced loop (the marker keeps the function out of the leaf
			// tier so the trace tier handles it).
			ID: -34, Kind: KindLoop, Seed: 134, Params: params,
			Expected: "call:kL1\nreturn:n:91\ncall:kL2\nreturn:n:78\ncall:kL3\nreturn:n:104\ncall:kL4\nreturn:n:156",
			Body: `function kL(a, b, n) {
  const traceOnlyMarker = {};
  let r = 0;
  for (let i = 0; i < n; i++) {
    if (a === b) r += 1;
    if (a) r += 2;
    if (a ?? b) r += 4;
    if (!a) r += 8;
  }
  return r;
}
try { LOG("call", "kL1"); LOG("return", SV(kL(SYM1, SYM1, 13))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kL2"); LOG("return", SV(kL(SYM1, SYM2, 13))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kL3"); LOG("return", SV(kL(0, SYM2, 13))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kL4"); LOG("return", SV(kL(null, SYM2, 13))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
	}
}

// applySource builds the full source for a fixed case.
func (c *Case) applySource() {
	if c.Source == "" {
		c.Source = harnessHead + "\n" + c.Body + "\n" + harnessTail
	}
}

// JoinExpected normalizes an expected event list for comparison.
func JoinExpected(events ...string) string {
	return strings.Join(events, "\n")
}
