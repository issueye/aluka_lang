# 2026-09-04 · 每日 TODO

> 总 TODO 见 [../README.md](../README.md)；证据规则见其 §1（**不满足即不许勾选**）。

**里程碑**：M0（ISA 规范化 + 技术原型）　|　**轨道**：F 轨（全线贯通，解锁并行）　|　**上一日**：[20260903](../20260903/README.md)

---

## 1. 今日目标（达成前后端完全并行开发的硬前提）

1. **完成 F 轨全套规范与校验体系（F1-F4）**：
   - 提取 106 条 ISA 事实表（TSV/JSON）；
   - 编写 `aluka_r/docs/aluvm-isa-spec.md` 逐指令规范，记录全部编码与格式陷阱；
   - 建立 V1..V16 编号校验规则，明确 Go 侧现状与缺失。
2. **Rust 侧 ISA 强类型契约落地**：
   - 扩展 `aluka-bytecode` crate，将 `Op` 枚举扩充为 106 条指令，实现编译期穷尽 `match` 栈效果与元数据映射；
   - 保证编译期保证，阻断漏写；全工作区测试全绿。
3. **完成 F5 golden 语料库收割与 100% 覆盖率**：
   - 自动生成 30+ 项覆盖算术、控制流、闭包、Class、Try-Finally、生成器、Async、正则等测试源码；
   - 驱动 Go 编译器收割真实 `.bc` 缓存文件，达成 106/106 全指令覆盖，产出索引与覆盖率报告；
   - 编写 Rust 集成测试 `golden_corpus_test.rs` 完成端到端自动断言。

---

## 2. 待办清单

| # | 待办 | 状态 | 关联总 TODO 项 |
|---|------|------|---------------|
| 1 | 编写只读 AST 提取工具 `aluka_r/tools/export_isa.go` | `[x]` | F1 |
| 2 | 导出 `isa-facts.tsv` 与 `isa-facts.json` 事实表（106 行） | `[x]` | F1 |
| 3 | 编写 `aluka_r/docs/aluvm-isa-spec.md` 逐指令规范文档 | `[x]` | F2 |
| 4 | 记录大端 uint24、打包双字段、跳转边界、int32 哨兵、十进制 BigInt 等陷阱 | `[x]` | F3 |
| 5 | 制定 V1..V16 编号校验规则，标注 Go 侧已有/缺失 | `[x]` | F4 |
| 6 | 扩展 `aluka-bytecode` 的 `Op` 枚举为 106 条并实现编译期穷尽元数据 | `[x]` | M0 ISA 契约 |
| 7 | 编写 `harvest_golden.go` 收割 Go 前端产物并达到 106/106 覆盖率 | `[x]` | F5 |
| 8 | 编写 Rust 集成测试 `golden_corpus_test.rs` 自动化断言 106 条覆盖率 | `[x]` | F5, D1 |
| 9 | 实现 Rust 字节码校验器 `verifier.rs`（覆盖 V1..V16 全部规则） | `[x]` | A1-1 |
| 10 | 编写 `verifier_test.rs` 测试套件（33 个 golden 100% 通过 + 16 个规则反例拒绝） | `[x]` | A1-1 |
| 11 | 全工作区 `cargo test`、`cargo clippy`、`cargo fmt` 全绿通过 | `[x]` | D3 |
| 21 | 在 `aluka_r/AGENTS.md` 中增加模块化单元拆分硬性铁律（禁止大单体 `lib.rs`） | `[x]` | 架构治理 |
| 22 | 将 `aluka-compiler` 拆分为 `error.rs`、`scope.rs`、`codegen.rs` 与门面 `lib.rs` | `[x]` | 架构治理 |
| 23 | 将 `aluka-vm` 拆分为 `value.rs`、`heap.rs`、`ops.rs`、`property.rs`、`class.rs`、`call.rs`、`interpreter.rs` 与门面 `lib.rs` | `[x]` | 架构治理 |
| 24 | 修复跨模块类型映射，保持全工作区 73 项测试、Clippy 与 Fmt 100% 全绿 | `[x]` | 质量门禁 |

---

## 3. 达成目标证据

