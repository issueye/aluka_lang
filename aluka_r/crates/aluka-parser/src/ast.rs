//! 语法树节点。
//!
//! 节点保持"贴近源码"的形状（不做提前 desugar），使 `aluka-compiler` 能
//! 自行决定 lowering 策略——JSX、装饰器、可选链都在编译期展开。

/// 表达式。
#[derive(Debug, Clone, PartialEq)]
pub enum Expr {
    /// 数值字面量
    Number(f64),
    /// 布尔字面量
    Boolean(bool),
    /// 空值字面量
    Null,
    /// 未定义字面量
    Undefined,
    /// 字符串字面量
    String(String),
    /// 标识符引用
    Ident(String),
    /// 一元运算：`op expr`
    Unary {
        /// 运算符原文（如 "-", "!", "~", "+"）
        op: String,
        /// 操作数
        expr: Box<Expr>,
    },
    /// 二元运算：`left op right`
    Binary {
        /// 运算符原文
        op: String,
        /// 左操作数
        left: Box<Expr>,
        /// 右操作数
        right: Box<Expr>,
    },
    /// 赋值表达式：`name = value`
    Assign {
        /// 目标变量名
        name: String,
        /// 赋予的值表达式
        value: Box<Expr>,
    },
    /// 条件三元表达式：`cond ? then_expr : else_expr`
    Conditional {
        /// 条件表达式
        cond: Box<Expr>,
        /// 条件为真表达式
        then_expr: Box<Expr>,
        /// 条件为假表达式
        else_expr: Box<Expr>,
    },
    /// 对象字面量：`{ key1: val1, key2: val2 }`
    Object(Vec<(String, Expr)>),
    /// 数组字面量：`[elem1, elem2, ...]`
    Array(Vec<Expr>),
    /// 属性读取：`obj.prop`
    Member {
        /// 目标对象表达式
        obj: Box<Expr>,
        /// 属性名
        prop: String,
    },
    /// 下标读取：`obj[index]`
    Index {
        /// 目标对象表达式
        obj: Box<Expr>,
        /// 下标表达式
        index: Box<Expr>,
    },
    /// 属性赋值：`obj.prop = value`
    MemberAssign {
        /// 目标对象表达式
        obj: Box<Expr>,
        /// 属性名
        prop: String,
        /// 赋予的新值
        value: Box<Expr>,
    },
    /// 下标赋值：`obj[index] = value`
    IndexAssign {
        /// 目标对象表达式
        obj: Box<Expr>,
        /// 下标表达式
        index: Box<Expr>,
        /// 赋予的新值
        value: Box<Expr>,
    },
}

/// 语句。
#[derive(Debug, Clone, PartialEq)]
pub enum Stmt {
    /// 表达式语句
    Expr(Expr),
    /// 局部变量声明：`let name = init` 或 `const name = init`
    VarDecl {
        /// 变量名
        name: String,
        /// 初始值表达式
        init: Option<Expr>,
    },
    /// 代码块：`{ stmts... }`
    Block(Vec<Stmt>),
    /// 条件分支语句：`if (cond) then_branch else else_branch`
    If {
        /// 条件表达式
        cond: Expr,
        /// 条件为真执行的语句
        then_branch: Box<Stmt>,
        /// 条件为假执行的语句（可选）
        else_branch: Option<Box<Stmt>>,
    },
    /// While 循环语句：`while (cond) body`
    While {
        /// 循环条件表达式
        cond: Expr,
        /// 循环体语句
        body: Box<Stmt>,
    },
}

/// 一个模块或脚本的顶层。
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Program {
    /// 顶层语句序列
    pub body: Vec<Stmt>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn program_defaults_to_empty_body() {
        assert!(Program::default().body.is_empty());
    }

    #[test]
    fn binary_expr_nests_operands() {
        let expr = Expr::Binary {
            op: "+".to_owned(),
            left: Box::new(Expr::Number(1.0)),
            right: Box::new(Expr::Ident("x".to_owned())),
        };
        match expr {
            Expr::Binary { op, left, .. } => {
                assert_eq!(op, "+");
                assert_eq!(*left, Expr::Number(1.0));
            }
            other => panic!("expected binary expression, got {other:?}"),
        }
    }
}
