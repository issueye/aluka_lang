# 后端虚拟机（A1 轨 · aluvm）专属任务清单

> **子团队 / 独立子代理工作域说明**：  
> 本任务清单用于指导 **后端虚拟机（aluvm）** 的独立开发与推进。  
> **解耦条件**：后端虚拟机仅依赖 [`aluka_r/docs/aluvm-isa-spec.md`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/docs/aluvm-isa-spec.md) 规范与 [`aluka-bytecode`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode) 强类型结构，**完全不需要等待 Rust 前端编译器完成**。  
> 输入直接读取已收割并通过校验的 **33 个黄金语料模块（[`aluka_r/tests/golden/corpus/*.bc`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/tests/golden/corpus/)，已 100% 覆盖 106 条操作码）**，使用现有的只读 Oracle（`aluka_g/bin/aluka.exe`）进行逐例输出一致性对拍。

---

## 1. 总体目标与交付边界

- **责任 Crates**：
  - `aluka_r/crates/aluka-core/`（`Value` 表示、堆分配器、Shape 隐藏类、GC）
  - `aluka_r/crates/aluka-vm/`（Tier 0 解释器核心分派循环、调用帧、闭包 Upvalue 环境、异常展开）
  - `aluka_r/crates/aluka-runtime/`（内置全局对象、微任务队列、Console/Process 基础支持）
  - `aluka_r/crates/aluka-cli/`（`aluvm` 虚拟机运行 CLI）