### 待办 1-2 · F1 导出 106 条 ISA 事实表
**结论**：达成  
**证据类型**：命令 + 产物  
```bash
cd aluka_r/tools && go run export_isa.go
```
- TSV 产物：`.work/evidence/20260904/isa-facts.tsv`（107 行，SHA256: `B3E22A9928E1B2EF34B27A0BAA91B4A3B16A5868B7940560EC6468C16094215E`）
- JSON 产物：`.work/evidence/20260904/isa-facts.json`（1910 行，SHA256: `63D99C6A7DA48B309A7BE7C517725916A46A5126B45A8FA71B3E0C8BC7CE7496`）
- 11 种 `OperandKind` 全部覆盖（无空缺）。

### 待办 3-5 · F2/F3/F4 逐指令规范与校验规则成文
**结论**：达成  
**证据类型**：产物  
- 文件路径：`aluka_r/docs/aluvm-isa-spec.md`（共 318 行，26KB）
- 包含 106 条指令全集操作码、名称、操作数、栈效果、语义与陷阱；
- 包含大端 uint24、打包双字段、跳转允许 `target == len(code)`、哨兵槽位 `-1` 转 `int32`、十进制 BigInt 等陷阱；
- 包含 V1..V16 完整校验规则，明确标注了 Go 侧缺失的 11 条安全校验项。

### 待办 6 · Rust 侧 106 条强类型 ISA 契约落地
**结论**：达成  
**证据类型**：命令 + 代码  
- 文件路径：`aluka_r/crates/aluka-bytecode/src/op.rs`（全 106 条操作码枚举、`OperandKind`、`StackEffect`、穷尽 `match` 栈效果）；
- 并在 `aluka-vm` 中显式匹配未实现操作码，杜绝任何隐式兜底；
- `cargo test -p aluka-bytecode` 输出：`4 passed; 0 failed`（包含 106 条操作码往返编码测试）。

### 待办 7-8 · F5 golden 语料 106/106 全覆盖与 Rust 集成测试
**结论**：达成  
**证据类型**：命令 + 产物  
运行收割与测试命令：
```bash
cd aluka_r/tools && go run harvest_golden.go
cd ../../aluka_r && cargo test -p aluka-bytecode --test golden_corpus_test -- --nocapture
```
输出关键信息：
```
Golden 语料测试统计: 扫描了 33 个模块，共 1259 条指令，覆盖了 106/106 种独立操作码
test test_golden_corpus_reaches_106_opcode_coverage ... ok
```
- 语料索引：`.work/evidence/20260904/golden-index.tsv`（33 个真实/合成模块索引）
- 覆盖报告：`.work/evidence/20260904/golden-coverage-report.tsv`（106/106 全覆盖）
- 重新生成指南：`aluka_r/tests/golden/README.md`

### 待办 9-10 · A1-1 实现 Rust verifier 规则并完成全套测试
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-bytecode/src/verifier.rs`（实现 V1..V16 全部规则与跨块栈深合流一致性数据流分析）；
- 测试路径：`aluka_r/crates/aluka-bytecode/tests/verifier_test.rs`；
运行校验器测试命令：
```bash
cd aluka_r && cargo test -p aluka-bytecode --test verifier_test -- --nocapture
```
输出关键信息：
```
running 17 tests
test test_v1_bad_magic ... ok
test test_v2_bad_version ... ok
test test_v3_invalid_opcode ... ok
test test_v4_slot_out_of_range ... ok
test test_v5_const_out_of_range ... ok
test test_v6_const_type_mismatch ... ok
test test_v7_bad_jump_target ... ok
test test_v8_stack_depth_mismatch ... ok
test test_v9_stack_underflow ... ok
test test_v10_max_stack_exceeded ... ok
test test_v11_bad_try_range ... ok
test test_v12_handler_inside_body ... ok
test test_v13_try_cross_overlap ... ok
test test_v14_bad_try_end ... ok
test test_v15_template_out_of_range ... ok
test test_v16_upvalue_out_of_range ... ok
正向验证成功：共 33 个真实 golden 模块全部通过 V1..V16 严格校验！
test test_golden_corpus_all_modules_pass_verifier ... ok

