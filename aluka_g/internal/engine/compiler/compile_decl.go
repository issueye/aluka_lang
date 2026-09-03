package compiler

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func (c *Compiler) compileVarDecl(d *ast.VarDecl) error {
	for _, decl := range d.Decls {
		if decl.Pattern != nil {
			// Destructuring declaration.
			if decl.Init == nil {
				// `let [a, b];` — declare all bindings as undefined.
				for _, name := range ast.PatternNames(decl.Pattern) {
					if d.Kind == "var" {
						c.declareVar(name)
					} else {
						slot := c.declareLocal(name)
						c.emitLetConstUninitialized(slot)
					}
				}
				continue
			}
			// Evaluate init → store in temp slot → bind pattern.
			if err := c.compileExpr(decl.Init); err != nil {
				return err
			}
			tmpSlot := c.newSlot()
			c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			if err := c.compileBindPattern(decl.Pattern, tmpSlot, d.Kind); err != nil {
				return err
			}
			continue
		}
		var slot int
		if d.Kind == "var" {
			slot = c.declareVar(decl.Name.Name)
		} else {
			slot = c.declareLocal(decl.Name.Name)
		}
		if decl.Init != nil {
			if d.Kind == "const" {
				// I-2：登记 const 绑定的函数表达式为内联候选（同名重绑定覆盖
				// 为不可内联）；let/var 可重赋值，不登记。
				if fe, ok := decl.Init.(*ast.ArrowFunc); ok {
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
					if fe.IsAsync {
						c.cur().inlineCandidates[decl.Name.Name] = -1
					} else {
						c.cur().inlineCandidates[decl.Name.Name] = c.lastFuncExprIdx
					}
				} else if fe, ok := decl.Init.(*ast.FunctionExpr); ok {
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
					if fe.IsAsync || fe.IsGenerator || fe.Name != nil {
						// NFE（具名函数表达式）体内引用自身，内联展开后
						// 自引用绑定丢失，不可内联。
						c.cur().inlineCandidates[decl.Name.Name] = -1
					} else {
						c.cur().inlineCandidates[decl.Name.Name] = c.lastFuncExprIdx
					}
				} else {
					c.cur().inlineCandidates[decl.Name.Name] = -1
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
				}
			} else {
				if err := c.compileExpr(decl.Init); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpStoreLocal, uint32(slot))
		} else if d.Kind != "var" {
			c.emitLetConstUninitialized(slot)
		}
	}
	return nil
}

// emitLetConstUninitialized 在循环体内为无初始化器的 let/const 每轮写成
// undefined。循环编译复用槽位，若不复位，`let l; l || (l = x)` 会把第一轮
// 赋值带到后续轮次（babel `@babel/template` 的 `let header;` 即如此）。
// 循环外保持旧行为（槽位函数入口即为 undefined），避免误清已赋值的同槽绑定。
func (c *Compiler) emitLetConstUninitialized(slot int) {
	if len(c.loopStack) == 0 {
		return
	}
	c.emit(bytecode.OpPushUndefined, 0)
	c.emit(bytecode.OpStoreLocal, uint32(slot))
}

// compileBindPattern emits code to destructure the value in srcSlot into the
// bindings declared by the pattern. `kind` is "var" or "let"/"const" and
// controls whether bindings go to the function scope or the block scope.
// declarePatternSlots 为解构模式中的所有绑定名提前占槽（用于 let/const 提升）。

func (c *Compiler) compileFunctionDecl(d *ast.FunctionDecl) error {
	// 函数声明已在 hoistFunctionDecls 中提升编译（名字提前绑定），
	// 这里跳过避免重复编译。
	if d.Name == nil {
		return nil
	}
	return nil
}

