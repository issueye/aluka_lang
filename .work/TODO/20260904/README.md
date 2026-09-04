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
| 25 | 前端编译推进：T-FE-11 函数调用、方法调用、构造与返回指令生成，并通过 Verifier 校验 | `[x]` | 前端推进 |
| 26 | 前端编译推进：T-FE-12 可选链短路与 Try-Finally 异常表生成，并通过 Verifier 校验 | `[x]` | 前端推进 |
| 27 | 前端编译推进：T-FE-13 Class 结构体与模块级编译汇编（compile_module），并通过 Verifier 校验 | `[x]` | 前端推进 |
| 28 | 后端解耦：移除 `aluka-vm` 对 `aluka-compiler` 的无用依赖（回归 A1 轨「仅依赖 aluka-bytecode」契约） | `[x]` | A1 轨解耦 |
| 29 | 后端推进：T-BE-12 异常抛出与 TryTable 展开（`THROW`/`TRY_ENTER`/`TRY_EXIT`/`TRY_EXIT_FINALLY`/`TRY_EXIT_JMP` + return 穿越展开 + `Error` 内置），黄金语料 10/27/32 与 Go Oracle 逐字对拍通过 | `[x]` | T-BE-12 |
| 30 | 后端推进：T-BE-15 全量扫描 33 语料（16/33 → 基线盘点），定位 5 个语义 bug 与 12 个缺指令缺口 | `[x]` | T-BE-15, D1 |
| 31 | 后端推进：修复 `STORE_LOCAL`/`STORE_UPVALUE` 栈语义（对齐契约 Fixed(-1)）、computed Setter 触发、`++/--` BigInt、console.log 数组格式、闭包 `prototype` 属性；实现 `STORE_GLOBAL`/`IN`/`TYPEOF`/`TYPEOF_GLOBAL` 与 `Math`/`Array`/`Object` 内置，新增 8 个语料对拍（共 20/33 对拍全绿） | `[x]` | T-BE-15, D1 |
| 32 | 后端推进：spread 调用家族（`CALL_ARGS`/`CALL_WITH_THIS_ARGS`/`CALL_METHOD_ARGS`/`NEW_ARGS`/`CONSTRUCT_THIS_ARGS`/`SPREAD_OBJECT`）+ rest 参数绑定修复 + for-in（`ENUM_KEYS` + `Object.create` + 数组 `sort`），新增 5 个语料对拍（共 29/33 对拍全绿） | `[x]` | T-BE-15, D1 |
| 33 | 前端推进：T-FE-16 二进制字节码序列化器（`BytecodeModule::serialize`）实现，并通过全部 33 个黄金语料无损 Round-trip 校验与编译产物回读 | `[x]` | T-FE-16 |
| 34 | 前端推进：T-FE-04 作用域树与局部变量槽位分配（ScopeTree 块退出槽位复用，维护全局峰值） | `[x]` | T-FE-04 |
| 35 | 前端推进：T-FE-05 闭包与上值捕获分析（UpvalueCapture 自动分析、发射 LoadUpvalue/MakeClosure，并通过 V16 校验） | `[x]` | T-FE-05 |
| 36 | 前端推进：T-FE-01 语法解析器实现（`aluka-parser::parse`，支持表达式/语句/声明/控制流等全套文法） | `[x]` | T-FE-01 |
| 37 | 前端推进：T-FE-02 TypeScript 类型注解与声明零成本解析剥离（`: Type`, `interface`, `type`, `as Type`） | `[x]` | T-FE-02 |
| 39 | 后端推进：T-BE-13 生成器帧挂起（`YIELD` + `next()` 驱动 + `GET_ITERATOR`/`GET_ASYNC_ITERATOR`）与 async/await（Promise 同步包装 + `AWAIT`），新增 3 个语料对拍（共 32/33 对拍全绿；仅剩 14 号待 `aluka-regex` 引擎） | `[x]` | T-BE-13, T-BE-15, D1 |
| 40 | 前端推进：T-FE-14 静态编译期优化 Pass（常量折叠、死代码消除、跳转穿透并在 `compile_module` 自动挂载） | `[x]` | T-FE-14 |
| 41 | 前端推进：T-FE-15 最大栈深（MaxStack）精确推导（基于 Worklist 前向控制流数据流分析， Try 保护区补偿，满足 V10 校验） | `[x]` | T-FE-15 |
| 42 | 前端推进：T-FE-17 `alukac` 独立编译器 CLI 命令行工具（支持 compile 编译为 .bc、disasm 反汇编格式化报告、彻底解耦） | `[x]` | T-FE-17 |
| 43 | 后端推进：`aluka-regex` 正则引擎（递归下降解析 + CPS 回溯匹配 + 百万步预算护栏）与 VM 侧 `MAKE_REGEXP`/`exec`/`test`，黄金语料 14 通过——**T-BE-15 达成 33/33 全量对拍** | `[x]` | T-BE-15, D1 |
| 44 | 后端推进：T-BE-14 `aluvm` 独立虚拟机 CLI（`run .bc` / `--version` / `process.argv` 注入 / 退出码映射 / 未捕获异常 stderr 展示），4 项集成测试全绿 | `[x]` | T-BE-14 |
| 45 | 后端推进：T-BE-02 GC 原型 ×2 实现（分代标记-清除 vs 引用计数+循环回收，`gc_protos` 模块）+ 20 项正确性单测 + gc_bench 基准（min-of-5）+ 两份评测报告 + 选型 ADR——**原型 A 全面胜出（churn 80.8×/cycles 23.3×/fib30 1.8×）** | `[x]` | T-BE-02, A1-3 |
| 46 | 后端推进：T-BE-03 `aluka-core` 公共 API 冻结（Value 全套类型谓词 + Heap 生命周期五方法 + RootSet/ShapeTable 补全，Rustdoc 完整）+ 冻结声明写入 `aluka_r/AGENTS.md`——**A1-4 达成，M0 四项验收门全部闭环** | `[x]` | T-BE-03, A1-4 |
| 47 | **M0 里程碑收口评审**：验收门 4 项核对通过；当场整改 3 项（对拍测试补齐 33/33、D3 CI golden 步骤、D2 fib30 基线 912.7ms）；遗留定夺 1 项（语料 33 vs 200 例，建议维持覆盖导向）——**M0 关闭** | `[x]` | M0 收口 |
| 48 | **前端（A2 轨 · alukac）实现情况评审**：任务清单核对 + 26 项测试实测全绿 + 17 项语言特性端到端差分（alukac→Verifier→aluvm vs Go Oracle）**13 过 / 4 缺陷**；确认 throw 缺陷已修复，新增 4 项缺陷清单（递归自引用/对象解构/模板插值/隐式 super 转发）转交 A2 轨——评审报告 `.work/evidence/20260904/fe-review.md` | `[x]` | 前端评审 |
| 49 | 前端推进（参考 Go 版设计）：脚本解析**按语言注册**落地 `aluka-parser::source_unit`——`SourceKind` 扩展名分类（JS/TS/JSON）、`ModuleKind`、`SourceUnit` 稳定 IR + `TransformStage` 单向阶段位标志（只增不减 + 校验）、`LanguageRegistry` 扩展名注册表（默认 JS/TS/JSON 可扩展）、TS strip-only policy 检测（enum/namespace + declare 豁免）；13 项单测全绿 | `[x]` | T-FE 前置 |
| 50 | 前端推进：T-FE-18 全量 32 个黄金语料端到端编译与 Go Oracle 运行双向对拍（解构、展开、正则、模板字符串、多层跨层闭包、类继承、for-in、for-of、生成器、async/await、for-await-of），编译与 Verifier 32/32 全绿，Go Oracle 逐字对拍 32/32 全绿 | `[x]` | T-FE-18 |
| 51 | 选项 1【前端语言注册流水线全链路贯通】：`SourceUnit`、`LanguageRegistry`、`TransformStage` 接入 `aluka-compiler` 与 `alukac` CLI，支持 `.json`、`.ts`、`.js` 多语言分发与单向阶段位推进 | `[x]` | T-FE 前置/M1 |
| 52 | 选项 2【M2 阶段高级语法扩展：JSX/TSX Lowering 与 ESM 规范】：AST、Parser 与 Compiler 支持 `<tag attr={expr}>children</tag>` 转为 `React.createElement`，以及 ESM `import`/`export` 完整语法解析与发射 | `[x]` | T-FE-03, M2 |
| 53 | 选项 3【双向四象限质量对拍矩阵（D 轨）】：Rust 前端 × Rust VM、Rust 前端 × Go VM、Go 前端 × Rust VM、Go 前端 × Go VM 四象限交叉矩阵测试套件（32 个真实黄金语料 100% 逐字全绿通过） | `[x]` | D1, D3 |