test result: ok. 17 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
```

### 待办 11 · 全工作区门禁验证
**结论**：达成  
**证据类型**：命令  
```bash
cd aluka_r
cargo test
cargo clippy --all-targets -- -D warnings
cargo fmt --all --check
```
- 测试：23 个测试目标，59 项单元与集成测试全部通过（0 失败）；
- Clippy：全目标 0 错误 0 警告；
- Fmt：格式化全绿。

### 待办 12 · 工作流 1：aluka-vm 解释器循环与真实黄金语料执行对拍（D1 支撑）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-vm/src/lib.rs`（实现基于 PC 字节寻址循环、常量池加载、完整算术/位运算/比较/跳转求值分派）；
- 测试路径：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs`；
- 执行命令：`cargo test -p aluka-vm --test golden_execution_oracle_test -- --nocapture`；
- 结果：4 项黄金对拍用例全部通过，Rust VM 输出与 `aluka_g/bin/aluka.exe run` 逐字 100% 对齐。

### 待办 13 · 工作流 2：aluka-compiler 算术/比较/逻辑代码生成与端到端互验
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-parser/src/ast.rs`、`aluka_r/crates/aluka-compiler/src/lib.rs`；
- 测试路径：`aluka_r/crates/aluka-compiler/tests/compile_and_verify_test.rs`；
- 执行命令：`cargo test -p aluka-compiler --test compile_and_verify_test -- --nocapture`；
- 结果：生成的字节码 100% 通过 Verifier 静态数据流分析校验，并在 VM 中求值得到正确结果。

### 待办 14 · 工作流 3：aluka-core Value 表示定案架构决策（A1-2）
**结论**：达成  
**证据类型**：文档 + 架构定案  
- 文件路径：`docs/adr/0001-aluka-r-value-representation.md`；
- 决策结论：M0/M1 阶段全面采用 16 字节安全 Tagged Enum + `ObjectRef(u32)` 句柄，避开 Go 侧 NaN-box 槽位的悬垂陷阱；M2 提供 `nan-boxing` 特征与统一抽象门面。

### 待办 15 · 全工作区最新门禁验证
**结论**：达成  
**证据类型**：命令  
- 测试：24 个测试目标，66 项单元与集成测试全部通过（0 失败）；
### 待办 16 · 工作流 5：aluka-compiler 词法作用域、变量声明与连续赋值代码生成（T-FE-08）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-parser/src/ast.rs`、`aluka_r/crates/aluka-compiler/src/lib.rs`；
- 实现内容：扩展 `Expr::Assign`、`Stmt::VarDecl`、`Stmt::Block`；在 `CompiledUnit` 中维护符号表并分配局部槽位，编译连续赋值与变量读取；
- 测试路径：`aluka_r/crates/aluka-compiler/tests/compile_and_verify_test.rs`；
- 执行命令：`cargo test -p aluka-compiler --test compile_and_verify_test -- --nocapture`；
- 结果：4 项端到端编译-校验-执行测试全绿通过。

### 待办 17 · 工作流 4：aluka-vm 堆对象、属性读写、Getter/Setter 与跨函数调用（T-BE-08, T-BE-09）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-vm/src/lib.rs`；
- 实现内容：实现 `HeapObject` 堆分配（普通对象、数组、字符串、闭包）；实现 `NewObject`, `NewArray`, `BuildArray`, `SetProp`, `SetPropObj`, `SetPropTop`, `GetProp`, `GetPropLocal`, `GetElem`, `SetElem`, `SetElemTop`, `DelProp`, `DelElem`, `SetGetterObj`, `SetSetterObj`；实现函数调用隔离 `invoke_function`（保存/恢复局部槽位与当前常量池）与 `CallWithThis`；
- 测试路径：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs`；
- 执行命令：`cargo test -p aluka-vm --test golden_execution_oracle_test -- --nocapture`；
### 待办 18 · 工作流 6：aluka-compiler 控制流与跳转偏移计算代码生成（T-FE-09）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-parser/src/ast.rs`、`aluka_r/crates/aluka-compiler/src/lib.rs`；
- 实现内容：AST 扩充 `Stmt::If`、`Stmt::While`、`Expr::Conditional`；实现 `emit_jump` 与 `backpatch_jump` 回填计算 24 位有符号相对字节跳转偏移；
- 测试路径：`aluka_r/crates/aluka-compiler/tests/compile_and_verify_test.rs::test_compile_control_flow_if_while_and_execute`；
- 执行命令：`cargo test -p aluka-compiler --test compile_and_verify_test -- --nocapture`；
- 结果：`while` 循环与 `if-else` 分支求值生成的新字节码 100% 通过 Verifier 严格静态校验并在 VM 中求值得到预期数值 110。

