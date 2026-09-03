//! 词法与语法分析。
//!
//! 把源码切成 token 流，再规约成 [`ast`] 里的语法树。TypeScript 的类型
//! 注解在这一层被识别并标记，实际剥离发生在 `aluka-compiler`——这与 Go 版
//! 的分工一致（parser 认语法，compiler 决定是否发射代码）。

pub mod ast;
pub mod lexer;

pub use ast::{Expr, Program, Stmt};
pub use lexer::{Lexer, Token, TokenKind};