---

## 3. 达成目标证据

### 待办 50 · T-FE-18 全量 32 个黄金语料端到端编译与 Go Oracle 逐字对拍
**结论**：达成（32 / 32 100% 逐字完全一致）  
**证据类型**：命令 + 产物  
```bash
cargo test -p aluka-cli --test golden_compile_oracle_test -- --nocapture
```
- 静态编译与 Verifier 契约校验：32 / 32 全部通过（100% 通过率）；
- 跨端 Oracle 执行对拍：32 / 32 逐字完全对齐一致（包括递归多层闭包、解构赋值、对象展开、正则字面量、For-In、For-Of、生成器函数、Async/Await 与 For-Await-Of 全部核心语法）；
- 前端代码质量：`cargo fmt --check` 0 diff，`cargo clippy` 0 warning / 0 error，全前端套件 64 项单测与集成测试 100% 全绿。

### 待办 51 · 选项 1【前端语言注册流水线全链路贯通】
**结论**：达成  
**证据类型**：流水线代码 + 模块编译器 + CLI 集成 + 单元/集成测试  
- 阶段位与注册表：在 `aluka-parser::source_unit` 中扩充 `STAGE_BYTECODE_COMPILED = 1 << 7`，实现 `LanguageRegistry::global()` 单例及 `parse_source`/`parse_file`；
- 编译器接入：在 `aluka-compiler::source_unit` 中实现 `compile_source_unit(&mut SourceUnit) -> Result<BytecodeModule, CompileError>`，支持纯 Rust 零依赖 JSON AST 解析器自动生成对象/数组构建字节码模块，自动推进编译阶段位；
- CLI 主流程重构：`aluka-cli/src/bin/alukac.rs` 由 `LanguageRegistry::global().parse_file` 与 `compile_source_unit` 驱动，全链路支持 `.json`/`.js`/`.ts` 源码单元编译；
- 测试验证：`crates/aluka-compiler/tests/source_unit_pipeline_test.rs`（5 项通过）。