func (c *Compiler) compileFunction(name string, params []*ast.Identifier, patterns []ast.Pattern, defaults []ast.Expression, rest *ast.Identifier, body ast.Node, isAsync, isGenerator, isArrow bool, bindSelf bool) error {
	restoreControlFlow := c.isolateControlFlow()
	defer restoreControlFlow()

	// I-1：简单函数体判定（单表达式、空函数体或单条 `return [expr];`），供可内联标记。
	simpleBody := false
	switch b := body.(type) {
	case ast.Expression:
		simpleBody = true
	case *ast.BlockStmt:
		if len(b.Body) == 0 {
			simpleBody = true
		} else if len(b.Body) == 1 {
			if _, ok := b.Body[0].(*ast.ReturnStmt); ok {
				simpleBody = true
			}
		}
	}

	// 隔离外层可选链：嵌套函数编译时 c.cur() 切换到本函数字节码缓冲区，
	// 若内层链的 OpOptionalJump 记入外层链，外层 endOptionalChain 会用
	// 错误缓冲区 patch（PatchOperand out of range panic）。
	savedOptionalStack := c.optionalChainStack
	c.optionalChainStack = nil
	savedOptionalResiduals := c.optionalChainResiduals
	c.optionalChainResiduals = nil
	savedChainPushCount := c.optChainPushCount
	savedChainPushSaved := c.optChainPushSaved
	savedChainPushActive := c.optChainPushActive
	c.optChainPushCount = 0
	c.optChainPushSaved = nil
	c.optChainPushActive = false
	defer func() {
		c.optionalChainStack = savedOptionalStack
		c.optionalChainResiduals = savedOptionalResiduals
		c.optChainPushCount = savedChainPushCount
		c.optChainPushSaved = savedChainPushSaved
		c.optChainPushActive = savedChainPushActive
	}()

	// 普通函数：`this` slot = 0；params = slots 1..N；rest 参数 = slot N+1。
	// 箭头函数：无 own `this`（slot 0 仍保留以兼容 frame 布局，但不会被引用）。
	numLocals := 1 + len(params)
	if rest != nil {
		numLocals++
	}
	tmpl := &bytecode.FuncTemplate{
		Name:          name,
		NumParams:     len(params),
		NumLocals:     numLocals,
		IsVarArgs:     rest != nil,
		IsAsync:       isAsync,
		IsGenerator:   isGenerator,
		IsArrow:       isArrow,
		ArgumentsSlot: -1,
		NFESlot:       -1,
		SourceFile:    c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)
	c.lastFuncExprIdx = funcIdx // I-2 const 绑定登记用（最近编译的函数模板）

	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	if !isArrow {
		fc.scopes[0].decls["__this__"] = 0
	}
	for i, p := range params {
		if p != nil {
			fc.scopes[0].decls[p.Name] = i + 1
		}
	}
	// 非箭头函数均绑定 `arguments` 对象（slot = numLocals，紧随 this/params/rest）。
	// 箭头函数不绑定 own arguments（词法继承外层），与 JS 语义一致。
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
	var nfeSlot int
	if bindSelf && astBodyReferencesName(body, name) {
		// 具名函数表达式（NFE）：仅当函数体内实际引用名字时分配自引用槽
		// （运行时帧建立时写入闭包自身）。未引用的带名函数表达式
		// （如 `add1 = function add1(x) {...}`）不分配，避免 JIT 误拒绝。
		nfeSlot = tmpl.NumLocals
		tmpl.NFESlot = nfeSlot
		tmpl.NumLocals++
	}
	if !isArrow {
		argsSlot := tmpl.NumLocals
		tmpl.ArgumentsSlot = argsSlot
		tmpl.NumLocals++
		fc.scopes[0].decls["arguments"] = argsSlot
		// new.target 槽位：非箭头函数分配；箭头函数不声明，
		// 经 upvalue 链词法解析到外层函数（与 `this` 同机制）。
		ntSlot := tmpl.NumLocals
		tmpl.NewTargetSlot = ntSlot
		tmpl.NumLocals++
		fc.scopes[0].decls["__newTarget__"] = ntSlot
	}
	if nfeSlot > 0 {
		// ES：NFE 名字在独立环境中包裹变量环境，函数体 var/let/const/形参
		// 同名必须能遮蔽它。Express 5 的
		// `Layer.prototype.match = function match(path) { let match; ... }`
		// 依赖此语义；若名字不被遮蔽，match 恒为函数自身，任意路径都“命中”。
		nfeScope := &scope{decls: map[string]int{name: nfeSlot}, isFunc: false}
		funcScope := fc.scopes[0]
		fc.scopes = []*scope{nfeScope, funcScope}
	}
	c.funcStack = append(c.funcStack, fc)

	// O-6 简单回调检测：纯箭头函数（非 async/非 generator、无默认值/rest）
	// 体为单表达式时生成 NativeCallback 描述（数组高阶方法 Go 侧直执行）。
	// 必须在 body 编译前调用（向常量池追加字面量/属性名索引）。
	// 注意：parser 对多参数箭头会填 nil 条目的 ParamPatterns/Defaults 数组
	// （单参数时才是空数组），必须按“是否存在非 nil 条目”判断，而非 len==0。
	if isArrow && !isAsync && !isGenerator && rest == nil &&
		!hasNonNilPatterns(patterns) && !hasNonNilDefaults(defaults) {
		tmpl.NativeCallback = c.analyzeSimpleCallback(params, body)
	}

	// Emit default-parameter initialization at function entry. For each param
	// with a default expression, if the bound argument is `undefined`, evaluate
	// the default and store it. (JS triggers defaults on === undefined, not on
	// falsy, so we use a strict equality check against undefined.)
	for i, def := range defaults {
		if def == nil {
			continue
		}
		slot := i + 1
		c.emit(bytecode.OpLoadLocal, uint32(slot))
		c.emit(bytecode.OpPushUndefined, 0)
		c.emit(bytecode.OpStrictEq, 0) // pushes true if param === undefined
		jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
		if err := c.compileExpr(def); err != nil {
			return err
		}
		c.emit(bytecode.OpStoreLocal, uint32(slot))
		c.patchJumpToHere(jSkip)
	}

	// 解构参数（({a, b}, [x]) => ...）：参数 slot 已填充，生成绑定指令。
	// 模式绑定名经 compileBindPattern 的 declareLocal 声明为局部变量。
	for i, pat := range patterns {
		if pat == nil {
			continue
		}
		if err := c.compileBindPattern(pat, i+1, "let"); err != nil {
			c.funcStack = c.funcStack[:len(c.funcStack)-1]
			return err
		}
	}

	bodyErr := func() error {
		switch b := body.(type) {
		case *ast.BlockStmt:
			c.hoistFunc(b)
			// 函数声明提升：在 body 开头依次编译所有 FunctionDecl，
			// 使其名字在后续语句执行前已绑定到函数对象（JS 语义）。
			c.hoistFunctionDecls(b.Body)
			return c.compileStmts(b.Body)
		case ast.Expression:
			if err := c.compileExpr(b); err != nil {
				return err
			}
			c.emit(bytecode.OpReturn, 0)
		}
		return nil
	}()
	if bodyErr != nil {
		c.funcStack = c.funcStack[:len(c.funcStack)-1]
		return bodyErr
	}
	c.emit(bytecode.OpReturnUndef, 0)
	fc = c.cur()
	if !fc.usedArguments {
		fc.tmpl.NoArgumentsObject = true
	}
	// I-1 可内联判定：纯箭头函数（非 async/generator/rest/默认值/解构、
	// 参数 ≤ 8、体为单表达式）、编译体后无闭包捕获（upvalueIndex 为空——
	// 箭头函数的 this/arguments 只能经 upvalue 引用，空即未引用）、未引用
	// own arguments、指令在白名单内（复制时可安全重映射）。仅作标记，调用
	// 点展开见 compileCall；未展开走正常调用。
	if isArrow && !isAsync && !isGenerator && rest == nil &&
		!hasNonNilDefaults(defaults) && !hasNonNilPatterns(patterns) &&
		len(params) <= 8 && simpleBody && len(fc.upvalueIndex) == 0 && !fc.usedArguments &&
		isInlinableCode(fc.tmpl.Code) {
		fc.tmpl.Inlinable = true
	}
	c.funcStack = c.funcStack[:len(c.funcStack)-1]

	// I-2：恢复 lastFuncExprIdx 为本函数模板索引。const 绑定的内联候选登记
	// 在 compileExpr(init) 返回后读取该字段；若函数体含嵌套函数表达式，
	// 嵌套编译会把字段覆盖为内层模板索引，导致外层 const 登记到错误模板
	// （调用点错误内联内层函数体）。
	c.lastFuncExprIdx = funcIdx

	// Emit OpMakeClosure in the enclosing function.
	c.emit(bytecode.OpMakeClosure, uint32(funcIdx))
	return nil
}

