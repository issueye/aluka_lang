//! AST → 字节码编译器门面。
//!
//! 提供语法树遍历生成、跳转回填及函数模板导出。

pub mod codegen;
pub mod error;
pub mod scope;

pub use codegen::{backpatch_jump, compile, emit_jump};
pub use error::CompileError;
pub use scope::CompiledUnit;