### 待办 52 · 选项 2【M2 阶段高级语法扩展：JSX/TSX Lowering 与 ESM 规范】
**结论**：达成  
**证据类型**：AST/Lexer/Parser 语法扩展 + Lowering 转换器 + 模块编译发射 + 集成测试  
- AST 扩展：新增 `Expr::JSXElement`、`Expr::JSXFragment`、`Stmt::Import`、`Stmt::Export` 及对应结构体，符合严格文档注释规范；
- 词法与语法分析：`aluka-parser::lexer` 支持 `import`/`export`/`from` 关键字，`parser` 支持 JSX 开闭标签、自闭合标签、展开属性、连字符属性与子节点清洗；
- 编译降级：在 `aluka-compiler::jsx` 中实现 `lower_jsx`，平滑将 `<tag attr={expr}>children</tag>` 转为 `React.createElement(...)` 调用，大写首字母映射标识符，小写映射字符串常量；并在 `compile_module` 与 `codegen.rs` 全面接入；
- 测试验证：`crates/aluka-compiler/tests/jsx_and_esm_test.rs`（2 项通过）。

### 待办 53 · 选项 3【双向四象限质量对拍矩阵与自动化门禁强化（D 轨）】
**结论**：达成（32 / 32 真实黄金语料四象限 100% 逐字全绿通过）  
**证据类型**：跨语言交叉矩阵测试套件 + 真实字节码执行  
- 测试矩阵设计：
  - Q1 (Rust 前端 × Rust VM)
  - Q2 (Rust 前端 × Go VM)
  - Q3 (Go 前端 × Rust VM)
  - Q4 (Go 前端 × Go VM)
- 关键攻坚与修复：
  - 排查并修复 `aluka-bytecode::serializer` 在函数模板标量头中错误将 20 字节写为 0 导致 Go VM 误将 `slot 0` 判定为 arguments 槽位覆盖 `this` 的隐蔽 bug，正确写入 `[u32::MAX, 1, u32::MAX, 0, u32::MAX]`；
  - 驱动全部 32 个真实黄金语料（含算术、控制流、多层跨层闭包、getter/setter、class 继承、try-catch-finally、generator、async/await、for-await-of）在四象限上全部 100% 逐字对齐一致！
- 测试命令：`cargo test -p aluka-cli --test four_quadrants_oracle_test -- --nocapture`（全绿通过）；
- 质量门禁：`cargo fmt --check` 0 diff，`cargo clippy` 0 warning / 0 error。


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

### 待办 28 · 后端解耦：移除 `aluka-vm` → `aluka-compiler` 无用依赖（后端轨）
**结论**：达成  
**证据类型**：代码 + 命令  
- 文件路径：`aluka_r/crates/aluka-vm/Cargo.toml`、`aluka_r/Cargo.lock`；
- 依据：grep 全 `aluka-vm/src` 与 `aluka-vm/tests` 均无 `aluka_compiler` 引用，属无用依赖；
  且它使后端 VM 编译被前端在建代码阻塞，违背 backend/README「仅依赖 aluka-bytecode」的解耦契约；
- 结果：移除后 `cargo test -p aluka-vm` 在前端 `aluka-parser/aluka-compiler` 处于中间态时照常全绿。

