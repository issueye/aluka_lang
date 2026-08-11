# aluka JIT 正确性覆盖矩阵（R0-3）

> 文档版本：v1.0
> 日期：2026-08-11
> 基线：`docs/jit-performance-optimization-plan.md` v2.13、`docs/jit-follow-up-development-plan.md` v1.0
> 用途：回答“代码已宣称支持的每个 JIT 能力，是否有至少一个权威测试”。

## 1. 口径

- **权威测试**：能证明能力真实工作、且失败时能指出具体能力缺陷的 Go 测试（函数级或集成级
  differential）。测试名指向 `internal/engine/interpreter/jit_test.go`、
  `internal/engine/interpreter/jit_lifetime_amd64_test.go`、`internal/engine/jit/*_test.go`、
  `internal/engine/jit/native/*_test.go`。
- **覆盖** 列语义：
  - `✓` = 该层实际处理该 opcode / 值类型；
  - `✗` = 该层显式拒绝（Native 拒绝整数 ABI 指令、Symbol 未建模等），失败后由上层/解释器接管；
  - `—` = 该 opcode 不在该层执行路径上（由 bridge 预先解析，如 `OpPushSelf/OpSelfCall`）。
- 差分测试统一做法：同一源码在 `--jit=off / quick / auto` 三种模式各跑一遍，逐值（Number 按位、
  NaN 按语义）比较，并断言统计计数（Compiled/Executed/GuardFailures 等）证明目标 tier 真实命中。
- 本矩阵只声明**现有**能力；`docs/jit-performance-optimization-plan.md` §17 的 P0-P2 未落地项
  （更广语法生成式差分、String/BigInt 算术、宽松相等、Native spill 等）不在矩阵内，它们进入矩阵
  的时机见 §8 维护约定。

## 2. IR opcode 覆盖矩阵

