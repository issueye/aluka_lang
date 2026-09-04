//! 模块级多函数与类模板汇编器。

use crate::codegen::{compile_expr, compile_stmt};
use crate::scope::{CompiledUnit, ParentScopeInfo};
use aluka_bytecode::{
    BytecodeModule, ClassMethod, ClassTemplate, FuncTemplate, Instr, Op, UpvalueCapture,
};
use aluka_parser::ast::{ClassMethodDef, Expr, FunctionDef, Program, PropKey, PropValue, Stmt};

/// 编译整个 AST 语法树模块，生成包含函数模板与类模板的完整字节码模块。
#[must_use]
pub fn compile_module(program: &Program) -> BytecodeModule {
    let mut compiler = ModuleCompiler::new();
    compiler.compile(program)
}

/// 模块级编译上下文。
#[derive(Debug, Default)]
pub struct ModuleCompiler {
    /// 函数模板池
    pub functions: Vec<FuncTemplate>,
    /// 类模板池
    pub classes: Vec<ClassTemplate>,
}

fn collect_ident_uses_in_expr(expr: &Expr, uses: &mut Vec<String>) {
    match expr {
        Expr::Ident(name) => uses.push(name.clone()),
        Expr::Assign { name, value } => {
            uses.push(name.clone());
            collect_ident_uses_in_expr(value, uses);
        }
        Expr::Binary { left, right, .. } => {
            collect_ident_uses_in_expr(left, uses);
            collect_ident_uses_in_expr(right, uses);
        }
        Expr::Unary { expr, .. } => collect_ident_uses_in_expr(expr, uses),
        Expr::Call { callee, args } | Expr::New { callee, args } => {
            collect_ident_uses_in_expr(callee, uses);
            for a in args {
                collect_ident_uses_in_expr(a, uses);
            }
        }
        Expr::MethodCall { receiver, args, .. } => {
            collect_ident_uses_in_expr(receiver, uses);
            for a in args {
                collect_ident_uses_in_expr(a, uses);
            }
        }
        Expr::Member { obj, .. } | Expr::OptionalMember { obj, .. } => {
            collect_ident_uses_in_expr(obj, uses);
        }
        Expr::Index { obj, index } | Expr::OptionalIndex { obj, index } => {
            collect_ident_uses_in_expr(obj, uses);
            collect_ident_uses_in_expr(index, uses);
        }
        Expr::Object(props) => {
            for p in props {
                if let PropKey::Computed(k) = &p.key {
                    collect_ident_uses_in_expr(k, uses);
                }
                match &p.value {
                    PropValue::Expr(v) | PropValue::Spread(v) => {
                        collect_ident_uses_in_expr(v, uses)
                    }
                    PropValue::Getter(def) | PropValue::Setter(def) => {
                        for s in &def.body {
                            collect_ident_uses(s, uses);
                        }
                    }
                }
            }
        }
        Expr::Array(elements) => {
            for e in elements {
                collect_ident_uses_in_expr(e, uses);
            }
        }
        Expr::Spread(inner) => {
            collect_ident_uses_in_expr(inner, uses);
        }
        Expr::Update { target, .. } => collect_ident_uses_in_expr(target, uses),
        Expr::Conditional {
            cond,
            then_expr,
            else_expr,
        } => {
            collect_ident_uses_in_expr(cond, uses);
            collect_ident_uses_in_expr(then_expr, uses);
            collect_ident_uses_in_expr(else_expr, uses);
        }
        Expr::OptionalCall { callee, args } => {
            collect_ident_uses_in_expr(callee, uses);
            for a in args {
                collect_ident_uses_in_expr(a, uses);
            }
        }
        Expr::Function(def) => {
            for stmt in &def.body {
                collect_ident_uses(stmt, uses);
            }
        }
        Expr::Yield { value: Some(v), .. } => collect_ident_uses_in_expr(v, uses),
        Expr::Yield { value: None, .. } => {}
        Expr::Await(arg) => collect_ident_uses_in_expr(arg, uses),
        Expr::Super => {}
        _ => {}
    }
}

