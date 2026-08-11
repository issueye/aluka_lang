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
