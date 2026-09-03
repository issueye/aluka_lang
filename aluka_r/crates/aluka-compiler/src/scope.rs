//! 编译产物单元与符号作用域定义。

use aluka_bytecode::{Constant, FuncTemplate, Instr};
use std::collections::HashMap;

/// 一个函数（或顶层脚本）的编译产物单元。
#[derive(Debug, Clone, Default, PartialEq)]
pub struct CompiledUnit {
    /// 指令序列
    pub code: Vec<Instr>,
    /// 常量池
    pub constants: Vec<Constant>,
    /// 局部槽位数
    pub locals: usize,
    /// 预估最大栈深
    pub max_stack: u32,
    /// 符号到局部变量槽位的映射表
    pub symbol_map: HashMap<String, usize>,
}

impl CompiledUnit {
    /// 转换为可被 Verifier 校验和 VM 执行的函数模板。
    #[must_use]
    pub fn to_func_template(self, name: &str) -> FuncTemplate {
        let max_stack = if self.max_stack == 0 {
            (self.code.len() as u32).max(16)
        } else {
            self.max_stack
        };

        FuncTemplate {
            name: name.to_owned(),
            num_params: 0,
            num_locals: self.locals as u32,
            is_var_args: false,
            is_generator: false,
            is_async: false,
            is_arrow: false,
            code: self.code,
            max_stack,
            source_file: "compiled.js".to_owned(),
            constants: self.constants,
            upvalues: Vec::new(),
            try_table: Vec::new(),
        }
    }
}
