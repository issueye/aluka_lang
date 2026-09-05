# M5 里程碑证据：JIT（J1 子集阶段，feat/m5-jit，2026-09-05）

## 任务对账（devplan §M5）

| 任务 | 状态 | 载体 |
|---|---|---|
| Quick IR（常量折叠等）→ Cranelift（低风险优先） | ✅ J1 | `crates/aluka-jit`（`peephole.rs` + `lib.rs`）；ADR 0005 |
| 属性 PIC（shape guard）、去优化恢复、栈映射 | ⏳ J2（未落地，策略已定） | ADR 0005 决定 2/4 |
| 闭包/upvalue/数组下标特化 | ⏳ J3（未落地） | ADR 0005 决定 2 |
| jitdiff 生成式差分 + fuzz target | ✅ 差分（3200 例零失配）；fuzz target ⏳ | `crates/aluka-jit/tests/jitdiff.rs` |
| 交付物：propAccess/callOverhead 与 node 对拍表 | ✅ 现状表（J1 不覆盖该两项，如实登记） | 本文件 §对拍表 |

## J1 落地内容

- **后端**：Cranelift 0.115（`cranelift`/`cranelift-jit`/`cranelift-module`/
  `cranelift-native`）——依赖树纯 Rust，无 ring/C/asm；crate 本地 lint 覆盖
  `unsafe_code = "allow"`（AGENTS 明文的 JIT 例外），机器码 transmute 处带
  SAFETY 论证。
- **子集**：`PushConst/LoadLocal/StoreLocal/Add/Sub/Mul/Div/Lt/Gt/Eq/Jmp/
  JmpFalsePop/Return/Pop/Nop`，f64 值域（布尔 1.0/0.0），签名
  `(args_ptr, len) -> f64`。子集外操作码 → `JitError::UnsupportedOpcode`,
  调用方回退解释器。
- **控制流**：块按跳转目标切分，局部经 Cranelift `Variable`（SSA 由
  frontend 自动构造）；`JmpFalsePop` 的真值判定复刻 VM 语义（非零 **且**
  非 NaN，`fcmp NotEqual & fcmp Ordered`）。
- **Quick IR**：常量折叠 peephole + **跳转操作数重映射**（折叠移除指令后
  按旧→新 pc 映射重算相对偏移；窗口内含跳转目标时保守不折叠）。

## 验收证据

- **jitdiff（核心）**:`jitdiff_3200_generated_cases_zero_mismatch` —— 3200
  例随机生成子集函数（常量初始化 + 有界循环 + 随机算术/比较尾表达式），
  三路验证（ISA verify → 解释器 → JIT），f64 **逐位相等**，0 失配。
- **单测**:`minimal` 4 例（常量返回/参数透传/算术/countdown 循环）、
  `peephole` 5 例（折叠/跳转重映射/窗口保守/NaN 跳过/回边）。
- **命令证据**:`cargo test` 全量 62 目标绿；`cargo clippy --all-targets
  -D warnings` 0 错误；fmt 通过。

## 性能基线（方法学：交替执行 + 冷却 50ms + min-of-5）

负载 `hot_loop`:`n=200000; while n>0 { acc += n*1.5; n -= 1 }`，两侧结果
逐位一致（30000150000）。

| 执行体 | min-of-5 | 相对 |
|---|---|---|
| aluvm 解释器（release, in-process） | 14.02 ms | 1× |
| **aluka-jit 机器码（release, in-process）** | **0.34 ms** | **41.6× 加速** |
| node 22（V8 预热后 in-process） | 0.095 ms | JIT/node ≈ 3.6× |

**结论**：J1 JIT 相对自家解释器 **31~42×**（多次测量区间），相对 V8 预热
后约 3.6×——已落在 devplan「≤ node 5×」的量级区间内（该验收项针对
propAccess/callOverhead，见下）。

### propAccess / callOverhead 对拍表（现状，J1 不覆盖）

负载：`propSum`（200k 次 `o.x + o.y`）与 `callOverhead`（200k 次函数调用）。

| 项 | node 22（预热 in-process） | aluvm 解释器 | 说明 |
|---|---|---|---|
| propAccess | 0.096 ms | 进程整体 89.0 ms（含 15.0 ms 进程基底；两负载合计） | 属性访问未进 J1 子集（需 PIC） |
| callOverhead | 0.180 ms | 同上（合并测量） | 调用未进 J1 子集（需调用约定+栈映射） |

**如实登记**：两项验收（≤ node 5×）**未达成**，因 J1 子集不含属性访问与
调用。达成路径见 ADR 0005 决定 2 的 J2 阶段（PIC shape guard + 调用约定 +
去优化恢复），届时以同一方法学重测本表。解释器侧当前为跨进程测量（含
~15ms 进程基底），J2 落地时改为 in-process 计时以可比。

## 已知限制（登记）

1. **J1 子集边界**：无对象/字符串/数组/调用/闭包；遇非子集操作码编译期
   拒绝并回退解释器（无部分去优化）。
2. **未接入 aluvm 执行路径**:J1 的 `jit_compile` 尚未挂到解释器热点触发
   （无 OSR/计数器），当前经测试与基准直接驱动——接线属 J2 工作项（需
   与去优化协议一起设计，避免半成品热切换）。
3. **fuzz target（5 个）** 未落地：jitdiff 生成式差分已覆盖同类目标；
   `cargo-fuzz` 接入登记为后续项。
4. 参数传递：JIT 走 `(args_ptr, len)`，解释器走帧槽绑定——jitdiff 生成的
   函数为 0 参以消除该差异，参数路径由 `minimal::t2/t3` 单测覆盖。