| IR opcode | 语义 | Quick 函数 | Quick trace | Native amd64 | 权威测试 |
|-----------|------|:---:|:---:|:---:|----------|
| `OpConst` | F64 常量 | ✓ | ✓ | ✓ | `TestCompileLeafAndExecute`、`TestNumberEdgeCases` |
| `OpLoadLocal` / `OpStoreLocal` | 局部变量读写 | ✓ | ✓ | ✓ | `TestCompileLeafAndExecute`、`TestAutoJITNativePropertyLoop` |
| `OpAdd/OpSub/OpMul/OpDiv` | 数值四则（IEEE-754） | ✓ | ✓ | ✓ | `TestJITNumericTiersDifferential`、`TestNativeMatchesQuickForRandomNumericIR` |
| `OpMod` | `%` 取模 | ✓ | ✓ | ✗（Auto 回退 Quick） | `TestJITExtendedNumericSyntaxCrossTierDifferential` |
| `OpPow` | `**` 幂 | ✓ | ✓ | ✗（Auto 回退 Quick） | `TestJITExtendedNumericSyntaxCrossTierDifferential` |
| `OpNeg` | 一元 `-` | ✓ | ✓ | ✓ | `TestNativeSwapAndNeg` |
| `OpNot` | 逻辑非 `!`（ToBoolean） | ✓ | ✓ | ✗（Auto 回退 Quick） | `TestJITExtendedNumericSyntaxCrossTierDifferential` |
| `OpBitNot` | 按位非 `~`（ToInt32） | ✓ | ✓ | ✗（整数 ABI 拒绝） | `TestJITBitwiseCrossTierDifferential` |
| `OpUnaryPlus` | 一元 `+`（identity，保留 `-0`） | ✓ | ✓ | ✓（零指令） | `TestNativeUnaryPlusPreservesNumberAndGuardsOtherTypes`、`TestQuickJITUnaryPlusGuardFallsBackForString` |
| `OpEq/OpNe` | Number-only `==/!=` | ✓ | ✓ | ✓ | `TestJITNumericTiersDifferential`、`TestExecuteNotEqualTreatsNaNAsUnequal` |
| `OpStrictEq/OpStrictNe` | 无 coercion `===/!==` | ✓ | ✓ | ✓（Number 入口） | `TestExecuteStrictEqualityAcrossQuickValues`、`TestJITStrictEqualityReferenceTraceDifferential`、`TestJITSymbolValuesGuardBackToTier0` |
| `OpBitAnd/OpBitOr/OpBitXor/OpShl/OpShr/OpUShr` | 32 位位运算（ToUint32/ToInt32） | ✓ | ✓ | ✗（整数 ABI 拒绝） | `TestJITBitwiseCrossTierDifferential` |
| `OpLt/OpLe/OpGt/OpGe` | 数值关系比较（NaN → false） | ✓ | ✓ | ✓ | `TestNativeNumericComparisonBranches`、`TestJITNumericTiersDifferential` |
| `OpPop` | 弹栈 | ✓ | ✓ | ✓ | 贯穿所有执行测试 |
| `OpReturn` / `OpReturnUndef` | 返回值 / 返回 undefined | ✓ | ✓ | ✓ | `TestCompileLeafAndExecute`、`TestJITTraceGuardedNoopCallDeoptsOnCalleeChange`（ReturnsUndefined） |
| `OpJump` | 无条件跳转（含回边预算） | ✓ | ✓ | ✓ | `TestTraceBudgetYieldsCompletedIterations`、`TestAutoJITNativeLoopYieldsAtSafepoints` |
| `OpJumpTrue/OpJumpFalse` | truthiness 分支 | ✓ | ✓ | ✓ | `TestNativeLogicalKeepBranchesMatchQuickTruthiness` |
| `OpJumpTrueKeep/OpJumpFalseKeep` | 短路 `&&/||`（保留左值） | ✓ | ✓ | ✓ | `TestExecuteLogicalKeepBranchesPreserveNumberValue`、`TestJITLogicalShortCircuitTraceCrossTierDifferential` |
| `OpJumpNullishKeep` | `??` nullish 合流 | ✓ | ✓ | ✓（Number 特化） | `TestExecuteNullishKeepDistinguishesNullishAndReferenceValues`、`TestNativeNullishKeepSpecializesNumbersAndGuardsNullish` |
| `OpPushSelf` / `OpSelfCall` | 自递归调用 | —（bridge 按 upvalue 解析） | — | — | `TestQuickJITSelfRecursive`、`TestQuickJITSelfIdentityGuard` |
| `OpDup` / `OpSwap` | 栈操作 | ✓ | ✓ | ✓ | `TestNativeSwapAndNeg` |
| `OpGetProp` | own data Number 属性读 | ✓ | ✓ | ✓（入口 Go 侧 shape/slot 校验） | `TestAutoJITNativeNumericPropertyGuard`、`TestQuickJITCompilesPropertyLoopTrace` |
| `OpSetProp` | own data Number 属性写 | —（仅 trace） | ✓ | ✓（Go 两阶段写回） | `TestAutoJITNativePropertyWriteTrace`、`TestJITPropertyWriteTracePreservesSetterAndProxy` |
| `OpTraceExit` | trace 语义出口（exitID/resumePC） | — | ✓ | ✓ | `TestTraceSupportsMultiplePreciseDeoptExits`、`TestJITTraceRestoresTwoDistinctDeoptExits` |
| `OpGuardNoopCall` | noop 调用身份 guard | — | ✓ | ✓（融合） | `TestJITTraceGuardedNoopCallDeoptsOnCalleeChange` |
| `OpGuardMethodGet` | `return this.x` getter guard | — | ✓ | ✓（融合） | `TestJITTraceGuardedTrivialMethodGetter` |

全部 opcode 均被 verifier 覆盖（栈深/合流/跳转目标检查），verifier 拒绝测试见
`TestVerifyRejectsNonEmptyTraceExitStack`、`TestVerifyRejectsLogicalKeepBranchWithMismatchedJoinDepth`、
`TestNativeRejectsLocalNotAssignedOnEveryPath`、`TestCompileLeafRejectsUnsupported`。

