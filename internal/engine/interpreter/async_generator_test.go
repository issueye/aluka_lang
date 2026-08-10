package interpreter

import "testing"

func TestAsyncGeneratorAwaitsBeforeYield(t *testing.T) {
	got := vmEvalPromise(t, `
async function* values() {
  yield await Promise.resolve("a");
  yield await Promise.resolve("b");
}
async function collect() {
  const out = [];
  for await (const value of values()) out.push(value);
  return out.join(",");
}
collect().then(function(value) { globalThis.__r = value; });
`)
	if got != "a,b" {
		t.Fatalf("async generator values = %q, want a,b", got)
	}
}

func TestAsyncGeneratorEventStreamWakeup(t *testing.T) {
	got := vmEvalPromise(t, `
function createEventStream() {
  const queue = [];
  const waiting = [];
  let done = false;
  return {
    push(event) {
      const waiter = waiting.shift();
      if (waiter) waiter({ value: event, done: false });
      else queue.push(event);
    },
    end() {
      done = true;
      while (waiting.length > 0) waiting.shift()({ value: undefined, done: true });
    },
    async *[Symbol.asyncIterator]() {
      while (true) {
        if (queue.length > 0) {
          yield queue.shift();
        } else if (done) {
          return;
        } else {
          const result = await new Promise((resolve) => waiting.push(resolve));
          if (result.done) return;
          yield result.value;
        }
      }
    }
  };
}

async function consume(stream) {
  const events = [];
  for await (const event of stream) events.push(event);
  return events.join("|");
}

const stream = createEventStream();
consume(stream).then(
  function(value) { globalThis.__r = value; },
  function(error) { globalThis.__r = "error:" + error.message; }
);
stream.push("start");
stream.push("delta");
stream.end();
`)
	if got != "start|delta" {
		t.Fatalf("event stream result = %q, want start|delta", got)
	}
}

func TestAsyncGeneratorClassEventStreamWakeup(t *testing.T) {
	got := vmEvalPromise(t, `
class EventStream {
  constructor() {
    this.queue = [];
    this.waiting = [];
    this.done = false;
  }
  push(event) {
    const waiter = this.waiting.shift();
    if (waiter) waiter({ value: event, done: false });
    else this.queue.push(event);
  }
  end() {
    this.done = true;
    while (this.waiting.length > 0) {
      this.waiting.shift()({ value: undefined, done: true });
    }
  }
  async *[Symbol.asyncIterator]() {
    while (true) {
      if (this.queue.length > 0) {
        yield this.queue.shift();
      } else if (this.done) {
        return;
      } else {
        const result = await new Promise((resolve) => this.waiting.push(resolve));
        if (result.done) return;
        yield result.value;
      }
    }
  }
}

async function consume(stream) {
  const events = [];
  for await (const event of stream) events.push(event);
  return events.join("|");
}

const stream = new EventStream();
consume(stream).then(
  function(value) { globalThis.__r = value; },
  function(error) { globalThis.__r = "error:" + error.message; }
);
stream.push("start");
stream.push("delta");
stream.end();
`)
	if got != "start|delta" {
		t.Fatalf("class event stream result = %q, want start|delta", got)
	}
}

func TestNestedAsyncGeneratorPipeline(t *testing.T) {
	got := vmEvalPromise(t, `
async function* source() {
  yield await Promise.resolve("a");
  yield await Promise.resolve("b");
}
async function* map() {
  for await (const value of source()) {
    yield value + "!";
  }
}
async function collect() {
  const values = [];
  for await (const value of map()) values.push(value);
  return values.join(",");
}
collect().then(
  function(value) { globalThis.__r = value; },
  function(error) { globalThis.__r = "error:" + error.message; }
);
`)
	if got != "a!,b!" {
		t.Fatalf("nested async generator result = %q, want a!,b!", got)
	}
}

func TestAsyncIteratorClassStoredFunction(t *testing.T) {
	got := vmEvalPromise(t, `
class Stream {
  constructor(iterator) {
    this.iterator = iterator;
  }
  [Symbol.asyncIterator]() {
    return this.iterator();
  }
}
async function* source() {
  yield "ok";
}
async function collect() {
  const out = [];
  for await (const value of new Stream(source)) out.push(value);
  return out.join(",");
}
collect().then(function(value) { globalThis.__r = value; });
`)
	if got != "ok" {
		t.Fatalf("stored async iterator function = %q, want ok", got)
	}
}

func TestAsyncIteratorNestedFunctionInClassMethod(t *testing.T) {
	got := vmEvalPromise(t, `
class Stream {
  constructor(iterator) {
    this.iterator = iterator;
  }
  static from() {
    async function* iterator() {
      yield "ok";
    }
    return new Stream(iterator);
  }
  [Symbol.asyncIterator]() {
    return this.iterator();
  }
}
async function collect() {
  const out = [];
  for await (const value of Stream.from()) out.push(value);
  return out.join(",");
}
collect().then(function(value) { globalThis.__r = value; });
`)
	if got != "ok" {
		t.Fatalf("nested class-method async iterator = %q, want ok", got)
	}
}