pub(crate) fn collect_ident_uses(stmt: &Stmt, uses: &mut Vec<String>) {
    match stmt {
        Stmt::Expr(expr) => collect_ident_uses_in_expr(expr, uses),
        Stmt::VarDecl {
            init: Some(init), ..
        } => {
            collect_ident_uses_in_expr(init, uses);
        }
        Stmt::VarDecl { init: None, .. } => {}
        Stmt::DestructureDecl { init, .. } => {
            collect_ident_uses_in_expr(init, uses);
        }
        Stmt::Block(stmts) => {
            for s in stmts {
                collect_ident_uses(s, uses);
            }
        }
        Stmt::If {
            cond,
            then_branch,
            else_branch,
        } => {
            collect_ident_uses_in_expr(cond, uses);
            collect_ident_uses(then_branch, uses);
            if let Some(eb) = else_branch {
                collect_ident_uses(eb, uses);
            }
        }
        Stmt::While { cond, body } | Stmt::DoWhile { cond, body } => {
            collect_ident_uses_in_expr(cond, uses);
            collect_ident_uses(body, uses);
        }
        Stmt::For {
            init,
            cond,
            update,
            body,
        } => {
            if let Some(i) = init {
                collect_ident_uses(i, uses);
            }
            if let Some(c) = cond {
                collect_ident_uses_in_expr(c, uses);
            }
            if let Some(u) = update {
                collect_ident_uses_in_expr(u, uses);
            }
            collect_ident_uses(body, uses);
        }
        Stmt::ForIn { right, body, .. } | Stmt::ForOf { right, body, .. } => {
            collect_ident_uses_in_expr(right, uses);
            collect_ident_uses(body, uses);
        }
        Stmt::Break | Stmt::Continue => {}
        Stmt::Return(Some(expr)) => collect_ident_uses_in_expr(expr, uses),
        Stmt::Try {
            body,
            catch_body,
            finally_body,
            ..
        } => {
            collect_ident_uses(body, uses);
            if let Some(cb) = catch_body {
                collect_ident_uses(cb, uses);
            }
            if let Some(fb) = finally_body {
                collect_ident_uses(fb, uses);
            }
        }
        Stmt::Throw(expr) => {
            collect_ident_uses_in_expr(expr, uses);
        }
        Stmt::Switch {
            discriminant,
            cases,
        } => {
            collect_ident_uses_in_expr(discriminant, uses);
            for c in cases {
                if let Some(t) = &c.test {
                    collect_ident_uses_in_expr(t, uses);
                }
                for s in &c.consequent {
                    collect_ident_uses(s, uses);
                }
            }
        }
        Stmt::Import(_) => {}
        Stmt::Export(export_decl) => match export_decl {
            aluka_parser::ast::ExportDecl::Named {
                decl: Some(inner), ..
            } => {
                collect_ident_uses(inner, uses);
            }
            aluka_parser::ast::ExportDecl::Default(expr) => {
                collect_ident_uses_in_expr(expr, uses);
            }
            _ => {}
        },
        _ => {}
    }
}

