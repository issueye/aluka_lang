//! AST 到字节码的代码生成器。

use crate::scope::CompiledUnit;
use aluka_bytecode::{Constant, Instr, Op};
use aluka_parser::ast::{Expr, Program, Stmt};

/// `PushInt` 立即值能表示的上界（24 位操作数）。超过它的数值走常量池。
const MAX_IMMEDIATE: f64 = ((1u32 << 24) - 1) as f64;

/// 把语法树编译成字节码产物单元。
#[must_use]
pub fn compile(program: &Program) -> CompiledUnit {
    let mut unit = CompiledUnit::default();
    let num_stmts = program.body.len();
    for (i, stmt) in program.body.iter().enumerate() {
        let is_last = i == num_stmts - 1;
        compile_stmt(stmt, &mut unit, is_last);
    }
    // 若最后一条语句没有留下返回值，补充 ReturnUndef
    if unit.code.is_empty()
        || !matches!(
            unit.code.last().map(|i| i.op),
            Some(Op::Return | Op::ReturnUndef)
        )
    {
        unit.code.push(Instr::new(Op::Return, 0));
    }
    unit
}

fn compile_stmt(stmt: &Stmt, unit: &mut CompiledUnit, is_last: bool) {
    match stmt {
        Stmt::Expr(expr) => {
            compile_expr(expr, unit);
            if !is_last {
                // 非末尾纯表达式语句，求值后弹栈保持栈平衡
                unit.code.push(Instr::new(Op::Pop, 0));
            }
        }
        Stmt::VarDecl { name, init } => {
            let slot = if let Some(&s) = unit.symbol_map.get(name) {
                s
            } else {
                let s = unit.locals;
                unit.locals += 1;
                unit.symbol_map.insert(name.clone(), s);
                s
            };

            if let Some(init_expr) = init {
                compile_expr(init_expr, unit);
            } else {
                unit.code.push(Instr::new(Op::PushUndefined, 0));
            }

            unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
            if is_last {
                unit.code.push(Instr::new(Op::PushUndefined, 0));
            }
        }
        Stmt::Block(stmts) => {
            let n = stmts.len();
            for (j, s) in stmts.iter().enumerate() {
                compile_stmt(s, unit, is_last && j == n - 1);
            }
        }
        Stmt::If {
            cond,
            then_branch,
            else_branch,
        } => {
            compile_expr(cond, unit);
            let jmp_false_idx = emit_jump(unit, Op::JmpFalsePop);
            compile_stmt(then_branch, unit, false);
            if let Some(else_stmt) = else_branch {
                let jmp_end_idx = emit_jump(unit, Op::Jmp);
                let else_start = unit.code.len();
                backpatch_jump(unit, jmp_false_idx, else_start);
                compile_stmt(else_stmt, unit, false);
                let end_idx = unit.code.len();
                backpatch_jump(unit, jmp_end_idx, end_idx);
            } else {
                let end_idx = unit.code.len();
                backpatch_jump(unit, jmp_false_idx, end_idx);
            }
            if is_last {
                unit.code.push(Instr::new(Op::PushUndefined, 0));
            }
        }
        Stmt::While { cond, body } => {
            let loop_start = unit.code.len();
            compile_expr(cond, unit);
            let exit_jmp_idx = emit_jump(unit, Op::JmpFalsePop);
            compile_stmt(body, unit, false);
            let loop_jmp_idx = emit_jump(unit, Op::Jmp);
            backpatch_jump(unit, loop_jmp_idx, loop_start);
            let loop_end = unit.code.len();
            backpatch_jump(unit, exit_jmp_idx, loop_end);
            if is_last {
                unit.code.push(Instr::new(Op::PushUndefined, 0));
            }
        }
    }
}

/// 发射占位跳转指令，返回该跳转指令在代码流中的索引。
pub fn emit_jump(unit: &mut CompiledUnit, op: Op) -> usize {
    let idx = unit.code.len();
    unit.code.push(Instr::new(op, 0));
    idx
}

/// 回填跳转指令的有符号相对字节偏移。
pub fn backpatch_jump(unit: &mut CompiledUnit, jump_idx: usize, target_idx: usize) {
    let offset_bytes = (target_idx as i32 - (jump_idx as i32 + 1)) * 4;
    let operand = (offset_bytes as u32) & 0x00FF_FFFF;
    unit.code[jump_idx].operand = operand;
}

fn add_constant(unit: &mut CompiledUnit, c: Constant) -> u32 {
    if let Some(pos) = unit.constants.iter().position(|x| *x == c) {
        pos as u32
    } else {
        let idx = unit.constants.len() as u32;
        unit.constants.push(c);
        idx
    }
}

