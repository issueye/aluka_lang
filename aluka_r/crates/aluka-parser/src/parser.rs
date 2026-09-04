//! 递归下降语法分析器（JS / TS 源码 → AST）。
//!
//! 支持 ECMAScript 核心文法、Class、Try/Catch、可选链，以及 TypeScript 类型注解零成本剥离。

use crate::ast::{
    ArrayPatternElem, ClassMethodDef, ExportDecl, ExportSpecifier, Expr, FunctionDef, ImportDecl,
    ImportSpecifier, JSXAttrValue, JSXAttribute, JSXChild, JSXElement, JSXFragment,
    JSXOpeningElement, JSXTagName, ObjectPatternProp, ObjectProp, Program, PropKey, PropValue,
    Stmt, SwitchCase, VarPattern,
};
use crate::lexer::{Lexer, Token, TokenKind};

/// 语法分析器。
pub struct Parser<'src> {
    tokens: Vec<Token>,
    pos: usize,
    _src: &'src str,
}

/// 解析源码文本为 AST 语法树。
#[must_use]
pub fn parse(src: &str) -> Program {
    let mut parser = Parser::new(src);
    parser.parse_program()
}

/// For 循环分类枚举
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ForLoopKind {
    ForIn,
    ForOf,
    Standard,
}

impl<'src> Parser<'src> {
    /// 创建语法解析器实例。
    #[must_use]
    pub fn new(src: &'src str) -> Self {
        let mut lexer = Lexer::new(src);
        let mut tokens = Vec::new();
        loop {
            let tok = lexer.next_token();
            let is_eof = tok.kind == TokenKind::Eof;
            tokens.push(tok);
            if is_eof {
                break;
            }
        }
        Self {
            tokens,
            pos: 0,
            _src: src,
        }
    }

    fn peek(&self) -> &Token {
        if self.pos < self.tokens.len() {
            &self.tokens[self.pos]
        } else {
            self.tokens.last().unwrap()
        }
    }

    fn peek_ahead(&self, n: usize) -> &Token {
        let idx = self.pos + n;
        if idx < self.tokens.len() {
            &self.tokens[idx]
        } else {
            self.tokens.last().unwrap()
        }
    }

    fn advance(&mut self) -> Token {
        let tok = self.peek().clone();
        if self.pos < self.tokens.len() {
            self.pos += 1;
        }
        tok
    }

    fn check_punct(&self, p: &str) -> bool {
        matches!(&self.peek().kind, TokenKind::Punct(s) if s == p)
    }

    fn match_punct(&mut self, p: &str) -> bool {
        if self.check_punct(p) {
            self.advance();
            true
        } else {
            false
        }
    }

    fn check_keyword(&self, kw: &str) -> bool {
        matches!(&self.peek().kind, TokenKind::Keyword(s) if s == kw)
    }

    fn match_keyword(&mut self, kw: &str) -> bool {
        if self.check_keyword(kw) {
            self.advance();
            true
        } else {
            false
        }
    }

    fn expect_punct(&mut self, p: &str) -> Result<(), String> {
        if self.match_punct(p) {
            Ok(())
        } else {
            Err(format!("预期标点 '{}', 实为 '{:?}'", p, self.peek()))
        }
    }

    fn expect_keyword(&mut self, kw: &str) -> Result<(), String> {
        if self.match_keyword(kw) {
            Ok(())
        } else {
            Err(format!("预期关键字 '{}', 实为 '{:?}'", kw, self.peek()))
        }
    }

    fn scan_for_loop_kind(&self) -> ForLoopKind {
        let mut depth = 0;
        let mut idx = self.pos;
        while idx < self.tokens.len() {
            let tok = &self.tokens[idx];
            match &tok.kind {
                TokenKind::Punct(p) if p == "(" || p == "[" || p == "{" => {
                    depth += 1;
                }
                TokenKind::Punct(p) if p == ")" || p == "]" || p == "}" => {
                    if depth == 0 {
                        break;
                    }
                    depth -= 1;
                }
                TokenKind::Punct(p) if p == ";" && depth == 0 => {
                    return ForLoopKind::Standard;
                }
                TokenKind::Keyword(k) if k == "in" && depth == 0 => {
                    return ForLoopKind::ForIn;
                }
                TokenKind::Keyword(k) if k == "of" && depth == 0 => {
                    return ForLoopKind::ForOf;
                }
                TokenKind::Ident(id) if id == "of" && depth == 0 => {
                    return ForLoopKind::ForOf;
                }
                _ => {}
            }
            idx += 1;
        }
        ForLoopKind::Standard
    }

    /// 跳过可选的分号
    fn eat_semi(&mut self) {
        self.match_punct(";");
    }

    /// 跳过 TypeScript 类型注解（例如 `: number`, `: Array<string>`, `: (x: number) => void` 等）
    fn skip_type_annotation(&mut self) {
        if self.match_punct(":") {
            let mut paren_depth = 0;
            let mut angle_depth = 0;
            while self.pos < self.tokens.len() {
                let tok = self.peek();
                if let TokenKind::Punct(p) = &tok.kind {
                    match p.as_str() {
                        "(" => paren_depth += 1,
                        ")" => {
                            if paren_depth > 0 {
                                paren_depth -= 1;
                            } else {
                                break;
                            }
                        }
                        "<" => angle_depth += 1,
                        ">" if angle_depth > 0 => {
                            angle_depth -= 1;
                        }
                        "{" | "=" | ";" | "," if paren_depth == 0 && angle_depth == 0 => {
                            break;
                        }
                        _ => {}
                    }
                }
                self.advance();
            }
        }
    }

    /// 解析完整 Program
    pub fn parse_program(&mut self) -> Program {
        let mut body = Vec::new();
        while self.peek().kind != TokenKind::Eof {
            // 跳过 TS interface / type 声明
            if self.check_keyword("interface") {
                self.advance();
                // 跳过名字
                self.advance();
                // 跳过主体 `{ ... }`
                if self.match_punct("{") {
                    let mut depth = 1;
                    while depth > 0 && self.peek().kind != TokenKind::Eof {
                        if self.match_punct("{") {
                            depth += 1;
                        } else if self.match_punct("}") {
                            depth -= 1;
                        } else {
                            self.advance();
                        }
                    }
                }
                continue;
            }
            if self.check_keyword("type") {
                self.advance();
                while !self.check_punct(";") && self.peek().kind != TokenKind::Eof {
                    self.advance();
                }
                self.eat_semi();
                continue;
            }

            body.push(self.parse_stmt());
        }
        Program { body }
    }