// hoistFunc pre-declares var/function declarations in the function scope.

func (c *Compiler) compileClassDecl(d *ast.ClassDecl) error {
	nameSlot := c.declareVar(d.Name.Name)
	if err := c.compileClass(d.Name.Name, d.SuperClass, d.Body); err != nil {
		return err
	}
	c.emit(bytecode.OpStoreLocal, uint32(nameSlot))
	return nil
}

// compileClassExpr compiles `class [Name] [extends Super] { body }` as an
// expression: the constructor is left on the stack.
func (c *Compiler) compileClassExpr(e *ast.ClassExpr) error {
	name := ""
	if e.Name != nil {
		name = e.Name.Name
	}
	if name == "" {
		return c.compileClass(name, e.SuperClass, e.Body)
	}

	// A named class expression has a private lexical self-binding visible to
	// its constructor, methods, and field initializers, but not to the outer
	// scope. Capture a hidden local and temporarily expose it under the class
	// name while method templates are compiled.
	hiddenName := fmt.Sprintf("__class_expr_self_%d__", c.classCounter)
	selfSlot := c.declareLocal(hiddenName)
	scope := c.cur().scopes[len(c.cur().scopes)-1]
	previousSlot, hadPrevious := scope.decls[name]
	scope.decls[name] = selfSlot
	defer func() {
		if hadPrevious {
			scope.decls[name] = previousSlot
		} else {
			delete(scope.decls, name)
		}
	}()

	if err := c.compileClass(name, e.SuperClass, e.Body); err != nil {
		return err
	}
	// Preserve the expression result while initializing the captured binding.
	c.emit(bytecode.OpDup, 0)
	c.emit(bytecode.OpStoreLocal, uint32(selfSlot))
	return nil
}

