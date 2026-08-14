import { BaseNode, ComputeNode, TransformNode } from "./node.ts";
import { createGraphProxy } from "./proxy.ts";
import { rangeStream, AsyncPipeline } from "./stream.ts";
import { GraphMeta, Serializable } from "./symbols.ts";

console.log("=== [Project 1] Advanced Meta-Graph & Async Stream Pipeline ===");

// 1. 实例化节点并配置计算链
const doubler = new ComputeNode("doubler", 2);
doubler.addOperation((x) => x + 10).addOperation((x) => x * 3);

const formatter = new TransformNode("formatter", (data: any) => {
  return {
    wrapped: true,
    value: String(data),
    timestamp: Date.now(),
  };
});

// 2. 注册到 Proxy Graph DSL
const nodeMap = new Map<string, BaseNode>([
  ["doubler", doubler],
  ["formatter", formatter],
]);

const graph = createGraphProxy(nodeMap);

console.log(`[GraphDSL] Initial nodes count: ${graph.count()}, list: ${graph.list().join(", ")}`);

// 3. 验证 Proxy 动态调用与派发
const computeRes = await graph.exec_doubler(5);
// (5 * 2 = 10) -> (+10 = 20) -> (*3 = 60)
console.log(`[ComputeNode] 5 -> doubler -> result: ${computeRes.result}, history: [${computeRes.history.join(" -> ")}]`);

if (computeRes.result !== 60) {
  throw new Error(`Expected result 60, got ${computeRes.result}`);
}

const formatRes = await graph.exec_formatter(computeRes.result);
console.log(`[TransformNode] Formatted: wrapped=${formatRes.wrapped}, value=${formatRes.value}`);

// 4. 符号与元数据检查
const meta = (doubler as any)[GraphMeta];
console.log(`[Metadata] Doubler created at: ${meta.created}, tags: ${meta.tags.join(",")}, version: ${String(meta.version)}n`);

const serialized = (doubler as any)[Serializable]();
console.log(`[Serialization] ${serialized}`);

// 5. 异步生成器管道流处理 (for await ... of + map + filter + reduce)
console.log("[AsyncPipeline] Starting stream calculation from range 1..10...");

const pipeline = new AsyncPipeline(rangeStream(1, 10))
  .filter((x) => x % 2 === 0) // 2, 4, 6, 8, 10
  .map(async (x) => {
    const res = await graph.exec_doubler(x);
    // (x * 2 + 10) * 3
    return res.result;
  });

const streamResults = await pipeline.toArray();
console.log(`[AsyncPipeline] Evens doubled: [${streamResults.join(", ")}]`);

const sum = await new AsyncPipeline(rangeStream(1, 5)).reduce((acc, curr) => acc + curr, 0);
console.log(`[AsyncPipeline] Reduce sum(1..5) = ${sum}`);

if (sum !== 15) {
  throw new Error(`Expected sum 15, got ${sum}`);
}

console.log(`[import.meta] url: ${import.meta.url}, main: ${import.meta.main}`);
console.log("=== [Project 1] Completed Successfully! ===");
