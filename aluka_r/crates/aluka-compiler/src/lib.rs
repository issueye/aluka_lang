//! AST → 字节码。
//!
//! 除了代码生成，这一层还承担 TypeScript 注解剥离与 JSX lowering——两者
//! 都不需要独立的转译器，直接在下降过程中跳过或展开即可（Go 版同款分工，
//! 见 `internal/engine/compiler/`）。

use aluka_bytecode::{Instr, Op};
use aluka_parser::ast::{Expr, Program, Stmt};

/// 一个函数（或顶层脚本）的编译产物。
#[derive(Debug, Clone, Default, PartialEq)]
pub struct CompiledUnit {
    /// 指令序列
    pub code: Vec<Instr>,
    /// 局部槽位数
    pub locals: usize,
}

/// `PushInt` 立即值能表示的上界（24 位操作数）。超过它的数值走常量池。
const MAX_IMMEDIATE: f64 = ((1u32 << 24) - 1) as f64;

/// 把语法树编译成字节码。
///
/// M0 阶段只覆盖数值字面量与二元加法，用于串通 parser → compiler → vm
/// 的数据流；完整的语句/表达式覆盖在 M1 落地。
#[must_use]
pub fn compile(program: &Program) -> CompiledUnit {
    let mut unit = CompiledUnit::default();
    for stmt in &program.body {
        match stmt {
            Stmt::Expr(expr) => compile_expr(expr, &mut unit),
        }
    }
    unit.code.push(Instr::new(Op::Return, 0));
    unit
}

fn compile_expr(expr: &Expr, unit: &mut CompiledUnit) {
    match expr {
        Expr::Number(n) => {
            // 立即值路径只覆盖 24 位内的非负整数；其余留给常量池（M1）。
            let fits_immediate = *n >= 0.0 && n.fract() == 0.0 && *n <= MAX_IMMEDIATE;
            let operand = if fits_immediate { *n as u32 } else { 0 };
            unit.code.push(Instr::new(Op::PushInt, operand));
        }
        Expr::Ident(_) => {
            // 作用域解析尚未实现：先压 undefined 占位，保持栈效果正确。
            unit.code.push(Instr::new(Op::PushUndefined, 0));
        }
        Expr::Binary { left, right, .. } => {
            // 求值顺序：左、右、运算——与 ECMAScript 一致。
            compile_expr(left, unit);
            compile_expr(right, unit);
            unit.code.push(Instr::new(Op::Add, 0));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compiles_number_literal_then_returns() {
        let program = Program {
            body: vec![Stmt::Expr(Expr::Number(7.0))],
        };
        let unit = compile(&program);
        assert_eq!(
            unit.code,
            vec![Instr::new(Op::PushInt, 7), Instr::new(Op::Return, 0)]
        );
    }

    #[test]
    fn compiles_addition_in_evaluation_order() {
        let program = Program {
            body: vec![Stmt::Expr(Expr::Binary {
                op: "+".to_owned(),
                left: Box::new(Expr::Number(1.0)),
                right: Box::new(Expr::Number(2.0)),
            })],
        };
        let unit = compile(&program);
        assert_eq!(
            unit.code,
            vec![
                Instr::new(Op::PushInt, 1),
                Instr::new(Op::PushInt, 2),
                Instr::new(Op::Add, 0),
                Instr::new(Op::Return, 0),
            ]
        );
    }

    #[test]
    fn oversized_literal_falls_back_off_the_immediate_path() {
        let big = f64::from(u32::MAX);
        let program = Program {
            body: vec![Stmt::Expr(Expr::Number(big))],
        };
        let unit = compile(&program);
        // 不走立即值：操作数为 0（常量池路径在 M1 接入）。
        assert_eq!(unit.code[0], Instr::new(Op::PushInt, 0));
    }
}
