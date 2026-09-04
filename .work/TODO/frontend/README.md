# 前端编译器（A2 轨 · alukac）专属任务清单

> **子团队 / 独立子代理工作域说明**：  
> 本任务清单用于指导 **前端编译器（alukac）** 的独立开发与推进。  
> **解耦条件**：前端编译器仅依赖 [`aluka-bytecode`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode) 契约及 [`aluvm-isa-spec.md`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/docs/aluvm-isa-spec.md) 规范，**完全不需要等待 Rust 虚拟机后端完成**。  
> 编译产物可通过 [`aluka-bytecode::verifier`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 进行即时安全校验，并可直接喂给现有的只读 Oracle（`aluka_g/bin/aluka.exe`）运行验证。

---

## 1. 总体目标与交付边界

- **责任 Crates**：
  - `aluka_r/crates/aluka-parser/`（词法与语法解析、TS 语法剥离）
  - `aluka_r/crates/aluka-compiler/`（作用域分析、IR / 字节码发射、优化 pass、序列化）
  - `aluka_r/crates/aluka-cli/`（`alukac` 子命令支持）
- **输入**：JavaScript / TypeScript 源代码文本（UTF-8）
- **输出**：符合 ISA 规范（`ALUKABC1` Version 30）的内存结构 [`BytecodeModule`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 及 `.bc` / `.aluc` 二进制文件
- **质量门禁**：
  1. 所有生成的模块必须 100% 通过 [`BytecodeModule::verify(&self)`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 静态校验；
  2. 生成的字节码反向喂给 Go 虚拟机（Oracle）执行，运行行为与 Go 前端产物完全等价。

---

## 2. 详细阶段与任务分解

### 阶段 F-A2-1：词法与语法解析完备化（`aluka-parser`）

- [x] **T-FE-01 ES5 / ES2015 基础语法 AST 解析完备化**
  - 范围：表达式、语句、变量声明、函数声明、返回、控制流、对象/数组字面量；
  - 证据：`aluka-parser/src/lexer.rs`、`aluka-parser/src/parser.rs`（`parse` 递归下降解析器）、测试 `parser::tests::parses_basic_statements_and_expressions`。
- [x] **T-FE-02 TypeScript 类型注解解析与零成本剥离**
  - 范围：变量与形参类型标注（`: Type`）、函数返回值类型、接口声明（`interface`）、类型别名（`type`）、类型断言（`as Type`）；
  - 证据：`aluka-parser/src/parser.rs`（`skip_type_annotation`）、测试 `compile_and_verify_test.rs::test_parse_and_compile_source_text_to_bytecode_and_verify`（输入完整 TS 源码成功无损剥离并编译）。
- [x] **T-FE-03 ES2016-2024 高级特性解析**
  - 范围：`Class` 声明与继承（`extends`、`constructor`、类成员方法）、可选链（`?.`、`?.[]`）、空值合并（`??`）、`try-catch-finally` 异常捕获；
  - 证据：`aluka-parser/src/parser.rs`（`parse_class_stmt`、`parse_nullish_or`、可选链解析）、测试 `parser::tests::parses_classes_and_optional_chaining_and_try`。

---

### 阶段 F-A2-2：作用域分析与符号解析（`aluka-compiler::scope`）

- [x] **T-FE-04 作用域树与局部变量槽位分配（`ScopeTree`）**
  - 范围：函数级作用域（Function Scope）、块级作用域（Block Scope, `let`/`const`）、全局作用域；
  - 规则：计算 `num_locals`，严格控制局部变量槽位索引 `[0, num_locals)`，块退出后槽位复用，维护全局历史最大槽位峰值；
  - 证据：`aluka-compiler/src/scope.rs`（`ScopeTree::enter_scope` / `leave_scope` / `declare_local` / `resolve_local`）、单元测试 `scope::tests::test_scope_tree_block_slot_reuse`（块级槽位复用验证通过）。
- [x] **T-FE-05 闭包与上值捕获分析（`Upvalue` 分析）**
  - 范围：识别内层函数对外部函数局部变量的跨层引用；
  - 规则：生成 [`UpvalueCapture`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 描述数组，准确设置 `is_local` 与 `index`，发射 `Op::LoadUpvalue`、`Op::StoreUpvalue` 与 `Op::MakeClosure`；
  - 证据：`aluka-compiler/src/module.rs`（`compile_function_with_parent` 自由变量预扫描与自动捕获）、测试 `compile_and_verify_test.rs::test_compile_nested_closure_upvalue_capture_and_verify`（生成包含 `LoadUpvalue` 与 `UpvalueCapture` 的函数模板，100% 通过 Verifier V16 上值边界校验）。

---

### 阶段 F-A2-3：代码生成与指令发射（`aluka-compiler::codegen`）

- [x] **T-FE-06 基础操作数与常量池发射**
  - 范围：数值加载（`PUSH_INT`, `PUSH_NEG_INT`）、字面量（`PUSH_NULL`, `PUSH_TRUE`, `PUSH_FALSE`, `PUSH_UNDEFINED`）、常量池（`PUSH_CONST`）；
  - 规则：常量池正确去重，支持 String、Number、BigInt、Bool，严格满足 V5/V6 常量类型约束；
  - 证据：`aluka-compiler/src/lib.rs`，测试 `tests/compile_and_verify_test.rs`。
- [x] **T-FE-07 算术、逻辑与位运算指令生成**
  - 范围：`ADD`, `SUB`, `MUL`, `DIV`, `MOD`, `POW`, `NEG`, `UNARY_PLUS`, `NOT`, `BIT_NOT`, `BIT_AND`, `BIT_OR`, `BIT_XOR`, `SHL`, `SHR`, `USHR`，比较操作符 `EQ`, `NE`, `STRICT_EQ`, `STRICT_NE`, `LT`, `LE`, `GT`, `GE`；
  - 规则：严格按照 ECMAScript 左到右求值次序发射操作数与操作码，严格保持操作数栈平衡；
  - 证据：`aluka_r/crates/aluka-compiler/tests/compile_and_verify_test.rs`（3 项端到端测试 100% 通过 Verifier 与 VM 求值）。
- [x] **T-FE-08 变量访问与赋值指令**
  - 范围：`LOAD_LOCAL`, `STORE_LOCAL`, `LOAD_GLOBAL`, `STORE_GLOBAL`, `DUP`, `POP`；
  - 规则：区分局部变量读写与全局变量读写，支持连续赋值 `a = b = 1`（利用 `DUP` 复制栈顶赋予不同槽位）；
  - 证据：`aluka-compiler/src/lib.rs`（作用域符号表映射分配），测试 `tests/compile_and_verify_test.rs::test_compile_variable_declaration_and_scoping`。
- [x] **T-FE-09 控制流与跳转偏移计算（Jumps）**
  - 范围：`if`/`else`、`while`、三元表达式；
  - 指令：`JMP`, `JMP_FALSE_POP`；
  - 规则：通过回填（Backpatching）准确计算有符号相对字节跳转偏移，跨块合流栈深一致且严格满足 Verifier V7/V8 校验；
  - 证据：`aluka-compiler/src/lib.rs`（`emit_jump`, `backpatch_jump`），测试 `tests/compile_and_verify_test.rs::test_compile_control_flow_if_while_and_execute`。
- [x] **T-FE-10 对象、数组与属性访问指令**
  - 范围：对象字面量（`NEW_OBJECT`）、数组字面量（`NEW_ARRAY`, `BUILD_ARRAY`）、属性读写（`GET_PROP`, `SET_PROP`, `GET_ELEM`, `SET_ELEM`）；
  - 规则：`NEW_OBJECT count` 压入 `count * 2` 个键值对；`NEW_ARRAY count` 压入 `count` 个元素；
  - 证据：`aluka-compiler/src/lib.rs`，测试 `tests/compile_and_verify_test.rs::test_compile_object_array_and_member_access`（端到端计算 60.0 通过）。
- [x] **T-FE-11 函数调用与构造指令**
  - 范围：普通调用（`CALL`）、方法调用（`CALL_METHOD`）、构造器调用（`NEW`）、返回语句（`RETURN`, `RETURN_UNDEF`）；
  - 规则：`CALL_METHOD` 高 16 位打包参数个数，低 16 位打包方法名常量索引；实参依次压栈后压入 receiver/callee；严格保持操作数栈平衡并满足 Verifier 静态校验；
  - 证据：`aluka-parser/src/ast.rs`（新增 `Expr::Call`、`Expr::MethodCall`、`Expr::New`、`Stmt::Return`）、`aluka-compiler/src/codegen.rs`、测试 `compile_and_verify_test.rs::test_compile_call_and_method_call_and_new_and_verify`（100% 通过 V1..V16 静态安全与栈平衡校验）。
- [x] **T-FE-12 高级控制流发射：可选链短路与 Try-Finally 表**
  - 范围：可选链表达式（`a?.b`、`a?.[b]`）、`try { ... } catch (e) { ... } finally { ... }`；
  - 规则：
    - `OPTIONAL_JUMP` 短路分支与顺序分支栈深保持一致；
    - 生成合法 [`TryEntry`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs)，严格满足 `StartPC < EndPC`（V11）、Handler 落在外侧（V12）、无非法交叉重叠（V13）及 4 字节对齐（V14）；
  - 证据：`aluka-parser/src/ast.rs`（新增 `Expr::OptionalMember`、`Expr::OptionalIndex`、`Stmt::Try`）、`aluka-compiler/src/codegen.rs`、`scope.rs`（`try_table` 汇编传递）、测试 `compile_and_verify_test.rs::test_compile_optional_chaining_and_try_catch_finally_and_verify`（100% 通过 Verifier 严格静态校验）。
- [x] **T-FE-13 Class 结构体与模块级代码生成**
  - 范围：`MAKE_CLASS` 与类继承（`extends`）、构造函数及成员方法生成；
  - 规则：生成规范的 [`ClassTemplate`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs)，正确绑定方法索引、父类标记，并在模块级别完成类模板与多函数模板的汇编组装；
  - 证据：`aluka-parser/src/ast.rs`（新增 `ClassMethodDef`、`FunctionDef`、`Stmt::Class`、`Stmt::Function`）、`aluka-compiler/src/module.rs`（`compile_module`）、测试 `compile_and_verify_test.rs::test_compile_module_with_classes_and_functions_and_verify`（成功生成包含继承关系的 2 个 ClassTemplate 及 5 个函数模板，100% 通过 Verifier V1..V16 严格校验）。

---

### 阶段 F-A2-4：优化器 Pass 与最大栈深推导（`aluka-compiler::opt`）

- [x] **T-FE-14 静态编译期优化 Pass**
  - 常量折叠（Constant Folding）：纯字面量二元/一元运算直接计算，减少指令数；
  - 死代码消除（Dead Code Elimination）：不可达分支（`if (false)`）不发射；
  - 跳转穿透优化（Jump Threading）：消除无意义的跳转中转跳；
  - 证据：`aluka-compiler/src/opt.rs`（`optimize_ast`、`optimize_jumps`）、单元测试 `opt::tests::test_constant_folding`、`opt::tests::test_dead_code_elimination` 全部通过并自动挂载至模块汇编流水线。
- [x] **T-FE-15 最大栈深（MaxStack）精确推导**
  - 规则：实现基于前向控制流工作表（Worklist）数据流分析，遍历全部可达指令并累加 Try/Catch 区间栈深峰值；
  - 验收：发射出的 `func.max_stack` 确保执行过程绝对不超限（满足 V10 规则），同时不出现无谓的巨额过量分配；
  - 证据：`aluka-compiler/src/max_stack.rs`（`compute_max_stack`）、单元测试 `max_stack::tests`、在 `CompiledUnit::to_func_template` 中全链路接入并 100% 通过 Verifier V10 校验。

---

### 阶段 F-A2-5：字节码序列化与 CLI 工具（`alukac`）

- [x] **T-FE-16 二进制序列化实现（`Serialize`）**
  - 规则：完全按照 ISA 规范的 `ALUKABC1`（Version 30）小端排布写入，字符串与 BigInt 使用 LEB128（`uvarint`）变长压缩编码，指令流采用大端 3 字节操作数格式；
  - 证据：`aluka-bytecode/src/serializer.rs`（`BytecodeModule::serialize`）、测试 `crates/aluka-bytecode/tests/serializer_roundtrip_test.rs`（全部 33 个真实黄金语料 100% 通过无损 Round-trip 序列化回读与 Verifier 严格校验）、`aluka-compiler/tests/compile_and_verify_test.rs`（编译器产物序列化回读严格等价）。
- [x] **T-FE-17 `alukac` 独立编译器 CLI 命令行工具**
  - 功能：支持 `alukac compile input.js -o output.bc`、`alukac --version`、`alukac --disasm input.bc`；
  - 验收：CLI 运行流畅，提供详细的编译错误定位，支持格式化反汇编打印函数模板、常量池、上值表、Try 保护区与反汇编指令；
  - 证据：`aluka-cli/src/bin/alukac.rs`、集成测试 `aluka-cli/tests/alukac_test.rs`（全量测试与真实黄金语料反汇编验证 100% 通过）。
- [x] **T-FE-18 全量 32 个黄金语料端到端编译与 Go Oracle 运行双向对拍**
  - 范围：`aluka_r/tests/golden/sources/*.js` 全部 32 个真实黄金语料；
  - 特性：解构绑定、展开运算（`...`）、正则表达式字面量（`MakeRegexp`）、模板字符串、多层闭包链式上值（`ParentScopeInfo`）、类与原型继承（`Op::ConstructThis`, `Op::CallThis`）、`For-In`（`Op::EnumKeys`）、`For-Of` / 生成器 / 异步函数 / `for-await-of`（`Op::GetIterator`, `Op::GetAsyncIterator`, `Op::Yield`, `Op::Await`）；
  - 验收：
    1. 编译与 Verifier 静态校验：**32 / 32 100% 绿通**；
    2. Go Oracle 运行输出对拍：**32 / 32 100% 逐字完全一致**；
  - 证据：`aluka-cli/tests/golden_compile_oracle_test.rs`。

---

### 阶段 F-A2-6：多语言注册流水线、JSX/TSX 降级与双向四象限质量矩阵

- [x] **T-FE-19 前端语言注册流水线全链路贯通（选项 1）**
  - 范围：`aluka-parser::source_unit` 与 `aluka-compiler::source_unit`；
  - 内容：单向阶段位模型（`STAGE_BYTECODE_COMPILED`）、全局语言注册表（`LanguageRegistry::global()`）、纯 Rust 零依赖 JSON AST 编译器、`alukac` CLI 全链路重构由 SourceUnit 流水线驱动；
  - 证据：`aluka-compiler/tests/source_unit_pipeline_test.rs`（5 项测试通过）。
- [x] **T-FE-20 M2 阶段高级语法扩展：JSX/TSX Lowering 与 ESM 规范（选项 2）**
  - 范围：`aluka-parser`（AST、Lexer、Parser）与 `aluka-compiler::jsx`；
  - 内容：支持 JSX 开闭标签、连字符属性、Spread 属性、子节点文本清洗，平滑降级至 `React.createElement(...)` 调用；解析与编译 ESM `import`/`export` 规范；
  - 证据：`aluka-compiler/tests/jsx_and_esm_test.rs`（2 项测试通过）。
- [x] **T-FE-21 双向四象限质量对拍矩阵与全量语料闭环（选项 3）**
  - 范围：Rust 前端 × Rust VM (Q1)、Rust 前端 × Go VM (Q2)、Go 前端 × Rust VM (Q3)、Go 前端 × Go VM (Q4)；
  - 修复：排查修复 `serializer.rs` 中函数模板标量头 20 字节错误写 0 导致 Go VM 误将 slot 0 当作 arguments 覆盖 `this` 的隐蔽 bug；
  - 验收：全量 32 个黄金语料在四象限上全部 100% 逐字全绿对齐通过！
  - 证据：`aluka-cli/tests/four_quadrants_oracle_test.rs`。

---

## 3. 验收标准与测试驱动方式

```bash
# 1. 运行编译器单元测试
cargo test -p aluka-compiler

# 2. 编译生成字节码并通过 Verifier 自检
cargo test -p aluka-compiler --test compile_and_verify_test

# 3. 反向对拍：用 Go 侧虚拟机运行 Rust 编译产物（确保 Oracle 运行行为一致）
aluka_g/bin/aluka.exe run output_from_alukac.bc
```

- **完成标志**：32 个黄金语料样例源码（`aluka_r/tests/golden/corpus/*.js`）经 `alukac` 编译后：
  1. 全部 100% 通过 `aluka-bytecode::verifier`；
  2. 交由 Go 虚拟机运行，控制台输出与返回值与原 Go 前端编译出的 `.bc` 运行结果完全相同。
