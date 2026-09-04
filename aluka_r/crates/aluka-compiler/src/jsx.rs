//! JSX 语法树编译期降级（Lowering）Pass。
//!
//! 将 AST 中的所有 `JSXElement` 与 `JSXFragment` 在前端转换为标准的
//! `React.createElement(tag, props, ...children)` 调用，无需底层虚拟机新增任何专用 Opcode。

use aluka_parser::ast::{
    Expr, FunctionDef, JSXAttrValue, JSXAttribute, JSXChild, JSXElement, JSXFragment, JSXTagName,
    ObjectProp, Program, PropKey, PropValue, Stmt,
};

/// 对语法树执行全局 JSX 降级转换。
pub fn lower_jsx(program: &mut Program) {
    for stmt in &mut program.body {
        lower_stmt(stmt);
    }
}

/// 降级单条语句中的 JSX 节点
pub fn lower_stmt(stmt: &mut Stmt) {
    match stmt {
        Stmt::Expr(expr) => lower_expr(expr),
        Stmt::VarDecl {
            init: Some(expr), ..
        } => lower_expr(expr),
        Stmt::VarDecl { init: None, .. } => {}
        Stmt::DestructureDecl { init, .. } => lower_expr(init),
        Stmt::Block(stmts) => {
            for s in stmts {
                lower_stmt(s);
            }
        }
        Stmt::If {
            cond,
            then_branch,
            else_branch,
        } => {
            lower_expr(cond);
            lower_stmt(then_branch);
            if let Some(eb) = else_branch {
                lower_stmt(eb);
            }
        }
        Stmt::While { cond, body } | Stmt::DoWhile { cond, body } => {
            lower_expr(cond);
            lower_stmt(body);
        }
        Stmt::For {
            init,
            cond,
            update,
            body,
        } => {
            if let Some(i) = init {
                lower_stmt(i);
            }
            if let Some(c) = cond {
                lower_expr(c);
            }
            if let Some(u) = update {
                lower_expr(u);
            }
            lower_stmt(body);
        }
        Stmt::ForIn { right, body, .. } | Stmt::ForOf { right, body, .. } => {
            lower_expr(right);
            lower_stmt(body);
        }
        Stmt::Break | Stmt::Continue => {}
        Stmt::Return(Some(expr)) | Stmt::Throw(expr) => lower_expr(expr),
        Stmt::Return(None) => {}
        Stmt::Try {
            body,
            catch_body,
            finally_body,
            ..
        } => {
            lower_stmt(body);
            if let Some(cb) = catch_body {
                lower_stmt(cb);
            }
            if let Some(fb) = finally_body {
                lower_stmt(fb);
            }
        }
        Stmt::Function(def) => lower_function_def(def),
        Stmt::Class {
            super_class,
            constructor,
            methods,
            ..
        } => {
            if let Some(sc) = super_class {
                lower_expr(sc);
            }
            if let Some(ctor) = constructor {
                lower_function_def(ctor);
            }
            for m in methods {
                for s in &mut m.body {
                    lower_stmt(s);
                }
            }
        }
        Stmt::Switch {
            discriminant,
            cases,
        } => {
            lower_expr(discriminant);
            for c in cases {
                if let Some(t) = &mut c.test {
                    lower_expr(t);
                }
                for s in &mut c.consequent {
                    lower_stmt(s);
                }
            }
        }
        Stmt::Import(_) => {}
        Stmt::Export(export_decl) => match export_decl {
            aluka_parser::ast::ExportDecl::Named { decl: Some(s), .. } => lower_stmt(s),
            aluka_parser::ast::ExportDecl::Default(expr) => lower_expr(expr),
            _ => {}
        },
    }
}

fn lower_function_def(def: &mut FunctionDef) {
    for s in &mut def.body {
        lower_stmt(s);
    }
}

/// 递归降级任意表达式中的 JSX 元素与片段
pub fn lower_expr(expr: &mut Expr) {
    match expr {
        Expr::JSXElement(el) => {
            *expr = lower_jsx_element_node(el);
        }
        Expr::JSXFragment(frag) => {
            *expr = lower_jsx_fragment_node(frag);
        }
        Expr::Unary { expr: inner, .. } => lower_expr(inner),
        Expr::Binary { left, right, .. } => {
            lower_expr(left);
            lower_expr(right);
        }
        Expr::Assign { value, .. } => lower_expr(value),
        Expr::Update { target, .. } => lower_expr(target),
        Expr::Conditional {
            cond,
            then_expr,
            else_expr,
        } => {
            lower_expr(cond);
            lower_expr(then_expr);
            lower_expr(else_expr);
        }
        Expr::Object(props) => {
            for p in props {
                if let PropKey::Computed(k) = &mut p.key {
                    lower_expr(k);
                }
                match &mut p.value {
                    PropValue::Expr(v) | PropValue::Spread(v) => lower_expr(v),
                    PropValue::Getter(def) | PropValue::Setter(def) => lower_function_def(def),
                }
            }
        }
        Expr::Array(elements) => {
            for e in elements {
                lower_expr(e);
            }
        }
        Expr::Spread(inner) | Expr::Await(inner) => lower_expr(inner),
        Expr::Member { obj, .. } | Expr::OptionalMember { obj, .. } => lower_expr(obj),
        Expr::Index { obj, index } | Expr::OptionalIndex { obj, index } => {
            lower_expr(obj);
            lower_expr(index);
        }
        Expr::MemberAssign { obj, value, .. } => {
            lower_expr(obj);
            lower_expr(value);
        }
        Expr::IndexAssign { obj, index, value } => {
            lower_expr(obj);
            lower_expr(index);
            lower_expr(value);
        }
        Expr::Call { callee, args }
        | Expr::OptionalCall { callee, args }
        | Expr::New { callee, args } => {
            lower_expr(callee);
            for a in args {
                lower_expr(a);
            }
        }
        Expr::MethodCall { receiver, args, .. } => {
            lower_expr(receiver);
            for a in args {
                lower_expr(a);
            }
        }
        Expr::Function(def) => lower_function_def(def),
        Expr::Yield {
            value: Some(val), ..
        } => lower_expr(val),
        Expr::TemplateLiteral { exprs, .. } => {
            for a in exprs {
                lower_expr(a);
            }
        }
        _ => {}
    }
}

