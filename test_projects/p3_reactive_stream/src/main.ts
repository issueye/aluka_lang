import { AsyncEventEmitter } from "./event_emitter.ts";
import { BatchBuffer, retryWithBackoff, sleep } from "./operators.ts";
import { ServiceA } from "./circular_a.ts";
import { ServiceB } from "./circular_b.ts";

console.log("=== [Project 3] Reactive Event Stream & Circular Dependency System ===");

// 1. 验证 ESM 循环依赖解析
const sA = new ServiceA();
const sB = new ServiceB();
console.log(`[Circular ESM] sA.describe(): "${sA.describe()}"`);
console.log(`[Circular ESM] sB.describe(): "${sB.describe()}"`);
console.log(`[Circular ESM] sA.createSibling(): "${sA.createSibling().name}"`);
console.log(`[Circular ESM] sB.createSibling(): "${sB.createSibling().name}"`);

if (sA.describe() !== "ServiceA -> NameFromB" || sB.describe() !== "ServiceB -> NameFromA") {
  throw new Error("Circular dependency resolution failed!");
}

// 2. 验证异步事件调度器与 AbortSignal
const emitter = new AsyncEventEmitter();
const receivedEvents: string[] = [];

const controller = new AbortController();
emitter.on("tick", (msg: string) => {
  receivedEvents.push(`listener1:${msg}`);
}, { signal: controller.signal });

emitter.once("tick", (msg: string) => {
  receivedEvents.push(`once:${msg}`);
});

await emitter.emit("tick", "first");
console.log(`[EventEmitter] After first tick: [${receivedEvents.join(", ")}]`);

// 取消 listener1
controller.abort();
await emitter.emit("tick", "second");
console.log(`[EventEmitter] After abort & second tick: [${receivedEvents.join(", ")}]`);

if (receivedEvents.length !== 2) {
  throw new Error(`Expected 2 received events, got ${receivedEvents.length}`);
}

// 3. 验证 BatchBuffer 批处理
const batches: number[][] = [];
const buffer = new BatchBuffer<number>(3).onFlush(async (items) => {
  batches.push(items);
});

for (let i = 1; i <= 7; i++) {
  await buffer.push(i);
}
await buffer.flush();

console.log(`[BatchBuffer] Flushed batches: ${JSON.stringify(batches)}`);
// Expect [[1,2,3], [4,5,6], [7]]
if (batches.length !== 3 || batches[0].length !== 3 || batches[2][0] !== 7) {
  throw new Error("BatchBuffer unexpected output!");
}

// 4. 验证指数退避重试 (retryWithBackoff)
let attemptsMade = 0;
const retryRes = await retryWithBackoff(async (attempt) => {
  attemptsMade++;
  if (attempt < 2) {
    throw new Error(`Simulated failure on attempt ${attempt}`);
  }
  return `Success on attempt ${attempt}`;
}, 3, 5);

console.log(`[RetryWithBackoff] Result: "${retryRes}", total attempts: ${attemptsMade}`);
if (attemptsMade !== 3) {
  throw new Error(`Expected 3 attempts, got ${attemptsMade}`);
}

// 5. 验证 Promise.allSettled 并发聚合
const tasks = [
  Promise.resolve(100),
  Promise.reject(new Error("planned error")),
  sleep(10).then(() => 300),
];

const settled = await Promise.allSettled(tasks);
const fulfilled = settled.filter((s) => s.status === "fulfilled").length;
const rejected = settled.filter((s) => s.status === "rejected").length;
console.log(`[Promise.allSettled] Fulfilled: ${fulfilled}, Rejected: ${rejected}`);

if (fulfilled !== 2 || rejected !== 1) {
  throw new Error(`Promise.allSettled status mismatch!`);
}

console.log("=== [Project 3] Completed Successfully! ===");