fn compile_expr(expr: &Expr, unit: &mut CompiledUnit) {
    match expr {
        Expr::Number(n) => {
            if *n >= 0.0 && n.fract() == 0.0 && *n <= MAX_IMMEDIATE {
                unit.code.push(Instr::new(Op::PushInt, *n as u32));
            } else if *n < 0.0 && n.fract() == 0.0 && -*n <= MAX_IMMEDIATE {
                unit.code.push(Instr::new(Op::PushNegInt, (-*n) as u32));
            } else {
                let idx = add_constant(unit, Constant::Number(*n));
                unit.code.push(Instr::new(Op::PushConst, idx));
            }
        }
        Expr::Boolean(true) => {
            unit.code.push(Instr::new(Op::PushTrue, 0));
        }
        Expr::Boolean(false) => {
            unit.code.push(Instr::new(Op::PushFalse, 0));
        }
        Expr::Null => {
            unit.code.push(Instr::new(Op::PushNull, 0));
        }
        Expr::Undefined => {
            unit.code.push(Instr::new(Op::PushUndefined, 0));
        }
        Expr::String(s) => {
            let idx = add_constant(unit, Constant::String(s.clone()));
            unit.code.push(Instr::new(Op::PushConst, idx));
        }
        Expr::Ident(name) => {
            if let Some(&slot) = unit.symbol_map.get(name) {
                unit.code.push(Instr::new(Op::LoadLocal, slot as u32));
            } else {
                unit.code.push(Instr::new(Op::PushUndefined, 0));
            }
        }
        Expr::Assign { name, value } => {
            let slot = if let Some(&s) = unit.symbol_map.get(name) {
                s
            } else {
                let s = unit.locals;
                unit.locals += 1;
                unit.symbol_map.insert(name.clone(), s);
                s
            };
            compile_expr(value, unit);
            unit.code.push(Instr::new(Op::Dup, 0));
            unit.code.push(Instr::new(Op::StoreLocal, slot as u32));
        }
        Expr::Unary { op, expr } => {
            compile_expr(expr, unit);
            let opcode = match op.as_str() {
                "-" => Op::Neg,
                "+" => Op::UnaryPlus,
                "!" => Op::Not,
                "~" => Op::BitNot,
                _ => Op::Nop,
            };
            if opcode != Op::Nop {
                unit.code.push(Instr::new(opcode, 0));
            }
        }
        Expr::Binary { op, left, right } => {
            compile_expr(left, unit);
            compile_expr(right, unit);
            let opcode = match op.as_str() {
                "+" => Op::Add,
                "-" => Op::Sub,
                "*" => Op::Mul,
                "/" => Op::Div,
                "%" => Op::Mod,
                "**" => Op::Pow,
                "==" => Op::Eq,
                "!=" => Op::Ne,
                "===" => Op::StrictEq,
                "!==" => Op::StrictNe,
                "<" => Op::Lt,
                "<=" => Op::Le,
                ">" => Op::Gt,
                ">=" => Op::Ge,
                "&" => Op::BitAnd,
                "|" => Op::BitOr,
                "^" => Op::BitXor,
                "<<" => Op::Shl,
                ">>" => Op::Shr,
                ">>>" => Op::UShr,
                _ => Op::Add,
            };
            unit.code.push(Instr::new(opcode, 0));
        }
        Expr::Conditional {
            cond,
            then_expr,
            else_expr,
        } => {
            compile_expr(cond, unit);
            let jmp_false_idx = emit_jump(unit, Op::JmpFalsePop);
            compile_expr(then_expr, unit);
            let jmp_end_idx = emit_jump(unit, Op::Jmp);
            let else_start = unit.code.len();
            backpatch_jump(unit, jmp_false_idx, else_start);
            compile_expr(else_expr, unit);
            let end_idx = unit.code.len();
            backpatch_jump(unit, jmp_end_idx, end_idx);
        }
        Expr::Object(pairs) => {
            for (k, v) in pairs {
                let k_idx = add_constant(unit, Constant::String(k.clone()));
                unit.code.push(Instr::new(Op::PushConst, k_idx));
                compile_expr(v, unit);
            }
            unit.code
                .push(Instr::new(Op::NewObject, pairs.len() as u32));
        }
        Expr::Array(elems) => {
            for elem in elems {
                compile_expr(elem, unit);
            }
            unit.code.push(Instr::new(Op::NewArray, elems.len() as u32));
        }
        Expr::Member { obj, prop } => {
            compile_expr(obj, unit);
            let p_idx = add_constant(unit, Constant::String(prop.clone()));
            unit.code.push(Instr::new(Op::GetProp, p_idx));
        }
        Expr::Index { obj, index } => {
            compile_expr(obj, unit);
            compile_expr(index, unit);
            unit.code.push(Instr::new(Op::GetElem, 0));
        }
        Expr::MemberAssign { obj, prop, value } => {
            compile_expr(value, unit);
            compile_expr(obj, unit);
            let p_idx = add_constant(unit, Constant::String(prop.clone()));
            unit.code.push(Instr::new(Op::SetProp, p_idx));
        }
        Expr::IndexAssign { obj, index, value } => {
            compile_expr(value, unit);
            compile_expr(index, unit);
            compile_expr(obj, unit);
            unit.code.push(Instr::new(Op::SetElem, 0));
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
        assert_eq!(unit.code[0], Instr::new(Op::PushConst, 0));
        assert_eq!(unit.constants, vec![Constant::Number(big)]);
    }
}