### 待办 19 · 工作流 7：aluka-vm 闭包与上值（Upvalue）捕获运行时及 Go Oracle 逐字对拍（T-BE-10）
**结论**：达成  
**证据类型**：代码 + 命令 + 深度排错  
- 文件路径：`aluka_r/crates/aluka-bytecode/src/verifier.rs`、`aluka_r/crates/aluka-vm/src/lib.rs`；
- 排错成果：发现并纠正了 `aluka-bytecode` 反序列化 `UpvalueCapture` 时 `is_local` 与 `index` 的字段读取逆序严重问题；
- 实现内容：实现 `Upvalue(Rc<RefCell<Value>>)` 共享句柄；`open_upvalues` 槽位共享映射与 `StoreLocal` 自动同步；`CloseUpvalues` 动态关闭；`MakeClosure` 自动分析父层局部槽位或继承上值；`CallMethod` 支持普通对象闭包方法调度与数组 `map`、`join` 回调调用；
- 测试路径：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs::test_execute_06_closures_and_upvalues`；
- 执行命令：`cargo test -p aluka-vm --test golden_execution_oracle_test test_execute_06_closures_and_upvalues -- --nocapture`；
- 结果：`06_closures_and_upvalues.bc` 真实执行输出（`12` 与 `0,1,2`）与 Go Oracle `aluka.exe run 06_closures_and_upvalues.js` **100% 逐字对齐通过**！

### 待办 20 · 第三阶段全工作区质量门禁
**结论**：达成  
**证据类型**：命令  
- 测试：24 个编译目标，**71 项测试全部通过（0 失败，0 忽略）**；
- 黄金对拍：7 个核心黄金语料模块（01, 02, 03, 04, 05, 06, 07）全部与 Go Oracle 对拍通过；
- Clippy：全 target `cargo clippy --all-targets -- -D warnings` **0 error, 0 warning**；
- Fmt：`cargo fmt --all --check` 严格通过。

### 待办 21 · 工作流 8：aluka-compiler 对象字面量、数组字面量与属性访问代码生成（T-FE-10）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-parser/src/ast.rs`、`aluka_r/crates/aluka-compiler/src/lib.rs`；
- 实现内容：AST 扩充 `Expr::Object`、`Expr::Array`、`Expr::Member`、`Expr::Index`、`Expr::MemberAssign`、`Expr::IndexAssign`；实现 `NewObject`、`NewArray`、`GetProp`、`SetProp`、`GetElem`、`SetElem` 的代码生成；
- 测试路径：`aluka_r/crates/aluka-compiler/tests/compile_and_verify_test.rs::test_compile_object_array_and_member_access`；
- 执行命令：`cargo test -p aluka-compiler --test compile_and_verify_test test_compile_object_array_and_member_access -- --nocapture`；
- 结果：通过编译执行复合表达式 `{ a: 10, b: 20 }` 与 `[10, 20, 30]`，求值 `10 + 20 + 30 = 60` 顺利通过 Verifier 静态校验与 VM 运行。

