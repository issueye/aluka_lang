# ADR：N-API（.node 原生插件）——永久非目标

- 状态：已接受（Accepted）
- 日期：2026-08-06
- 关联：docs/node22-full-api-development-plan.md §M9（架构阻塞项）；manifest 的
  `process.dlopen`、node:module 原生插件面

## 现状（Context）

Node 22 支持 N-API（napi.h C ABI）加载 `.node` 原生插件：C/C++ 代码以 V8
句柄/Isolate 内存模型编译为动态库，`process.dlopen` / `require()` 在运行时
加载并执行机器码。manifest 中 `process.dlopen` 属于 process 导出面。

aluka 是纯 Go 运行时，自研字节码 VM（internal/engine/interpreter），对象模型
（engine.Value）与 V8 句柄语义完全不同构，且进程内不承载 V8。

## 决策（Decision）

**N-API / `.node` 原生插件为永久非目标（permanent non-goal）。**

- 不引入 cgo 链接 V8 / 其他 JS 引擎来获得 N-API 面（违背纯 Go 定位）。
- 不实现 N-API C ABI shim（需要 v8::Isolate 句柄语义、external String、
  TypedArray backing store 对齐等，全部依赖 V8 内存模型）。
- `process.dlopen` 提供方法面但拒绝加载 `.node`：返回明确的错误（Node 风格
  `ERR_DLOPEN_FAILED` 语义），绝不静默成功。

## 理由（Rationale）

1. N-API 的正确性取决于 V8 对象生命周期（句柄作用域、垃圾回收根、Isolate
   隔离）。在 aluka 对象模型上复刻这些语义，等于重新实现 V8 接口层，成本
   超过任何兼容收益。
2. 加载任意机器码（`.node`）本身是安全边界；纯 Go 运行时不提供该机制是
   特性而非缺陷。
3. 生态现状：现代 npm 包优先纯 JS / WASM 路径，`.node` 插件主要用于性能
   敏感场景；aluka 面向 Node 兼容的可移植子集，明确排除原生插件面。

## 验收（Acceptance）

- [x] `process.dlopen` 存在且调用时抛出明确错误（不加载、不崩溃）。
- [x] require 一个 `.node` 文件路径时返回可诊断的错误信息。
- [x] 完成率统计不把 N-API 面计入 L4（按非目标处理）。

## knownDifference

- `process.dlopen`：Node 可加载原生插件；aluka 仅提供报错方法面。
- 依赖 `.node` 插件的包无法运行（预期、记录在案）。