## 3. 值类型覆盖矩阵

| 值类型 | Quick 建模 | 支持的语义 | 权威测试 |
|--------|-----------|-----------|----------|
| Number | 原生 `quickNumber` | 算术/比较/位运算/truthiness/短路/nullish/严格相等/属性/返回/栈恢复/Native 全量 | `TestJITNumericTiersDifferential`、`TestNativeMatchesQuickForRandomNumericIR`、`TestJITBitwiseCrossTierDifferential` |
| Boolean | 原生 `quickBoolean` | truthiness/短路/严格相等/返回 | `TestExecuteLogicalKeepBranchesPreserveNumberValue`、`TestJITStrictEqualityReferenceTraceDifferential`（false 输入） |
| null / undefined | 原生 `quickNull/quickUndefined` | nullish/严格相等/truthiness（falsy）/栈恢复 | `TestExecuteNullishKeepDistinguishesNullishAndReferenceValues`、`TestJITNullishCoalescingTraceCrossTierFallback`、`TestJITStrictEqualityReferenceTraceDifferential`（null/undefined） |
| Object | `quickObject`（引用） | truthiness/严格相等（identity）/属性 PIC/方法 guard/返回 | `TestExecuteStrictEqualityAcrossQuickValues`（object identity）、`TestQuickJITCompilesPropertyLoopTrace` |
| String | `quickString`（opaque 引用） | truthiness/nullish/短路/严格相等/返回/栈恢复；无算术、无比较 | `TestJITLogicalReferenceValuesStayInQuick`、`TestJITStrictEqualityReferenceTraceDifferential`（"same"） |
| BigInt | `quickBigInt`（opaque 引用） | truthiness/nullish/短路/严格相等（按整数值）/返回/栈恢复；无算术 | `TestJITLogicalReferenceValuesStayInQuick`、`TestJITStrictEqualityReferenceTraceDifferential`（7n） |
| Symbol | 不建模 → guard 回 Tier 0 | 无；`===/!==` 与 truthiness 均在任何 JIT 副作用前回退 | `TestJITSymbolValuesGuardBackToTier0`（R0-3 新增） |

Number 边界值（`NaN`、`+0/-0`、`±Infinity`、除零、位运算截断、`1 / -0`）在
`TestNumberEdgeCases`、`TestJITNumericTiersDifferential`、`TestJITNumericNotEqualTiersDifferential`、
`TestJITBitwiseCrossTierDifferential`、`TestNativeCallbackNumericFastPathsPreserveFallback` 中逐位验证。

## 4. 函数 / trace / Native 生命周期覆盖