impl ModuleCompiler {
    /// 创建新的模块编译器实例。
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// 编译整个模块，返回符合规范的 BytecodeModule。
    pub fn compile(&mut self, program: &Program) -> BytecodeModule {
        let mut optimized_program = program.clone();
        crate::jsx::lower_jsx(&mut optimized_program);
        crate::opt::optimize_ast(&mut optimized_program);

        self.functions.clear();
        self.classes.clear();
        self.functions.push(FuncTemplate {
            name: "main".to_owned(),
            num_params: 0,
            num_locals: 0,
            is_var_args: false,
            is_generator: false,
            is_async: false,
            is_arrow: false,
            code: Vec::new(),
            max_stack: 0,
            source_file: String::new(),
            constants: Vec::new(),
            upvalues: Vec::new(),
            try_table: Vec::new(),
        });

        let mut top_unit = CompiledUnit::default();

        for (i, stmt) in optimized_program.body.iter().enumerate() {
            let is_last = i == optimized_program.body.len() - 1;
            match stmt {
                Stmt::Function(func_def) => {
                    let slot = if let Some(&s) = top_unit.symbol_map.get(&func_def.name) {
                        s
                    } else {
                        let s = top_unit.locals;
                        top_unit.locals += 1;
                        top_unit.symbol_map.insert(func_def.name.clone(), s);
                        s
                    };
                    let parent_info = ParentScopeInfo::new(
                        top_unit.symbol_map.clone(),
                        top_unit.upvalue_map.clone(),
                    );
                    let fn_idx = self.compile_function_with_parent(func_def, Some(&parent_info));
                    top_unit
                        .code
                        .push(Instr::new(Op::MakeClosure, fn_idx as u32));
                    top_unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
                }
                Stmt::Class {
                    name,
                    super_class,
                    constructor,
                    methods,
                } => {
                    let class_id = self.classes.len();
                    if let Some(super_expr) = super_class {
                        compile_expr(super_expr, &mut top_unit);
                        top_unit.code.push(Instr::new(Op::Dup, 0));

                        let ctor_sym = format!("__home_ctor_{class_id}__");
                        let ctor_slot = top_unit.locals;
                        top_unit.locals += 1;
                        top_unit.symbol_map.insert(ctor_sym, ctor_slot);
                        top_unit
                            .code
                            .push(Instr::new(Op::StoreLocal, ctor_slot as u32));

                        top_unit
                            .code
                            .push(Instr::new(Op::LoadLocal, ctor_slot as u32));
                        let proto_idx = crate::codegen::add_constant(
                            &mut top_unit,
                            aluka_bytecode::Constant::String("prototype".to_owned()),
                        );
                        top_unit.code.push(Instr::new(Op::GetProp, proto_idx));
                        let proto_sym = format!("__home_proto_{class_id}__");
                        let proto_slot = top_unit.locals;
                        top_unit.locals += 1;
                        top_unit.symbol_map.insert(proto_sym, proto_slot);
                        top_unit
                            .code
                            .push(Instr::new(Op::StoreLocal, proto_slot as u32));
                    }

                    let parent_info = ParentScopeInfo::new(
                        top_unit.symbol_map.clone(),
                        top_unit.upvalue_map.clone(),
                    );
                    let class_idx = self.compile_class(
                        name,
                        super_class.is_some(),
                        constructor,
                        methods,
                        Some(&parent_info),
                        class_id,
                    );
                    top_unit
                        .code
                        .push(Instr::new(Op::MakeClass, class_idx as u32));
                    let slot = if let Some(&s) = top_unit.symbol_map.get(name) {
                        s
                    } else {
                        let s = top_unit.locals;
                        top_unit.locals += 1;
                        top_unit.symbol_map.insert(name.clone(), s);
                        s
                    };
                    top_unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
                }
                Stmt::Import(_) => {
                    // 静态导入在单模块执行中无需生成运行时操作指令
                }
                Stmt::Export(export_decl) => match export_decl {
                    aluka_parser::ast::ExportDecl::Named {
                        decl: Some(inner), ..
                    } => match inner.as_ref() {
                        Stmt::Function(func_def) => {
                            let parent_info = ParentScopeInfo::new(
                                top_unit.symbol_map.clone(),
                                top_unit.upvalue_map.clone(),
                            );
                            let fn_idx =
                                self.compile_function_with_parent(func_def, Some(&parent_info));
                            let slot = if let Some(&s) = top_unit.symbol_map.get(&func_def.name) {
                                s
                            } else {
                                let s = top_unit.locals;
                                top_unit.locals += 1;
                                top_unit.symbol_map.insert(func_def.name.clone(), s);
                                s
                            };
                            top_unit
                                .code
                                .push(Instr::new(Op::MakeClosure, fn_idx as u32));
                            top_unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
                        }
                        other_inner => {
                            compile_stmt(other_inner, &mut top_unit, is_last);
                        }
                    },
                    aluka_parser::ast::ExportDecl::Default(expr) => {
                        compile_expr(expr, &mut top_unit);
                        if is_last {
                            top_unit.code.push(Instr::new(Op::Return, 0));
                        } else {
                            top_unit.code.push(Instr::new(Op::Pop, 0));
                        }
                    }
                    _ => {}
                },
                other => {
                    compile_stmt(other, &mut top_unit, is_last);
                }
            }
        }

        // 递归回填顶层单元中所有的闭包表达式占位指令
        while let Some((instr_idx, closure_def, mut parent_info)) =
            top_unit.closure_backpatches.pop()
        {
            for (k, v) in &top_unit.symbol_map {
                parent_info.locals.entry(k.clone()).or_insert(*v);
            }
            for (k, v) in &top_unit.upvalue_map {
                parent_info.upvalues.entry(k.clone()).or_insert(*v);
            }
            let child_idx = self.compile_function_with_parent(&closure_def, Some(&parent_info));
            top_unit.code[instr_idx].operand = child_idx as u32;
        }

        if top_unit.code.is_empty()
            || !matches!(
                top_unit.code.last().map(|i| i.op),
                Some(Op::Return | Op::ReturnUndef)
            )
        {
            top_unit.code.push(Instr::new(Op::ReturnUndef, 0));
        }

        let top_func = top_unit.to_func_template("main");
        self.functions[0] = top_func;

        BytecodeModule {
            version: 30,
            functions: std::mem::take(&mut self.functions),
            classes: std::mem::take(&mut self.classes),
        }
    }

