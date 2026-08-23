package interpreter

import "testing"

func TestNestedAsyncGeneratorsPassThrough(t *testing.T) {
	got := vmEvalPromise(t, `
async function* inner() {
  yield "a";
  yield "b";
}
async function* mid() {
  for await (const value of inner()) yield value;
}
async function* outer() {
  for await (const value of mid()) yield value;
}
async function collect() {
  const out = [];
  for await (const value of outer()) out.push(value);
  return out.join(",");
}
collect().then(function (value) { globalThis.__r = value; });
`)
	if got != "a,b" {
		t.Fatalf("nested async generators = %q, want a,b", got)
	}
}

func TestAsyncYieldStarEmptyDelegateAfterResume(t *testing.T) {
	// 委托 generator 无产出 + 多次 resume 后再委托：贴近 consumeLine 空行场景
	got := vmEvalPromise(t, `
async function* outer() {
  const inner = function* () { return; };   // 永不 yield
  for (let i = 0; i < 5; i++) {
    yield "before-" + i;
    yield* inner();
  }
}
async function collect() {
  const out = [];
  for await (const value of outer()) out.push(value);
  return out.join(",");
}
collect().then(function (value) { globalThis.__r = value; });
`)
	if got != "before-0,before-1,before-2,before-3,before-4" {
		t.Fatalf("empty delegate after resume = %q", got)
	}
}