| 能力 | 权威测试 |
|------|----------|
| Quick 函数编译与执行 | `TestQuickJITCompilesHotNumericLeaf` |
| Quick trace 编译与执行 | `TestQuickJITCompilesNumericLoopFromBackedge` |
| Native 函数 | `TestAutoJITUsesNativeLinearKernel` |
| Native trace | `TestAutoJITRunsNativeLoopFromBackedge` |
| trace 预算 yield / safepoint | `TestQuickJITTraceYieldsAtSafepointBudget`、`TestAutoJITNativeLoopYieldsAtSafepoints` |
| 自递归 Quick 执行 | `TestQuickJITSelfRecursive`、`TestQuickJITSelfIdentityGuard` |
| 单态 callee 特化与内联 | `TestAutoJITNativeInlinesMonomorphicCallee`、`TestAutoJITNativeInlinePreservesArgumentOrder` |
| callee PIC（两路）与第三 target 熔断 | `TestQuickJITMonomorphicCalleeGuard`、`TestQuickJITCalleeGuardDisablesAfterRepeatedThirdTarget` |
| 闭包实例隔离 | `TestQuickJITCalleeGuardIsolatesClosureInstances` |
| 属性 PIC（两路 shape）与第三 shape 熔断 | `TestAutoJITNativePropertyGuardDisablesOnlyNativeAfterThirdShape`、`TestAutoJITNativeTraceGuardDisablesOnlyNativeAfterThirdShape` |
| 数组 push 范围 trace | `TestJITTraceGuardedArrayPushRange`、`TestJITArrayPushTraceRejectsProxy`、`TestJITArrayPushTraceRejectsUnsafeNumbers` |
| numeric-upvalue closure trace | `TestJITTraceGuardedClosureIncrementUpvalue`、`TestJITClosureIncrementTraceDeoptsOnCalleeChange`、`TestJITClosureIncrementTraceRejectsAliasedUpvalue` |
| NativeCallback 数值快路径 | `TestNativeCallbackNumericFastPathsPreserveFallback` |
| 后台编译（大 IR） | `TestAutoJITBackgroundCompilesLargeProgram` |
| 后台编译排空（重配置/关闭） | `TestAutoJITBackgroundCompileDrainsOnReconfigure`、`TestAutoJITCloseReleasesPendingBackgroundCompile` |
| LRU 代码缓存（函数/trace） | `TestAutoJITNativeCodeCacheEvictsLRU`、`TestAutoJITNativeTraceCacheEvictsLRU`、`TestAutoJITNativeCacheSurvivesRepeatedGC` |
| safepoint 中断与 OOM | `TestJITSafepointInterruptsQuickAndNativeFunctionLoops`、`TestJITSafepointInterruptsQuickAndNativePropertyTraces`、`TestQuickJITSafepointInterruptsRecursion`、`TestNativeJITSafepointPropagatesOOMRangeError` |
| instruction metrics 门控 | `TestJITBypassesWhenInstructionMetricsAreEnabled` |
| 冷启动 | `BenchmarkJITColdStart`（bench 包，`off 2.353ms / auto 2.447ms`） |

## 5. guard / deopt 覆盖

| 能力 | 权威测试 |
|------|----------|
| 类型 guard 失败回退 Tier 0 | `TestQuickJITGuardFallsBackToInterpreter` |
| 函数/trace 分层熔断（Quick 2 次、Native 2 次独立计数） | `TestQuickJITTraceGuardDisablesAfterRepeatedTypeFailure`、`TestAutoJITNativeGuardDisablesOnlyNativeAfterThirdShape` |
| 第三 shape / 第三 target 稳定回退且 RX 释放 | `TestAutoJITNativeTraceGuardDisablesOnlyNativeAfterThirdShape`、`TestQuickJITCalleeGuardDisablesAfterRepeatedThirdTarget` |
| accessor/Proxy/prototype 不进入快路径 | `TestAutoJITNativePropertyGuardDoesNotInvokeAccessor`、`TestJITPropertyWriteTracePreservesSetterAndProxy`、`TestJITArrayPushTraceRejectsProxy` |
| 多出口 deopt（exitID/resumePC） | `TestTraceSupportsMultiplePreciseDeoptExits`、`TestJITTraceRestoresTwoDistinctDeoptExits` |
| 操作数栈恢复（≤8 槽） | `TestTraceExitRestoresExternalOperandStack`、`TestJITTraceRestoresOperandStackIntoVM`、`TestNativeTraceExitRestoresExternalOperandStack` |
| dirty locals 精确写回 | `TestNativeTraceReturnsPreciseExitAndDirtyLocals` |
| 属性写提交与写回 | `TestAutoJITNativePropertyWriteTrace`、`TestJITPropertyWriteTracePreservesSetterAndProxy` |
| 副作用与异常恢复（不重复、不丢失） | `TestJITTraceDeoptStackPreservesSideEffectBeforeThrow`、`TestJITTraceGuardFailurePreservesThrowAndCatch`、`TestJITTraceReturnsIntoTryCatch` |
| Native/Quick 双执行验证与 mismatch 恢复 | `TestAutoJITVerifiesNativePropertyLoop`、`TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch` |
| Symbol 值 guard 回 Tier 0（无副作用、无 verify） | `TestJITSymbolValuesGuardBackToTier0`（R0-3 新增） |

