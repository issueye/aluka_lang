package bytecode

// 本文件是指令集的集中式元数据表（单一事实来源）：
//
//   - 指令名称（String/HasOperand 由此派生，取代手写 opNames/HasOperand）；
//   - 操作数语义（OperandKind，供 validateFunc 按类校验与文档审计）；
//   - 栈效果（Pops/Pushes，供优化器/未来分析 pass 使用）；
//   - 分类标记（PurePush/IsJump/IsTerminal）。
//
// 新增指令时必须在此表登记一条元数据（并同步 bump FormatVersion），
// 否则 meta_test 的一致性断言与 validateFunc 校验会失败。
//
// 操作数语义以 internal/engine/interpreter/vm.go 的实现为准：
//
//   - OperandConstIdx：operand 为函数常量池索引（tmpl.Constants[operand]）。
//     属性名类指令（GET_PROP/SET_PROP/...）与全局名指令（LOAD_GLOBAL/...）
//     共用该 kind——都是 name 常量索引。
//   - OperandInt：operand 直接编码 24 位无符号整数（PUSH_INT/PUSH_NEG_INT）。
//   - OperandSlot：operand 为当前帧局部槽索引（含 CloseUpvalues 的首槽）。
//   - OperandUpvalueIdx：operand 为 upvalue 捕获索引。
//   - OperandTemplateIdx：operand 为模块级函数/类模板索引。
//   - OperandTryIdx：operand 为 try 表索引。
//   - OperandSignedOff：operand 为有符号相对跳转偏移（相对下一条指令）。
//   - OperandCount：operand 为参数/元素数量（调用与构造类指令）。
//   - OperandPackedSlotName：operand 打包 slot<<16|nameIdx（GET_PROP_LOCAL）。
//   - OperandPackedCall：operand 打包 numArgs<<16|nameIdx（CALL_METHOD）。
//
// 栈效果约定：
//
//   - Pops/Pushes 描述单条指令在操作数栈上的净弹出/压入数量；
//   - StackCond：效果依赖运行时条件或跳转是否发生（如 JMP_TRUE_KEEP
//     跳转时保持、不跳转时弹出），优化器不得基于其 Pops/Pushes 推断；
//   - VarStack：效果由 operand 或运行时决定（调用/构造/数组构造类），
//     优化器不得基于其 Pops/Pushes 推断。

// OperandKind 描述指令 3 字节操作数域的语义类别。
type OperandKind uint8

const (
	OperandNone OperandKind = iota
	OperandConstIdx
	OperandInt
	OperandSlot
	OperandUpvalueIdx
	OperandTemplateIdx
	OperandTryIdx
	OperandSignedOff
	OperandCount
	OperandPackedSlotName
	OperandPackedCall
)

// OpMeta 是单条指令的结构化元数据。
type OpMeta struct {
	Name    string
	Operand OperandKind
	Pops    uint8
	Pushes  uint8
	// PurePush：纯字面量压栈（无副作用，可安全删除 push+pop 对）。
	PurePush bool
	// IsJump：带相对偏移操作数的跳转类指令（目标重定位/不可达分析用）。
	IsJump bool
	// IsTerminal：return/throw 类，其后顺序指令不可达。
	IsTerminal bool
	// StackCond：栈效果依赖运行时条件/跳转（见文件头约定）。
	StackCond bool
	// VarStack：栈效果由 operand 或运行时决定（见文件头约定）。
	VarStack bool
}

