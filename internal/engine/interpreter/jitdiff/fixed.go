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
		{
			// R3-4: String `+` concat loop plus a relational comparison. The
			// same-type String calls concatenate inside Quick; the mixed
			// String+Number call falls back to Tier 0 for the coercion.
			ID: -40, Kind: KindStringOps, Seed: 132, Params: params,
			Expected: "call:kQ1\nreturn:s:b\ncall:kQ2\nreturn:s:x\ncall:kQ3\nreturn:n:1",
			Body: `function kQ(a, b, n) {
  let s = a;
  for (let i = 0; i < n; i++) { s = s + b; }
  if (s < a) { return s; }
  return b;
}
try { LOG("call", "kQ1"); LOG("return", SV(kQ("a", "b", 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kQ2"); LOG("return", SV(kQ("", "x", 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kQ3"); LOG("return", SV(kQ("a", 1, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-4: all four relational operators over same-type Strings.
			// "2" > "10" exercises the lexicographic (code-unit) order.
			ID: -41, Kind: KindStringOps, Seed: 133, Params: params,
			Expected: "call:kR1\nreturn:n:3\ncall:kR2\nreturn:n:12\ncall:kR3\nreturn:n:10\ncall:kR4\nreturn:n:12",
			Body: `function kR(a, b) {
  let r = 0;
  if (a < b) r += 1;
  if (a <= b) r += 2;
  if (a > b) r += 4;
  if (a >= b) r += 8;
  return r;
}
try { LOG("call", "kR1"); LOG("return", SV(kR("a", "b"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kR2"); LOG("return", SV(kR("ab", "a"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kR3"); LOG("return", SV(kR("a", "a"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kR4"); LOG("return", SV(kR("2", "10"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-4: a concat result feeds strict equality and truthiness.
			// Single-return shape so the function-level leaf compiles and the
			// whole kernel executes in Quick.
			ID: -47, Kind: KindStringOps, Seed: 139, Params: params,
			Expected: "call:kV1\nreturn:s:a\ncall:kV2\nreturn:s:\ncall:kV3\nreturn:s:xy",
			Body: `function kV(a, b, c) {
  let s = a + b;
  if (s === c) { s = a; } else if (!s) { s = b; }
  return s;
}
try { LOG("call", "kV1"); LOG("return", SV(kV("a", "b", "ab"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kV2"); LOG("return", SV(kV("", "", "x"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kV3"); LOG("return", SV(kV("x", "y", "z"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-5: BigInt + - * / % and unary - (same-type in Quick); the
			// mixed BigInt+Number call throws the identical TypeError.
			ID: -42, Kind: KindBigIntArith, Seed: 134, Params: params,
			Expected: "call:kB1\nreturn:bi:-3\ncall:kB2\nreturn:bi:-1\ncall:kB3\nthrow:TypeError:cannot mix BigInt and other types, use explicit conversions\ncall:kB4\nreturn:bi:-14",
			Body: `function kB(a, b, c) {
  let s = -a;
  for (let i = 0; i < c; i++) { s = s + b; }
  return s * b - a;
}
try { LOG("call", "kB1"); LOG("return", SV(kB(5n, 2n, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kB2"); LOG("return", SV(kB(1n, 0n, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kB3"); LOG("return", SV(kB(1n, 1, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kB4"); LOG("return", SV(kB(7n, 1n, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-5: BigInt division/modulo truncate toward zero; division by
			// zero throws the identical RangeError in every tier.
			ID: -43, Kind: KindBigIntArith, Seed: 135, Params: params,
			Expected: "call:kD1\nreturn:bi:3\ncall:kD2\nthrow:RangeError:Division by zero\ncall:kD3\nreturn:bi:0\ncall:kD4\nreturn:bi:-2",
			Body: `function kD(a, b) { return (a / b) % b; }
try { LOG("call", "kD1"); LOG("return", SV(kD(17n, 5n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kD2"); LOG("return", SV(kD(1n, 0n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kD3"); LOG("return", SV(kD(1n, 2n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kD4"); LOG("return", SV(kD(-7n, 3n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-5: BigInt & | ^ << >> (same-type in Quick) with the fallback
			// exceptions: negative shift RangeError, mixed TypeError.
			ID: -44, Kind: KindBigIntBitwise, Seed: 136, Params: params,
			Expected: "call:kF1\nreturn:bi:48\ncall:kF2\nthrow:RangeError:BigInt negative shift\ncall:kF3\nthrow:TypeError:cannot mix BigInt and other types, use explicit conversions\ncall:kF4\nreturn:bi:56",
			Body: `function kF(a, b, c) {
  let s = a;
  for (let i = 0; i < c; i++) { s = s ^ b; }
  return s << b;
}
try { LOG("call", "kF1"); LOG("return", SV(kF(5n, 3n, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kF2"); LOG("return", SV(kF(1n, -1n, 1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kF3"); LOG("return", SV(kF(5n, 1, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kF4"); LOG("return", SV(kF(7n, 3n, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-5: BigInt `>>>` is a TypeError (BigInts have no unsigned
			// right shift) in every tier; Number `>>>` is unaffected.
			ID: -45, Kind: KindBigIntBitwise, Seed: 137, Params: params,
			Expected: "call:kU1\nthrow:TypeError:BigInts have no unsigned right shift, use >> instead\ncall:kU2\nreturn:n:4",
			Body: `function kU(a, b) { return a >>> b; }
try { LOG("call", "kU1"); LOG("return", SV(kU(8n, 1n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kU2"); LOG("return", SV(kU(8, 1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-5: all six same-type BigInt comparisons in one kernel; the
			// mixed BigInt/Number call resolves through Tier 0.
			ID: -46, Kind: KindBigIntCompare, Seed: 138, Params: params,
			Expected: "call:kC1\nreturn:n:35\ncall:kC2\nreturn:n:26\ncall:kC3\nreturn:n:44\ncall:kC4\nreturn:n:44",
			Body: `function kC(a, b) {
  let r = 0;
  if (a < b) r += 1;
  if (a <= b) r += 2;
  if (a > b) r += 4;
  if (a >= b) r += 8;
  if (a === b) r += 16;
  if (a !== b) r += 32;
  return r;
}
try { LOG("call", "kC1"); LOG("return", SV(kC(5n, 8n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kC2"); LOG("return", SV(kC(7n, 7n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kC3"); LOG("return", SV(kC(8n, 5n))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kC4"); LOG("return", SV(kC(2n, 1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-6: ternary `a ? b : c`. Falsy tests (0, "", 7n is truthy)
			// must take the alternate path; the branch values must be
			// preserved exactly in every tier.
			ID: -48, Kind: KindTernary, Seed: 132, Params: params,
			Expected: "call:kT1\nreturn:n:2\ncall:kT2\nreturn:n:3\ncall:kT3\nreturn:n:6\ncall:kT4\nreturn:n:2",
			Body: `function kT(a, b, c) { return a ? b : c; }
try { LOG("call", "kT1"); LOG("return", SV(kT(1, 2, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT2"); LOG("return", SV(kT(0, 2, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT3"); LOG("return", SV(kT("", 5, 6))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT4"); LOG("return", SV(kT(7n, 2, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-6: integer and string switch leaves (strict-equality jump
			// chains). The string case tests exercise the OpConstString pool;
			// non-matching discriminants take the default body.
			ID: -49, Kind: KindSwitch, Seed: 133, Params: params,
			Expected: "call:kS1\nreturn:n:10\ncall:kS2\nreturn:n:20\ncall:kS3\nreturn:n:30\ncall:kT1\nreturn:n:1\ncall:kT2\nreturn:n:2\ncall:kT3\nreturn:n:3",
			Body: `function kS(x) { switch (x) { case 1: return 10; case 2: return 20; default: return 30; } }
function kT(x) { switch (x) { case "a": return 1; case "b": return 2; default: return 3; } }
try { LOG("call", "kS1"); LOG("return", SV(kS(1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS2"); LOG("return", SV(kS(2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kS3"); LOG("return", SV(kS(9))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT1"); LOG("return", SV(kT("a"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT2"); LOG("return", SV(kT("b"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT3"); LOG("return", SV(kT("z"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-6: multi-level short-circuit `a && b || c && d` and
			// `(a || b) && c`. The keep-branch merge depths stay consistent
			// and the short-circuited value is preserved in every tier.
			ID: -50, Kind: KindShortCircuit, Seed: 134, Params: params,
			Expected: "call:kN1\nreturn:n:2\ncall:kN2\nreturn:n:4\ncall:kN3\nreturn:n:0\ncall:kM1\nreturn:n:3\ncall:kM2\nreturn:n:0",
			Body: `function kN(a, b, c, d) { return a && b || c && d; }
function kM(a, b, c) { return (a || b) && c; }
try { LOG("call", "kN1"); LOG("return", SV(kN(1, 2, 0, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN2"); LOG("return", SV(kN(0, 2, 3, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN3"); LOG("return", SV(kN(1, 0, 3, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kM1"); LOG("return", SV(kM(0, 2, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kM2"); LOG("return", SV(kM(1, 0, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-3: loose equality across the primitive domain executes in
			// Quick (== and != kernels): null/undefined, String/Number
			// coercion, Boolean coercion, BigInt across types, NaN, signed
			// zero, Symbol identity and Symbol-vs-primitive (always false).
			ID: -51, Kind: KindLooseEq, Seed: 151, Params: params,
			Expected: "call:kE1\nreturn:b:true\ncall:kE2\nreturn:b:false\ncall:kE3\nreturn:b:true\ncall:kE4\nreturn:b:false\ncall:kE5\nreturn:b:true\ncall:kE6\nreturn:b:true\ncall:kE7\nreturn:b:true\ncall:kE8\nreturn:b:false\ncall:kE9\nreturn:b:true\ncall:kE10\nreturn:b:true\ncall:kE11\nreturn:b:false\ncall:kE12\nreturn:b:false\ncall:kE13\nreturn:b:false\ncall:kN1\nreturn:b:false\ncall:kN2\nreturn:b:false\ncall:kN3\nreturn:b:false\ncall:kN4\nreturn:b:true",
			Body: `function kE(a, b) { return a == b; }
function kN(a, b) { return a != b; }
try { LOG("call", "kE1"); LOG("return", SV(kE(null, undefined))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE2"); LOG("return", SV(kE(null, 0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE3"); LOG("return", SV(kE("2", 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE4"); LOG("return", SV(kE("a", 1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE5"); LOG("return", SV(kE(true, 1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE6"); LOG("return", SV(kE(7n, 7))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE7"); LOG("return", SV(kE(7n, "7"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE8"); LOG("return", SV(kE(NaN, NaN))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE9"); LOG("return", SV(kE(0, -0))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE10"); LOG("return", SV(kE(SYM1, SYM1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE11"); LOG("return", SV(kE(SYM1, SYM2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE12"); LOG("return", SV(kE(SYM1, 7))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kE13"); LOG("return", SV(kE(SYM1, null))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN1"); LOG("return", SV(kN(1, "1"))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN2"); LOG("return", SV(kN(7n, 7))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN3"); LOG("return", SV(kN(SYM1, SYM1))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kN4"); LOG("return", SV(kN(NaN, NaN))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R3-3: a traced loop kernel with == and != on primitives (the
			// trace-only marker keeps it out of the leaf tier), plus object
			// operand pairs whose guard-fallback must produce Tier 0's
			// identity results identically in every tier.
			ID: -52, Kind: KindLooseEq, Seed: 152, Params: params,
			Expected: "call:kT1\nreturn:n:4\ncall:kT2\nreturn:n:3\ncall:kT3\nreturn:n:6\ncall:kT4\nreturn:n:4\ncall:kT5\nreturn:n:6\ncall:kT6\nreturn:n:3\ncall:kT7\nreturn:n:2\ncall:kT8\nreturn:n:4\ncall:kT9\nreturn:n:5\ncall:kT10\nreturn:n:6\ncall:kT11\nreturn:n:2",
			Body: `function kT(a, b, n) {
  const traceOnlyMarker = {};
  let r = 0;
  for (let i = 0; i < n; i++) {
    if (a == b) r += 1;
    if (a != b) r += 2;
  }
  return r;
}
try { LOG("call", "kT1"); LOG("return", SV(kT("2", 2, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT2"); LOG("return", SV(kT(7n, 7, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT3"); LOG("return", SV(kT("a", 1, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT4"); LOG("return", SV(kT(NaN, NaN, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT5"); LOG("return", SV(kT(OBJ_A, OBJ_B, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT6"); LOG("return", SV(kT(OBJ_A, OBJ_A, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT7"); LOG("return", SV(kT(SYM1, SYM1, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT8"); LOG("return", SV(kT(SYM1, SYM2, 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT9"); LOG("return", SV(kT(null, undefined, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT10"); LOG("return", SV(kT(null, 0, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kT11"); LOG("return", SV(kT("", "", 2))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R4-1: four-argument leaf with two call sites in one loop body
			// (argument order + multi-site inlining). Per iteration:
			// (1+2)*(3-4) = -3 and (i + i+1)*(5-2) = (2i+1)*3; i=0..3 sums to
			// (-3+3) + (-3+9) + (-3+15) + (-3+21) = 36.
			ID: -61, Kind: KindCall, Seed: 135, Params: params,
			Expected: "call:k51\nreturn:n:36",
			Body: `function leaf51(a, b, c, d) { return (a + b) * (c - d); }
function run51(n) { let s = 0; for (let i = 0; i < n; i++) { s += leaf51(1, 2, 3, 4); s += leaf51(i, i + 1, 5, 2); } return s; }
try { LOG("call", "k51"); LOG("return", SV(run51(4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R4-1: boolean-returning leaf feeding a branch. The inlined
			// `x > 2` result drives JMP_FALSE_POP; i = 3..9 count, c = 7.
			ID: -62, Kind: KindCall, Seed: 136, Params: params,
			Expected: "call:k52\nreturn:n:7",
			Body: `function leaf52(x) { return x > 2; }
function run52(n) { let c = 0; for (let i = 0; i < n; i++) { if (leaf52(i)) c++; } return c; }
try { LOG("call", "k52"); LOG("return", SV(run52(10))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R4-2: closure with two numeric upvalues read and written in
			// order (`() => { a++; b += a; return b; }`). Five calls return
			// T_1..T_5 = 1+3+6+10+15 = 35; the post call returns T_5 + 6 = 21.
			ID: -63, Kind: KindClosure, Seed: 137, Params: params,
			Expected: "call:k53\nreturn:n:35\npost:n:21",
			Body: `function make53() { let a = 0; let b = 0; return () => { a++; b += a; return b; }; }
function run53(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const C53 = make53();
try { LOG("call", "k53"); LOG("return", SV(run53(C53, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(C53()));
`,
		},
		{
			// R4-2: read-only capture (`() => a * b`) and an in-frame
			// non-escaping closure (`() => ++acc` created inside the loop
			// function). The read-only kernel adds a constant 10 per call; the
			// in-frame closure adds 1..4.
			ID: -64, Kind: KindClosure, Seed: 138, Params: params,
			Expected: "call:k54a\nreturn:n:60\ncall:k54b\nreturn:n:30\ncall:k54c\nreturn:n:10",
			Body: `function make54() { let a = 2; let b = 5; return () => a * b; }
function run54(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
const R54 = make54();
try { LOG("call", "k54a"); LOG("return", SV(run54(R54, 6))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "k54b"); LOG("return", SV(run54(R54, 3))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
function inFrame54(end) { let acc = 0; const inc = () => ++acc; let sum = 0; for (let i = 0; i < end; i++) sum += inc(); return sum; }
try { LOG("call", "k54c"); LOG("return", SV(inFrame54(4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
`,
		},
		{
			// R4-3: a stable four-shape property-read PIC. The first two shapes
			// rotate until the baseline is stable, then the third and fourth
			// shapes arrive twice each and are absorbed by the extended guard
			// (Quick tier). The four-shape rotation afterwards must keep every
			// shape on the fast path with identical results in every tier.
			ID: -55, Kind: KindPropRead, Seed: 155, Params: params,
			Expected: "call:kP1\nreturn:n:12\ncall:kP2\nreturn:n:28\ncall:kP3\nreturn:n:12\ncall:kP4\nreturn:n:28\ncall:kP5\nreturn:n:12\ncall:kP6\nreturn:n:28\ncall:kP7\nreturn:n:44\ncall:kP8\nreturn:n:44\ncall:kP9\nreturn:n:60\ncall:kP10\nreturn:n:60\ncall:kP11\nreturn:n:12\ncall:kP12\nreturn:n:28\npost:n:1,n:5",
			Body: `function kP(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a + o.b; } return s; }
const S1 = { a: 1, b: 2 };
const S2 = { a: 3, b: 4, c: 1 };
const S3 = { a: 5, b: 6, c: 2, d: 3 };
const S4 = { a: 7, b: 8, d: 4, e: 5, f: 6 };
try { LOG("call", "kP1"); LOG("return", SV(kP(S1, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP2"); LOG("return", SV(kP(S2, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP3"); LOG("return", SV(kP(S1, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP4"); LOG("return", SV(kP(S2, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP5"); LOG("return", SV(kP(S1, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP6"); LOG("return", SV(kP(S2, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP7"); LOG("return", SV(kP(S3, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP8"); LOG("return", SV(kP(S3, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP9"); LOG("return", SV(kP(S4, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP10"); LOG("return", SV(kP(S4, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP11"); LOG("return", SV(kP(S1, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kP12"); LOG("return", SV(kP(S2, 4))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(S1.a) + "," + SV(S3.a));
`,
		},
		{
			// R4-3/R4-4: a stable three-shape property-write PIC with a return
			// to the baseline. The third shape is absorbed by the extended
			// Quick-tier guard; every tier must commit the identical values
			// (the post event proves no write was lost or duplicated).
			ID: -56, Kind: KindPropWrite, Seed: 156, Params: params,
			Expected: "call:kW1\nreturn:n:4\ncall:kW2\nreturn:n:4\ncall:kW3\nreturn:n:4\ncall:kW4\nreturn:n:4\ncall:kW5\nreturn:n:4\ncall:kW6\nreturn:n:4\ncall:kW7\nreturn:n:4\ncall:kW8\nreturn:n:4\npost:n:4,n:4,n:4",
			Body: `function kW(o, n) { for (let i = 0; i < n; i++) { o.a = i; } return o.a; }
const W1 = { a: 0 };
const W2 = { a: 0, b: 1 };
const W3 = { a: 0, c: 2 };
try { LOG("call", "kW1"); LOG("return", SV(kW(W1, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW2"); LOG("return", SV(kW(W2, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW3"); LOG("return", SV(kW(W1, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW4"); LOG("return", SV(kW(W2, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW5"); LOG("return", SV(kW(W1, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW6"); LOG("return", SV(kW(W2, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW7"); LOG("return", SV(kW(W3, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
try { LOG("call", "kW8"); LOG("return", SV(kW(W3, 5))); } catch (e) { LOG("throw", e.name + ":" + e.message); }
LOG("post", SV(W1.a) + "," + SV(W2.a) + "," + SV(W3.a));
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