    /// 编译单一函数定义（无父级上下文）
    pub fn compile_function(&mut self, def: &FunctionDef) -> usize {
        self.compile_function_with_parent(def, None)
    }

    /// 编译函数定义，并支持向父级作用域捕获闭包变量（Upvalues）
    pub fn compile_function_with_parent(
        &mut self,
        def: &FunctionDef,
        parent_scope: Option<&ParentScopeInfo>,
    ) -> usize {
        self.compile_method_function(def, parent_scope, None)
    }

    /// 编译方法函数定义，支持向父级作用域捕获闭包变量及派生类的父类上值
    pub fn compile_method_function(
        &mut self,
        def: &FunctionDef,
        parent_scope: Option<&ParentScopeInfo>,
        class_id: Option<usize>,
    ) -> usize {
        let num_params = if def.is_var_args && !def.params.is_empty() {
            (def.params.len() - 1) as u32
        } else {
            def.params.len() as u32
        };
        let mut unit = CompiledUnit {
            locals: 1, // locals[0] 保留给 this
            num_params,
            is_var_args: def.is_var_args,
            class_id,
            ..Default::default()
        };
        for param in &def.params {
            let s = unit.locals;
            unit.locals += 1;
            unit.symbol_map.insert(param.clone(), s);
        }

        // 若类拥有父类，将外层声明的 __home_ctor_{cid}__ 和 __home_proto_{cid}__ 预置为闭包 Upvalue
        if let (Some(cid), Some(parent_info)) = (class_id, parent_scope) {
            let ctor_sym = format!("__home_ctor_{cid}__");
            if let Some(&parent_slot) = parent_info.locals.get(&ctor_sym) {
                let uv_idx = unit.upvalues.len();
                unit.upvalues.push(UpvalueCapture {
                    is_local: true,
                    index: parent_slot as u32,
                });
                unit.upvalue_map.insert(ctor_sym, uv_idx);
            }
            let proto_sym = format!("__home_proto_{cid}__");
            if let Some(&parent_slot) = parent_info.locals.get(&proto_sym) {
                let uv_idx = unit.upvalues.len();
                unit.upvalues.push(UpvalueCapture {
                    is_local: true,
                    index: parent_slot as u32,
                });
                unit.upvalue_map.insert(proto_sym, uv_idx);
            }
        }

        // 若存在父级符号表，预先识别并建立闭包上值捕获（Upvalues，包括直接局部变量与跨层上值继承）
        if let Some(parent_info) = parent_scope {
            let mut uses = Vec::new();
            for stmt in &def.body {
                collect_ident_uses(stmt, &mut uses);
            }
            for name in uses {
                if !unit.symbol_map.contains_key(&name) {
                    if let Some(&parent_slot) = parent_info.locals.get(&name) {
                        if !unit.upvalue_map.contains_key(&name) {
                            let uv_idx = unit.upvalues.len();
                            unit.upvalues.push(UpvalueCapture {
                                is_local: true,
                                index: parent_slot as u32,
                            });
                            unit.upvalue_map.insert(name, uv_idx);
                        }
                    } else if let Some(&parent_uv) = parent_info.upvalues.get(&name) {
                        if !unit.upvalue_map.contains_key(&name) {
                            let uv_idx = unit.upvalues.len();
                            unit.upvalues.push(UpvalueCapture {
                                is_local: false,
                                index: parent_uv as u32,
                            });
                            unit.upvalue_map.insert(name, uv_idx);
                        }
                    }
                }
            }
        }

        for (i, stmt) in def.body.iter().enumerate() {
            let is_last = i == def.body.len() - 1;
            match stmt {
                Stmt::Function(child_def) => {
                    let parent_info =
                        ParentScopeInfo::new(unit.symbol_map.clone(), unit.upvalue_map.clone());
                    let child_idx =
                        self.compile_function_with_parent(child_def, Some(&parent_info));
                    let slot = if let Some(&s) = unit.symbol_map.get(&child_def.name) {
                        s
                    } else {
                        let s = unit.locals;
                        unit.locals += 1;
                        unit.symbol_map.insert(child_def.name.clone(), s);
                        s
                    };
                    unit.code
                        .push(Instr::new(Op::MakeClosure, child_idx as u32));
                    unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
                }
                other => {
                    compile_stmt(other, &mut unit, is_last);
                }
            }
        }

        // 递归回填该函数单元中所有的闭包表达式占位指令
        while let Some((instr_idx, closure_def, mut parent_info)) = unit.closure_backpatches.pop() {
            for (k, v) in &unit.symbol_map {
                parent_info.locals.entry(k.clone()).or_insert(*v);
            }
            for (k, v) in &unit.upvalue_map {
                parent_info.upvalues.entry(k.clone()).or_insert(*v);
            }
            let child_idx = self.compile_function_with_parent(&closure_def, Some(&parent_info));
            unit.code[instr_idx].operand = child_idx as u32;
        }

        if unit.code.is_empty()
            || !matches!(
                unit.code.last().map(|i| i.op),
                Some(Op::Return | Op::ReturnUndef)
            )
        {
            unit.code.push(Instr::new(Op::ReturnUndef, 0));
        }
        let mut func_tpl = unit.to_func_template(&def.name);
        func_tpl.is_async = def.is_async;
        func_tpl.is_generator = def.is_generator;
        let idx = self.functions.len();
        self.functions.push(func_tpl);
        idx
    }