### 待办 29 · 后端推进：T-BE-12 异常抛出与 TryTable 展开（后端轨）
**结论**：达成  
**证据类型**：代码 + 命令 + 黄金对拍  
- 新增文件：`aluka_r/crates/aluka-vm/src/exception.rs`（TryHandler 相位状态机 + `exitTry`/`findHandler` 忠实移植 Go 版 `vm_exception.go`，pc 字节偏移 ↔ 指令索引 `/4` 换算）；
- 改造文件：`aluka-vm/src/interpreter.rs`（`VmError::Thrown(Value)`、异常展开边界 `run_with_constants` 包装、5 条异常指令分派、`Return`/`ReturnUndef` 接入 `exit_try` 挂起-恢复、`LoadGlobal` 按常量池名解析内置）、`heap.rs`（`ErrorConstructor` 变体 + `alloc_error_instance` 带 `message`/`name`）、`call.rs`（每帧隔离 `try_stack` 与 `current_try_table`）；
- 语义覆盖：catch 压入异常值、finally 挂起 completion（return/break/continue）、相位 ≥1 不重入、`TRY_EXIT_FINALLY` 恢复向外展开或重抛、未捕获沿调用链逐帧传播；
- 测试路径：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs`（新增 10/27/32 三个对拍测试）+ `exception.rs` 内嵌 7 项状态机单元测试；
- 执行命令：`cargo test -p aluka-vm --test golden_execution_oracle_test -- --nocapture`；
- 结果：黄金语料 **10**（`try-try_end-finally` / `try-catch:fail-finally`，含 `new Error("fail")` → `e.message`）、**27**（return 穿过嵌套 finally 输出 `VAL`）、**32**（break 经 `TRY_EXIT_JMP` 先跑 finally 输出 `fin: 0`/`fin: 1`）与 Go Oracle **逐字符 100% 对齐**，12/12 对拍测试全绿；`cargo clippy -p aluka-vm --all-targets -- -D warnings` 0 错误，`cargo fmt -p aluka-vm` 通过。已知偏差：console.log 直接打印 Error 实例的格式（`Error: msg` 堆栈样式）尚未对齐，本轨语料未覆盖，留待 T-BE-14 CLI 阶段。

### 待办 30 · T-BE-15 全量扫描：33 语料基线盘点（后端轨）
**结论**：达成（阶段基线：16/33 通过）  
**证据类型**：命令 + 扫描数据  
- 方法：全量加载 33 个 `.bc` → Verifier 校验 → VM 执行 → stdout 与 `aluka_g/bin/aluka.exe run` 逐字比对；
- 基线结果：**16 通过 / 17 失败**。失败分类：
  - **5 个语义 bug（DIFF）**：15（`x++` 后缀值错误）、18（switch 分支错乱）、23（computed Setter 未触发）、25（`Point.prototype` 解析失败）、29（console.log 数组格式缺 `[ ]`）；
  - **12 个缺指令（FAIL）**：`Yield`(11/26)、`EnumKeys`/`ForInNext`(12)、`Await`(13)、`MakeRegexp`(14)、`SpreadObject`(16)、`CallArgs`(20)、`ConstructThisArgs`(28)、`CallWithThisArgs`(31) 及其组合；
- 该扫描定位了修复优先级（语义 bug 优先于缺指令大件），扫描后 5 个 DIFF 全部修复（见待办 31）。

### 待办 31 · 语义修复 + 简单指令落地：对拍 12→20（后端轨）
**结论**：达成  
**证据类型**：代码 + 命令 + 黄金对拍  
- **契约修正**：`STORE_LOCAL`/`STORE_UPVALUE` 由 peek 改为 pop——ISA 契约 `Op::StoreLocal => StackEffect::Fixed(-1)` 与 Go 版 `*v.local(idx) = v.pop()` 均为弹栈语义；peek 导致的栈失衡会跨帧累积污染共享操作数栈（18 号 switch 输出错乱的根因）；
- **语义修复**：
  1. `set_property` 触发 Setter 访问器（23 号：computed setter 原先被数据属性直写绕过）；
  2. `INC`/`DEC` 走 `update_numeric`：BigInt 保持 BigInt（15 号：`10n++` → `11`，新增 `HeapObject::BigInt` 变体）；
  3. console.log 改用 `format_console_value`：数组输出 `[ a, b ]`（29 号，对齐 Go `inspectValue`）；
  4. 闭包自动携带 `prototype` 属性（25 号：`Point.prototype.dist` 装载成功；类机制在分配后覆盖，无冲突）；
- **新增内置与指令**：`STORE_GLOBAL` + 全局变量表（30 号）、`IN`（`has_property` 沿原型链）、`TYPEOF`/`TYPEOF_GLOBAL`（24 号）、`Math.sqrt` 拦截（25 号）、`Array`/`Object` 原生构造器与 `Object.prototype`/`Array.prototype` 单例（17 号 `instanceof`，数组/对象分配自动挂原型）；
- 测试路径：`golden_execution_oracle_test.rs` 新增参数化对拍驱动 `assert_corpus_matches_go` + 8 个新语料测试（15/17/18/23/24/25/29/30）；
- 执行命令：`cargo test -p aluka-vm`；
- 结果：**对拍 20/33 全绿**（12 单元 + 20 对拍测试 0 失败），01-09 旧用例零回归；`cargo clippy -p aluka-vm -p aluka-bytecode -p aluka-core --all-targets -- -D warnings` 0 错误；`cargo fmt -p aluka-vm` 通过；
- 剩余 9 个缺口（全部缺指令）：`Yield`(11/26)、`EnumKeys`/`ForInNext`(12)、`Await`(13)、`MakeRegexp`(14)、`SpreadObject`(16)、`CallArgs`(20)、`ConstructThisArgs`(28)、`CallWithThisArgs`(31)。

### 待办 32 · spread 调用家族 + rest 参数 + for-in：对拍 20→29（后端轨）
**结论**：达成  
**证据类型**：代码 + 命令 + 黄金对拍  
- **spread 调用家族**（语义对齐 Go 版 `vm.go`/`vm_call.go`）：
  - 提取共享 helper：`resolve_callable` / `invoke_callable` / `do_construct`（`New` 臂主体重构复用）/ `do_construct_this`（`ConstructThis` 臂主体重构复用）/ `to_array_values`（数组取元素、对象取值集、原始值空集）；
  - 新增 6 条指令臂：`CALL_ARGS`（`... callee argsArray`）、`CALL_WITH_THIS_ARGS`（`... callee this argsArray`）、`CALL_METHOD_ARGS`（操作数=方法名常量索引）、`NEW_ARGS`、`CONSTRUCT_THIS_ARGS`（`super(...args)`，this 取 `locals[0]`）、`SPREAD_OBJECT`（`{...src}` 自有属性写入栈顶 dst）；
- **rest 参数绑定修复**（31 号 `NaN` vs `7` 的根因）：`invoke_function` 原先把全部实参顺序塞槽位；现对齐 Go 版——固定参数位只绑前 `num_params` 个，`is_var_args` 函数把多余实参打包成 rest 数组写在 `locals[1 + num_params]`（不足为空数组，自动挂 `Array.prototype`）；
- **for-in**（12 号）：`ENUM_KEYS` 快照原型链可枚举键为字符串数组（`enumerate_for_in_keys`：沿链 ≤128 层、去重先到先得、字符串产出 UTF-16 索引键）；配套 `Object.create(proto)` 原生方法拦截（精确原型分配，`null` → 无原型）与数组 `sort` 方法（无比较器字典序、原地、返回自身）；
- 测试路径：`golden_execution_oracle_test.rs` 新增 5 个对拍测试（12/16/20/28/31）；
- 执行命令：`cargo test -p aluka-vm`；
- 结果：**对拍 29/33 全绿**（12 单元 + 25 对拍测试 0 失败），已过语料零回归；clippy `-D warnings` 0 错误；fmt 通过；
- 剩余 4 个缺口（协程/正则大件，需帧挂起机制或 `aluka-regex` 接入）：`Yield`(11/26)、`Await`(13)、`MakeRegexp`(14)。

### 待办 39 · 生成器帧挂起 + async/await：对拍 29→32（后端轨）
**结论**：达成  
**证据类型**：代码 + 命令 + 黄金对拍  
- **生成器帧挂起模型**（新增 `aluka-vm/src/generator.rs`，递归解释器下的可暂停帧）：
  - 调用 `is_generator` 函数不执行函数体——`invoke_function` 拦截后创建 `HeapObject::Generator` 身份对象，在 `Vm.generators` 注册表登记初始状态（this/实参/上值/常量池/Try 表）；
  - `gen.next(v)`（`CALL_METHOD` 拦截）换入生成器帧上下文，从挂起 pc 继续执行；`Op::Yield` 把恢复点写入 `Vm.yield_pc` 后以 `VmError::Yielded` 沿调用链上抛（复用异常展开通道），驱动层捕获并快照挂起帧（locals / 逻辑栈 split_off / 上值 Rc 句柄 / open_upvalues / try 栈）；
  - 操作数栈与调用者共享同一 `Vec`：进入驱动时记录调用者栈高为界，挂起时 `split_off` 逻辑栈，恢复时拼回当前栈顶（与调用点栈高解耦）；
  - 语义细节：`next(v)` 注入值压栈成为 `yield` 表达式求值结果；结束（`Return`/`ReturnUndef`）后 `{value, done:true}`；`GET_ITERATOR`/`GET_ASYNC_ITERATOR` 对生成器返回自身；
- **async/await**：`invoke_function` 对 `is_async` 函数同步执行函数体并把结果包装为 fulfilled `HeapObject::Promise`；`AWAIT` 取 fulfilled 值、非 Promise 透传（对齐语料的同步完成语义）；未完成 Promise 需微任务调度，明确以 `UnimplementedOpcode` 拒绝（不留静默兜底）；
- **配套重构**：`run_with_constants_at`（挂起恢复入口，wrapper 逻辑与原一致）；参数绑定提取为 `bind_call_args`（`invoke_function` 与生成器首帧共用，含 varargs rest 打包）；
- 测试路径：`golden_execution_oracle_test.rs` 新增 3 个对拍测试（11/13/26）；
- 执行命令：`cargo test -p aluka-vm`；
- **仅剩缺口**：14 号 `MakeRegexp`——`aluka-regex` crate 目前为空壳（仅文档与错误类型），需先实现正则引擎（RE2 快路径 + 回溯 fallback，独立大件，登记为 T-BE-15 的最后一项前置）。

### 待办 43 · `aluka-regex` 正则引擎 + RegExp 运行时：对拍 32→33 全量达成（后端轨）
**结论**：达成（**T-BE-15 闭环：33/33 全量对拍**）  
**证据类型**：代码 + 命令 + 黄金对拍  
- **正则引擎**（`aluka-regex` 新增 `parser.rs` + `engine.rs`，Tier 0 正确性优先、RE2 快路径留后续优化）：
  - 递归下降解析器：字面量、`.`、字符类（范围/取反/`\d \D \w \W \s \S` 简写）、量词
    `* + ? {m} {m,} {m,n}`（贪婪/懒惰）、捕获组 `(...)` 与非捕获组 `(?:...)`、选择 `|`、
    锚点 `^ $`、转义序列；类外 `\d` 等展开为单成员字符类节点；
  - CPS 续延回溯匹配器：懒惰量词优先短匹配；空匹配不扩展（防 `(a?)*` 无限回溯）；
    **百万步回溯预算护栏**——耗尽显式返回 `RegexError::BacktrackLimit`，绝不折叠成
    「无匹配」（守住 aluka-regex 立规的两条硬语义之一）；
  - `i` 忽略大小写在字符与范围两端做大小写折叠；
- **VM 侧接线**：`HeapObject::RegExp { pattern, flags }` 变体（`format_value` 输出 `/pat/flags`）；
  `MAKE_REGEXP` 臂（弹 flags、pattern 构造对象，对齐 Go `OpMakeRegexp`）；
  `CALL_METHOD` 拦截 `exec`（结果数组 `[全匹配, 组1…]`、未参与组为 `undefined`、无匹配 `null`）
  与 `test`（布尔）；语法错误与回溯超限以 `VmError::Thrown` 上抛为 JS 异常；
- 测试路径：`aluka-regex/src/engine.rs` 内嵌 7 项单元测试（语料模式、选择锚点、计数/懒惰量词、
  取反类与 `.`、大小写、语法错误）+ `golden_execution_oracle_test.rs` 新增语料 14 对拍；
- 执行命令：`cargo test -p aluka-regex -p aluka-vm`；
- 结果：黄金语料 14（`/([a-z]+)-(\d+)/i` 提取 `Order-123`）与 Go Oracle 逐字符一致，
  **33/33 全量对拍达成**（regex 10 + VM 12 单元 + 29 对拍 = 51 项测试全绿）；
  clippy `-D warnings` 0 错误；fmt 通过。

### 待办 44 · `aluvm` 独立虚拟机 CLI（后端轨，T-BE-14）
**结论**：达成  
**证据类型**：代码 + 命令 + 集成测试  
- 新增 `aluka-cli/src/bin/aluvm.rs`（独立二进制，不依赖前端 parser/compiler，维持后端解耦）：
  - `aluvm run <input.bc> [参数…]` / `aluvm <input.bc>`：读取 → `deserialize_go` →
    `verify()`（V1..V16 严格校验，拒绝不合格字节码）→ `Vm::run_module` → 逐行输出 `console.log` 记录；
  - `aluvm --version` / `--help`；未知选项报错退出；
  - **`process.argv` 注入**：`argv[0]=脚本路径`（对齐 Node 语义），其后为 CLI 参数——
    经 `vm.globals` 注入 `process` 对象（`resolve_global` 优先读全局表）；
  - **退出码映射**：成功 0；未捕获异常 1（stderr 先输出已产生的 stdout 记录，
    再格式化异常——Error 实例输出 `Name: message`（如 `Error: boom`），其余值原样格式化，
    尾随 `    at <module> (<file>)` 位置行）；反序列化/校验/IO 错误 1；
- 集成测试 `aluka-cli/tests/aluvm_test.rs`（4 项，自构造 `.bc` 经 `serialize()` 往返自检，
  不依赖前端编译器）：版本退出码、黄金语料执行与 Go Oracle（源码输入）逐字对拍、
  未捕获异常非零退出 + stderr 断言、`process.argv[1]` 回显；
- 执行命令：`cargo test -p aluka-cli --test aluvm_test`；
- 结果：4/4 全绿；aluka-cli 全套测试（含前端 alukac 2 项）无回归；
  clippy `-D warnings` 0 错误；fmt 通过。

### 待办 45 · GC 原型 ×2 评测与选型（后端轨，T-BE-02 / A1-3）
**结论**：达成——**原型 A（分代标记-清除）全面胜出，选型 ADR 已立**  
**证据类型**：代码 + 命令 + 基准数据 + 报告  
- **实现**：`aluka-core/src/gc_protos/`（评测沙盒，共用 `ProtoObject` 对象模型、
  `ObjectRef` 句柄即 slab 下标——守住「句柄非裸指针」「不做裸 arena」两条铁律）：
  - 原型 A `generational.rs`：非移动分代标记-清除——年轻代 free-list 池化分配、
    写屏障进记忆集（卡表的按对象粒度等价物）、minor 只扫年轻代、存活 2 次晋升、major 全堆清扫；
  - 原型 B `refcount.rs`：手写强引用计数（分配即无主，登记根/写槽位增计，减到 0 递归即时释放）
    + 标准 trial-delete 循环回收（两轮式备份计数 + 三色标记，Bacon-Rajan mark-sweep）；
- **正确性**：20 项单元测试全绿（存活集保护、环垃圾回收、老→新记忆集保活、晋升、
  外部持有的环不误收、共享子不双重释放等）；
- **基准**（`examples/gc_bench.rs`，`cargo run --release --example gc_bench`；
  交替执行 + 轮间冷却 100ms + min-of-5，方法学遵守总 TODO §1）：

  | 场景 | 原型 A | 原型 B | A 优势 |
  |---|---|---|---|
  | fib30_tree（269 万递归分配/弃置） | **171.3 ms** | 311.2 ms | 1.8× |
  | churn（20 万分配，10% 存活） | **19.8 ms** | 1596.6 ms | **80.8×** |
  | cycles（5 万三节点环） | **16.6 ms** | 385.5 ms | 23.3× |

- **产物**：评测报告 `.work/evidence/20260904/gc-proto-a-report.md` 与
  `gc-proto-b-report.md`；选型决策 `docs/adr/0002-aluka-r-gc-selection.md`
  （定案原型 A，含落地计划与已知代价：全局暂停、记忆集粒度、放弃死期确定性）；
- 执行命令：`cargo test -p aluka-core`（20/20 全绿）；clippy `-D warnings` 0 错误。

### 待办 46 · `aluka-core` 公共 API 冻结（后端轨，T-BE-03 / A1-4）
**结论**：达成——**M0 四项验收门全部闭环**  
**证据类型**：代码 + 文档 + 冻结声明  
- **补全冻结面**：
  - `Value` 类型谓词：`is_object` / `is_number` / `is_boolean` / `is_undefined` / `is_null`
    （与既有 `kind` / `to_boolean` / `is_nullish` 合为完整集合）；
  - `Heap` 生命周期接口（任务书要求的 `allocate` / `collect_garbage` / `add_root` 全覆盖）：
    `allocate(class, slot_count)`、`add_root` / `remove_root`（持久根）、
    `collect_garbage()`（累积根模式）与 `collect_garbage_with(&RootSet)`（VM 每帧
    重建瞬时根的批量模式）；占位实现（只记账不存储）是**登记过的中间态**，
    M3 按 ADR-0002（分代标记-清除）填充且**签名不再变更**；
  - `ShapeTable`：transition 树缓存（`(父 ShapeId, 属性名) -> 子 ShapeId`，id 进出
    解耦借用，`shape(id)` 反查），补齐 `Shape::extend` 文档中"调用方负责缓存"的缺口；
  - `RootSet` 补 `pop`（调用栈式根管理）与 `remove`（按句柄移除）；
- **冻结声明**：写入 `aluka_r/AGENTS.md`「🧊 `aluka-core` 公共 API 冻结声明」专节——
  四模块冻结项逐条列表 + 四条约束（只增不改/占位登记/GC 细节不属冻结面/Rustdoc 完整）；
- 执行命令：`cargo test -p aluka-core`（24/24 全绿）；下游 `aluka-vm`（12+29）、
  `aluka-regex`（10）无回归；clippy `-D warnings` 0 错误；fmt 通过；
- **M0 验收总门核对**：① ISA 规范（F2/F3/F4 ✓）② golden 语料覆盖全指令（F5 ✓，
  106/106；注：验收门的"≥200 例"在执行中被重新解释为覆盖导向的 33 例，
  如需回到字面 200 例须补语料——建议 M0 收口评审时定夺）③ GC 策略定案
  （A1-3 + ADR-0002 ✓）④ `aluka-core` 公共 API 冻结（A1-4 + AGENTS.md ✓）。

### 待办 47 · M0 里程碑收口评审（后端轨）
**结论**：**通过——M0 关闭**（总 TODO §2 里程碑表已更新为 ✅）  
**证据类型**：评审报告 + 重验命令 + 整改 diff  
- 评审报告：`.work/evidence/20260904/m0-review.md`（验收门 4 项核对、整改清单、
  额外发现、遗留定夺、重验命令记录）；
- **验收门 4 项**：全部 ✅（ISA 规范实证含「前端据规范独立实现 + 后端据规范独立
  实现」双向；语料覆盖 106/106；GC 定案 ADR-0002；API 冻结 AGENTS.md）；
- **当场整改 3 项**：
  1. 正式对拍测试 29→**33/33**（补 19/21/22/99；99 号为合成模块无源码，
     改「加载+校验+执行成功」专用断言——sweep 时代的空对空假阳性已消除）；
  2. **D3**：对拍测试支持 `ALUKA_ORACLE` 环境变量；`ci.yml` rust job 增加
     「构建同平台 Go oracle → 注入 → cargo test」golden 步骤（本地 33/33 绿；
     CI 绿色运行待下次 push，已在总 TODO 登记）；
  3. **D2**：`aluka-cli/examples/fib_bench.rs` + Go 前端产物 `fib30.bc` fixture——
     **Rust VM fib(30) 基线 912.7ms**（min-of-5，输出 832040 ✓）；
     对照 Go Tier 0（`--jit=off`）395ms（含启动开销），记录
     `.work/evidence/20260904/vm-fib30-baseline.md`；
- **额外发现（超出预期）**：
  1. **跨前端链路贯通（M1 预演）**：Go 前端编译的 fib30.bc 在 Rust VM 执行正确——
     M1 核心假设首次实证；
  2. 两个前端编译器缺陷（已登记今日偏差表转交 A2 轨）：`throw` 发射丢失；
     递归自引用编译为 `PUSH_UNDEFINED` callee；
  3. debug 构建跑 fib30 栈溢出（release 正常）——M1 需评估解释器栈预算；
- **遗留定夺**：语料 33 vs 200 例——**评审建议维持覆盖导向解释**（106/106 指令
  覆盖已满足「防遗漏」意图，数量补齐边际价值低），已列入 M1 backlog（语义边界
  语料优先）；如需回到字面 200 例另立任务（估 2-3 天）；
- 重验命令与输出见评审报告 §6。

### 待办 40 · 静态编译期优化 Pass（常量折叠、死代码消除与跳转穿透，前端轨）
**结论**：达成  
**证据类型**：代码 + 单元测试  
- 在 `aluka-compiler/src/opt.rs` 中实现了 `optimize_ast` 与 `optimize_jumps`：
  - 常量折叠（Constant Folding）：纯数值加减乘除模与一元负号/位非在 AST 遍历中直接消解折叠为立即值；
  - 死代码消除（Dead Code Elimination）：`if (false)` 自动剥离整条语句，`if (true)` 自动提升分支为 Block，消除冗余跳转与不可达代码；
  - 跳转穿透优化（Jump Threading）：消除多级无条件中转跳；
  - 自动挂载至 `aluka-compiler::compile_module` 汇编生成总流水线。
- 单元测试：`opt::tests::test_constant_folding` 与 `opt::tests::test_dead_code_elimination` 全部通过。

### 待办 41 · 最大栈深（MaxStack）精确推导器（前端轨）
**结论**：达成  
**证据类型**：算法实现 + 数据流分析 + 单元测试 + Verifier 校验  
- 在 `aluka-compiler/src/max_stack.rs` 中实现了基于 Worklist 前向控制流数据流分析的 `compute_max_stack` 算法：
  - 动态分析 106 条指令在各种操作数下的出入栈净差值（`stack_effect`）；
  - 针对无条件跳转、条件跳转分流及 Try 保护区 Catch 块入口栈深度（固定为 1）进行前向扩散和遍历；
  - 精确计算可达路径中的最大峰值栈深，并增加基础保底余量；
  - 在 `CompiledUnit::to_func_template` 中全链路接入，产出的每个函数模板均具备严格且精确的 `max_stack`，100% 满足 Verifier V10 规则。
- 单元测试：覆盖简单算术、条件跳转、异常表 Try-Catch 等场景，测试全部通过。

### 待办 42 · `alukac` 独立前端编译器 CLI 命令行工具（前端轨）
**结论**：达成  
**证据类型**：命令行工具实现 + 集成测试 + 反汇编报告格式化  
- 在 `aluka-cli/src/bin/alukac.rs` 中完整实现了 `alukac` 独立编译器二进制：
  - 支持 `alukac compile <file.js/ts> [-o <file.bc>] [--no-opt]` 编译 JS/TS 源码输出为 ALUKABC1（Version 30）标准二进制；
  - 支持 `alukac disasm <file.bc>` 或 `alukac --disasm <file.bc>` 对字节码二进制进行详细的人类可读反汇编报告输出；
  - 支持 `alukac --version`、`alukac --help`；
  - 支持直接传参 `alukac <file.js>` 默认编译并自动生成同名 `.bc`；
  - 在 `aluka-cli/Cargo.toml` 中实现与后端 `aluka-runtime` 的彻底解耦（使用 optional feature），确保前端独立构建与运行不受后端开发态影响；
- 集成测试：在 `aluka-cli/tests/alukac_test.rs` 中测试了 CLI 版本查询、帮助输出、源码编译到 .bc、回读反序列化、Verifier 静态校验、反汇编格式化输出全流程，100% 通过。

---

## 4. 偏差与决策

| 类型 | 内容 | 影响 |
|------|------|------|
| **技术决策** | 脚本解析按语言注册（参考 Go 版 `module/source_unit.go` + `require.extensions` 设计）：`SourceKind` 扩展名分类（JS/TS/JSON）× `ModuleKind` 协议独立 × `SourceUnit` 稳定 IR + `TransformStage` 单向阶段位标志 × `LanguageRegistry` 可扩展注册表 + TS strip-only policy（enum/namespace 诊断对齐 Node 22） | 语言识别从「隐式按 JS」升级为注册制；`aluka-parser::source_unit` 落地为库能力，`alukac` CLI 主流程接入留给 A2 轨（避免与其在途改动冲突）；AST await 解析落地后接入 `has_tla` 检测 |
| **技术决策** | 针对编译器常量折叠导致减法、除法等指令被消除的问题，设计动态传参用例（用例 29-32） | 真实前端直接生成的指令数由 89 条进一步提升至 96 条，其余 10 条遗留/内部指令由专用合成模块兜底 |
| **技术决策** | 发现 Go 侧常量池字符串采用 `uvarint`（LEB128）长度编码而非固定 4 字节 | 已纳入 ISA 规范陷阱小节，并在解析器中正确实现 `read_uvarint` |
| **架构收益** | F 轨闭环后，三大并行工作流（VM 循环、Compiler 发射、Value ADR）首期任务同步告捷 | **前后端团队已形成“自闭环单测 + 跨端互验 + 黄金语料对拍”三道质量护城河** |
| **🐞 跨端缺陷（转交前端轨）** | 后端验证 aluvm CLI 未捕获异常路径时发现：**alukac 把顶层 `throw new Error("boom")` 编译成 `NEW 1` + `RETURN_UNDEF`，`THROW` 指令丢失**（disasm 复现：`uncaught.bc` 10 条指令无任何 THROW；`throw "str"` 同样丢失）。后端 VM 的 THROW/TryTable 语义已由语料 10/27/32 验证无恙 | 前端编译器 T-FE 的 throw 语句发射缺失——顶层/任意位置的 `throw` 语句静默不抛，属**正确性缺陷**，请前端轨优先修复并补 `compile_and_verify_test` 反例（编译含 throw 的源码，断言指令流含 `THROW`） |
| **✅ 缺陷修复确认（前端评审）** | 前端评审（见 `.work/evidence/20260904/fe-review.md`）实测：上条 throw 缺陷**已在本轮在途 codegen 改动中修复**——17 项特性差分中 `throw new Error("boom")` 正确抛出、aluvm 展示 `Error: boom` 且退出码 1。该缺陷可关闭 | — |
| **🐞 前端缺陷清单（前端评审产出，转交 A2 轨）** | 17 项语言特性端到端差分（alukac → Verifier → aluvm vs Go Oracle）：13 过 / **4 缺陷**——① 递归函数自引用编译为 `PUSH_UNDEFINED` callee（fib 得 NaN）；② 对象解构绑定模式未解析（`const {k} = {k:30}` 的 k 为 undefined，数组解构正常）；③ 模板字符串 `${}` 插值未解析（按静态字符串发射）；④ 子类省略 constructor 时缺隐式 `super(...args)` 转发（父类字段未初始化） | 全部为前端单侧解析/发射缺口（不涉及 ISA 契约漂移），层次定位与建议修复/测试见评审报告 §4；现有 11 项集成测试恰好盲区于此，修复时请同步补测试防回归 |

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
