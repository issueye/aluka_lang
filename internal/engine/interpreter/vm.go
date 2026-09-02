// Package interpreter: vm.go implements the stack-based bytecode VM (Phase 1B).
//
// It reuses the builtins / prototypes / helpers set up by the AST interpreter
// (setupBuiltins, strictEqual, applyBinaryOp, nativeMethod, ...) and adds a
// bytecode execution engine on top. The VM and the AST-walker can coexist so
// 1A tests keep passing while 1B matures.

package interpreter

import (
	"fmt"
	"math"
	"math/big"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// VM is a stack-based bytecode VM that shares builtins with the AST interpreter.
// objLitSite 是对象字面量站点键：同一模板同一 PC 的字面量键序列恒定。
type objLitSite struct {
	tmpl *bytecode.FuncTemplate
	pc   uint32
}

// objLitEntry 是字面量站点缓存项（解析好的 shape 与 pair→slot 索引）。
type objLitEntry struct {
	shape *engine.Shape
	idxs  []int32
}

type VM struct {
	interp *Interpreter // provides builtins, globalObj, prototypes, etc.
	module *bytecode.Module

	stack  []engine.Value // value stack: holds locals + operands
	frames []vmFrame      // call-frame stack

	// ic 是属性访问内联缓存（隐藏类 shape 缓存，1B.5）。
	ic engine.ICache

	// objLitCache 是对象字面量站点缓存：(模板,PC) → 解析好的 shape 与
	// pair→slot 索引。同一字面量站点的键序列恒定，首次执行解析后缓存，
	// 后续创建零哈希/零 transition 行走。
	objLitCache map[objLitSite]*objLitEntry

	// numSlab/numIdx 是 VM 私有数字 slab：JS 执行单线程独占 VM，
	// 数字单元 bump 分配免全局原子（engine.Number 走全局原子 slab）。
	numSlab []engine.NumberBox
	numIdx  int

	// 覆盖率统计（aluka test --coverage 启用；常态零开销）。
	coverEnabled bool
	coverMu      sync.Mutex
	coverLines   map[string]map[int]bool // 源文件 → 已执行行集合

	// callCountEnabled 是监控调用计数的缓存开关（NewVM 时从 engine 读取）。
	// 热路径每次函数调用读取普通布尔字段，避免 engine.BumpCalls 的原子 load。
	callCountEnabled bool

	// insnsEnabled / oomEnabled 是 run() 主循环监控开关缓存：默认（无监控、
	// 未设 --max-memory）热路径零原子 load。内存上限需在 VM 创建前设置
	// （CLI --max-memory 在创建 VM 前解析）。
	insnsEnabled bool
	oomEnabled   bool

	// optimizeBytecode 控制 vm.Compile/CompileAST 是否在编译后运行字节码
	// 优化器（默认 true）。run/缓存路径受益；build 路径按 --bytecode-opt
	// 显式设置；REPL（EvalProgram）不优化。
	optimizeBytecode bool

	jitConfig      jit.Config
	jitStates      map[*bytecode.FuncTemplate]*quickJITState
	jitHotCounts   map[*bytecode.FuncTemplate]jitHotCount
	jitStats       jit.Stats
	jitGeneration  uint64
	jitTraces      map[quickTraceKey]*quickTraceState
	jitNativeBytes uint64
	jitNativeClock uint64
	jitRejections  map[jitRejectionKey]uint64
	jitDeopts      map[jitDeoptKey]uint64
	jitCompileDone chan nativeCompileResult
	jitCompileWG   sync.WaitGroup
	jitPending     int
	// jitCompileSlots is the R5-4 background-compile concurrency semaphore
	// (capacity = effective CompileWorkers). Every queued job's goroutine
	// acquires a slot before compiling, so at most CompileWorkers compiles run
	// concurrently even during a compile storm; the interpreter never blocks
	// on it (slots are only taken by background goroutines).
	jitCompileSlots chan struct{}
	// jitBudgetSpent accumulates measured compile time across every tier
	// (R5-4). It is only mutated on the interpreter thread.
	jitBudgetSpent uint64
	// jitAdaptive holds the R5-3 feedback loop state (boost/cool levels and
	// window counters). Mutated only on the interpreter thread.
	jitAdaptive jitAdaptiveState
	// Package tests use these hooks to pin background-compile lifecycle
	// interleavings. They are nil in production and are not part of the API.
	jitCompileStartHook func()
	jitCloseStartHook   func()
}

// EnableCoverage 启用行级覆盖率统计。
func (v *VM) EnableCoverage() {
	v.coverEnabled = true
	v.coverLines = make(map[string]map[int]bool)
}

// ICStats 返回内联缓存命中统计（O1：--ic-stats 报告）。
func (v *VM) ICStats() engine.ICStats {
	return v.ic.Stats()
}

// CoverageLines 返回覆盖率统计（源文件 → 已执行行集合）。
func (v *VM) CoverageLines() map[string]map[int]bool {
	v.coverMu.Lock()
	defer v.coverMu.Unlock()
	out := make(map[string]map[int]bool, len(v.coverLines))
	for f, lines := range v.coverLines {
		m := make(map[int]bool, len(lines))
		for ln := range lines {
			m[ln] = true
		}
		out[f] = m
	}
	return out
}

type vmFrame struct {
	tmpl               *bytecode.FuncTemplate
	pc                 int
	base               int // index in vm.stack of this frame's slot 0
	upvalues           []*upvalue
	tryStack           []*vmTryHandler
	openUpvalues       []*upvalue // open upvalues pointing into this frame's locals
	jitState           *quickJITState
	jitGeneration      uint64
	jitTrace           *quickTraceState
	jitTracePC         int
	jitTraceGeneration uint64
	// jitTraceFailedPC records the backedge whose trace version failed a guard
	// in this frame (R4-8): after a deopt the same frame must not retry the
	// failed trace at the same backedge, but a different loop (a different
	// backedge PC) in the same frame may still use its own trace. −1 means no
	// failure. It is never cleared within the frame's lifetime.
	jitTraceFailedPC int
	// isCtor 标记构造器帧（asNew 调用）：super() 调用原生父类构造（Error 等）
	// 时返回的全新实例会写回 slot 0；doReturn 对非对象返回以 slot 0 当前值
	// 作为 `new` 结果（此前用调用时的 thisVal 快照，丢失了 super 初始化）。
	isCtor bool
}

// NewVM creates a VM backed by a fresh interpreter's builtins.
func NewVM() (*VM, error) {
	interp, err := NewInterpreter()
	if err != nil {
		return nil, err
	}
	config := defaultJITConfig()
	return &VM{
		// 预分配值栈：fib(30) 峰值约 60 槽、常见程序几十槽，避免启动时
		// 反复扩容（分配 + 锁 + duffcopy，pprof 合计 ~10%）。
		stack:  make([]engine.Value, 0, 64),
		interp: interp, callCountEnabled: engine.MetricsEnabled(),
		insnsEnabled: engine.MetricsEnabled(), oomEnabled: engine.MemoryLimitBytes() != 0,
		optimizeBytecode: true,
		jitConfig:        config,
		// JIT 状态表预分配：冷启动（256 函数各调用一次）时 map 写入避免
		// 反复扩容分配（auto 相对 off 的 allocs 差 ~13/VM 主要来源）。
		jitStates:     make(map[*bytecode.FuncTemplate]*quickJITState, 64),
		jitHotCounts:  make(map[*bytecode.FuncTemplate]jitHotCount, 64),
		jitTraces:     make(map[quickTraceKey]*quickTraceState),
		jitRejections: make(map[jitRejectionKey]uint64), jitGeneration: 1,
		jitDeopts:       make(map[jitDeoptKey]uint64),
		jitCompileDone:  make(chan nativeCompileResult, 16),
		jitCompileSlots: make(chan struct{}, 1),
	}, nil
}

// bumpCall 计数一次函数调用。监控关闭（默认）时是普通布尔判断，零原子开销；
// 监控开启需在 VM 创建前启动（CLI --monitor 在创建 VM 前解析）。
func (v *VM) bumpCall() {
	if v.callCountEnabled {
		engine.BumpCalls()
	}
}

// Interp returns the backing interpreter (for RegisterFunc / Global access).
func (v *VM) Interp() *Interpreter { return v.interp }

// Global returns the global object (implements engine.Context).
func (v *VM) Global() engine.Object { return v.interp.Global() }

// ObjectPrototype returns %Object.prototype% (implements engine.Context).
func (v *VM) ObjectPrototype() engine.Object { return v.interp.objectProto }

// RegisterFunc registers a Go function as a global (implements engine.Context).
func (v *VM) RegisterFunc(name string, fn engine.Func) error {
	return v.interp.RegisterFunc(name, fn)
}

// PostTask 投递任务到 JS 执行线程（implements engine.Context）。
func (v *VM) PostTask(fn func()) {
	v.interp.PostTask(fn)
}

// AddRef 跟踪活跃句柄（implements engine.Context）。
func (v *VM) AddRef() func() {
	return v.interp.NewTaskHandle()
}

// RunLoop 启动事件循环（处理定时器/http 回调，直到无 pending 任务）。
func (v *VM) RunLoop() {
	v.interp.RunLoop()
}

// Stop 停止事件循环。
func (v *VM) Stop() {
	v.interp.Stop()
}

// Close releases context resources (implements engine.Context).
func (v *VM) Close() error {
	v.closeJIT()
	return nil
}

// === Stack helpers ========================================================

// push 把 val 压入操作数栈。实现见 stack_push*.go（按构建标签分文件）：
//
//   - 生产构建：裸 append——帧入口的 ensureFrameStack 已按 tmpl.MaxStack
//     预留足够槽位，帧内 push 永不扩容，从而无分支、可内联进 run 分派循环。
//   - vmstackcheck 构建：保留越界断言，用于校验 MaxStack 的 soundness。
//
// run 主循环之外的调用方（throw/resume 路径）用 pushSafe（自带容量检查）。

// === Main loop ============================================================

func (v *VM) run() (engine.Value, error) {
	// 覆盖统计开关局部化（O1-C5）：coverage 在模块加载前启用、运行期间
	// 不变——提为局部布尔，主循环零字段访问（常态分支预测为不跳转）。
	coverEnabled := v.coverEnabled
	for {
		frame := v.cur()
		tmpl := frame.tmpl
		code := tmpl.Code

		// Each iteration: decode + dispatch.
		for {
			// Re-fetch frame pointer: call/invoke operations may reallocate
			// v.frames (via append), invalidating the previously cached pointer.
			frame = v.cur()
			pc := frame.pc
			if pc >= len(code) {
				// Ran off the end without OpReturn — treat as undefined return.
				return v.doReturn(engine.Undefined()), nil
			}
			// 指令解码：op = 高 8 位，operand = 低 24 位。手动解包（编译器可
			// 合并边界检查与字节读；实测 binary.BigEndian.Uint32 在 x86 上
			// 需 BSWAP，反而不如移位解包）。
			op := bytecode.Opcode(code[pc])
			operand := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
			frame.pc = pc + bytecode.InstrSize

			// 覆盖率统计（仅启用时）：记录 (源文件, 行) 执行。
			if coverEnabled && tmpl.SourceFile != "" {
				if line := lineForPC(tmpl, pc); line > 0 {
					v.coverMu.Lock()
					m := v.coverLines[tmpl.SourceFile]
					if m == nil {
						m = make(map[int]bool)
						v.coverLines[tmpl.SourceFile] = m
					}
					m[line] = true
					v.coverMu.Unlock()
				}
			}

			// 监控：指令计数（gated）+ OOM 安全点（--max-memory 超限抛
			// 可捕获的 JS RangeError，V8 同款 "JavaScript heap out of memory"）。
			// 开关缓存到 VM 字段（NewVM 时读取），默认热路径零原子 load。
			if v.insnsEnabled {
				engine.BumpInsns()
			}
			if v.oomEnabled && engine.OOMTriggered() {
				// 一次性消费：抛 RangeError 前清除，使 catch 可运行。
				engine.ConsumeOOM()
				return v.handleThrow(v.interp.goErrorToJSValue(engine.OOMError()))
			}

			switch op {
			// --- Literals & stack ---
			case bytecode.OpNop:
			case bytecode.OpPushUndefined:
				v.push(engine.Undefined())
			case bytecode.OpPushNull:
				v.push(engine.Null())
			case bytecode.OpPushTrue:
				v.push(engine.Boolean(true))
			case bytecode.OpPushFalse:
				v.push(engine.Boolean(false))
			case bytecode.OpPushConst:
				v.push(tmpl.Constants[operand])
			case bytecode.OpPushInt:
				v.push(v.num(float64(operand)))
			case bytecode.OpPushNegInt:
				v.push(v.num(float64(-int(operand))))
			case bytecode.OpPop:
				v.pop()
			case bytecode.OpDup:
				v.push(v.peek())
			case bytecode.OpSwap:
				n := len(v.stack) - 1
				v.stack[n], v.stack[n-1] = v.stack[n-1], v.stack[n]

			// --- Variables ---
			case bytecode.OpLoadLocal:
				v.push(*v.local(int(operand)))
			case bytecode.OpStoreLocal:
				*v.local(int(operand)) = v.pop()
			case bytecode.OpLoadGlobal:
				name := tmpl.Constants[operand].String()
				val, _ := v.interp.globalObj.Get(name)
				v.push(val)
			case bytecode.OpStoreGlobal:
				name := tmpl.Constants[operand].String()
				_ = v.interp.globalObj.Set(name, v.pop())

			// --- Upvalues ---
			case bytecode.OpLoadUpvalue:
				uv := v.cur().upvalues[operand]
				if uv.slot != nil {
					v.push(*uv.slot)
				} else {
					v.push(uv.closed)
				}
			case bytecode.OpStoreUpvalue:
				uv := v.cur().upvalues[operand]
				val := v.pop()
				if uv.slot != nil {
					*uv.slot = val
				} else {
					uv.closed = val
				}
			case bytecode.OpMakeClosure:
				fnIdx := int(operand)
				closureTmpl := v.module.Functions[fnIdx]
				closure := newVMClosure(v, closureTmpl, v.captureUpvalues(closureTmpl))
				engine.SetProto(closure.obj, v.interp.functionProto)
				_ = closure.obj.Set("name", engine.Str(closureTmpl.Name))
				_ = closure.obj.Set("length", engine.IntValue(closureTmpl.NumParams))
				// Create prototype object so `new Fn()` sets the instance's
				// [[Prototype]] to Fn.prototype (needed for instanceof).
				proto := engine.NewObject()
				engine.SetProto(proto, v.interp.objectProto)
				_ = proto.Set("constructor", closure)
				_ = closure.obj.Set("prototype", proto)
				v.push(closure)

				// --- Binary arithmetic & bitwise ---
			case bytecode.OpInc, bytecode.OpDec:
				// ++ / -- use ToNumeric: BigInt stays BigInt and all other
				// supported primitives use ToNumber.
				value := v.pop()
				delta := int64(1)
				if op == bytecode.OpDec {
					delta = -1
				}
				updated, err := updateNumeric(value, delta)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(updated)
			case bytecode.OpEnumKeys:
				// for-in 头部键枚举：原型链可枚举键（EnumerateObjectProperties）。
				src := v.pop()
				keys := engine.EnumerateForInKeys(src)
				vals := make([]engine.Value, len(keys))
				for i, k := range keys {
					vals[i] = engine.Str(k)
				}
				v.push(engine.NewArray(vals))
			case bytecode.OpAdd:
				r := v.pop()
				l := v.pop()
				// 数字快路径：双 Number 直接 Float 运算（fib 等数值密集
				// 代码跳过 isBigInt/jsToNumber 分派）。
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(v.num(lf + rf))
				} else if isBigInt(l) || isBigInt(r) {
					// BigInt 与非 BigInt 的加法必须显式转换；保持运行时
					// 对混合 String/BigInt 的 TypeError 约定，避免静默拼接。
					result, err := bigintArith2(l, r, '+')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
					v.push(v.binAdd(l, r))
				} else {
					v.push(v.binAdd(l, r))
				}
			case bytecode.OpSub:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(v.num(lf - rf))
				} else if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '-')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					v.push(v.num(jsToNumber(l) - jsToNumber(r)))
				}
			case bytecode.OpMul:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(v.num(lf * rf))
				} else if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '*')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					v.push(v.num(jsToNumber(l) * jsToNumber(r)))
				}
			case bytecode.OpDiv:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(v.num(lf / rf))
				} else if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '/')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					v.push(v.num(jsToNumber(l) / jsToNumber(r)))
				}
			case bytecode.OpMod:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					// 整数快路径：安全整数范围内用 int64 取模（硬件 fmod 约慢一个量级；
					// 整数操作数的 fmod 与整型 % 结果完全一致）
					modFast := false
					if li, ok1 := fastInt64(lf); ok1 {
						if ri, ok2 := fastInt64(rf); ok2 && ri != 0 {
							m := li % ri
							// 余数为零时 fmod 结果符号随被除数（fmod(-1,1) = -0）
							if m == 0 && li < 0 {
								v.push(v.num(math.Copysign(0, -1)))
							} else {
								v.push(v.num(float64(m)))
							}
							modFast = true
						}
					}
					if !modFast {
						v.push(v.num(math.Mod(lf, rf)))
					}
				} else if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '%')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					if rn == 0 {
						v.push(v.num(math.NaN()))
					} else {
						v.push(v.num(math.Mod(ln, rn)))
					}
				}
			case bytecode.OpPow:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(v.num(math.Pow(lf, rf)))
				} else if isBigInt(l) || isBigInt(r) {
					result, err := bigintPow(l, r)
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					v.push(v.num(math.Pow(jsToNumber(l), jsToNumber(r))))
				}
			case bytecode.OpBitAnd:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "&")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					v.push(v.num(float64(jsToInt32(ln) & jsToInt32(rn))))
				}
			case bytecode.OpBitOr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "|")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					v.push(v.num(float64(jsToInt32(ln) | jsToInt32(rn))))
				}
			case bytecode.OpBitXor:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "^")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					v.push(v.num(float64(jsToInt32(ln) ^ jsToInt32(rn))))
				}
			case bytecode.OpShl:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "<<")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					v.push(v.num(float64(jsToInt32(ln) << (jsToUint32(rn) & 31))))
				}
			case bytecode.OpShr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, ">>")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln := jsToNumber(l)
					rn := jsToNumber(r)
					v.push(v.num(float64(jsToInt32(ln) >> (jsToUint32(rn) & 31))))
				}
			case bytecode.OpUShr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					// BigInt 不支持 >>> （无符号右移），抛 TypeError。
					return v.handleThrow(fmt.Errorf("%w: BigInts have no unsigned right shift, use >> instead", engine.ErrTypeError))
				}
				ln, _ := l.Float()
				rn, _ := r.Float()
				v.push(v.num(float64(jsToUint32(ln) >> (jsToUint32(rn) & 31))))

			// --- Unary ---
			case bytecode.OpNeg:
				val := v.pop()
				if isBigInt(val) {
					v.push(bigintNeg(val))
				} else {
					n, _ := val.Float()
					v.push(v.num(-n))
				}
			case bytecode.OpNot:
				b, _ := v.pop().Bool()
				v.push(engine.Boolean(!b))
			case bytecode.OpBitNot:
				value := v.pop()
				if bi, ok := engine.BigIntValue(value); ok {
					// `~x` on BigInt is the two's-complement bitwise NOT
					// (-x-1); the generic ToInt32 path would silently turn
					// BigInts into Numbers.
					v.push(engine.BigInt(new(big.Int).Not(bi)))
					break
				}
				n, _ := value.Float()
				v.push(v.num(float64(^jsToInt32(n))))
			case bytecode.OpTypeof:
				v.push(engine.Str(v.pop().Type().String()))
			case bytecode.OpTypeofGlobal:
				name := tmpl.Constants[operand].String()
				val, _ := v.interp.globalObj.Get(name)
				v.push(engine.Str(val.Type().String()))
			case bytecode.OpUnaryPlus:
				val := v.pop()
				if val.Type() == engine.TypeNumber {
					v.push(val)
				} else {
					v.push(v.num(jsToNumber(val)))
				}

			// --- Comparisons ---
			case bytecode.OpEq:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(looseEquals(l, r)))
			case bytecode.OpNe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(!looseEquals(l, r)))
			case bytecode.OpStrictEq:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(strictEqual(l, r)))
			case bytecode.OpStrictNe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(!strictEqual(l, r)))
			case bytecode.OpLt:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(engine.Boolean(!math.IsNaN(lf) && !math.IsNaN(rf) && lf < rf))
				} else {
					v.push(engine.Boolean(compareBool(l, r, func(c int) bool { return c < 0 })))
				}
			case bytecode.OpLe:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(engine.Boolean(!math.IsNaN(lf) && !math.IsNaN(rf) && lf <= rf))
				} else {
					v.push(engine.Boolean(compareBool(l, r, func(c int) bool { return c <= 0 })))
				}
			case bytecode.OpGt:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(engine.Boolean(!math.IsNaN(lf) && !math.IsNaN(rf) && lf > rf))
				} else {
					v.push(engine.Boolean(compareBool(l, r, func(c int) bool { return c > 0 })))
				}
			case bytecode.OpGe:
				r := v.pop()
				l := v.pop()
				if l.Type() == engine.TypeNumber && r.Type() == engine.TypeNumber {
					lf, _ := l.Float()
					rf, _ := r.Float()
					v.push(engine.Boolean(!math.IsNaN(lf) && !math.IsNaN(rf) && lf >= rf))
				} else {
					v.push(engine.Boolean(compareBool(l, r, func(c int) bool { return c >= 0 })))
				}

			// --- Control flow ---
			case bytecode.OpJmp:
				target := pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				if target < pc {
					if v.jitConfig.Mode != jit.Off {
						if result, ok, err := v.tryQuickFrame(frame); err != nil {
							return v.handleThrow(v.interp.goErrorToJSValue(err))
						} else if ok {
							return v.doReturn(result), nil
						}
						if exitPC, ok, err := v.tryQuickTrace(frame, target, pc); err != nil {
							if jt, isJS := err.(*jsThrow); isJS {
								// Exception exit: the pending exception is the
								// original JS thrown value; feed it straight
								// into the handler machinery (catch/finally).
								return v.handleThrow(jt.val)
							}
							return v.handleThrow(v.interp.goErrorToJSValue(err))
						} else if ok {
							frame.pc = exitPC
							break
						}
					}
					// Embedders need the same cancellation boundary when a loop is
					// still interpreted (including --jit=off). Compiled loops poll
					// inside their budgeted executor and return through the branch
					// above, so this does not double-poll a completed JIT slice.
					if v.jitConfig.InterpreterSafepoints && v.jitConfig.Safepoint != nil {
						if err := v.pollJITSafepoint(); err != nil {
							return v.handleThrow(v.interp.goErrorToJSValue(err))
						}
					}
				}
				frame.pc = target
			case bytecode.OpJmpTruePop:
				val := v.pop()
				if b, _ := val.Bool(); b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpJmpFalsePop:
				val := v.pop()
				if b, _ := val.Bool(); !b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpJmpTrueKeep:
				// Keep value if true (jump past right operand); pop if false.
				if b, _ := v.peek().Bool(); b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				} else {
					v.pop()
				}
			case bytecode.OpJmpFalseKeep:
				// Keep value if false (jump past right operand); pop if true.
				if b, _ := v.peek().Bool(); !b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				} else {
					v.pop()
				}
			case bytecode.OpJmpNullishKeep:
				// For `a ?? b`: if a is nullish, pop a and fall through to b.
				// If a is non-nullish, keep a and jump past b.
				val := v.peek()
				if val.IsUndefined() || val.IsNull() {
					v.pop()
				} else {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpOptionalJump:
				// Optional chaining short-circuit (?.): if top is null/undefined,
				// pop it, push undefined, and jump to the chain end. Otherwise,
				// keep the value on stack and fall through. 链内残留由编译器在
				// 链尾生成的清理块（POP × 残留数）弹出。
				val := v.peek()
				if val.IsUndefined() || val.IsNull() {
					v.pop()
					v.push(engine.Undefined())
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}

			// --- Functions ---
			case bytecode.OpCall:
				numArgs := int(operand)
				result, err := v.doCall(numArgs, engine.Undefined())
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallWithThis:
				// Stack: ... callee this arg0 ... argN-1
				numArgs := int(operand)
				argStart := len(v.stack) - numArgs
				thisVal := v.stack[argStart-1]
				callee := v.stack[argStart-2]
				args := make([]engine.Value, numArgs)
				copy(args, v.stack[argStart:argStart+numArgs])
				v.stack = v.stack[:argStart-2]
				result, err := v.invoke(callee, thisVal, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallWithThisArgs:
				// Stack: ... callee this argsArray
				argsArr := v.pop()
				thisVal := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, thisVal, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallMethod:
				numArgs := int(operand >> 16)
				nameIdx := int(operand & 0xFFFF)
				result, err := v.doCallMethod(numArgs, nameIdx, pc)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpNew:
				numArgs := int(operand)
				result, err := v.doNew(numArgs)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpReturn:
				retVal := v.pop()
				// return 穿出 try/finally 区域时必须先运行 finally。
				// 快速路径：无活跃 try handler（绝大多数函数）直接返回，
				// 避免每次 return 都进入 exitTry 展开 walk。
				if len(v.cur().tryStack) == 0 {
					return v.doReturn(retVal), nil
				}
				if v.exitTry(&vmCompletion{kind: compReturn, value: retVal}) != exitContinue {
					return v.doReturn(retVal), nil
				}
			case bytecode.OpReturnUndef:
				if len(v.cur().tryStack) == 0 {
					return v.doReturn(engine.Undefined()), nil
				}
				if v.exitTry(&vmCompletion{kind: compReturn, value: engine.Undefined()}) != exitContinue {
					return v.doReturn(engine.Undefined()), nil
				}

			// --- Objects & arrays ---
			case bytecode.OpNewObject:
				propCount := int(operand)
				var obj engine.Object
				if propCount == 0 {
					obj = engine.NewObjectWithProto(v.interp.objectProto)
				} else {
					pairCount := propCount * 2
					start := len(v.stack) - pairCount
					pairs := v.stack[start:]
					// 字面量站点缓存：命中则免哈希直接构建（热路径）；
					// 未命中（首次执行）解析并缓存
					site := objLitSite{tmpl: tmpl, pc: uint32(pc)}
					if e := v.objLitCache[site]; e != nil {
						obj = engine.NewObjectFromShapeWithProto(e.shape, e.idxs, pairs, v.interp.objectProto)
					} else {
						shape, idxs := engine.ResolveLiteralShape(pairs)
						if v.objLitCache == nil {
							v.objLitCache = make(map[objLitSite]*objLitEntry, 8)
						}
						v.objLitCache[site] = &objLitEntry{shape: shape, idxs: idxs}
						obj = engine.NewObjectFromShapeWithProto(shape, idxs, pairs, v.interp.objectProto)
					}
					v.stack = v.stack[:start]
				}
				v.push(obj)
			case bytecode.OpNewArray:
				n := int(operand)
				start := len(v.stack) - n
				arr := engine.NewArray(v.stack[start:])
				v.stack = v.stack[:start]
				engine.SetProto(arr, v.interp.arrayProto)
				v.push(arr)
			case bytecode.OpMakeRegexp:
				// 正则字面量：弹 flags + pattern，构造 RegExp 实例。
				flagsVal := v.pop()
				patVal := v.pop()
				rv, err := v.interp.makeRegexp(patVal.String(), flagsVal.String())
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(rv)
			case bytecode.OpGetProp:
				name := tmpl.Constants[operand].String()
				obj := v.pop()
				val, err := v.getProperty(obj, name)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpGetPropLocal:
				// O2-D1 superinstruction：LoadLocal slot + GetProp name
				// （operand 高 16 位 slot，低 16 位 name-const 索引）。
				slot := int(operand >> 16)
				nameIdx := int(operand & 0xFFFF)
				name := tmpl.Constants[nameIdx].String()
				obj := v.local(slot)
				val, err := v.getProperty(*obj, name)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetProp:
				name := tmpl.Constants[operand].String()
				val := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetPropObj:
				name := tmpl.Constants[operand].String()
				val := v.pop()
				obj := v.peek()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpSetPropTop:
				// Stack: [..., val, obj] — assignTo pushes val first, then obj.
				name := tmpl.Constants[operand].String()
				obj := v.pop()
				val := v.pop()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpSetGetterObj:
				// 对象字面量 getter：栈 [obj, fn]，注册为 accessor getter。
				fn := v.pop()
				obj := v.peek()
				name := tmpl.Constants[operand].String()
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, true, fn)
				}
			case bytecode.OpSetSetterObj:
				// 对象字面量 setter：栈 [obj, fn]，注册为 accessor setter。
				fn := v.pop()
				obj := v.peek()
				name := tmpl.Constants[operand].String()
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, false, fn)
				}
			case bytecode.OpSetGetterComputedObj:
				// 对象字面量计算 getter：栈 [obj, key, fn]，注册为 accessor getter。
				fn := v.pop()
				key := v.pop()
				obj := v.peek()
				name := propertyKeyOf(key)
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, true, fn)
				}
			case bytecode.OpSetSetterComputedObj:
				// 对象字面量计算 setter：栈 [obj, key, fn]，注册为 accessor setter。
				fn := v.pop()
				key := v.pop()
				obj := v.peek()
				name := propertyKeyOf(key)
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, false, fn)
				}
			case bytecode.OpGetElem:
				key := v.pop()
				obj := v.pop()
				// 数组数值下标读快路径（M1-2）：number key + ArrayValue 时
				// 直读元素，绕过 propertyKeyOf 的 number→string 与
				// getProperty 数组分支的 strconv.Atoi 双重转换。
				if val, ok := v.tryArrayIndexGet(obj, key); ok {
					v.push(val)
					break
				}
				val, err := v.getProperty(obj, propertyKeyOf(key))
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElem:
				val := v.pop()
				key := v.pop()
				obj := v.pop()
				// 数组数值下标写快路径（M1-2 写侧）：number key + ArrayValue
				// 直写元素，绕过 propertyKeyOf 的 number→string 与 ArrayValue.Set
				// 的 strconv.Atoi 双重转换。
				if v.tryArrayIndexSet(obj, key, val) {
					v.push(val)
					break
				}
				if err := v.setProperty(obj, propertyKeyOf(key), val); err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElemTop:
				// Stack: [..., val, obj, key] — assignTo pushes val, then obj, then key.
				key := v.pop()
				obj := v.pop()
				val := v.pop()
				if v.tryArrayIndexSet(obj, key, val) {
					break
				}
				if err := v.setProperty(obj, propertyKeyOf(key), val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpDelProp:
				// A: name-const index; pop obj, delete own prop, push bool result.
				nameIdx := int(operand)
				name := tmpl.Constants[nameIdx].String()
				obj := v.pop()
				// Proxy interception: dispatch to the deleteProperty trap if defined.
				if p, ok := obj.(*ProxyValue); ok {
					ok, err := p.proxyDelete(name)
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(engine.Boolean(ok))
					break
				}
				result := true
				if p, ok := obj.(*ProxyValue); ok {
					deleted, err := p.proxyDelete(name)
					if err != nil {
						return v.handleThrow(err)
					}
					result = deleted
				} else if o, ok := obj.AsObject(); ok {
					result = o.Delete(name)
				}
				v.push(engine.Boolean(result))
			case bytecode.OpDelElem:
				key := propertyKeyOf(v.pop())
				obj := v.pop()
				result := true
				if p, ok := obj.(*ProxyValue); ok {
					deleted, err := p.proxyDelete(key)
					if err != nil {
						return v.handleThrow(err)
					}
					result = deleted
				} else if o, ok := obj.AsObject(); ok {
					result = o.Delete(key)
				}
				v.push(engine.Boolean(result))
			case bytecode.OpSetPropComputedObj:
				// Stack: [..., obj, key, value] → set obj[key] = value, obj stays.
				val := v.pop()
				key := v.pop()
				keyStr := propertyKeyOf(key)
				obj := v.peek()
				if err := v.setProperty(obj, keyStr, val); err != nil {
					return v.handleThrow(err)
				}

			// --- Spread (ES2015) ---
			case bytecode.OpBuildArray:
				arr := engine.NewArray(nil)
				engine.SetProto(arr, v.interp.arrayProto)
				v.push(arr)
			case bytecode.OpArrayPush:
				val := v.pop()
				arrVal := v.peek()
				arr, ok := arrVal.(*engine.ArrayValue)
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: ARRAY_PUSH target not an array", engine.ErrTypeError))
				}
				arr.Append(val)
			case bytecode.OpArraySpread:
				spreadVal := v.pop()
				arrVal := v.peek()
				arr, ok := arrVal.(*engine.ArrayValue)
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: ARRAY_SPREAD target not an array", engine.ErrTypeError))
				}
				if sa, ok := spreadVal.(*engine.ArrayValue); ok {
					for _, el := range sa.Elems() {
						arr.Append(el)
					}
				} else {
					// Use the iterator protocol for all other iterables
					// (generators, strings, objects with [Symbol.iterator], etc.).
					iter, err := v.getIterator(spreadVal)
					if err != nil {
						return v.handleThrow(err)
					}
					for {
						nextFn, err := v.getProperty(iter, "next")
						if err != nil {
							return v.handleThrow(err)
						}
						result, err := v.invoke(nextFn, iter, nil, false)
						if err != nil {
							return v.handleThrow(err)
						}
						doneVal, err := v.getProperty(result, "done")
						if err != nil {
							return v.handleThrow(err)
						}
						done, _ := doneVal.Bool()
						if done {
							break
						}
						valueVal, err := v.getProperty(result, "value")
						if err != nil {
							return v.handleThrow(err)
						}
						arr.Append(valueVal)
					}
				}
			case bytecode.OpSpreadObject:
				src := v.pop()
				dst := v.peek()
				dstObj, ok := dst.AsObject()
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: SPREAD_OBJECT target not an object", engine.ErrTypeError))
				}
				// Proxy interception: use ownKeys + get traps.
				if p, ok := src.(*ProxyValue); ok {
					keys, err := p.proxyOwnKeys()
					if err != nil {
						return v.handleThrow(err)
					}
					for _, k := range keys {
						pv, err := p.proxyGet(k)
						if err != nil {
							return v.handleThrow(err)
						}
						_ = dstObj.Set(k, pv)
					}
					break
				}
				if srcObj, ok := src.AsObject(); ok {
					for _, k := range srcObj.Keys() {
						// 用 getProperty 读取：getter 访问器会被调用（spread 语义
						// 复制 getter 的结果值，而非 AccessorValue 本身）。
						if pv, err := v.getProperty(src, k); err == nil {
							_ = dstObj.Set(k, pv)
						}
					}
				}
			case bytecode.OpCallArgs:
				// Stack: ... callee argsArray
				argsArr := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, engine.Undefined(), args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallMethodArgs:
				// Stack: ... receiver argsArray
				nameIdx := int(operand)
				argsArr := v.pop()
				receiver := v.pop()
				args := v.toArrayValues(argsArr)
				name := tmpl.Constants[nameIdx].String()
				fn, err := v.getProperty(receiver, name)
				if err != nil {
					return v.handleThrow(err)
				}
				result, err := v.invoke(fn, receiver, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpNewArgs:
				// Stack: ... callee argsArray
				argsArr := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, engine.Undefined(), args, true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)

			// --- Try / catch ---
			case bytecode.OpTryEnter:
				tryIdx := int(operand)
				entry := &tmpl.TryTable[tryIdx]
				v.cur().tryStack = append(v.cur().tryStack, &vmTryHandler{
					entry: entry,
					exc:   nil,
					phase: 0,
				})
			case bytecode.OpTryExit:
				tryIdx := int(operand)
				v.handleTryExit(tryIdx)
			case bytecode.OpTryExitFinally:
				tryIdx := int(operand)
				act, val := v.handleTryExitFinally(tryIdx)
				switch act {
				case exitRethrow:
					return v.handleThrow(val)
				case exitReturn:
					return v.doReturn(val), nil
				}
			case bytecode.OpTryExitJmp:
				// break/continue 位于 try 区域内：跳转穿出区域前先运行 finally。
				target := pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				v.exitTry(&vmCompletion{kind: compJump, pc: target})
			case bytecode.OpThrow:
				exc := v.pop()
				return v.handleThrow(exc)
			case bytecode.OpInstanceof:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(v.instanceof(l, r)))
			case bytecode.OpIn:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(v.inOp(l, r)))

			// --- Class (ES2015) ---
			case bytecode.OpMakeClass:
				classIdx := int(operand)
				result, err := v.doMakeClass(classIdx)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpGetProto:
				obj := v.pop()
				proto := v.getProto(obj)
				if proto != nil {
					v.push(proto)
				} else {
					v.push(engine.Null())
				}
			case bytecode.OpCallThis:
				numArgs := int(operand)
				result, err := v.doCallThis(numArgs, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpConstructThis:
				numArgs := int(operand)
				result, err := v.doCallThis(numArgs, true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallThisArgs:
				result, err := v.doCallThisArgs(false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpConstructThisArgs:
				result, err := v.doCallThisArgs(true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)

			// --- Iterator protocol (ES2015) ---
			case bytecode.OpGetIterator:
				iterable := v.pop()
				iter, err := v.getIterator(iterable)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(iter)
			case bytecode.OpGetAsyncIterator:
				// ES2018 异步迭代协议：优先 [Symbol.asyncIterator]()，
				// 回退到 [Symbol.iterator]()（其 next 结果由后续 OpAwait 解包）。
				iterable := v.pop()
				iter, err := v.getAsyncIterator(iterable)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(iter)
			case bytecode.OpYield:
				// Generator yield: pop yielded value, suspend frame, return to caller.
				yieldVal := v.pop()
				return v.doYield(yieldVal)
			case bytecode.OpAwait:
				// Async await: pop awaited value, suspend frame. The asyncRunner
				// catches the awaitSignal and schedules resumption when the
				// value's Promise settles.
				awaitedVal := v.pop()
				return v.doAwait(awaitedVal)
			case bytecode.OpCloseUpvalues:
				v.closeUpvalues(v.cur().base + int(operand))

			default:
				return engine.Undefined(), fmt.Errorf("aluka: unknown opcode %s (%d)", op, op)
			}
		}
	}
}

// 其余 VM 实现按职责分文件：
//   - vm_stack.go     栈/帧原语（ensureStack、local、num）
//   - vm_module.go    Eval/Compile/RunModule 与字节码优化开关
//   - vm_call.go      调用协议（doCall/doNew/callClosure/invoke）
//   - vm_class.go     class 装配
//   - vm_iterator.go  同步/异步迭代协议
//   - vm_closure.go   vmClosure 值与 upvalue 捕获/关闭
//   - vm_property.go  属性读写、原型链、in/instanceof
//   - vm_exception.go throw 传播与 try/finally 出口
//   - vm_async.go     微任务/nextTick 与 await
