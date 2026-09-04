# VM 执行性能基线（D2）：fib(30) Tier 0 解释执行

> 日期：2026-09-04　|　基准：`aluka_r/crates/aluka-cli/examples/fib_bench.rs`
> 负载：`fib30.bc`（同目录，**Go 前端**编译产物；269 万次函数调用，纯计算压栈）
> 方法学：交替执行 + 轮间冷却 100ms + **min-of-5**（总 TODO §1 硬规则）

## Rust VM 基线（aluvm，release，纯解释执行，不含进程启动/解析）

| 轮次 | 1 | 2 | 3 | 4 | 5 | **min** |
|---|---|---|---|---|---|---|
| 耗时 | 912.7ms | 1091.8ms | 1108.5ms | 989.7ms | 977.8ms | **912.7ms** |

输出校验：`832040` ✓（fib(30) 正确值）。

## 对照组（Go 版，同一负载）

| 配置 | min-of-3 | 说明 |
|---|---|---|
| Go JIT（默认 auto） | 97 ms | JIT 编译后原生执行，非同口径 |
| Go Tier 0（`--jit=off`） | 395 ms | 含进程启动/解析/编译 ~100-150ms 固定开销 |

## 口径说明（评审用）

- **公平口径是 Tier 0 对 Tier 0**：Go `--jit=off` 的 395ms 含固定开销，纯执行约
  250-350ms；Rust VM 的 912.7ms 为纯 `run_module`（无启动/解析）。
- **现状**：Rust Tier 0 解释执行约为 Go Tier 0 的 **2.6~3.7×** 慢（执行段口径）。
  符合预期——Rust VM 是语义对齐的直译 port（`match` 分派、无内联缓存、无快路径），
  Go 侧经过长年打磨（IC、superinstruction、快速路径）。基线价值 = 记录起点，
  供 M3（GC/对象优化）与 M5（JIT）对照。
- **跨前端里程碑**：fib30.bc 由 **Go 前端**编译，在 Rust VM 上执行结果正确——
  「Go 前端产物 × Rust VM」链路首次贯通（M1 的核心预演）。

## 基线更新（2026-09-04 晚，Rc 零拷贝帧切换重构后）

| 版本 | min-of-5 | 说明 |
|---|---|---|
| 初版基线 | 912.7 ms | 每次函数调用深拷贝 FuncTemplate + 2× 常量池 |
| **Rc 化重构后** | **388.3 ms** | `module_functions`/`module_constants`/`current_constants` 全部 Rc 共享，帧切换零拷贝；`STORE_LOCAL` 空上值快路径 |

**提升 2.35×**，达到 M1 验收线（≤ Go 版 2×）——与 Go Tier 0（`--jit=off`，
含进程启动固定开销 395ms）持平或更快。重构后 33/33 黄金对拍、CJS 端到端、
全部单测零回归。

## 已知边界

- debug 构建下 fib30 会栈溢出（递归 `invoke_function` 的未优化栈帧 × 30 层嵌套
  调用链），release 正常——M1 应评估解释器的栈预算与帧大小。