## 6. 平台与可执行内存覆盖

| 能力 | 权威测试 | 平台 |
|------|----------|------|
| W^X（RW → RX 发布并执行） | `TestPublishUsesWXRAndExecutes` | windows + linux（交叉/CI） |
| 全局 RX 计数归零（关闭/释放） | `TestExecutableMemoryAccountingReturnsToBaseline`、`TestAutoJITShortLivedVMsReleaseExecutableMemory` | amd64 |
| Native 执行时并发 GC | `TestGeneratedCodeSurvivesConcurrentGC` | windows + linux |
| 崩溃隔离（子进程） | `TestNativeCrashIsIsolated` | amd64 |
| 未支持平台不申请 RX | `execmem_unsupported.go` / `call_unsupported.go` 编译路径 | 由构建矩阵覆盖 |
| 32 函数 1KB 预算反复淘汰 | `TestAutoJITNativeCodeCacheEvictsLRU`（配合 `--jit-code-cache` 调小） | amd64 |

**未覆盖（明确依赖外部环境，不记入矩阵）**：Linux 实机 W^X/maps 检查、长期 GC/抢占 soak、
race 构建。`go test -race` 在本机因 Windows TSan 影子内存分配失败（error code 87）不可执行；
这些门禁在 `docs/jit-follow-up-development-plan.md` 的 R2 中定义，由 `.github/workflows/ci.yml`
的 `jit-linux` job 承担，不能以交叉编译或本机快照代替。

## 7. 缺口与处置（R0-3）

审计发现的唯一缺口：v2.11 宣称“未建模的 Symbol 等值仍 guard 回 Tier 0”，但审计前没有任何权威
测试用真实 `Symbol` 值流过 JIT 函数/trace。处置：新增 `TestJITSymbolValuesGuardBackToTier0`
（函数级 `symStrictLeaf` + trace 级 `symStrictTrace`，覆盖 `===` 与 truthiness，断言三模式结果一致、
guard 失败被记录、Auto 不产生 verify 失败）。审计后矩阵不存在“代码已宣称支持但无测试”的条目。

## 8. 生成式差分与 Tier 0 语义覆盖（R1-1/R1-2/R1-8）