    /// 编译类定义，返回类在 classes 中的索引
    pub fn compile_class(
        &mut self,
        name: &str,
        has_super: bool,
        constructor: &Option<FunctionDef>,
        methods: &[ClassMethodDef],
        parent_scope: Option<&ParentScopeInfo>,
        class_id: usize,
    ) -> usize {
        let ctor_idx = if let Some(ctor_def) = constructor {
            self.compile_method_function(ctor_def, parent_scope, Some(class_id)) as u32
        } else {
            let def =
                FunctionDef::new(format!("{name}_constructor"), Vec::new(), false, Vec::new());
            self.compile_method_function(&def, parent_scope, Some(class_id)) as u32
        };

        let mut class_methods = Vec::with_capacity(methods.len());
        for m in methods {
            let fn_def = FunctionDef::new(
                format!("{name}_{}", m.name),
                m.params.clone(),
                false,
                m.body.clone(),
            );
            let func_index =
                self.compile_method_function(&fn_def, parent_scope, Some(class_id)) as u32;
            class_methods.push(ClassMethod {
                name: m.name.clone(),
                func_index,
                is_static: m.is_static,
                kind: m.kind,
            });
        }

        let class_tpl = ClassTemplate {
            name: name.to_owned(),
            has_super,
            constructor_index: ctor_idx,
            methods: class_methods,
            computed_indices: Vec::new(),
        };

        let idx = self.classes.len();
        self.classes.push(class_tpl);
        idx
    }
}