### 待办 22 · 工作流 9：aluka-vm 数组展开、数组 slice 与 ES6 Class 原型链继承运行时（T-BE-11）
**结论**：达成  
**证据类型**：代码 + 命令 + 算法自研  
- 文件路径：`aluka_r/crates/aluka-vm/src/lib.rs`；
- 实现内容：
  1. 实现 `ArraySpread`（数组展开求值）以及数组原型方法 `slice`；
  2. 实现 ECMAScript 规范的字符串自动拼接（升级 `Op::Add`）；
  3. `HeapObject::Ordinary` 与 `HeapObject::Closure` 增加 `proto: Option<ObjectRef>` 隐式原型与闭包属性字典；
  4. 实现 `MakeClass`：构造 `prototype` 原型对象、构造器闭包、继承原型链绑定、静态方法与实例方法/Getter/Setter 安装；
  5. 实现 `New`、`ConstructThis`（`super()`）、`CallThis`（`super.greet()`）、`GetProto` 以及 `Instanceof` 原型链递归判定；
- 测试路径：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs`；
- 执行命令：
  - `cargo test -p aluka-vm --test golden_execution_oracle_test test_execute_08_arrays_and_methods -- --nocapture`（输出 `0 1 2,3,4,5,6`）；
  - `cargo test -p aluka-vm --test golden_execution_oracle_test test_execute_09_classes_and_inheritance -- --nocapture`（输出 `Hello Alice (Engineer) true true`）；
- 结果：08 与 09 两大黄金模块输出与 Go 原型 Oracle 逐字符 **100% 精确对齐通过**！

### 待办 23 · 第四阶段全工作区质量门禁
**结论**：达成  
**证据类型**：命令  
- 测试：24 个编译目标，**73 项测试全部通过（0 失败，0 忽略）**；
- 黄金对拍：9 个核心黄金语料模块（01, 02, 03, 04, 05, 06, 07, 08, 09）逐字与 Go Oracle 对拍全绿通过；
- Clippy：全 target `cargo clippy --all-targets -- -D warnings` **0 error, 0 warning**；
- Fmt：`cargo fmt --all --check` 严格通过。

---

## 4. 偏差与决策

| 类型 | 内容 | 影响 |
|------|------|------|
| **技术决策** | 针对编译器常量折叠导致减法、除法等指令被消除的问题，设计动态传参用例（用例 29-32） | 真实前端直接生成的指令数由 89 条进一步提升至 96 条，其余 10 条遗留/内部指令由专用合成模块兜底 |
| **技术决策** | 发现 Go 侧常量池字符串采用 `uvarint`（LEB128）长度编码而非固定 4 字节 | 已纳入 ISA 规范陷阱小节，并在解析器中正确实现 `read_uvarint` |
| **架构收益** | F 轨闭环后，三大并行工作流（VM 循环、Compiler 发射、Value ADR）首期任务同步告捷 | **前后端团队已形成“自闭环单测 + 跨端互验 + 黄金语料对拍”三道质量护城河** |

---

## 5. 未达成与阻塞

无。所有预定前置条件与第一批并行开发任务均已圆满达成。

---

## 6. 下一步建议

1. **后端 VM 推进**：推进 `T-BE-08` 对象与数组访问（`GET_PROP`, `SET_PROP`, `GET_ELEM`, `SET_ELEM`）以及 `T-BE-09` 函数调用帧（`CallFrame` 与 `CALL`）；
2. **前端编译器推进**：推进 `T-FE-04` 局部变量作用域树（`ScopeTree`）与 `T-FE-08` 变量赋值代码生成；
3. **GC 预研（A1-3）**：启动 `ObjectRef` 句柄堆的分代与标记回收原型设计。


## 6. 明日入口（并行开发启动）

**前端 A2 轨与后端 A1 轨可以同时启动并行开发：**

- **A1 轨入口（后端虚拟机与加载器）**：
  - 读取 `aluka_r/docs/aluvm-isa-spec.md` 与 `aluka_r/tests/golden/corpus/` 下的 33 个 `.bc` 语料；
  - 在 `aluka-vm` 中实现基于 106 条指令的 Tier 0 解释循环与加载器，直接跑通 golden 语料！
- **A2 轨入口（前端编译器）**：
  - 基于 `aluka_r/docs/aluvm-isa-spec.md` 与 `aluka-bytecode` 提供的 `Instr` 接口；
  - 在 `aluka-compiler` 中实现 JS AST 到 106 条指令的发射，产出字节码直接用 Go 现成二进制比对！