| 能力 | 权威测试 |
|------|----------|
| 固定种子生成式差分框架（`internal/engine/interpreter/jitdiff`） | `TestDifferentialPRSet`（1,000 例，PR 门禁）、`TestDifferentialNightly`（100,000 例 × 5 seed，`JITDIFF_NIGHTLY=1` 每日门禁）、CI `jit-differential-nightly` |
| 生成器可复现（同 seed 同源码） | `TestGeneratorDeterminism` |
| 值域覆盖（Number 边界/Boolean/nullish/String/BigInt/Symbol/对象 identity） | `TestGeneratedCorpusIncludesValueLeaves`（随机语料存在性）、`TestValueDomainOperationCoverage`（逐值结构化覆盖 return/shortCircuit/comparison/guardChange，三 tier 差分） |
| Quick/Native 命中按 Kind 证明 | `TestDifferentialPRSet` 逐一断言 8 类 Quick 与 5 类 Native 预期命中集合，`SuiteSummary.quickHitsByKind/nativeHitsByKind` 归档分布 |
| 事件日志确定性用例（属性写/数组 append/upvalue 写/函数调用/getter/setter/回调抛错/try-catch/safepoint yield deopt 前缀/Symbol identity/BigInt TypeError/宽松相等回退） | `TestEventLogFixedCases`（17 个固定用例，逐断言 off 事件日志 + 三 tier 一致） |
| 失败产物与单命令重放 | `TestArtifactRoundTrip`（原始 mismatch 不被 IR 重跑覆盖）、`TestSaveArtifactRejectsPassingResults`、`TestReplayFailure`（`-artifact` 标志）、`TestRunTierHonorsVerify` |
| verifier 拒绝非法 deopt map（缺失/越界/负 ID/歧义深度） | `TestVerifyRejectsInvalidDeoptMaps`（jit 包） |
| 差分发现并修复：Tier 0 BigInt/NaN 关系比较（panic + `NaN > 3` 语义错误） | `TestBigIntCompare`（NaN/Infinity 扩展）、`TestNaNRelationalComparisons` |
| 差分发现并修复：parser `skipAngleBraces` 把比较 `<` 误当 TS 泛型 | `TestComparisonInsideIfBeforeRelationalExpression`、`TestComparisonWithParenthesizedRightOperand`、`TestGenericFunctionTypeKeepsWorking`、`TestGenericCallResultRuntimeSemantics` |
| TS 泛型调用/声明（简单/多参/嵌套 `>>`/`>>>`/默认值/extends/嵌套函数类型/对象类型/new/调用结果/方法/箭头/interface/class） | `TestGenericCallArguments`（15 形态，断言类型参数被擦除）、`TestGenericTypeDeclarations`（10 形态） |
| JS 关系/移位链不误判为泛型（链式 `<` `>`、`>>`、`>>>`、`>=`、`<=`、括号、三目、短路、for/while 控制流） | `TestComparisonChainsParseAsComparisons`（15 形态，断言比较链 AST）、`TestComparisonInControlFlow`（7 形态） |
| 泛型 vs 比较歧义（`foo < bar > (baz)` 按 TSC 解析为泛型调用；无 `(` 时按比较；`>` 在括号内为比较） | `TestGenericVsComparisonDisambiguation`（6 形态）、`TestGenericAmbiguityRuntimeSemantics`（3 形态，运行时锁定 TSC 兼容语义） |
| 比较链运行时语义（NaN、-0、布尔强转、链式短路、循环条件） | `TestComparisonChainRuntimeSemantics`（25 例，interpreter 包，对照 Node） |
| trace 级 IR dump（失败产物含 trace IR） | `TestArtifactRoundTrip`（断言 quick/auto 层 IR 非空） |
| 异常差分：BigInt 除零 / getter/setter 抛错（含前缀副作用）/ 回调抛错 / OOM / 嵌入方取消 / safepoint 中断 | `TestEventLogFixedCases` 的 `bigIntDivZero`/`getterSetterThrow`/`oom`/`cancel`/`safepoint` 固定用例（jitdiff，断言三 tier 的异常类型、消息、catch 路径和事件前缀一致；延迟中断断言已提交迭代无重复/遗漏） |
| OOM/取消中断注入（RunHook：OOMBytes/TriggerOOM/CancelAfter/CancelErr；差分夹具显式启用 `InterpreterSafepoints`，使解释循环回边与 JIT budget yield 共用回调） | `TestEventLogFixedCases`（oom/cancel/safepoint）、`TestDifferentialPRSet`、`TestDifferentialNightly` |
| BigInt 字面量后 `/` 为除法（非 regex） | `TestLexSlashAfterBigInt`（lexer 包） |
| `++/--` 对 BigInt 保持 BigInt（`i++` 加 1n） | `TestBigIntDivisionByZero`（interpreter 包）；`OpInc/OpDec` 字节码（`opcodes.go`），JIT lowering 展开为 Number 序列（`ir.go`/`trace.go`），BigInt guard 回退 Tier 0 |
| verifier 拒绝非法 deopt map（缺失/越界/负 ID/歧义/预置冲突/栈深过深/预置越界） | `TestVerifyRejectsInvalidDeoptMaps`（jit 包，7 类拒绝 + 1 个合法子用例） |
| deopt 恢复映射完整性（对齐 ResumePC/去重 local 槽/合法栈深/顺序 ID） | `TestDeoptExitMapIntegrity`（jit 包） |
| guard 失败前无部分写入、类型变化后整循环回退 | `TestEventLogFixedCases` 的 propWrite 固定用例 -17（jitdiff） |
| pending exception 正式状态（`DeoptExit.PendingException`，nil=无；原始 JS 值保真） | `TestExceptionExitCompilesAndExecutes`（jit 包：异常值入 PendingException、栈丢弃、IR dump 标注）、`TestSameDeoptExitPendingException`（10 子用例：nil/Number/NaN/字符串/对象 identity） |
| exception exit 编译与执行（trace 内 `OpThrow` → exception exit，不新增 IR opcode） | `TestExceptionExitCompilesAndExecutes`、interpreter 包 `TestDeoptExceptionExitNumericThrow`/`StringThrow`/`ObjectIdentity` |
| Native 拒绝 exception exit（机器码无法表示 Go 指针/engine.Value） | `TestNativeRejectsExceptionExit`（jit 包）、`TestDeoptExceptionExitAutoFallsBackToQuick`（interpreter：Auto 回退 Quick 稳定） |
| verifier 拒绝非法 exception map（截断/扩展 map、异常值缺失/栈下溢） | `TestVerifyRejectsInvalidExceptionMaps`（jit 包，4 子用例） |
| 异常恢复链路（dirty locals 前缀提交、catch 后继续、finally 重抛、嵌套 catch、guard 失败、deopt stats） | interpreter 包 `TestDeoptExceptionExit*`（8 个场景，off/quick/auto 三 tier 一致） |
| jitdiff exception exit 差分与 artifact 保存/重放 | 固定用例 -18（`TestEventLogFixedCases`）、`TestArtifactRoundTripExceptionExit`（jitdiff） |
| R1-5 副作用两阶段提交协议（prepare/validate/commit；validate-all + 原值快照 → store-all → 失败回滚；提交点只有语义 exit 与预算 yield） | jit 包 `TestTraceCommitProtocolAppliesWritesExactlyOnce`（跨 3 个 budget slice 提交恰一次、恢复 PC 精确）、`TestTraceGuardFailureAfterCommittedSliceNoPartialWrite`（已提交 slice 后 guard 失败零写入、locals 停在最后提交点） |
| verifier 拒绝副作用协议违规（`OpSetProp`/`OpGuardNoopCall`/`OpGuardMethodGet` 出现在非 trace 程序；trace guard 索引越界；伪造 deopt map 后走函数返回） | jit 包 `TestVerifyRejectsSideEffectsWithoutTraceProtocol`（6 类拒绝 + 合法对照） |
| Native 属性写回协议（全量 validate 后 store/回滚；store 失败走干净 Yielded；verify 快照恢复原子且不重复 safepoint 回调） | `TestAutoJITNativePropertyWriteTrace`、`TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch`、`TestNativeCommitValidatesAllWritesBeforeMutation`、`TestNativeRestoreValidatesAllPropertiesBeforeMutation`、amd64 `TestNativePropertyWriteVerifyDoesNotDoublePollSafepoint` |
| 属性写提交先于异常（deferred 写先 commit，再把原始值移入 PendingException） | interpreter 包 `TestDeoptPropertyWriteCommitBeforeException`（catch 读到 o.a=2）、jitdiff 固定用例 -22（属性写 + throw + finally） |
| 调用 guard 失败无部分写（noop callee 换成延迟抛错者；guard 在调用前失效；Tier 0 重放后用户调用抛错进同一 catch） | interpreter 包 `TestDeoptCallGuardFailureNoPartialWrite`、jitdiff 固定用例 -19（事件日志含抛错与提交前缀） |
| 数组 append 中断不重复、不漏写（每 chunk 原子；取消后 `A.length === A[A.length-1]+1` 不变量） | interpreter 包 `TestDeoptArrayPushInterruptNoDuplicateNoLoss`、jitdiff 固定用例 -20 |
| upvalue 写原子性（upvalue 与 sum 同 chunk 写回；中断后 `sum === N(N+1)/2` 不变量） | interpreter 包 `TestDeoptUpvalueWriteAtomicOnInterrupt`、jitdiff 固定用例 -21 |
| 方法 guard 不执行用户代码（仅接受普通对象 own data method；accessor/原型/Proxy 明确回退） | engine 包 `TestOwnDataProperty`、interpreter 包 `TestTraceMethodGuardDoesNotProbeProxy`（off/quick/auto trap 次数精确一致） |
| 属性写 + OOM 中断（已提交前缀完整；`count === last + 1` 不变量进入同一 catch） | jitdiff 固定用例 -23（`TestEventLogFixedCases`） |
| R1-5 artifact 保存/重放（call guard 失败 + 属性写用例） | `TestArtifactRoundTripSideEffect`（jitdiff，-19 合成 mismatch 经 SaveArtifact/LoadArtifact/Replay） |
| R1-6 随机 guard 失效：1st/2nd/3rd property shape（两路 PIC 吸收前两个、第三 shape 回退且第一 shape 继续正确） | jitdiff 固定用例 -24（`TestGuardMutationFixedCases`）、interpreter `TestGuardMutationThirdShapeDisablesNativeReleasesRX`（第三 shape 连续失败后 Native 禁用 + RX 回基线） |
| R1-6 类型 mutation：Number→String/BigInt/nullish/object（BigInt 混用抛同一 TypeError） | jitdiff 固定用例 -25 |
| R1-6 callee identity mutation（第二 PIC target、第三 target 禁用 callee 特化） | jitdiff 固定用例 -26、interpreter `TestGuardMutationThirdTargetDisablesNativeReleasesRX` |
| R1-6 trivial method target 替换 | jitdiff 固定用例 -27 |
| R1-6 own method→accessor（getter 只在 Tier 0 每迭代恰好一次，gget 事件计数） | jitdiff 固定用例 -28 |
| R1-6 own method→prototype method（delete 后原型链解析） | jitdiff 固定用例 -29、`TestMethodCallICInvalidatesOnDelete`（Tier 0 回归：CallCached 未查 deleted） |
| R1-6 数组 push 被替换 / receiver 变非数组（push/nopush 事件证明无重复 append、无 JIT 侧调用） | jitdiff 固定用例 -30 |
| R1-6 closure upvalue 类型 / identity 变化（回退 Tier 0 结果一致） | jitdiff 固定用例 -31 |
| R1-6 每类 mutation 实际命中目标 guard（非仅 Tier 0）：TracesCompiled/Compiled ≥ 1 且 GuardFailures ≥ 1 | `TestGuardMutationFixedCases`（jitdiff，对 -24..-31 断言 quick/auto stats） |
| R1-6 随机生成器（warmup/mutation/post 调度内嵌源码，seed/源码/调度随 artifact 重放一致） | `TestGeneratorProducesGuardMutationCases`、`TestArtifactRoundTripGuardMutation`（jitdiff）、`TestGeneratorDeterminism` |
| R1-6 方法 guard 纯数据查找（不触发 accessor/Proxy trap/用户代码；非 plain receiver/原型链回退） | `TestGuardedMethodLookup`（engine 包）+ 基线 `OwnDataProperty`（0a71963）|

## 9. 维护约定

1. 新增任何 JIT 能力（新 opcode、新值类型建模、新 guard/deopt 点、新平台路径）时，必须同步在
   本矩阵对应行补充权威测试引用，否则视为“已宣称但未验证”，R0/R1 验收不通过；
2. 某能力被降级/删除时，先更新矩阵与计划文档，再改代码；
3. 差分测试的新增用例只扩大本矩阵行内的值组合，不改变矩阵结构；
4. 每个开发任务的完成条件里“覆盖矩阵不存在已宣称无测试条目”即指向本文档；
5. 生成式框架新增用例形态（新 `Kind`）时，同时更新 `jitdiff` 的 `KindCount`、`FixedCases` 与
   本文档 §8 对应行；差分发现的 Tier 0 引擎 bug 修复必须附带独立回归测试（不依赖差分框架本身）。
