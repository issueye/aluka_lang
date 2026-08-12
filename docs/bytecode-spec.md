# 字节码指令集规范（Bytecode Spec）

> 本文是 Aluka 字节码 VM 指令集的权威规范。权威实现与元数据见
> `internal/engine/bytecode/opcodes.go`（指令枚举）与
> `internal/engine/bytecode/meta.go`（集中式元数据表），
> 执行语义以 `internal/engine/interpreter/vm.go` 的分发循环为准。

## 1. 编码格式

每条指令固定 4 字节，大端序：

```
[ opcode:1 ][ operand:3 (big-endian uint24) ]
```

- 无操作数指令的 operand 三字节恒为 0（执行时忽略）。
- 固定宽度使分发循环无重解码开销，并允许预计算跳转目标。
- 跳转操作数为**有符号相对偏移**（相对下一条指令的字节偏移，24 位补码，
  范围 ±8MB），解码用 `SignedOperand`（module.go）。
- 重值（字符串/大数/函数模板）不直接进操作数，统一走常量池
  （`OpPushConst`）；小整数 0..2^24-1 可用 `OpPushInt` 内联，
  负数用 `OpPushNegInt`（压入 `-(A)`）。

指令数值一经发布即稳定：**禁止重排已发布的 opcode 数值**
（序列化字节码依赖其数值）。

## 2. 操作数语义分类（OperandKind）

每条指令的 operand 域语义由 `meta.go` 的 `OperandKind` 统一描述：

| Kind | 语义 | 代表性指令 |
|------|------|-----------|
| `OperandNone` | 不读操作数域 | ADD、GET_ELEM、RETURN |
| `OperandConstIdx` | 函数常量池索引（`tmpl.Constants[operand]`）。属性名类与全局名类共用 | PUSH_CONST、GET_PROP、LOAD_GLOBAL、TYPEOF_GLOBAL |
| `OperandInt` | 直接编码的 24 位无符号整数 | PUSH_INT、PUSH_NEG_INT |
| `OperandSlot` | 当前帧局部槽索引 | LOAD_LOCAL、STORE_LOCAL、CLOSE_UPVALUES |
| `OperandUpvalueIdx` | upvalue 捕获索引 | LOAD_UPVALUE、STORE_UPVALUE |
| `OperandTemplateIdx` | 模块级函数/类模板索引 | MAKE_CLOSURE、MAKE_CLASS |
| `OperandTryIdx` | try 表索引 | TRY_ENTER、TRY_EXIT、TRY_EXIT_FINALLY |
| `OperandSignedOff` | 有符号相对跳转偏移 | JMP、JMP_TRUE_POP、TRY_EXIT_JMP |
| `OperandCount` | 参数/元素数量（调用与构造类） | CALL、NEW、NEW_ARRAY、NEW_OBJECT |
| `OperandPackedSlotName` | 打包双字段 `slot<<16 \| nameIdx` | GET_PROP_LOCAL |
| `OperandPackedCall` | 打包双字段 `numArgs<<16 \| nameIdx` | CALL_METHOD |

校验规则（`validateOperand`，optimize.go）：

- `OperandConstIdx`：`operand < len(Constants)`
- `OperandSlot`：`operand < NumLocals`
- `OperandUpvalueIdx`：`operand < len(Upvalues)`
- `OperandPackedSlotName`：`slot < NumLocals` 且 `nameIdx < len(Constants)`
- `OperandPackedCall`：`nameIdx < len(Constants)`
- `OperandTemplateIdx`/`OperandTryIdx` 需模块级上下文，不做函数级校验

## 3. 栈效果（StackEffect）

`OpMeta.Pops/Pushes` 描述单条指令在操作数栈上的净弹出/压入数量。
两类指令的栈效果**不可静态推断**，优化器必须拒绝使用：

- `StackCond`：效果依赖运行时条件或跳转是否发生。如 `JMP_TRUE_KEEP`
  跳转时保持值、不跳转时弹出（`&&` 短路）。
- `VarStack`：效果由 operand 或运行时决定。如 `CALL`（弹出
  callee+numArgs，压入 1）、`NEW_OBJECT`（弹出 propCount×2）。

访问器 `OpMeta.StackEffect() (pops, pushes uint8, known bool)`：
`StackCond`/`VarStack` 返回 `known=false`。

## 4. 分类标记

- `PurePush`：无副作用的纯字面量压栈（PUSH_UNDEFINED/NULL/TRUE/FALSE/
  CONST/INT/NEG_INT）。`push + pop` 对可安全删除。
- `IsJump`：带相对偏移的跳转类指令（目标重定位、不可达分析依赖）。
- `IsTerminal`：`RETURN`/`RETURN_UNDEF`/`THROW`，其后顺序指令不可达
  （优化器的不可达代码删除依赖）。

## 5. 新增指令清单（维护指南）

新增指令时**必须**同步完成以下全部步骤，否则测试失败或旧缓存错读：

1. `opcodes.go` 枚举中追加指令（仅可追加，不可重排已有数值）。
2. `meta.go` 的 `opMeta` 表登记 `OpMeta`：
   - `Name`（大写指令名，全仓库唯一）；
   - `Operand`（按上表分类；无操作数填 `OperandNone`）；
   - `Pops/Pushes`（固定效果时填写，否则置 0 并标记
     `StackCond`/`VarStack`）；
   - `PurePush`/`IsJump`/`IsTerminal` 按语义标记。
3. `interpreter/vm.go` 分发循环添加 case（`String()`/`HasOperand()`
   由元数据表自动派生，无需手改）。
4. 若为跳转类，确认 `isRelativeJump` 覆盖（meta 表 IsJump 派生，
   无需单独修改 optimize.go）。
5. `serialize.go` **bump `FormatVersion`** 使旧磁盘缓存失效
   （旧缓存可能含旧指令形态产物）。
6. 若指令形态影响 JIT 匹配器（arrayPush/closureIncrement 等依赖
   特定指令序列，见 serialize.go 头部注释），同步评审
   `internal/engine/interpreter/jit_bridge.go` 的匹配器。
7. `meta_test.go` 的断言自动覆盖登记完整性（每个 opcode 必须有
   元数据、IsJump/PurePush 集合与历史一致、栈效果与预期一致）。

## 6. 与周边系统的关系

- **序列化**（serialize.go）：FormatVersion 是缓存失效的唯一防线；
  序列化顺序：magic(8) | version(u32) | funcCount | classCount | funcs | classes。
- **优化器**（optimize.go）：`OptimizeModule` 基于元数据表做保守重写
  （删除/融合/跳转穿透 + 常量折叠/不可达删除/冗余 store-load 消除），
  任何改变指令位置的改写都经 `relocateMetadata` 重定位 TryTable
  全部 PC（含 v18 区域边界）与 LineStarts。
- **JIT**（internal/engine/jit/）：Quick IR 与 native 发射从 FuncTemplate
  字节码下降；元数据表的操作数分类与 JIT 的 candidate 过滤
  （RejectLeafReason/RejectTraceReason）相互独立，但指令形态变化
  需保证 JIT 匹配器可安全拒绝而非误匹配。
- **校验**：`ValidateModule`（optimize.go）在任何优化后运行，校验
  指令合法性、操作数范围、跳转目标对齐、TryTable/LineStarts PC。
