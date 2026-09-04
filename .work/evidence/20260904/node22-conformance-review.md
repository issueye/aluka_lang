# node22 conformance 实测评估（M1 收口项，2026-09-04）

> 方式：17 个用例各经 **Go 前端**编译为 `.bc`（缓存增量识别）→ `aluvm` 执行 →
> 与 Go Oracle（`aluka run` 源码）逐字对比。复现脚本思路见本文件 §3。
> 防假阳性：Go 侧自身运行失败（空输出）的用例不计 PASS。

## 1. 实测结果：0 有效通过 / 14 失败 / 3 无效对比

首轮的 3 个"PASS"（07-x509、15-test-runner、16-m7-test-core）经复核全部是
**"双空"假阳性**——Go 侧自身因缺 `node:crypto` 等内置运行失败输出为空，
与 Rust 的空输出偶然相等。这 3 个用例在 Go 版上同样未通过（Go 版 CI 的
conformance 也依赖 Node 环境），对 Rust 的对比无意义。

## 2. 失败分类（14 项）

| 类别 | 用例 | 缺口 | 归属 |
|---|---|---|---|
| **Node 内置模块缺失**（9） | 01(node:stream)、08(node:http)、10(node:http2)、11(node:cluster)、12(node:dgram)、14(node:trace_events)、09(node:inspector)、13(node:test)、07(node:crypto/x509) | `Cannot find module 'node:xxx'`——内置模块库 | **M2+**（内置库建设） |
| **语义缺口**（4） | 02-thisarg、17-arguments、04-nexttick、05-es2024 | 见 §2.1 | **M1 可修（2）/ 需微任务队列（2）** |
| **ESM/分发**（1） | 03-require-esm（+03-esm.mjs） | `require("./03-esm.mjs")` 的 hash 缓存布局映射 + ESM 语义 | M2（模块系统完整） |

### 2.1 语义缺口明细（M1 口径内可修复）

| 用例 | 缺口 | 修复点（后端 VM） |
|---|---|---|
| 02-thisarg | `Array.prototype` 的 `forEach/some/find/filter/reduce/reduceRight` 未实现，且 thisArg（第二参数）未传递 | `CALL_METHOD` 数组分支扩展五个方法 + this 绑定参数 |
| 17-arguments | `arguments` 对象语义缺失（`arguments.length/[i]` 全部 undefined） | 函数帧提供 arguments 对象（实参数组 + length） |
| 04-nexttick | `process.nextTick` 未实现（微任务队列） | 需微任务队列基建（M2 async 配套） |
| 05-es2024 | `Promise.withResolvers` / `Array.fromAsync` / `Object.groupBy` 未实现 | 同上（依赖 Promise/微任务基建） |

## 3. M1 验收口径评估

devplan M1 验收「node conformance ≥8/11」在本引擎当前形态下**不可达**：
17 个用例中 9 个依赖 Node 内置库（Go 版同样依赖——其 CI 的 conformance 需要
Node 环境），属 **M2 内置库建设**范围；纯语义类 4 项中 2 项（thisArg/arguments）
可短期修复，另 2 项依赖微任务队列。

**评审建议**：M1 关闭时将 conformance 项标注为「实测完成：0/17 有效通过；
4 项语义缺口列入 M1→M2 backlog；9 项 Node 内置依赖归 M2」——M1 的核心价值
（跨前端字节码互通、全指令语义、CJS/fs、性能线）已全部达成，不应被 Node
内置库缺口阻塞里程碑。

## 4. 复现

```bash
# 每用例：Go 编译（缓存增量）→ aluvm 执行 vs Go Oracle 对比
# 多模块用例（03）需按 disasm 特征分发；脚本细节见评审会话记录
ALUKA=aluka_g/bin/aluka.exe  # Go oracle + 前端
ALUVM=aluka_r/target/release/aluvm.exe
```