    /// 解析语句
    pub fn parse_stmt(&mut self) -> Stmt {
        if (self.peek().kind == TokenKind::Keyword("import".to_owned())
            || self.peek().kind == TokenKind::Ident("import".to_owned()))
            && !self.peek_ahead(1).is_punct("(")
        {
            return self.parse_import_stmt();
        }

        if self.peek().kind == TokenKind::Keyword("export".to_owned())
            || self.peek().kind == TokenKind::Ident("export".to_owned())
        {
            return self.parse_export_stmt();
        }

        if self.match_punct("{") {
            let mut stmts = Vec::new();
            while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                stmts.push(self.parse_stmt());
            }
            let _ = self.expect_punct("}");
            return Stmt::Block(stmts);
        }

        if self.match_keyword("if") {
            let _ = self.expect_punct("(");
            let cond = self.parse_expr();
            let _ = self.expect_punct(")");
            let then_branch = Box::new(self.parse_stmt());
            let else_branch = if self.match_keyword("else") {
                Some(Box::new(self.parse_stmt()))
            } else {
                None
            };
            return Stmt::If {
                cond,
                then_branch,
                else_branch,
            };
        }

        if self.match_keyword("while") {
            let _ = self.expect_punct("(");
            let cond = self.parse_expr();
            let _ = self.expect_punct(")");
            let body = Box::new(self.parse_stmt());
            return Stmt::While { cond, body };
        }

        if self.match_keyword("do") {
            let body = Box::new(self.parse_stmt());
            let _ = self.match_keyword("while");
            let _ = self.expect_punct("(");
            let cond = self.parse_expr();
            let _ = self.expect_punct(")");
            self.eat_semi();
            return Stmt::DoWhile { body, cond };
        }

        if self.match_keyword("for") {
            let is_await = self.match_keyword("await");
            let _ = self.expect_punct("(");

            let loop_kind = self.scan_for_loop_kind();
            if loop_kind == ForLoopKind::ForIn || loop_kind == ForLoopKind::ForOf {
                if self.peek().kind == TokenKind::Keyword("let".to_owned())
                    || self.peek().kind == TokenKind::Keyword("const".to_owned())
                    || self.peek().kind == TokenKind::Keyword("var".to_owned())
                {
                    self.advance();
                }
                let pattern = if self.check_punct("[") || self.check_punct("{") {
                    self.parse_var_pattern()
                } else if let TokenKind::Ident(id) = self.advance().kind {
                    VarPattern::Ident(id)
                } else {
                    VarPattern::Ident("anonymous".to_owned())
                };

                if loop_kind == ForLoopKind::ForIn {
                    let _ = self.expect_keyword("in");
                    let right = self.parse_expr();
                    let _ = self.expect_punct(")");
                    let body = Box::new(self.parse_stmt());
                    return Stmt::ForIn {
                        pattern,
                        right,
                        body,
                    };
                } else {
                    if self.peek().kind == TokenKind::Keyword("of".to_owned())
                        || self.peek().kind == TokenKind::Ident("of".to_owned())
                    {
                        self.advance();
                    }
                    let right = self.parse_expr();
                    let _ = self.expect_punct(")");
                    let body = Box::new(self.parse_stmt());
                    return Stmt::ForOf {
                        is_await,
                        pattern,
                        right,
                        body,
                    };
                }
            }

            let init = if self.match_punct(";") {
                None
            } else if self.peek().kind == TokenKind::Keyword("let".to_owned())
                || self.peek().kind == TokenKind::Keyword("var".to_owned())
                || self.peek().kind == TokenKind::Keyword("const".to_owned())
            {
                Some(Box::new(self.parse_var_decl()))
            } else {
                let expr = self.parse_expr();
                let _ = self.expect_punct(";");
                Some(Box::new(Stmt::Expr(expr)))
            };

            let cond = if self.check_punct(";") {
                self.advance();
                None
            } else {
                let c = self.parse_expr();
                let _ = self.expect_punct(";");
                Some(c)
            };

            let update = if self.check_punct(")") {
                None
            } else {
                Some(self.parse_expr())
            };
            let _ = self.expect_punct(")");
            let body = Box::new(self.parse_stmt());
            return Stmt::For {
                init,
                cond,
                update,
                body,
            };
        }

        if self.match_keyword("break") {
            self.eat_semi();
            return Stmt::Break;
        }

        if self.match_keyword("continue") {
            self.eat_semi();
            return Stmt::Continue;
        }

        if self.match_keyword("throw") {
            let expr = self.parse_expr();
            self.eat_semi();
            return Stmt::Throw(expr);
        }

        if self.match_keyword("return") {
            let expr = if self.check_punct(";")
                || self.check_punct("}")
                || self.peek().kind == TokenKind::Eof
            {
                None
            } else {
                Some(self.parse_expr())
            };
            self.eat_semi();
            return Stmt::Return(expr);
        }

        if self.match_keyword("try") {
            let body = Box::new(self.parse_stmt());
            let mut catch_param = None;
            let mut catch_body = None;
            if self.match_keyword("catch") {
                if self.match_punct("(") {
                    if let TokenKind::Ident(id) = self.peek().kind.clone() {
                        self.advance();
                        catch_param = Some(id);
                        self.skip_type_annotation();
                    }
                    let _ = self.expect_punct(")");
                }
                catch_body = Some(Box::new(self.parse_stmt()));
            }
            let finally_body = if self.match_keyword("finally") {
                Some(Box::new(self.parse_stmt()))
            } else {
                None
            };
            return Stmt::Try {
                body,
                catch_param,
                catch_body,
                finally_body,
            };
        }

        if self.peek().kind == TokenKind::Keyword("let".to_owned())
            || self.peek().kind == TokenKind::Keyword("const".to_owned())
            || self.peek().kind == TokenKind::Keyword("var".to_owned())
        {
            return self.parse_var_decl();
        }

        if self.match_keyword("async") {
            if self.match_keyword("function") {
                let is_generator = self.match_punct("*");
                let mut def = self.parse_function_def();
                def.is_async = true;
                def.is_generator = is_generator;
                return Stmt::Function(def);
            }
            self.pos -= 1;
        }

        if self.match_keyword("function") {
            let is_generator = self.match_punct("*");
            let mut def = self.parse_function_def();
            def.is_generator = is_generator;
            return Stmt::Function(def);
        }

        if self.match_keyword("class") {
            return self.parse_class_stmt();
        }