fn lower_jsx_element_node(el: &mut JSXElement) -> Expr {
    let callee = Expr::Member {
        obj: Box::new(Expr::Ident("React".to_owned())),
        prop: "createElement".to_owned(),
    };

    let tag_arg = match &el.opening.name {
        JSXTagName::Ident(name) => {
            if is_component_ident_name(name) {
                Expr::Ident(name.clone())
            } else {
                Expr::String(name.clone())
            }
        }
        JSXTagName::Member { obj, prop } => Expr::Member {
            obj: Box::new(Expr::Ident(obj.clone())),
            prop: prop.clone(),
        },
    };

    let props_arg = if el.opening.attributes.is_empty() {
        Expr::Null
    } else {
        let mut props = Vec::with_capacity(el.opening.attributes.len());
        for attr in &mut el.opening.attributes {
            match attr {
                JSXAttribute::Named { name, value } => {
                    let val = match value {
                        None => Expr::Boolean(true),
                        Some(JSXAttrValue::String(s)) => Expr::String(s.clone()),
                        Some(JSXAttrValue::Expr(e)) => {
                            lower_expr(e);
                            e.clone()
                        }
                    };
                    props.push(ObjectProp {
                        key: PropKey::Literal(name.clone()),
                        value: PropValue::Expr(val),
                    });
                }
                JSXAttribute::Spread(arg) => {
                    lower_expr(arg);
                    props.push(ObjectProp {
                        key: PropKey::Literal(String::new()),
                        value: PropValue::Spread(arg.clone()),
                    });
                }
            }
        }
        Expr::Object(props)
    };

    let mut args = vec![tag_arg, props_arg];

    for child in &mut el.children {
        match child {
            JSXChild::Element(sub_el) => {
                args.push(lower_jsx_element_node(sub_el));
            }
            JSXChild::Fragment(sub_frag) => {
                args.push(lower_jsx_fragment_node(sub_frag));
            }
            JSXChild::Text(text) => {
                let cleaned = clean_jsx_text(text);
                if !cleaned.is_empty() {
                    args.push(Expr::String(cleaned));
                }
            }
            JSXChild::Expr(expr) => {
                lower_expr(expr);
                args.push(expr.clone());
            }
        }
    }

    Expr::Call {
        callee: Box::new(callee),
        args,
    }
}

fn lower_jsx_fragment_node(frag: &mut JSXFragment) -> Expr {
    let callee = Expr::Member {
        obj: Box::new(Expr::Ident("React".to_owned())),
        prop: "createElement".to_owned(),
    };

    let tag_arg = Expr::Member {
        obj: Box::new(Expr::Ident("React".to_owned())),
        prop: "Fragment".to_owned(),
    };

    let props_arg = Expr::Null;
    let mut args = vec![tag_arg, props_arg];

    for child in &mut frag.children {
        match child {
            JSXChild::Element(sub_el) => {
                args.push(lower_jsx_element_node(sub_el));
            }
            JSXChild::Fragment(sub_frag) => {
                args.push(lower_jsx_fragment_node(sub_frag));
            }
            JSXChild::Text(text) => {
                let cleaned = clean_jsx_text(text);
                if !cleaned.is_empty() {
                    args.push(Expr::String(cleaned));
                }
            }
            JSXChild::Expr(expr) => {
                lower_expr(expr);
                args.push(expr.clone());
            }
        }
    }

    Expr::Call {
        callee: Box::new(callee),
        args,
    }
}

fn is_component_ident_name(name: &str) -> bool {
    name.chars()
        .next()
        .is_some_and(|c| c.is_uppercase() || c == '_' || c == '$')
}

fn clean_jsx_text(raw: &str) -> String {
    let lines: Vec<&str> = raw.split('\n').collect();
    let mut cleaned = Vec::new();
    for (i, line) in lines.iter().enumerate() {
        let trimmed = line.trim();
        if !trimmed.is_empty() || (i > 0 && i < lines.len() - 1) {
            cleaned.push(trimmed);
        }
    }
    cleaned.join(" ").trim().to_owned()
}
