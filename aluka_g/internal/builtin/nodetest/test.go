package nodetest

// node:test 内置模块——describe/it/test/beforeEach/afterEach/mock。
//
// 用法与 Node 22 一致：
//
//	import { describe, it, beforeEach, afterEach } from "node:test";
//	import assert from "node:assert";
//
//	describe("suite", () => {
//	  beforeEach(() => { ... });
//	  it("case", () => { assert.strictEqual(1, 1); });
//	});
//
// 测试注册进包级 registry；`aluka test` 子命令加载测试文件后调用
// RunRegisteredTests 执行并汇报。每个测试文件运行前 ResetTestRegistry。
// 用例/钩子支持同步与 async（返回 promise，经 vm.AwaitPromise 驱动）。
//
// 实现分布：
//   - test_registry.go —— suite 树注册、模块导出面、自定义断言
//   - test_runner.go   —— suite/用例调度、name/skip 过滤、子测试与 hook 顺序
//   - test_context.go  —— TestContext（t 参数）与单次运行状态
//   - test_mock.go     —— 函数/方法/属性 spy 与 mock tracker
//   - test_assert.go   —— deepStrictEqual/deepEqual 与错误消息
//   - test_snapshot.go —— 快照断言与 --update-snapshots
//   - test_reporters.go —— TAP/spec 报告输出