        if self.match_keyword("switch") {
            let _ = self.expect_punct("(");
            let discriminant = self.parse_expr();
            let _ = self.expect_punct(")");
            let _ = self.expect_punct("{");
            let mut cases = Vec::new();
            while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                if self.match_keyword("case") {
                    let test = self.parse_expr();
                    let _ = self.expect_punct(":");
                    let mut consequent = Vec::new();
                    while !self.check_keyword("case")
                        && !self.check_keyword("default")
                        && !self.check_punct("}")
                        && self.peek().kind != TokenKind::Eof
                    {
                        consequent.push(self.parse_stmt());
                    }
                    cases.push(SwitchCase {
                        test: Some(test),
                        consequent,
                    });
                } else if self.match_keyword("default") {
                    let _ = self.expect_punct(":");
                    let mut consequent = Vec::new();
                    while !self.check_keyword("case")
                        && !self.check_keyword("default")
                        && !self.check_punct("}")
                        && self.peek().kind != TokenKind::Eof
                    {
                        consequent.push(self.parse_stmt());
                    }
                    cases.push(SwitchCase {
                        test: None,
                        consequent,
                    });
                } else {
                    self.advance();
                }
            }
            let _ = self.expect_punct("}");
            return Stmt::Switch {
                discriminant,
                cases,
            };
        }

        // 默认作为表达式语句
        let expr = self.parse_expr();
        self.eat_semi();
        Stmt::Expr(expr)
    }

    fn parse_var_pattern(&mut self) -> VarPattern {
        if self.match_punct("[") {
            let mut elements = Vec::new();
            while !self.check_punct("]") && self.peek().kind != TokenKind::Eof {
                if self.match_punct("...") {
                    let name = if let TokenKind::Ident(id) = self.advance().kind {
                        id
                    } else {
                        String::new()
                    };
                    elements.push(ArrayPatternElem {
                        name,
                        is_rest: true,
                    });
                    break;
                } else if let TokenKind::Ident(id) = self.advance().kind {
                    elements.push(ArrayPatternElem {
                        name: id,
                        is_rest: false,
                    });
                }
                if !self.match_punct(",") {
                    break;
                }
            }
            let _ = self.expect_punct("]");
            VarPattern::Array(elements)
        } else if self.match_punct("{") {
            let mut props = Vec::new();
            while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                let key = if let TokenKind::Ident(id) = self.advance().kind {
                    id
                } else {
                    String::new()
                };
                let value = if self.match_punct(":") {
                    self.parse_var_pattern()
                } else {
                    VarPattern::Ident(key.clone())
                };
                props.push(ObjectPatternProp { key, value });
                if !self.match_punct(",") {
                    break;
                }
            }
            let _ = self.expect_punct("}");
            VarPattern::Object(props)
        } else if let TokenKind::Ident(id) = self.advance().kind {
            VarPattern::Ident(id)
        } else {
            VarPattern::Ident("anonymous".to_owned())
        }
    }

    fn parse_var_decl(&mut self) -> Stmt {
        self.advance(); // 跳过 let / const / var
        if self.check_punct("[") || self.check_punct("{") {
            let pattern = self.parse_var_pattern();
            let _ = self.expect_punct("=");
            let init = self.parse_expr();
            self.eat_semi();
            return Stmt::DestructureDecl { pattern, init };
        }

        let name = if let TokenKind::Ident(id) = self.advance().kind {
            id
        } else {
            "anonymous".to_owned()
        };
        self.skip_type_annotation();
        let init = if self.match_punct("=") {
            Some(self.parse_expr())
        } else {
            None
        };
        self.eat_semi();
        Stmt::VarDecl { name, init }
    }

    fn parse_function_def(&mut self) -> FunctionDef {
        let name = if let TokenKind::Ident(id) = self.peek().kind.clone() {
            self.advance();
            id
        } else {
            String::new()
        };
        let _ = self.expect_punct("(");
        let mut params = Vec::new();
        let mut is_var_args = false;
        while !self.check_punct(")") && self.peek().kind != TokenKind::Eof {
            if self.match_punct("...") {
                is_var_args = true;
            }
            if let TokenKind::Ident(param_name) = self.advance().kind {
                params.push(param_name);
                self.skip_type_annotation();
            }
            if !self.match_punct(",") {
                break;
            }
        }
        let _ = self.expect_punct(")");
        self.skip_type_annotation(); // 函数返回值类型
        let body_stmt = self.parse_stmt();
        let body = match body_stmt {
            Stmt::Block(stmts) => stmts,
            other => vec![other],
        };
        FunctionDef {
            name,
            params,
            is_var_args,
            body,
            is_async: false,
            is_generator: false,
        }
    }

    fn parse_class_stmt(&mut self) -> Stmt {
        let name = if let TokenKind::Ident(id) = self.advance().kind {
            id
        } else {
            "AnonymousClass".to_owned()
        };
        let super_class = if self.match_keyword("extends") {
            Some(self.parse_expr_primary())
        } else {
            None
        };

        let _ = self.expect_punct("{");
        let mut constructor = None;
        let mut methods = Vec::new();

        while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
            let is_static = self.match_keyword("static");
            let m_name = if let TokenKind::Ident(id) = self.peek().kind.clone() {
                self.advance();
                id
            } else if let TokenKind::Keyword(kw) = self.peek().kind.clone() {
                self.advance();
                kw
            } else {
                break;
            };

            let _ = self.expect_punct("(");
            let mut params = Vec::new();
            while !self.check_punct(")") && self.peek().kind != TokenKind::Eof {
                if let TokenKind::Ident(p) = self.advance().kind {
                    params.push(p);
                    self.skip_type_annotation();
                }
                if !self.match_punct(",") {
                    break;
                }
            }
            let _ = self.expect_punct(")");
            self.skip_type_annotation();
            let body_stmt = self.parse_stmt();
            let body = match body_stmt {
                Stmt::Block(stmts) => stmts,
                other => vec![other],
            };

            if m_name == "constructor" {
                constructor = Some(FunctionDef {
                    name: format!("{name}_constructor"),
                    params,
                    is_var_args: false,
                    body,
                    is_async: false,
                    is_generator: false,
                });
            } else {
                methods.push(ClassMethodDef {
                    name: m_name,
                    params,
                    body,
                    is_static,
                    kind: 0,
                });
            }
        }
        let _ = self.expect_punct("}");

        Stmt::Class {
            name,
            super_class,
            constructor,
            methods,
        }
    }

    /// 解析表达式入口
    pub fn parse_expr(&mut self) -> Expr {
        self.parse_assignment()
    }

    fn parse_assignment(&mut self) -> Expr {
        if self.match_keyword("yield") {
            let delegate = self.match_punct("*");
            let value = if !self.check_punct(";")
                && !self.check_punct(")")
                && !self.check_punct("]")
                && !self.check_punct("}")
                && !self.check_punct(",")
                && self.peek().kind != TokenKind::Eof
            {
                Some(Box::new(self.parse_assignment()))
            } else {
                None
            };
            return Expr::Yield { value, delegate };
        }

        let expr = self.parse_conditional();
        if self.match_punct("=") {
            let val = self.parse_assignment();
            return match expr {
                Expr::Ident(name) => Expr::Assign {
                    name,
                    value: Box::new(val),
                },
                Expr::Member { obj, prop } => Expr::MemberAssign {
                    obj,
                    prop,
                    value: Box::new(val),
                },
                Expr::Index { obj, index } => Expr::IndexAssign {
                    obj,
                    index,
                    value: Box::new(val),
                },
                other => other,
            };
        }

        if let TokenKind::Punct(p) = &self.peek().kind {
            let p_str = p.clone();
            let compound_ops = [
                "+=", "-=", "*=", "/=", "%=", "**=", "<<=", ">>=", ">>>=", "&=", "|=", "^=",
            ];
            if compound_ops.contains(&p_str.as_str()) {
                self.advance();
                let bin_op = p_str.strip_suffix('=').unwrap().to_owned();
                let val = self.parse_assignment();
                return match expr {
                    Expr::Ident(name) => {
                        let rhs = Expr::Binary {
                            op: bin_op,
                            left: Box::new(Expr::Ident(name.clone())),
                            right: Box::new(val),
                        };
                        Expr::Assign {
                            name,
                            value: Box::new(rhs),
                        }
                    }
                    Expr::Member { obj, prop } => {
                        let rhs = Expr::Binary {
                            op: bin_op,
                            left: Box::new(Expr::Member {
                                obj: obj.clone(),
                                prop: prop.clone(),
                            }),
                            right: Box::new(val),
                        };
                        Expr::MemberAssign {
                            obj,
                            prop,
                            value: Box::new(rhs),
                        }
                    }
                    Expr::Index { obj, index } => {
                        let rhs = Expr::Binary {
                            op: bin_op,
                            left: Box::new(Expr::Index {
                                obj: obj.clone(),
                                index: index.clone(),
                            }),
                            right: Box::new(val),
                        };
                        Expr::IndexAssign {
                            obj,
                            index,
                            value: Box::new(rhs),
                        }
                    }
                    other => other,
                };
            }
        }
        expr
    }

    fn parse_conditional(&mut self) -> Expr {
        let cond = self.parse_nullish_or();
        if self.match_punct("?") {
            let then_expr = self.parse_assignment();
            let _ = self.expect_punct(":");
            let else_expr = self.parse_assignment();
            return Expr::Conditional {
                cond: Box::new(cond),
                then_expr: Box::new(then_expr),
                else_expr: Box::new(else_expr),
            };
        }
        cond
    }

    fn parse_nullish_or(&mut self) -> Expr {
        let mut left = self.parse_logical_or();
        while self.match_punct("??") {
            let right = self.parse_logical_or();
            left = Expr::Binary {
                op: "??".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_logical_or(&mut self) -> Expr {
        let mut left = self.parse_logical_and();
        while self.match_punct("||") {
            let right = self.parse_logical_and();
            left = Expr::Binary {
                op: "||".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_logical_and(&mut self) -> Expr {
        let mut left = self.parse_bitwise_or();
        while self.match_punct("&&") {
            let right = self.parse_bitwise_or();
            left = Expr::Binary {
                op: "&&".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_bitwise_or(&mut self) -> Expr {
        let mut left = self.parse_bitwise_xor();
        while self.match_punct("|") {
            let right = self.parse_bitwise_xor();
            left = Expr::Binary {
                op: "|".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_bitwise_xor(&mut self) -> Expr {
        let mut left = self.parse_bitwise_and();
        while self.match_punct("^") {
            let right = self.parse_bitwise_and();
            left = Expr::Binary {
                op: "^".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_bitwise_and(&mut self) -> Expr {
        let mut left = self.parse_equality();
        while self.match_punct("&") {
            let right = self.parse_equality();
            left = Expr::Binary {
                op: "&".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_equality(&mut self) -> Expr {
        let mut left = self.parse_relational();
        while let TokenKind::Punct(p) = &self.peek().kind {
            let op = p.clone();
            if op == "===" || op == "!==" || op == "==" || op == "!=" {
                self.advance();
                let right = self.parse_relational();
                left = Expr::Binary {
                    op,
                    left: Box::new(left),
                    right: Box::new(right),
                };
            } else {
                break;
            }
        }
        left
    }

    fn parse_relational(&mut self) -> Expr {
        let mut left = self.parse_shift();
        loop {
            let is_rel = match &self.peek().kind {
                TokenKind::Punct(p) if p == "<" || p == "<=" || p == ">" || p == ">=" => true,
                TokenKind::Keyword(k) if k == "instanceof" || k == "in" => true,
                _ => false,
            };
            if !is_rel {
                break;
            }
            let op = match self.advance().kind {
                TokenKind::Punct(p) => p,
                TokenKind::Keyword(k) => k,
                _ => unreachable!(),
            };
            let right = self.parse_shift();
            left = Expr::Binary {
                op,
                left: Box::new(left),
                right: Box::new(right),
            };
        }
        left
    }

    fn parse_shift(&mut self) -> Expr {
        let mut left = self.parse_additive();
        while let TokenKind::Punct(p) = &self.peek().kind {
            let op = p.clone();
            if op == "<<" || op == ">>" || op == ">>>" {
                self.advance();
                let right = self.parse_additive();
                left = Expr::Binary {
                    op,
                    left: Box::new(left),
                    right: Box::new(right),
                };
            } else {
                break;
            }
        }
        left
    }

    fn parse_additive(&mut self) -> Expr {
        let mut left = self.parse_multiplicative();
        while let TokenKind::Punct(p) = &self.peek().kind {
            let op = p.clone();
            if op == "+" || op == "-" {
                self.advance();
                let right = self.parse_multiplicative();
                left = Expr::Binary {
                    op,
                    left: Box::new(left),
                    right: Box::new(right),
                };
            } else {
                break;
            }
        }
        left
    }

    fn parse_multiplicative(&mut self) -> Expr {
        let mut left = self.parse_exponentiation();
        while let TokenKind::Punct(p) = &self.peek().kind {
            let op = p.clone();
            if op == "*" || op == "/" || op == "%" {
                self.advance();
                let right = self.parse_exponentiation();
                left = Expr::Binary {
                    op,
                    left: Box::new(left),
                    right: Box::new(right),
                };
            } else {
                break;
            }
        }
        left
    }

    fn parse_exponentiation(&mut self) -> Expr {
        let left = self.parse_unary();
        if self.match_punct("**") {
            let right = self.parse_exponentiation();
            Expr::Binary {
                op: "**".to_owned(),
                left: Box::new(left),
                right: Box::new(right),
            }
        } else {
            left
        }
    }

    fn parse_unary(&mut self) -> Expr {
        if self.match_keyword("await") {
            let sub = self.parse_unary();
            return Expr::Await(Box::new(sub));
        }
        if let TokenKind::Keyword(kw) = &self.peek().kind {
            let op = kw.clone();
            if op == "delete" || op == "typeof" || op == "void" {
                self.advance();
                let sub = self.parse_unary();
                return Expr::Unary {
                    op,
                    expr: Box::new(sub),
                };
            }
        }
        if let TokenKind::Punct(p) = &self.peek().kind {
            let op = p.clone();
            if op == "++" || op == "--" {
                self.advance();
                let sub = self.parse_unary();
                return Expr::Update {
                    op,
                    target: Box::new(sub),
                    prefix: true,
                };
            }
            if op == "-" || op == "+" || op == "!" || op == "~" {
                self.advance();
                let sub = self.parse_unary();
                return Expr::Unary {
                    op,
                    expr: Box::new(sub),
                };
            }
        }
        self.parse_postfix()
    }

    fn parse_postfix(&mut self) -> Expr {
        let mut expr = self.parse_expr_primary();
        loop {
            // 普通成员访问: obj.prop
            if self.match_punct(".") {
                if let TokenKind::Ident(prop) = self.advance().kind {
                    // 查看后续是否为方法调用
                    if self.match_punct("(") {
                        let args = self.parse_args();
                        expr = Expr::MethodCall {
                            receiver: Box::new(expr),
                            method: prop,
                            args,
                        };
                    } else {
                        expr = Expr::Member {
                            obj: Box::new(expr),
                            prop,
                        };
                    }
                }
                continue;
            }
            // 可选链访问: obj?.prop, obj?.[idx], callee?.(args)
            if self.match_punct("?.") {
                if self.match_punct("(") {
                    let args = self.parse_args();
                    expr = Expr::OptionalCall {
                        callee: Box::new(expr),
                        args,
                    };
                } else if self.match_punct("[") {
                    let idx = self.parse_expr();
                    let _ = self.expect_punct("]");
                    expr = Expr::OptionalIndex {
                        obj: Box::new(expr),
                        index: Box::new(idx),
                    };
                } else if let TokenKind::Ident(prop) = self.advance().kind {
                    expr = Expr::OptionalMember {
                        obj: Box::new(expr),
                        prop,
                    };
                }
                continue;
            }
            // 下标访问: obj[idx]
            if self.match_punct("[") {
                let index = self.parse_expr();
                let _ = self.expect_punct("]");
                expr = Expr::Index {
                    obj: Box::new(expr),
                    index: Box::new(index),
                };
                continue;
            }
            // 普通函数调用: fn(a, b)
            if self.match_punct("(") {
                let args = self.parse_args();
                expr = Expr::Call {
                    callee: Box::new(expr),
                    args,
                };
                continue;
            }
            // TypeScript `as Type` 断言零成本剥离
            if self.match_keyword("as") {
                // 跳过类型名
                self.advance();
                continue;
            }
            break;
        }

        // 后缀自增自减：i++ 或 i--
        if let TokenKind::Punct(p) = &self.peek().kind {
            if p == "++" || p == "--" {
                let op = p.clone();
                self.advance();
                return Expr::Update {
                    op,
                    target: Box::new(expr),
                    prefix: false,
                };
            }
        }

        expr
    }

    fn parse_args(&mut self) -> Vec<Expr> {
        let mut args = Vec::new();
        while !self.check_punct(")") && self.peek().kind != TokenKind::Eof {
            if self.match_punct("...") {
                let sub = self.parse_expr();
                args.push(Expr::Spread(Box::new(sub)));
            } else {
                args.push(self.parse_expr());
            }
            if !self.match_punct(",") {
                break;
            }
        }
        let _ = self.expect_punct(")");
        args
    }

    fn parse_expr_primary(&mut self) -> Expr {
        if self.is_jsx_start() {
            return self.parse_jsx_primary();
        }

        let tok = self.peek().clone();
        match tok.kind {
            TokenKind::Number(n) => {
                self.advance();
                Expr::Number(n)
            }
            TokenKind::BigInt(b) => {
                self.advance();
                Expr::BigInt(b)
            }
            TokenKind::String(s) => {
                self.advance();
                Expr::String(s)
            }
            TokenKind::Keyword(kw) => {
                self.advance();
                match kw.as_str() {
                    "true" => Expr::Boolean(true),
                    "false" => Expr::Boolean(false),
                    "null" => Expr::Null,
                    "undefined" => Expr::Undefined,
                    "this" => Expr::This,
                    "super" => Expr::Super,
                    "function" => {
                        let is_generator = self.match_punct("*");
                        let mut def = self.parse_function_def();
                        def.is_generator = is_generator;
                        Expr::Function(def)
                    }
                    "async" => {
                        if self.match_keyword("function") {
                            let is_generator = self.match_punct("*");
                            let mut def = self.parse_function_def();
                            def.is_async = true;
                            def.is_generator = is_generator;
                            Expr::Function(def)
                        } else {
                            Expr::Ident(kw)
                        }
                    }
                    "new" => {
                        let callee = self.parse_expr_primary();
                        let args = if self.match_punct("(") {
                            self.parse_args()
                        } else {
                            Vec::new()
                        };
                        Expr::New {
                            callee: Box::new(callee),
                            args,
                        }
                    }
                    _ => Expr::Ident(kw),
                }
            }
            TokenKind::Ident(id) => {
                if self.peek_ahead(1).kind == TokenKind::Punct("=>".to_owned()) {
                    self.advance(); // 消耗 id
                    self.advance(); // 消耗 =>
                    let body = self.parse_arrow_body();
                    Expr::Function(FunctionDef {
                        name: String::new(),
                        params: vec![id],
                        is_var_args: false,
                        body,
                        is_async: false,
                        is_generator: false,
                    })
                } else {
                    self.advance();
                    Expr::Ident(id)
                }
            }
            TokenKind::Punct(p) if p == "(" => {
                if self.is_arrow_function() {
                    self.advance(); // 消耗 (
                    let mut params = Vec::new();
                    let mut is_var_args = false;
                    while !self.check_punct(")") && self.peek().kind != TokenKind::Eof {
                        if self.match_punct("...") {
                            is_var_args = true;
                        }
                        if let TokenKind::Ident(p_name) = self.advance().kind {
                            params.push(p_name);
                            self.skip_type_annotation();
                        }
                        if !self.match_punct(",") {
                            break;
                        }
                    }
                    let _ = self.expect_punct(")");
                    self.skip_type_annotation();
                    let _ = self.expect_punct("=>");
                    let body = self.parse_arrow_body();
                    Expr::Function(FunctionDef {
                        name: String::new(),
                        params,
                        is_var_args,
                        body,
                        is_async: false,
                        is_generator: false,
                    })
                } else {
                    self.advance();
                    let expr = self.parse_expr();
                    let _ = self.expect_punct(")");
                    expr
                }
            }
            TokenKind::Punct(p) if p == "[" => {
                self.advance();
                let mut elements = Vec::new();
                while !self.check_punct("]") && self.peek().kind != TokenKind::Eof {
                    if self.match_punct("...") {
                        let sub = self.parse_expr();
                        elements.push(Expr::Spread(Box::new(sub)));
                    } else {
                        elements.push(self.parse_expr());
                    }
                    if !self.match_punct(",") {
                        break;
                    }
                }
                let _ = self.expect_punct("]");
                Expr::Array(elements)
            }
            TokenKind::Punct(p) if p == "{" => {
                self.advance();
                let mut props = Vec::new();
                while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                    // 0. 检查是否为对象展开属性: ...expr
                    if self.match_punct("...") {
                        let inner = self.parse_expr();
                        props.push(ObjectProp {
                            key: PropKey::Literal(String::new()),
                            value: PropValue::Spread(inner),
                        });
                        if !self.match_punct(",") {
                            break;
                        }
                        continue;
                    }

                    // 1. 检查是否为 getter: get prop() { ... }
                    if let TokenKind::Ident(ref id) = self.peek().kind {
                        if id == "get"
                            && !self.peek_ahead(1).is_punct(":")
                            && !self.peek_ahead(1).is_punct(",")
                            && !self.peek_ahead(1).is_punct("}")
                        {
                            self.advance(); // 消耗 "get"
                            let key = self.parse_prop_key();
                            let _ = self.expect_punct("(");
                            let _ = self.expect_punct(")");
                            let body_stmt = self.parse_stmt();
                            let body = match body_stmt {
                                Stmt::Block(stmts) => stmts,
                                other => vec![other],
                            };
                            props.push(ObjectProp {
                                key,
                                value: PropValue::Getter(FunctionDef {
                                    name: String::new(),
                                    params: Vec::new(),
                                    is_var_args: false,
                                    body,
                                    is_async: false,
                                    is_generator: false,
                                }),
                            });
                            if !self.match_punct(",") {
                                break;
                            }
                            continue;
                        } else if id == "set"
                            && !self.peek_ahead(1).is_punct(":")
                            && !self.peek_ahead(1).is_punct(",")
                            && !self.peek_ahead(1).is_punct("}")
                        {
                            self.advance(); // 消耗 "set"
                            let key = self.parse_prop_key();
                            let _ = self.expect_punct("(");
                            let param = if let TokenKind::Ident(param_name) = self.advance().kind {
                                param_name
                            } else {
                                String::new()
                            };
                            let _ = self.expect_punct(")");
                            let body_stmt = self.parse_stmt();
                            let body = match body_stmt {
                                Stmt::Block(stmts) => stmts,
                                other => vec![other],
                            };
                            props.push(ObjectProp {
                                key,
                                value: PropValue::Setter(FunctionDef {
                                    name: String::new(),
                                    params: if param.is_empty() {
                                        Vec::new()
                                    } else {
                                        vec![param]
                                    },
                                    is_var_args: false,
                                    body,
                                    is_async: false,
                                    is_generator: false,
                                }),
                            });
                            if !self.match_punct(",") {
                                break;
                            }
                            continue;
                        }
                    }

                    // 2. 普通属性、计算属性或方法简写
                    let key = self.parse_prop_key();
                    if self.match_punct("(") {
                        let mut params = Vec::new();
                        let mut is_var_args = false;
                        while !self.check_punct(")") && self.peek().kind != TokenKind::Eof {
                            if self.match_punct("...") {
                                is_var_args = true;
                            }
                            if let TokenKind::Ident(param_name) = self.advance().kind {
                                params.push(param_name);
                                self.skip_type_annotation();
                            }
                            if !self.match_punct(",") {
                                break;
                            }
                        }
                        let _ = self.expect_punct(")");
                        self.skip_type_annotation();
                        let body_stmt = self.parse_stmt();
                        let body = match body_stmt {
                            Stmt::Block(stmts) => stmts,
                            other => vec![other],
                        };
                        let m_name = match &key {
                            PropKey::Literal(n) => n.clone(),
                            _ => String::new(),
                        };
                        props.push(ObjectProp {
                            key,
                            value: PropValue::Expr(Expr::Function(FunctionDef {
                                name: m_name,
                                params,
                                is_var_args,
                                body,
                                is_async: false,
                                is_generator: false,
                            })),
                        });
                    } else {
                        let _ = self.expect_punct(":");
                        let val = self.parse_expr();
                        props.push(ObjectProp {
                            key,
                            value: PropValue::Expr(val),
                        });
                    }
                    if !self.match_punct(",") {
                        break;
                    }
                }
                let _ = self.expect_punct("}");
                Expr::Object(props)
            }
            TokenKind::Punct(ref p) if p == "/" || p == "/=" => {
                if let Some(regex) = self.parse_regexp_literal() {
                    regex
                } else {
                    self.advance();
                    Expr::Undefined
                }
            }
            _ => {
                self.advance();
                Expr::Undefined
            }
        }
    }

    /// 解析正则表达式字面量 `/pattern/flags` 并重同步 Token 游标
    fn parse_regexp_literal(&mut self) -> Option<Expr> {
        let tok = self.peek();
        let start = tok.start;
        let bytes = self._src.as_bytes();
        if start >= bytes.len() || bytes[start] != b'/' {
            return None;
        }
        let mut idx = start + 1;
        let mut in_class = false;
        let mut closed = false;
        while idx < bytes.len() {
            let b = bytes[idx];
            if b == b'\n' || b == b'\r' {
                break;
            }
            if b == b'\\' {
                idx += 1;
                if idx < bytes.len() {
                    idx += 1;
                }
                continue;
            }
            if b == b'[' {
                in_class = true;
            } else if b == b']' {
                in_class = false;
            } else if b == b'/' && !in_class {
                closed = true;
                break;
            }
            idx += 1;
        }

        if !closed {
            return None;
        }

        let pattern = self._src[start + 1..idx].to_owned();
        idx += 1; // 消耗闭合 '/'

        let flags_start = idx;
        while idx < bytes.len() && bytes[idx].is_ascii_alphabetic() {
            idx += 1;
        }
        let flags = self._src[flags_start..idx].to_owned();

        // 推进 tokens 游标至 idx 之后
        while self.pos < self.tokens.len() && self.tokens[self.pos].start < idx {
            self.pos += 1;
        }

        Some(Expr::RegExp { pattern, flags })
    }

    fn parse_prop_key(&mut self) -> PropKey {
        if self.match_punct("[") {
            let expr = self.parse_expr();
            let _ = self.expect_punct("]");
            PropKey::Computed(expr)
        } else if let TokenKind::Ident(k) = self.peek().kind.clone() {
            self.advance();
            PropKey::Literal(k)
        } else if let TokenKind::String(k) = self.peek().kind.clone() {
            self.advance();
            PropKey::Literal(k)
        } else if let TokenKind::Keyword(k) = self.peek().kind.clone() {
            self.advance();
            PropKey::Literal(k)
        } else {
            PropKey::Literal(String::new())
        }
    }

    fn is_arrow_function(&self) -> bool {
        if !self.check_punct("(") {
            return false;
        }
        let mut depth = 0;
        let mut i = self.pos;
        while i < self.tokens.len() {
            let tok = &self.tokens[i];
            if let TokenKind::Punct(p) = &tok.kind {
                if p == "(" {
                    depth += 1;
                } else if p == ")" {
                    depth -= 1;
                    if depth == 0 {
                        // 查看 ) 之后是否有 =>
                        let mut j = i + 1;
                        // 跳过类型注解 (e.g. `): void =>`)
                        if j < self.tokens.len()
                            && self.tokens[j].kind == TokenKind::Punct(":".to_owned())
                        {
                            j += 1;
                            while j < self.tokens.len()
                                && !matches!(&self.tokens[j].kind, TokenKind::Punct(p) if p == "=>" || p == ";")
                            {
                                j += 1;
                            }
                        }
                        if j < self.tokens.len() {
                            return self.tokens[j].kind == TokenKind::Punct("=>".to_owned());
                        }
                        return false;
                    }
                }
            }
            i += 1;
        }
        false
    }

    fn parse_arrow_body(&mut self) -> Vec<Stmt> {
        if self.check_punct("{") {
            let stmt = self.parse_stmt();
            match stmt {
                Stmt::Block(stmts) => stmts,
                other => vec![other],
            }
        } else {
            let expr = self.parse_assignment();
            vec![Stmt::Return(Some(expr))]
        }
    }

    /// 探测当前位置是否为 JSX 元素或 Fragment 开始 (<tag / <> / <App)
    fn is_jsx_start(&self) -> bool {
        if !self.check_punct("<") {
            return false;
        }
        // <> Fragment 开始
        if self.peek_ahead(1).is_punct(">") {
            return true;
        }
        // 紧跟标识符 (如 <div, <App, <UI.Button) 或部分关键字
        match &self.peek_ahead(1).kind {
            TokenKind::Ident(_) | TokenKind::Keyword(_) => {
                let p2 = self.peek_ahead(2);
                if p2.is_punct(">") || p2.is_punct("/") || p2.is_punct(".") || p2.is_punct("{") {
                    return true;
                }
                matches!(&p2.kind, TokenKind::Ident(_) | TokenKind::Keyword(_))
            }
            _ => false,
        }
    }

    /// 解析 JSX Primary 表达式（元素或片段）
    fn parse_jsx_primary(&mut self) -> Expr {
        if self.peek_ahead(1).is_punct(">") {
            Expr::JSXFragment(self.parse_jsx_fragment())
        } else {
            Expr::JSXElement(Box::new(self.parse_jsx_element()))
        }
    }

    /// 解析 <>children</>
    fn parse_jsx_fragment(&mut self) -> JSXFragment {
        let _ = self.expect_punct("<");
        let _ = self.expect_punct(">");
        let children = self.parse_jsx_children();
        JSXFragment { children }
    }

    /// 解析 <Tag attr="val">children</Tag> 或 <Tag />
    fn parse_jsx_element(&mut self) -> JSXElement {
        let _ = self.expect_punct("<");
        let tag_name = self.parse_jsx_tag_name();
        let (attributes, self_closing) = self.parse_jsx_attributes();

        if self_closing {
            return JSXElement {
                opening: JSXOpeningElement {
                    name: tag_name,
                    attributes,
                    self_closing: true,
                },
                children: Vec::new(),
            };
        }

        let children = self.parse_jsx_children();
        JSXElement {
            opening: JSXOpeningElement {
                name: tag_name,
                attributes,
                self_closing: false,
            },
            children,
        }
    }

    fn parse_jsx_tag_name(&mut self) -> JSXTagName {
        let name = match self.advance().kind {
            TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
            _ => "div".to_owned(),
        };
        if self.match_punct(".") {
            let prop = match self.advance().kind {
                TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                _ => "Unknown".to_owned(),
            };
            JSXTagName::Member { obj: name, prop }
        } else {
            JSXTagName::Ident(name)
        }
    }

    fn parse_jsx_attributes(&mut self) -> (Vec<JSXAttribute>, bool) {
        let mut attrs = Vec::new();
        loop {
            if self.match_punct("/>") || (self.check_punct("/") && self.peek_ahead(1).is_punct(">"))
            {
                let _ = self.match_punct("/");
                let _ = self.match_punct(">");
                return (attrs, true);
            }
            if self.match_punct(">") {
                return (attrs, false);
            }
            if self.peek().kind == TokenKind::Eof {
                break;
            }

            // {...props} 属性展开
            if self.check_punct("{") && self.peek_ahead(1).is_punct("...") {
                let _ = self.match_punct("{");
                let _ = self.match_punct("...");
                let expr = self.parse_expr();
                let _ = self.expect_punct("}");
                attrs.push(JSXAttribute::Spread(expr));
                continue;
            }

            // 属性名
            let mut attr_name = match self.advance().kind {
                TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                _ => break,
            };
            // 连字符属性名如 aria-label
            while self.match_punct("-") {
                match self.advance().kind {
                    TokenKind::Ident(s) | TokenKind::Keyword(s) => {
                        attr_name.push('-');
                        attr_name.push_str(&s);
                    }
                    _ => break,
                }
            }

            if self.match_punct("=") {
                if let TokenKind::String(s) = self.peek().kind.clone() {
                    self.advance();
                    attrs.push(JSXAttribute::Named {
                        name: attr_name,
                        value: Some(JSXAttrValue::String(s)),
                    });
                } else if self.match_punct("{") {
                    let expr = self.parse_expr();
                    let _ = self.expect_punct("}");
                    attrs.push(JSXAttribute::Named {
                        name: attr_name,
                        value: Some(JSXAttrValue::Expr(expr)),
                    });
                } else {
                    attrs.push(JSXAttribute::Named {
                        name: attr_name,
                        value: None,
                    });
                }
            } else {
                attrs.push(JSXAttribute::Named {
                    name: attr_name,
                    value: None,
                });
            }
        }
        (attrs, false)
    }

    fn parse_jsx_children(&mut self) -> Vec<JSXChild> {
        let mut children = Vec::new();
        loop {
            if self.peek().kind == TokenKind::Eof {
                break;
            }

            // 闭合标签 </Tag> 或 </>
            if self.check_punct("<") && self.peek_ahead(1).is_punct("/") {
                self.advance(); // <
                self.advance(); // /
                if !self.match_punct(">") {
                    // 消耗标签名及可能存在的点成员
                    self.advance();
                    while self.match_punct(".") {
                        self.advance();
                    }
                    let _ = self.expect_punct(">");
                }
                break;
            }

            // 嵌套元素或片段
            if self.check_punct("<") {
                if self.peek_ahead(1).is_punct(">") {
                    children.push(JSXChild::Fragment(self.parse_jsx_fragment()));
                } else {
                    children.push(JSXChild::Element(Box::new(self.parse_jsx_element())));
                }
                continue;
            }

            // 表达式插值 {expr}
            if self.match_punct("{") {
                if self.match_punct("}") {
                    continue;
                }
                let expr = self.parse_expr();
                let _ = self.expect_punct("}");
                children.push(JSXChild::Expr(expr));
                continue;
            }

            // 文本节点
            let mut text_tokens = Vec::new();
            while !self.check_punct("<")
                && !self.check_punct("{")
                && self.peek().kind != TokenKind::Eof
            {
                text_tokens.push(self.advance().text);
            }
            let raw_text = text_tokens.join(" ");
            let trimmed = raw_text.trim();
            if !trimmed.is_empty() {
                children.push(JSXChild::Text(raw_text));
            }
        }
        children
    }

    fn parse_import_stmt(&mut self) -> Stmt {
        self.advance(); // 消耗 import
        let mut specifiers = Vec::new();

        // 纯副作用导入：import "mod.css";
        if let TokenKind::String(s) = self.peek().kind.clone() {
            self.advance();
            self.eat_semi();
            return Stmt::Import(ImportDecl {
                source: s,
                specifiers,
            });
        }

        // 默认导入：import x from 'mod'; 或 import x, { a } from 'mod';
        if let TokenKind::Ident(id) = self.peek().kind.clone() {
            if id != "from" {
                self.advance();
                specifiers.push(ImportSpecifier::Default(id));
                if self.match_punct(",") {
                    // 可能后接 { ... } 或 * as ns
                }
            }
        }

        // 命名空间导入：import * as ns from 'mod';
        if self.match_punct("*") {
            let _ = self.expect_keyword("as");
            let ns = match self.advance().kind {
                TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                _ => "ns".to_owned(),
            };
            specifiers.push(ImportSpecifier::Namespace(ns));
        } else if self.match_punct("{") {
            // 命名导入：import { a, b as c } from 'mod';
            while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                let imported = match self.advance().kind {
                    TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                    _ => break,
                };
                let local = if self.match_keyword("as") {
                    match self.advance().kind {
                        TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                        _ => imported.clone(),
                    }
                } else {
                    imported.clone()
                };
                specifiers.push(ImportSpecifier::Named { local, imported });
                if !self.match_punct(",") {
                    break;
                }
            }
            let _ = self.expect_punct("}");
        }

        let _ = self.expect_keyword("from");
        let source = match self.advance().kind {
            TokenKind::String(s) => s,
            _ => String::new(),
        };
        self.eat_semi();
        Stmt::Import(ImportDecl { source, specifiers })
    }

    fn parse_export_stmt(&mut self) -> Stmt {
        self.advance(); // 消耗 export

        // export default ...
        if self.match_keyword("default") {
            let expr = if self.match_keyword("function") {
                let is_generator = self.match_punct("*");
                let mut def = self.parse_function_def();
                def.is_generator = is_generator;
                Expr::Function(def)
            } else {
                self.parse_expr()
            };
            self.eat_semi();
            return Stmt::Export(ExportDecl::Default(Box::new(expr)));
        }

        // export * from 'mod'; 或 export * as ns from 'mod';
        if self.match_punct("*") {
            let alias = if self.match_keyword("as") {
                match self.advance().kind {
                    TokenKind::Ident(s) | TokenKind::Keyword(s) => Some(s),
                    _ => None,
                }
            } else {
                None
            };
            let _ = self.expect_keyword("from");
            let source = match self.advance().kind {
                TokenKind::String(s) => s,
                _ => String::new(),
            };
            self.eat_semi();
            return Stmt::Export(ExportDecl::All { source, alias });
        }

        // export { a, b as c } [from 'mod'];
        if self.match_punct("{") {
            let mut specifiers = Vec::new();
            while !self.check_punct("}") && self.peek().kind != TokenKind::Eof {
                let local = match self.advance().kind {
                    TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                    _ => break,
                };
                let exported = if self.match_keyword("as") {
                    match self.advance().kind {
                        TokenKind::Ident(s) | TokenKind::Keyword(s) => s,
                        _ => local.clone(),
                    }
                } else {
                    local.clone()
                };
                specifiers.push(ExportSpecifier { local, exported });
                if !self.match_punct(",") {
                    break;
                }
            }
            let _ = self.expect_punct("}");
            let source = if self.match_keyword("from") {
                match self.advance().kind {
                    TokenKind::String(s) => Some(s),
                    _ => None,
                }
            } else {
                None
            };
            self.eat_semi();
            return Stmt::Export(ExportDecl::Named {
                decl: None,
                specifiers,
                source,
            });
        }

        // export const/let/var/function/class ...
        let inner = self.parse_stmt();
        Stmt::Export(ExportDecl::Named {
            decl: Some(Box::new(inner)),
            specifiers: Vec::new(),
            source: None,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_basic_statements_and_expressions() {
        let code = r#"
            let a: number = 10;
            const b = 20;
            if (a < b) {
                a = a + 5;
            } else {
                a = 0;
            }
            return a;
        "#;
        let prog = parse(code);
        assert_eq!(prog.body.len(), 4);
    }

    #[test]
    fn parses_classes_and_optional_chaining_and_try() {
        let code = r#"
            class User extends Person {
                constructor(name: string) {
                    this.name = name;
                }
                greet() {
                    return this.name?.toUpperCase();
                }
            }
            try {
                let u = new User("alice");
                let val = u?.greet();
            } catch (e) {
                return null;
            }
        "#;
        let prog = parse(code);
        assert_eq!(prog.body.len(), 2);
    }

    #[test]
    fn parses_jsx_elements_fragments_and_attributes() {
        let code = r#"
            let el = <div id="main" className="container" disabled>
                <h1>Title</h1>
                <p>Hello {user.name}!</p>
                <Custom.Button count={42} {...props} />
                <>fragment text</>
            </div>;
        "#;
        let prog = parse(code);
        assert_eq!(prog.body.len(), 1);
        if let Stmt::VarDecl {
            init: Some(Expr::JSXElement(el)),
            ..
        } = &prog.body[0]
        {
            assert_eq!(el.opening.name, JSXTagName::Ident("div".to_owned()));
            assert_eq!(el.opening.attributes.len(), 3);
            assert_eq!(el.children.len(), 4);
        } else {
            panic!("期望解析出 JSXElement 变量声明");
        }
    }

    #[test]
    fn parses_esm_import_and_export_declarations() {
        let code = r#"
            import React, { useState as useMyState } from 'react';
            import * as utils from './utils';
            import 'style.css';

            export const answer = 42;
            export default function run() { return answer; };
            export { a, b as c } from 'other';
            export * as ns from 'bar';
        "#;
        let prog = parse(code);
        assert_eq!(prog.body.len(), 7);
        assert!(matches!(prog.body[0], Stmt::Import(..)));
        assert!(matches!(prog.body[1], Stmt::Import(..)));
        assert!(matches!(prog.body[2], Stmt::Import(..)));
        assert!(matches!(prog.body[3], Stmt::Export(..)));
        assert!(matches!(prog.body[4], Stmt::Export(..)));
        assert!(matches!(prog.body[5], Stmt::Export(..)));
        assert!(matches!(prog.body[6], Stmt::Export(..)));
    }
}