// meta 表以指针形式组织：未定义条目为 nil（非法/未来指令），
// 与 opNames 时代「空名 = 非法」的语义一致。
var opMeta = [256]*OpMeta{
	// --- Literals & stack ---
	OpNop:           {Name: "NOP"},
	OpPushUndefined: {Name: "PUSH_UNDEFINED", Pushes: 1, PurePush: true},
	OpPushNull:      {Name: "PUSH_NULL", Pushes: 1, PurePush: true},
	OpPushTrue:      {Name: "PUSH_TRUE", Pushes: 1, PurePush: true},
	OpPushFalse:     {Name: "PUSH_FALSE", Pushes: 1, PurePush: true},
	OpPushConst:     {Name: "PUSH_CONST", Operand: OperandConstIdx, Pushes: 1, PurePush: true},
	OpPushInt:       {Name: "PUSH_INT", Operand: OperandInt, Pushes: 1, PurePush: true},
	OpPushNegInt:    {Name: "PUSH_NEG_INT", Operand: OperandInt, Pushes: 1, PurePush: true},
	OpPop:           {Name: "POP", Pops: 1},
	OpDup:           {Name: "DUP", Pushes: 1},
	OpSwap:          {Name: "SWAP", Pops: 2, Pushes: 2},

	// --- Variables ---
	OpLoadLocal:   {Name: "LOAD_LOCAL", Operand: OperandSlot, Pushes: 1},
	OpStoreLocal:  {Name: "STORE_LOCAL", Operand: OperandSlot, Pops: 1},
	OpLoadGlobal:  {Name: "LOAD_GLOBAL", Operand: OperandConstIdx, Pushes: 1},
	OpStoreGlobal: {Name: "STORE_GLOBAL", Operand: OperandConstIdx, Pops: 1},

	// --- Upvalues (closures) ---
	OpLoadUpvalue:  {Name: "LOAD_UPVALUE", Operand: OperandUpvalueIdx, Pushes: 1},
	OpStoreUpvalue: {Name: "STORE_UPVALUE", Operand: OperandUpvalueIdx, Pops: 1},
	OpMakeClosure:  {Name: "MAKE_CLOSURE", Operand: OperandTemplateIdx, Pushes: 1},

	// --- Binary arithmetic & bitwise ---
	OpAdd:    {Name: "ADD", Pops: 2, Pushes: 1},
	OpSub:    {Name: "SUB", Pops: 2, Pushes: 1},
	OpMul:    {Name: "MUL", Pops: 2, Pushes: 1},
	OpDiv:    {Name: "DIV", Pops: 2, Pushes: 1},
	OpMod:    {Name: "MOD", Pops: 2, Pushes: 1},
	OpPow:    {Name: "POW", Pops: 2, Pushes: 1},
	OpBitAnd: {Name: "BIT_AND", Pops: 2, Pushes: 1},
	OpBitOr:  {Name: "BIT_OR", Pops: 2, Pushes: 1},
	OpBitXor: {Name: "BIT_XOR", Pops: 2, Pushes: 1},
	OpShl:    {Name: "SHL", Pops: 2, Pushes: 1},
	OpShr:    {Name: "SHR", Pops: 2, Pushes: 1},
	OpUShr:   {Name: "USHR", Pops: 2, Pushes: 1},

	// --- Unary ---
	OpNeg:    {Name: "NEG", Pops: 1, Pushes: 1},
	OpNot:    {Name: "NOT", Pops: 1, Pushes: 1},
	OpBitNot: {Name: "BIT_NOT", Pops: 1, Pushes: 1},
	OpTypeof: {Name: "TYPEOF", Pops: 1, Pushes: 1},

	// --- Comparisons ---
	OpEq:       {Name: "EQ", Pops: 2, Pushes: 1},
	OpStrictEq: {Name: "STRICT_EQ", Pops: 2, Pushes: 1},
	OpStrictNe: {Name: "STRICT_NE", Pops: 2, Pushes: 1},
	OpNe:       {Name: "NE", Pops: 2, Pushes: 1},
	OpLt:       {Name: "LT", Pops: 2, Pushes: 1},
	OpLe:       {Name: "LE", Pops: 2, Pushes: 1},
	OpGt:       {Name: "GT", Pops: 2, Pushes: 1},
	OpGe:       {Name: "GE", Pops: 2, Pushes: 1},

	// --- Control flow (operand = relative byte offset, signed) ---
	OpJmp:            {Name: "JMP", Operand: OperandSignedOff, IsJump: true},
	OpJmpTruePop:     {Name: "JMP_TRUE_POP", Operand: OperandSignedOff, Pops: 1, IsJump: true},
	OpJmpFalsePop:    {Name: "JMP_FALSE_POP", Operand: OperandSignedOff, Pops: 1, IsJump: true},
	OpJmpTrueKeep:    {Name: "JMP_TRUE_KEEP", Operand: OperandSignedOff, IsJump: true, StackCond: true},
	OpJmpFalseKeep:   {Name: "JMP_FALSE_KEEP", Operand: OperandSignedOff, IsJump: true, StackCond: true},
	OpJmpNullishKeep: {Name: "JMP_NULLISH_KEEP", Operand: OperandSignedOff, IsJump: true, StackCond: true},
	OpOptionalJump:   {Name: "OPTIONAL_JUMP", Operand: OperandSignedOff, IsJump: true, StackCond: true},

	// --- Functions ---
	OpCall:             {Name: "CALL", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpCallMethod:       {Name: "CALL_METHOD", Operand: OperandPackedCall, Pushes: 1, VarStack: true},
	OpCallWithThis:     {Name: "CALL_WITH_THIS", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpCallWithThisArgs: {Name: "CALL_WITH_THIS_ARGS", Pushes: 1, VarStack: true},
	OpNew:              {Name: "NEW", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpReturn:           {Name: "RETURN", Pops: 1, IsTerminal: true},
	OpReturnUndef:      {Name: "RETURN_UNDEF", IsTerminal: true},

	// --- Objects & arrays ---
	OpNewObject:          {Name: "NEW_OBJECT", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpNewArray:           {Name: "NEW_ARRAY", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpGetProp:            {Name: "GET_PROP", Operand: OperandConstIdx, Pops: 1, Pushes: 1},
	OpSetProp:            {Name: "SET_PROP", Operand: OperandConstIdx, Pops: 2, Pushes: 1},
	OpSetPropObj:         {Name: "SET_PROP_OBJ", Operand: OperandConstIdx, Pops: 1}, // 弹 value，保留 obj（对象字面量连续 set）
	OpSetPropTop:         {Name: "SET_PROP_TOP", Operand: OperandConstIdx, Pops: 2},
	OpGetElem:            {Name: "GET_ELEM", Pops: 2, Pushes: 1},
	OpSetElem:            {Name: "SET_ELEM", Pops: 3, Pushes: 1},
	OpSetElemTop:         {Name: "SET_ELEM_TOP", Pops: 3},
	OpDelProp:            {Name: "DEL_PROP", Operand: OperandConstIdx, Pops: 1, Pushes: 1},
	OpDelElem:            {Name: "DEL_ELEM", Pops: 2, Pushes: 1},
	OpSetPropComputedObj: {Name: "SET_PROP_COMPUTED_OBJ", Pops: 2}, // 弹 key+value，保留 obj

	// --- Spread (ES2015) ---
	OpBuildArray:     {Name: "BUILD_ARRAY", Pushes: 1},
	OpArrayPush:      {Name: "ARRAY_PUSH", Pops: 1},
	OpArraySpread:    {Name: "ARRAY_SPREAD", Pops: 1},
	OpCallArgs:       {Name: "CALL_ARGS", Pushes: 1, VarStack: true},
	OpCallMethodArgs: {Name: "CALL_METHOD_ARGS", Operand: OperandConstIdx, Pushes: 1, VarStack: true},
	OpNewArgs:        {Name: "NEW_ARGS", Pushes: 1, VarStack: true},
	OpSpreadObject:   {Name: "SPREAD_OBJECT", Pops: 1},

	// --- Unary extras ---
	OpUnaryPlus:    {Name: "UNARY_PLUS", Pops: 1, Pushes: 1},
	OpTypeofGlobal: {Name: "TYPEOF_GLOBAL", Operand: OperandConstIdx, Pushes: 1},

	// --- Try / catch (try-table driven) ---
	OpTryEnter:       {Name: "TRY_ENTER", Operand: OperandTryIdx},
	OpTryExit:        {Name: "TRY_EXIT", Operand: OperandTryIdx},
	OpTryExitFinally: {Name: "TRY_EXIT_FINALLY", Operand: OperandTryIdx},
	OpTryExitJmp:     {Name: "TRY_EXIT_JMP", Operand: OperandSignedOff, IsJump: true},

	// --- throw / instanceof / in ---
	OpThrow:      {Name: "THROW", Pops: 1, IsTerminal: true},
	OpInstanceof: {Name: "INSTANCEOF", Pops: 2, Pushes: 1},
	OpIn:         {Name: "IN", Pops: 2, Pushes: 1},

	// --- Iteration support ---
	// 遗留指令：interpreter 已无分发 case（for-in 走迭代器协议），
	// 保留登记以维持 optimize.go 的保守跳转判定。
	OpForInNext: {Name: "FOR_IN_NEXT", Operand: OperandSignedOff, IsJump: true, VarStack: true},

	// --- Class (ES2015) ---
	OpMakeClass:         {Name: "MAKE_CLASS", Operand: OperandTemplateIdx, Pushes: 1, VarStack: true},
	OpGetProto:          {Name: "GET_PROTO", Pops: 1, Pushes: 1},
	OpCallThis:          {Name: "CALL_THIS", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpConstructThis:     {Name: "CONSTRUCT_THIS", Operand: OperandCount, Pushes: 1, VarStack: true},
	OpCallThisArgs:      {Name: "CALL_THIS_ARGS", Pushes: 1, VarStack: true},
	OpConstructThisArgs: {Name: "CONSTRUCT_THIS_ARGS", Pushes: 1, VarStack: true},

	// --- Iterator protocol (ES2015) ---
	OpGetIterator: {Name: "GET_ITERATOR", Pops: 1, Pushes: 1},
	OpYield:       {Name: "YIELD", Pops: 1},

	// --- Async iteration protocol (ES2018) ---
	OpGetAsyncIterator: {Name: "GET_ASYNC_ITERATOR", Pops: 1, Pushes: 1},

	// --- Async/await (ES2017) ---
	OpAwait: {Name: "AWAIT", Pops: 1, Pushes: 1},

	// --- RegExp literal ---
	OpMakeRegexp: {Name: "MAKE_REGEXP", Pops: 2, Pushes: 1},

	// --- Object literal accessors (get/set) ---
	OpSetGetterObj:         {Name: "SET_GETTER_OBJ", Operand: OperandConstIdx, Pops: 1},
	OpSetSetterObj:         {Name: "SET_SETTER_OBJ", Operand: OperandConstIdx, Pops: 1},
	OpSetGetterComputedObj: {Name: "SET_GETTER_COMPUTED_OBJ", Pops: 2},
	OpSetSetterComputedObj: {Name: "SET_SETTER_COMPUTED_OBJ", Pops: 2},

	// --- Superinstructions (O2-D1) ---
	OpGetPropLocal: {Name: "GET_PROP_LOCAL", Operand: OperandPackedSlotName, Pushes: 1},

	// --- Close captured lexical bindings ---
	OpCloseUpvalues: {Name: "CLOSE_UPVALUES", Operand: OperandSlot},

	// --- ++ / -- (ES update expressions) ---
	OpInc: {Name: "INC", Pops: 1, Pushes: 1},
	OpDec: {Name: "DEC", Pops: 1, Pushes: 1},

	// OpEnd 哨兵：标记代码结尾，不执行。
	OpEnd: {Name: "END"},
}

// Meta 返回指令的结构化元数据；未知/未登记 opcode 返回 nil。
func Meta(op Opcode) *OpMeta {
	if int(op) >= len(opMeta) {
		return nil
	}
	return opMeta[op]
}

// String 返回人类可读的指令名（反汇编/调试用）。
func (op Opcode) String() string {
	if m := Meta(op); m != nil {
		return m.Name
	}
	return "OP_UNKNOWN"
}

// HasOperand 报告指令是否读取其 3 字节操作数域。
// 无操作数指令忽略操作数字节（栈操作数指令如 GET_ELEM 不读操作数域）。
func (op Opcode) HasOperand() bool {
	if m := Meta(op); m != nil {
		return m.Operand != OperandNone
	}
	return false
}

// StackEffect 返回指令在操作数栈上的固定效果。
// 对 StackCond/VarStack 指令返回 known=false，调用方不得据此推断。
func (m *OpMeta) StackEffect() (pops, pushes uint8, known bool) {
	if m == nil || m.StackCond || m.VarStack {
		return 0, 0, false
	}
	return m.Pops, m.Pushes, true
}
