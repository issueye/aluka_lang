//! 编译器错误类型定义。

/// 编译期发生的错误。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CompileError {
    /// 语法节点不受支持或暂未实现
    UnsupportedSyntax(String),
    /// 操作数超出编码限制
    OperandOverflow(String),
}

impl std::fmt::Display for CompileError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::UnsupportedSyntax(msg) => write!(f, "不支持的语法: {msg}"),
            Self::OperandOverflow(msg) => write!(f, "操作数溢出: {msg}"),
        }
    }
}

impl std::error::Error for CompileError {}
