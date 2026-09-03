# ADR：JVM 式架构——字节码升格为发布契约，aluvm 独立执行

> 状态：提案（待决策）｜日期：2026-09-03
> 关联：docs/bytecode-spec.md（现有 ISA）、docs/rust-reimplementation-devplan.md（并行轨道）

## 1. 先厘清现状：执行模型已经是 JVM 式的

提案容易被误读为"要改成 JVM 架构"，但 aluka 的**执行链路本来就是**：

```
源码 → lexer → parser → AST → compiler → 字节码 → VM 解释 → JIT 分层
                                          （Tier 0）  （Quick/Native）
```

对照 JVM：`javac` → `.class` → 解释器 → C1/C2。结构同构，且 aluka 已具备
JVM 后端的多数要件：106 条定长指令（4 字节，大端）、集中式元数据表
（`meta.go` 是操作数语义与栈效果的单一事实来源）、常量池、函数/类模板、
try 表、`ComputeMaxStack` 静态栈深分析、`ValidateModule` 操作数越界校验、
9 类优化 pass、三层执行 tier。

**真正缺的不是"字节码 + VM"，而是"字节码作为契约"。** 当前字节码是
*缓存*：`FormatVersion = 30`（已递增 30 次）、magic `ALUKABC1`、版本不符
即丢弃重编。它是引擎的内部实现细节，不是可发布、可跨版本、可被第三方
生成的格式。

## 2. 提案：把字节码从缓存升格为 ABI

三件事：

1. **稳定 ISA**：指令集与文件格式版本化并承诺兼容窗口（不再"随手 bump"）。
2. **发布格式**：定义单模块文件（`.aluc`）与归档（`.alua`，类比 `.jar`），
   可脱离源码分发与执行。
3. **拆分二进制**：`alukac`（前端：源码 → 字节码）与 `aluvm`（后端：
   加载 + 校验 + 执行）。现 `aluka` 退化为二者的便利壳（等价于 `java`
   同时能跑 `.java` 的语法糖）。

## 3. 收益

| 收益 | 说明 |
|------|------|
| **启动时延** | 跳过 lex/parse/compile。Go 版磁盘缓存已验证该收益，但缓存只在本机有效；发布格式让收益可分发 |
| **源码保护** | 商业分发只给字节码（与 `--compile` 单文件产物互补：那是"打包成 exe"，这是"发布成库"） |
| **前端解耦** | 任何语言只要产出合规字节码即可跑在 aluvm 上（TS/JSX 已在前端处理，未来可接其它 DSL） |
| **重构并行化** | 这是对 Rust 重构最实际的价值：`alukac` 与 `aluvm` 只通过 ISA 规范耦合，可**完全独立**开发/测试/替换。当前 devplan 的轨道 A 内部还是"parser→compiler→vm"串行依赖，拆开后可真并行 |
| **校验成为安全边界** | 现有 `ValidateModule`/`ComputeMaxStack` 升格为**加载期强制校验**（JVM verifier 的对应物），执行不可信字节码时是必需前提 |
| **测试可分层** | 前端测"源码→字节码"（golden 文件对拍），后端测"字节码→行为"（直接喂手写字节码，不必绕语法）。现有 jitdiff 已在做后者，可扩展 |

## 4. 代价与风险（必须正视）

### 4.1 JS 的动态性使字节码无法像 `.class` 那样"封闭"

JVM 的字节码是完备的执行单元；JS 有几处必须保留前端能力：

- `eval` / `new Function`：运行期编译。→ aluvm 必须**内嵌编译器**（可选
  feature），或在无编译器时对这些构造抛错（受限模式，类比无 JIT 的 JVM 不影响
  正确性，但 `eval` 是语义必需而非优化）。
- `Function.prototype.toString`：规范要求返回源文本。→ 字节码需携带源片段
  或接受"返回 `function () { [native code] }`"的不合规降级。
- 错误栈迹的行列号、sourcemap。→ 需要**调试信息段**（类比 `LineNumberTable`
  / `SourceFile`），可选剥离（类比 `-g:none`）。

### 4.2 ISA 稳定性与优化自由度冲突

本次会话就新增了 3 条 IR 指令（`OpLoadUpvalueNum`/`OpStoreUpvalueNum`/
`OpGetElem`）。ISA 一旦对外承诺，这种改动就要走版本协商而非直接改。
缓解：**分层版本**——
- *核心 ISA*（承诺稳定，跨小版本兼容）；
- *扩展指令*（在 header 声明能力位，旧 aluvm 见到未知能力位即拒绝加载，
  而不是误执行）。