// compileClass is the core class compilation routine. It compiles all methods
// into FuncTemplates, builds a ClassTemplate, and emits OpMakeClass. After
// OpMakeClass, the constructor function is on top of the stack.
func (c *Compiler) compileClass(name string, super ast.Expression, body *ast.ClassBody) error {
	classID := c.classCounter
	c.classCounter++
	savedClassID := c.curClassID
	c.curClassID = classID
	defer func() { c.curClassID = savedClassID }()
	// 类体内静态语境从零开始：实例构造器/字段初始化器为实例语境；进入
	// 静态方法/静态块/静态字段时置位。外层语境由 defer 恢复（嵌套类场景）。
	savedStaticCtx := c.inStaticCtx
	c.inStaticCtx = false
	defer func() { c.inStaticCtx = savedStaticCtx }()

	hasSuper := super != nil

	// Evaluate the superclass and stash it in unique local slots so that
	// methods can capture them as upvalues for `super` resolution.
	if hasSuper {
		if err := c.compileExpr(super); err != nil {
			return err
		}
		// stack: [super]
		c.emit(bytecode.OpDup, 0)
		// stack: [super, super]
		ctorSlot := c.declareLocal(superCtorName(classID))
		c.emit(bytecode.OpStoreLocal, uint32(ctorSlot))
		// stack: [super]
		c.emit(bytecode.OpLoadLocal, uint32(ctorSlot))
		protoIdx := c.cur().tmpl.AddStringConst("prototype")
		c.emit(bytecode.OpGetProp, uint32(protoIdx))
		protoSlot := c.declareLocal(superProtoName(classID))
		c.emit(bytecode.OpStoreLocal, uint32(protoSlot))
		// stack: [super]  — consumed by OpMakeClass below
	}

	classTpl := &bytecode.ClassTemplate{
		Name:     name,
		HasSuper: hasSuper,
	}

	// Collect class field declarations and static blocks (ES2022 / TypeScript). Instance fields
	// with initializers are injected into the constructor body; static fields
	// and static initialization blocks are evaluated after OpMakeClass. Fields without initializers
	// (e.g. `x: number;`) have no runtime effect and are skipped.
	var instanceFieldInits []ast.Statement
	var staticElements []*ast.MethodDefinition
	for fieldIndex, m := range body.Methods {
		if m.Kind == ast.MethodStaticBlock {
			staticElements = append(staticElements, &m)
			continue
		}
		if m.Kind != ast.MethodField {
			continue
		}
		if m.Static {
			if m.Init != nil {
				staticElements = append(staticElements, &m)
			}
			continue
		}
		if m.Init == nil {
			continue
		}
		fieldKey := m.Key
		if m.Computed {
			// Computed field names are evaluated once when the class is defined,
			// not once per instance. Store the key in the surrounding scope; the
			// generated constructor captures that slot as an upvalue.
			keyName := fmt.Sprintf("__class_field_key_%d_%d__", classID, fieldIndex)
			keySlot := c.declareLocal(keyName)
			if err := c.compileExpr(m.Key); err != nil {
				return err
			}
			c.emit(bytecode.OpStoreLocal, uint32(keySlot))
			fieldKey = &ast.Identifier{Name: keyName, Loc: m.Loc}
		}
		instanceFieldInits = append(instanceFieldInits, &ast.ExprStmt{
			Expr: &ast.AssignExpr{
				Op: "=",
				Left: &ast.MemberExpr{
					Object:   &ast.ThisExpr{Loc: m.Loc},
					Property: fieldKey,
					Computed: m.Computed,
					Loc:      m.Loc,
				},
				Right: m.Init,
				Loc:   m.Loc,
			},
			Loc: m.Loc,
		})
	}

	// Compile the constructor (or synthesize a default). Instance field
	// initializers are prepended to the constructor body so they run before
	// user code (base class) or after super() (derived synthesized ctor).
	hasCtor := false
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor {
			ctorFn := *m.Value // shallow copy — we'll replace Body
			if len(instanceFieldInits) > 0 {
				newBody := make([]ast.Statement, 0, len(instanceFieldInits)+len(m.Value.Body.Body))
				if !hasSuper {
					// Base class: field inits run first.
					newBody = append(newBody, instanceFieldInits...)
					newBody = append(newBody, m.Value.Body.Body...)
				} else {
					// Derived class: field inits should run after super().
					// For the MVP, we prepend them — user is responsible for
					// calling super() first. A correct implementation would
					// split the body at the super() call.
					newBody = append(newBody, instanceFieldInits...)
					newBody = append(newBody, m.Value.Body.Body...)
				}
				ctorFn.Body = &ast.BlockStmt{Body: newBody, Loc: m.Value.Body.Loc}
			}
			idx, err := c.compileMethod("constructor", &ctorFn)
			if err != nil {
				return err
			}
			classTpl.CtorIdx = idx
			hasCtor = true
			break
		}
	}
	if !hasCtor {
		var idx int
		var err error
		if hasSuper {
			if len(instanceFieldInits) > 0 {
				// Synthesize: function(...args) { super(...args); fieldInits }
				superCall := &ast.ExprStmt{
					Expr: &ast.CallExpr{
						Callee: &ast.SuperExpr{},
						Arguments: []ast.Expression{
							&ast.SpreadElement{Arg: &ast.Identifier{Name: "args"}},
						},
					},
				}
				body := append([]ast.Statement{superCall}, instanceFieldInits...)
				ctorFn := &ast.FunctionExpr{
					RestParam: &ast.Identifier{Name: "args"},
					Body:      &ast.BlockStmt{Body: body},
				}
				idx, err = c.compileMethod("constructor", ctorFn)
			} else {
				idx, err = c.compileDefaultDerivedCtor()
			}
		} else if len(instanceFieldInits) > 0 {
			// Synthesize a base constructor that initializes fields.
			ctorFn := &ast.FunctionExpr{
				Body: &ast.BlockStmt{Body: instanceFieldInits},
			}
			idx, err = c.compileMethod("constructor", ctorFn)
		} else {
			idx, err = c.compileDefaultBaseCtor()
		}
		if err != nil {
			return err
		}
		classTpl.CtorIdx = idx
	}

	// Compile non-constructor, non-field, non-static-block methods.
	// 计算键方法（[expr]() {}）：键表达式按方法顺序求值压栈，供
	// OpMakeClass 弹出使用；记录其在 Methods 中的索引。
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor || m.Kind == ast.MethodField || m.Kind == ast.MethodStaticBlock {
			continue
		}
		if m.Computed {
			if err := c.compileExpr(m.Key); err != nil {
				return err
			}
			classTpl.ComputedIdx = append(classTpl.ComputedIdx, len(classTpl.Methods))
		}
		methodName := propKey(m.Key)
		if m.Computed {
			methodName = "computed"
		}
		// 静态方法体为静态语境：super.prop 解析到父类构造器。
		c.inStaticCtx = m.Static
		idx, err := c.compileMethod(methodName, m.Value)
		c.inStaticCtx = false
		if err != nil {
			return err
		}
		kind := bytecode.MethodKindNormal
		switch m.Kind {
		case ast.MethodGetter:
			kind = bytecode.MethodKindGetter
		case ast.MethodSetter:
			kind = bytecode.MethodKindSetter
		}
		classTpl.Methods = append(classTpl.Methods, bytecode.ClassMethodTemplate{
			Name:    methodName,
			Kind:    kind,
			Static:  m.Static,
			TmplIdx: idx,
		})
	}

	classIdx := c.module.AddClass(classTpl)
	c.emit(bytecode.OpMakeClass, uint32(classIdx))

	// Static field & static block initialization (ES2022):
	// Evaluated in source declaration order after the class constructor is created.
	// The constructor (class function) is on top of the stack.
	for _, elem := range staticElements {
		// 静态块/静态字段初始化器为静态语境：super.prop 解析到父类构造器。
		c.inStaticCtx = true
		if elem.Kind == ast.MethodStaticBlock {
			// static { ... } block execution:
			// Stack: [class] -> OpDup -> [class, class]
			// compileExpr(elem.Value) -> [class, class, blockFn]
			// OpSwap -> [class, blockFn, class]
			// OpCallWithThis(0) -> [class, ret]
			// OpPop -> [class]
			c.emit(bytecode.OpDup, 0)
			if err := c.compileExpr(elem.Value); err != nil {
				return err
			}
			c.emit(bytecode.OpSwap, 0)
			c.emit(bytecode.OpCallWithThis, 0)
			c.emit(bytecode.OpPop, 0)
			continue
		}
		if elem.Init == nil {
			continue
		}
		c.emit(bytecode.OpDup, 0)
		if elem.Computed {
			if err := c.compileExpr(elem.Key); err != nil {
				return err
			}
			if err := c.compileExpr(elem.Init); err != nil {
				return err
			}
			c.emit(bytecode.OpSetPropComputedObj, 0)
			continue
		}
		fieldName := propKey(elem.Key)
		nameIdx := c.cur().tmpl.AddStringConst(fieldName)
		if err := c.compileExpr(elem.Init); err != nil {
			return err
		}
		c.emit(bytecode.OpSetPropObj, uint32(nameIdx))
	}
	c.inStaticCtx = false
	return nil
}

