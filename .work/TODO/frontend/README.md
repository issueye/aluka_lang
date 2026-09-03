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

- [ ] **T-FE-01 ES5 / ES2015 基础语法 AST 解析完备化**
  - 范围：表达式、语句、函数声明/函数表达式、箭头函数、解构赋值、模板字符串；
  - 产出：完备且紧凑的 AST 节点定义；
  - 验收：`cargo test -p aluka-parser`，覆盖主流语法树构建。
- [ ] **T-FE-02 TypeScript 类型注解解析与零成本剥离**
  - 范围：类型注解（`: Type`）、接口声明（`interface`）、类型别名（`type`）、类型断言（`as Type` / `<Type>`）、枚举（`enum`）；
  - 对齐策略：继承 Go 版 parser 层零成本快速剥离策略，直接将类型语法节点在编译前剔除或跳过；
  - 验收：带类型注解的 TS 文件解析后与纯 JS 拥有等价的可执行 AST。
- [ ] **T-FE-03 ES2016-2024 高级特性解析**
  - 范围：`Class` 声明与继承（静态方法、Getter/Setter、计算属性名）、`async` / `await`、生成器 `function*` / `yield`、可选链 `?.`、空值合并 `??`、数字分隔符、BigInt 字面量；
  - 验收：提供包含全部 ES 特性的源码解析集成测试。

---

### 阶段 F-A2-2：作用域分析与符号解析（`aluka-compiler::scope`）

- [ ] **T-FE-04 作用域树与局部变量槽位分配（`ScopeTree`）**
  - 范围：函数级作用域（Function Scope）、块级作用域（Block Scope, `let`/`const`）、全局作用域；
  - 规则：计算 `num_locals`，严格控制局部变量槽位索引 `[0, num_locals)`，分配局部槽位；
  - 验收：无槽位重叠泄漏，不越界（满足 V4 规则校验）。
- [ ] **T-FE-05 闭包与上值捕获分析（`Upvalue` 分析）**
  - 范围：识别内层函数对外部函数局部变量的跨层引用；
  - 规则：生成 [`UpvalueCapture`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 描述数组，准确设置 `is_local` 与 `index`；
  - 验收：满足 V16 校验，正确表达单层及多层嵌套闭包捕获。

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
- [ ] **T-FE-11 函数调用与构造指令**
  - 范围：普通调用（`CALL`）、方法调用（`CALL_METHOD`）、带 this 调用（`CALL_WITH_THIS`）、构造器调用（`NEW`, `CONSTRUCT_THIS`）；
  - 规则：`CALL_METHOD` 高 16 位打包参数个数，低 16 位打包方法名常量索引；精确遵循实参压栈协议。
- [ ] **T-FE-12 高级控制流发射：可选链短路与 Try-Finally 表**
  - 范围：可选链表达式（`a?.b?.c`）、`try { ... } catch (e) { ... } finally { ... }`；
  - 规则：
    - `OPTIONAL_JUMP` 短路分支与顺序分支栈深保持一致；
    - 生成合法 [`TryEntry`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs)，严格满足 `StartPC < EndPC`（V11）、Handler 落在外侧（V12）、无非法交叉重叠（V13）及 4 字节对齐（V14）。
- [ ] **T-FE-13 Class 结构体与协程代码生成**
  - 范围：`MAKE_CLASS` 与类继承、`yield`（`YIELD`）、`await`（`AWAIT`）；
  - 规则：生成规范的 [`ClassTemplate`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs)，正确绑定方法索引与父类标记。

---

### 阶段 F-A2-4：优化器 Pass 与最大栈深推导（`aluka-compiler::opt`）

- [ ] **T-FE-14 静态编译期优化 Pass**
  - 常量折叠（Constant Folding）：纯字面量二元/一元运算直接计算，减少指令数；
  - 死代码消除（Dead Code Elimination）：不可达分支（`if (false)`）不发射；
  - 跳转穿透优化（Jump Threading）：消除无意义的跳转中转跳。
- [ ] **T-FE-15 最大栈深（MaxStack）精确推导**
  - 规则：实现类似 Go 侧 `maxstack.go` 的前向深度分析，遍历全部可达基本块并累加 Try 区间峰值补偿；
  - 验收：发射出的 `func.max_stack` 必须确保执行过程绝对不超限（满足 V10 规则），同时不出现无谓的巨额过量分配。

---

### 阶段 F-A2-5：字节码序列化与 CLI 工具（`alukac`）

- [ ] **T-FE-16 二进制序列化实现（`Serialize`）**
  - 规则：完全按照 ISA 规范的 `ALUKABC1`（Version 30）小端排布写入，字符串与 BigInt 使用 LEB128（`uvarint`）压缩长度；
  - 验收：输出的 `.bc` 能够被 [`BytecodeModule::deserialize_go`](file:///e:/codes/go_projects/aluka_lang/aluka_lang/aluka_r/crates/aluka-bytecode/src/verifier.rs) 无损回读。
- [ ] **T-FE-17 `alukac` 独立编译器 CLI 命令行工具**
  - 功能：支持 `alukac compile input.js -o output.bc`、`alukac --version`、`alukac --disasm input.bc`；
  - 验收：CLI 运行流畅，提供详细的编译错误定位（行号、列号与源码切片展示）。

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