注意 JIT 内部 IR 不属于 ISA，可自由变更（现有三层 tier 的 IR 已经是内部的）。

### 4.3 校验器是新的正确性负担

verifier 必须**拒绝一切不安全字节码**（栈下溢/越界跳转/操作数越界/
try 表畸形），否则 VM 内存安全依赖输入合法性——这在 Rust 版尤其致命
（`unsafe` 的 JIT 后端会信任 verifier 的结论）。现有 `ValidateModule` 是
函数级检查，尚不覆盖跨块栈深合流与 try 表结构，需要补齐到"通过校验即
可安全执行"的强度。这是一项独立的、可观的工程量。

### 4.4 兼容性面变大

一旦发布，就要面对"旧字节码 + 新 aluvm"与"新字节码 + 旧 aluvm"两个方向，
以及第三方生成器产出的畸形输入。测试矩阵显著扩张。

## 5. 建议的格式草案

```
.aluc（单模块）
┌──────────────────────────────────────────────┐
│ magic "ALUC" | isaVersion u16 | fileVersion u16│
│ capabilityBits u64      ← 扩展指令的能力协商    │
│ flags u32               ← stripped-debug 等     │
├──────────────────────────────────────────────┤
│ 常量池（字符串/数字/BigInt/模板引用）            │
│ 函数模板表（含 NumLocals/MaxStack/Upvalues/     │
│              TryTable/字节码体）                │
│ 类模板表                                        │
│ 调试信息段（行列表、源文件名、函数源片段）  可剥离 │
└──────────────────────────────────────────────┘

.alua（归档）= 多个 .aluc + 模块图/入口清单（类比 jar 的 MANIFEST）
```

`--compile` 的现有单文件产物（基座 + payload + footer + sha256）与之正交：
它是"把 `.alua` 嵌进可执行文件"，格式统一后可直接复用。

## 6. 对 Rust 重构的影响（最实际的价值）

现 devplan 的轨道 A 是一条串行链。引入 ISA 契约后可拆成两条真并行轨道：

```
轨道 A1（aluvm 后端）：aluka-core → aluka-bytecode(ISA+verifier) → aluka-vm → aluka-jit
轨道 A2（alukac 前端）：aluka-parser → aluka-compiler（含 TS/JSX）
两轨道的唯一接口 = ISA 规范 + golden 字节码语料
```

- **A1 可以先用 Go 版 `alukac` 产出的字节码做输入**——不必等 Rust parser 完成，
  M1 的"能跑 hello.js"提前到"能跑 hello.aluc"，而 hello.aluc 由现有 Go
  工具链生成。这实质上把 M1 的关键路径缩短了一整个 parser+compiler 的工期。
- 反向亦然：Rust `alukac` 完成后可先用 Go 版 VM 验证产物（双向 diff）。
- 迁移期的行为仲裁从"整条链路对拍"细化为"字节码层对拍 + 行为层对拍"，
  定位缺陷更快。

**前提**：Go 版与 Rust 版必须共用同一 ISA 规范与 golden 语料——这需要先把
`docs/bytecode-spec.md` 从"实现说明"提升为"规范"（含校验规则与一致性用例）。

## 7. 决策建议

**建议采纳，但分两步且不阻塞当前里程碑：**

- **第一步（低风险，立即可做）**：把现有字节码格式**文档化为规范** +
  补齐 verifier 到"通过即安全"强度 + 建立 golden 字节码语料。产出物对
  Go 版也直接有用（verifier 强化本身就是安全收益），且解锁 Rust 侧
  A1/A2 并行。
- **第二步（发布契约，M2 之后）**：定 `.aluc`/`.alua` 格式、拆
  `alukac`/`aluvm` 二进制、决定 `eval` 与调试信息策略、承诺兼容窗口。
  这一步要等语言特性稳定（M2 全语言完成）再做，否则 ISA 会反复变。

**不建议**：现在就冻结 ISA 并对外承诺。当前仍在高频增删指令（本会话即
3 条），过早冻结会把优化空间锁死，或逼出一堆兼容包袱。

## 8. 待定问题（决策前需回答）

1. `eval`/`new Function` 在纯 aluvm（无编译器）下：抛错还是必须内嵌编译器？
2. `Function.prototype.toString` 的合规性：携带源片段（体积）还是降级
   （不合规）？
3. ISA 兼容窗口承诺多久？谁有权递增核心版本？
4. verifier 强度目标：仅内存安全，还是也保证类型健全（后者更贵）？
5. 是否允许第三方前端（若允许，ISA 需要更严的文档与一致性测试套件）？