- **输入**：经 [`BytecodeModule::verify`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 校验合格的字节码模块（由 Go 编译器或黄金语料直接提供）
- **输出**：代码执行结果、副作用状态、内存回收、宿主环境交互
- **质量门禁**：
  1. 通过 33 个全指令黄金语料库端到端执行对拍（行为与 Go 版 100% 一致）；
  2. 满足内存安全硬准则，杜绝未定义行为（UB）；
  3. 执行 `fib(30)` 达到基准性能要求。

---

## 2. 详细阶段与任务分解

### 阶段 B-A1-1：核心数据表示与 ADR 定案（`A1-2`）

- [x] **T-BE-01 `Value` 表示定案：`enum` vs NaN-box `u64` 决策**
  - 背景：深入总结 Go 侧 NaN-box 槽位导致 GC 悬垂指针的教训，结合 Rust 显式句柄堆模型完成评估；
  - 产出：`docs/adr/0001-aluka-r-value-representation.md`（M0/M1 采用 16 字节安全枚举，M2 提供 `nan-boxing` 抽象门面）。

---

### 阶段 B-A1-2：GC 原型评测与选型（`A1-3`）

- [ ] **T-BE-02 分代标记-清除 vs 引用计数（RC + 循环回收）原型对比**
  - 原型 A：分代标记-清除（Bump 指针分配新生代 + 卡表 Card Table 记录跨代引用）；
  - 原型 B：带循环回收器的引用计数；
  - 测试负载：`fib(30)`（纯计算压栈）+ 对象创建与弃置循环（内存分配与回收吞吐）；
  - 必须避开的 Go 侧已证伪陷阱（血泪教训）：
    - **槽位严禁用无指针 `u64` 存放对象引用**：除非 GC 自身具备精确可达性扫描，否则会导致野指针或无法追踪存活（参考 `docs/adr/stage2-nanbox-slots-rejected.md`）；
    - **带指针对象严禁简单 Arena bump 分配**：存活的长生命周期对象会 pin 住整块 arena 导致级联保活，RSS 内存放大达 22-71 倍（参考 `docs/adr/object-arena-rejected.md`）。
  - 产出：两份评测报告与最终选型 ADR。

---

### 阶段 B-A1-3：`aluka-core` 公共 API 冻结（`A1-4`）

- [ ] **T-BE-03 冻结核心公共抽象（`Value` / `Heap` / `Shape`）**
  - 范围：
    - `Value` 基础类型操作与类型谓词；
    - `Heap` 分配器生命周期接口（`allocate`, `collect_garbage`, `add_root`）；
    - `Shape` 属性转换迁移树（Hidden Class 隐藏类转移与字段偏移定位）。
  - 交付：在 `aluka_r/crates/aluka-core/` 建立稳定的公共 API，并完成 Rustdoc 文档；在 `aluka_r/AGENTS.md` 做出冻结声明，解除后续模块的依赖风险。

---

### 阶段 B-A1-4：Tier 0 解释器全指令实现（`aluka-vm::interpreter`）

- [x] **T-BE-04 常量与字面量加载执行**
  - 指令：`PUSH_INT`, `PUSH_NEG_INT`, `PUSH_CONST`, `PUSH_NULL`, `PUSH_TRUE`, `PUSH_FALSE`, `PUSH_UNDEFINED`；
  - 规则：严格按常量池类型反序列化为对应的运行时 `Value`，支持格式化打印；
  - 证据：`aluka-vm/src/lib.rs`，测试 `tests/golden_execution_oracle_test.rs`。
- [x] **T-BE-05 算术、逻辑与一元/二元运算执行**
  - 指令：`ADD`, `SUB`, `MUL`, `DIV`, `MOD`, `POW`, `NEG`, `UNARY_PLUS`, `NOT`, `BIT_NOT`, `BIT_AND`, `BIT_OR`, `BIT_XOR`, `SHL`, `SHR`, `USHR`, `EQ`, `NE`, `STRICT_EQ`, `STRICT_NE`, `LT`, `LE`, `GT`, `GE`, `INC`, `DEC`；
  - 规则：严格对齐 JavaScript 弱类型转换与隐式类型提升语义，`01_arithmetic_bitwise.bc`、`02_literals_and_stack.bc`、`03_comparisons.bc` 逐字对拍通过；
  - 证据：`cargo test -p aluka-vm --test golden_execution_oracle_test`。
- [x] **T-BE-06 局部变量与全局变量操作**
  - 指令：`LOAD_LOCAL`, `STORE_LOCAL`, `LOAD_GLOBAL`, `DUP`, `POP`, `SWAP`；
  - 规则：局部变量采用寄存器数组/槽位直接索引，无越界 panic；
  - 证据：`aluka-vm/src/lib.rs` 单元测试与黄金用例。
- [x] **T-BE-07 控制流与条件跳转**
  - 指令：`JMP`, `JMP_TRUE_POP`, `JMP_FALSE_POP`, `JMP_TRUE_KEEP`, `JMP_FALSE_KEEP`, `JMP_NULLISH_KEEP`, `OPTIONAL_JUMP`；
  - 规则：精确更新程序计数器 `pc = (((pc as i32 * 4) + 4 + signed_off) / 4) as usize`；Keep 指令仅在不跳转时弹栈，NullishKeep 精确对齐 `a ?? b`；
  - 证据：`04_control_flow_jumps.bc` 黄金执行对拍 100% 通过。
- [x] **T-BE-08 对象字面量与原型链访问**
  - 指令：`NEW_OBJECT`, `NEW_ARRAY`, `BUILD_ARRAY`, `GET_PROP`, `GET_PROP_LOCAL`, `SET_PROP`, `SET_PROP_OBJ`, `SET_PROP_TOP`, `SET_PROP_COMPUTED_OBJ`, `GET_ELEM`, `SET_ELEM`, `SET_ELEM_TOP`, `DEL_PROP`, `DEL_ELEM`；
  - 规则：实现堆内普通对象、数组与字符串对象的句柄存储，支持计算属性名设置、动态属性删除、Getter/Setter 注册；
  - 证据：`aluka-vm/src/lib.rs`，测试 `tests/golden_execution_oracle_test.rs::test_execute_07_objects_and_properties` 与 Go 100% 对齐。

---

### 阶段 B-A1-5：函数调用与闭包环境（`aluka-vm::call`）

- [x] **T-BE-09 调用帧（CallFrame）与调用栈管理**
  - 范围：实现函数调用环境隔离（`invoke_function`）；
  - 指令：`CALL`, `CALL_METHOD`, `CALL_WITH_THIS`, `RETURN`, `RETURN_UNDEF`；
  - 规则：正确传递实参，处理 `this` 绑定（locals[0]），维护被调用者的独立常量池与槽位空间，确保调用返回后环境精准恢复；
  - 证据：`tests/golden_execution_oracle_test.rs::test_execute_05_optional_chaining` 与 Go 100% 对齐。
- [x] **T-BE-10 闭包与上值（Upvalue）捕获运行时**
  - 指令：`MAKE_CLOSURE`, `LOAD_UPVALUE`, `STORE_UPVALUE`, `CLOSE_UPVALUES`；
  - 规则：
    - 打开状态（Open Upvalue）：通过 `open_upvalues` 哈希表保证同一槽位共享同一 `RefCell` 实例，随 `StoreLocal` 自动同步；
    - 关闭状态（Closed Upvalue）：离开作用域时通过 `CLOSE_UPVALUES` 或函数返回自动转为独立堆持有；
    - 修复了反序列化中 `UpvalueCapture` 结构体 `is_local` 与 `index` 的字段次序问题；
    - 支持数组方法 `map` 与 `join` 的闭包动态回调；
  - 证据：`aluka-vm/src/lib.rs`，测试 `tests/golden_execution_oracle_test.rs::test_execute_06_closures_and_upvalues` 与 Go Oracle 逐字符 100% 对齐（输出 `12` 与 `0,1,2`）。
- [x] **T-BE-11 ES6 Class 运行时**
  - 指令：`MAKE_CLASS`, `NEW`, `CONSTRUCT_THIS`, `CALL_THIS`, `GET_PROTO`, `INSTANCEOF`，以及数组切片/展开 `ARRAY_SPREAD`, `slice`，字符串加法自动拼接；
  - 规则：构造类原型（`prototype`）对象与构造函数，处理 `super` 原型链绑定，管理静态方法与 Getter/Setter，原型链递归查找与实例类型判定；
  - 证据：`aluka-vm/src/lib.rs`，测试 `tests/golden_execution_oracle_test.rs::test_execute_08_arrays_and_methods`（输出 `0 1 2,3,4,5,6`）与 `test_execute_09_classes_and_inheritance`（输出 `Hello Alice (Engineer) true true`），与 Go 原型 Oracle 逐字符 100% 对齐并通过。

---

### 阶段 B-A1-6：高级特性与异常处理（`aluka-vm::advanced`）

- [ ] **T-BE-12 异常抛出与 TryTable 展开处理**
  - 指令：`THROW`, `TRY_ENTER`, `TRY_EXIT_JMP`；
  - 规则：
    - 当发生运行时异常或执行 `THROW` 时，在当前函数的 `try_table` 中根据 `pc` 查找保护区间 `[start_pc, end_pc)`；
    - 若有 Catch 块，将 PC 重定向至 `catch_pc` 并将异常对象压入栈顶；
    - 若有 Finally 块，优先调度 Finally 执行；
    - 若当前帧无捕获，按调用栈级联逐层展开（Unwind），直到顶层未捕获报错退出。
- [ ] **T-BE-13 生成器（Generator）与 Async 协程挂起恢复**
  - 指令：`YIELD`, `AWAIT`；
  - 规则：
    - `YIELD`：打包当前调用帧状态（PC、局部变量槽位、操作数栈），将控制权返回给调度者；
    - `gen.next(val)`：恢复调用帧，将 `val` 作为 yield 表达式的结果压入栈顶继续执行；
    - `AWAIT`：挂起当前执行上下文，接入微任务队列回调。

---

### 阶段 B-A1-7：虚拟机 CLI 与逐例对拍（`aluvm`）

- [ ] **T-BE-14 `aluvm` 独立虚拟机 CLI 命令行**
  - 功能：支持 `aluvm run input.bc`、`aluvm --version`、参数传递（`process.argv`）；
  - 规则：提供标准退出码映射与友好的未捕获异常堆栈回溯展示。
- [ ] **T-BE-15 33 个黄金语料全量行为对拍（D1 轨交付）**
  - 编写对拍驱动测试：`cargo test -p aluka-vm --test golden_execution_oracle_test`；
  - 规则：逐例执行 `aluka_r/tests/golden/corpus/*.bc`，将输出与 Go 版执行结果比对，实现 100% 行为一致。

---

## 3. 验收标准与测试驱动方式

```bash
# 1. 运行 VM 核心单元测试
cargo test -p aluka-vm

# 2. 端到端执行 33 个全指令黄金语料
cargo test -p aluka-vm --test golden_execution_oracle_test

# 3. 对照 Go 版基线对拍
aluka_r/target/debug/aluka run tests/golden/corpus/01_arithmetic.bc
aluka_g/bin/aluka.exe run tests/golden/corpus/01_arithmetic.bc
```

- **完成标志**：
  1. 33 个包含全 106 条操作码的黄金语料在 `aluvm` 上全部正常跑通；
  2. 算术、控制流、函数闭包、对象属性、Try-Catch-Finally 及生成器输出完全一致；
  3. 全工作区无内存泄漏与 UB 风险。