// compileMethod compiles a class method body into a FuncTemplate and returns
// its index. Unlike compileFunction, it does NOT emit OpMakeClosure — the
// class assembler (OpMakeClass) creates the closure at runtime.
func (c *Compiler) compileMethod(name string, fn *ast.FunctionExpr) (int, error) {
	restoreControlFlow := c.isolateControlFlow()
	defer restoreControlFlow()

	params := fn.Params
	defaults := fn.Defaults
	rest := fn.RestParam

	numLocals := 1 + len(params)
	if rest != nil {
		numLocals++
	}
	tmpl := &bytecode.FuncTemplate{
		Name:        name,
		NumParams:   len(params),
		NumLocals:   numLocals,
		IsVarArgs:   rest != nil,
		IsAsync:     fn.IsAsync,
		IsGenerator: fn.IsGenerator,
		SourceFile:  c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)

	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	for i, p := range params {
		if p != nil {
			fc.scopes[0].decls[p.Name] = i + 1
		}
	}
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
	// 方法同样绑定 own `arguments` 对象。必须显式分配槽并递增 NumLocals，
	// 否则 ArgumentsSlot 默认 0 会覆盖 slot 0 的 `this`。
	argsSlot := tmpl.NumLocals
	tmpl.ArgumentsSlot = argsSlot
	tmpl.NumLocals++
	fc.scopes[0].decls["arguments"] = argsSlot
	// 类方法同样分配 new.target 槽位（new.target.prototype 等用法）。
	ntSlot := tmpl.NumLocals
	tmpl.NewTargetSlot = ntSlot
	tmpl.NumLocals++
	fc.scopes[0].decls["__newTarget__"] = ntSlot
	c.funcStack = append(c.funcStack, fc)

	// Default-parameter initialization at function entry.
	for i, def := range defaults {
		if def == nil {
			continue
		}
		slot := i + 1
		c.emit(bytecode.OpLoadLocal, uint32(slot))
		c.emit(bytecode.OpPushUndefined, 0)
		c.emit(bytecode.OpStrictEq, 0)
		jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
		if err := c.compileExpr(def); err != nil {
			return 0, err
		}
		c.emit(bytecode.OpStoreLocal, uint32(slot))
		c.patchJumpToHere(jSkip)
	}

	// Class methods and constructors use the same destructuring parameter
	// semantics as ordinary functions. Bind patterns only after whole-parameter
	// defaults have been applied (for example constructor({x = fallback} = {})).
	for i, pat := range fn.ParamPatterns {
		if pat == nil {
			continue
		}
		if err := c.compileBindPattern(pat, i+1, "let"); err != nil {
			c.funcStack = c.funcStack[:len(c.funcStack)-1]
			return 0, err
		}
	}

	c.hoistFunc(fn.Body)
	// Methods are function scopes too. Hoist and emit nested function
	// declarations before compiling the body; npm stream implementations use
	// local async generator declarations inside static methods.
	c.hoistFunctionDecls(fn.Body.Body)
	if err := c.compileStmts(fn.Body.Body); err != nil {
		return 0, err
	}
	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// compileDefaultBaseCtor synthesizes an empty constructor for a base class
// (no extends): `function() {}`.
func (c *Compiler) compileDefaultBaseCtor() (int, error) {
	tmpl := &bytecode.FuncTemplate{
		Name:       "constructor",
		NumParams:  0,
		NumLocals:  1, // slot 0 = this
		SourceFile: c.cur().tmpl.SourceFile,
		// 合成空构造器不引用 `arguments` / `new.target`：显式置 -1，
		// 避免默认 0 覆盖 this。
		ArgumentsSlot: -1,
		NewTargetSlot: -1,
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	c.funcStack = append(c.funcStack, fc)
	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// compileDefaultDerivedCtor synthesizes `function(...args) { super(...args); }`
// for a derived class with no explicit constructor.
func (c *Compiler) compileDefaultDerivedCtor() (int, error) {
	tmpl := &bytecode.FuncTemplate{
		Name:       "constructor",
		NumParams:  0,
		NumLocals:  3, // slot 0 = this, slot 1 = rest "args", slot 2 = new.target
		IsVarArgs:  true,
		SourceFile: c.cur().tmpl.SourceFile,
		// 合成派生构造器通过 rest 转发 super，不引用 `arguments`：置 -1。
		// new.target 槽必须分配：super() 调用原生父类构造（Error/DOMException
		// 等）时，constructThis 需要 newTarget.prototype 修正实例原型。
		ArgumentsSlot: -1,
		NewTargetSlot: 2,
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	fc.scopes[0].decls["args"] = 1
	fc.scopes[0].decls["__newTarget__"] = 2
	c.funcStack = append(c.funcStack, fc)

	// super(...args): load home-ctor, load rest args, construct with this.
	c.emitSuperCtor()
	c.emit(bytecode.OpLoadLocal, 1) // rest param "args"
	c.emit(bytecode.OpConstructThisArgs, 0)
	c.emit(bytecode.OpPop, 0)

	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// astBodyReferencesName 扫描函数体（含嵌套函数）是否引用了指定标识符。
// 用于 NFE 自引用槽的按需分配：仅当体内实际引用名字时才需要槽位。
// 简化实现：同名 Identifier（引用或声明）即视为引用——遮蔽等罕见场景
// 只会多分配一个槽位（语义正确，仅 JIT 拒绝的轻微性能损失）。
