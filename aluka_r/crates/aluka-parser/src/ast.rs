//! 语法树节点。
//!
//! 节点保持"贴近源码"的形状（不做提前 desugar），使 `aluka-compiler` 能
//! 自行决定 lowering 策略——JSX、装饰器、可选链都在编译期展开。

/// 表达式。
#[derive(Debug, Clone, PartialEq)]
pub enum Expr {
    /// 数值字面量
    Number(f64),
    /// 标识符引用
    Ident(String),
    /// 二元运算：`left op right`
    Binary {
        /// 运算符原文
        op: String,
        /// 左操作数
        left: Box<Expr>,
        /// 右操作数
        right: Box<Expr>,
    },
}

/// 语句。
#[derive(Debug, Clone, PartialEq)]
pub enum Stmt {
    /// 表达式语句
    Expr(Expr),
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
