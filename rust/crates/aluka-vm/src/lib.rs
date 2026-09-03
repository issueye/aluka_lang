//! Tier 0 字节码虚拟机。
//!
//! 执行 `aluka-compiler` 产出的指令序列：一个操作数栈加一组局部槽位，
//! 逐条解码分派。属性访问的内联缓存（IC）、异常与调用帧在 M1 落地。
//!
//! # 与 JIT 的关系
//!
//! Tier 0 是语义的**唯一权威**：JIT 的每条快路径都必须与这里逐值对拍
//! （Go 版的 jitdiff 三 tier 零失配纪律）。因此本 crate 的求值语义要写得
//! 直白、可读，宁可慢也不要出现"只有 JIT 知道的捷径"。

use aluka_bytecode::{Instr, Op};
use aluka_core::Value;

/// 执行失败的原因。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VmError {
    /// 指令要求的操作数不足（编译器或字节码损坏）
    StackUnderflow,
    /// 局部槽位越界
    LocalOutOfRange,
    /// 指令流结束前没有 `Return`
    MissingReturn,
}

impl std::fmt::Display for VmError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            VmError::StackUnderflow => write!(f, "operand stack underflow"),
            VmError::LocalOutOfRange => write!(f, "local slot out of range"),
            VmError::MissingReturn => write!(f, "instruction stream ended without return"),
        }
    }
}

impl std::error::Error for VmError {}

/// 一次执行的状态：操作数栈与局部槽位。
#[derive(Debug, Default)]
pub struct Vm {
    stack: Vec<Value>,
    locals: Vec<Value>,
}

impl Vm {
    /// 创建虚拟机，预留 `locals` 个局部槽位（初值 `undefined`）。
    #[must_use]
    pub fn new(locals: usize) -> Self {
        Self {
            stack: Vec::new(),
            locals: vec![Value::Undefined; locals],
        }
    }

    /// 执行指令序列，返回 `Return` 携带的值。
    pub fn run(&mut self, code: &[Instr]) -> Result<Value, VmError> {
        for instr in code {
            match instr.op {
                Op::PushUndefined => self.stack.push(Value::Undefined),
                Op::PushInt => self.stack.push(Value::Number(f64::from(instr.operand))),
                Op::LoadLocal => {
                    let slot = instr.operand as usize;
                    let value = *self.locals.get(slot).ok_or(VmError::LocalOutOfRange)?;
                    self.stack.push(value);
                }
                Op::StoreLocal => {
                    let slot = instr.operand as usize;
                    let value = self.stack.pop().ok_or(VmError::StackUnderflow)?;
                    *self.locals.get_mut(slot).ok_or(VmError::LocalOutOfRange)? = value;
                }
                Op::Pop => {
                    self.stack.pop().ok_or(VmError::StackUnderflow)?;
                }
                Op::Add => {
                    let right = self.stack.pop().ok_or(VmError::StackUnderflow)?;
                    let left = self.stack.pop().ok_or(VmError::StackUnderflow)?;
                    self.stack.push(add(left, right));
                }
                Op::Return => {
                    return self.stack.pop().ok_or(VmError::StackUnderflow);
                }
            }
        }
        Err(VmError::MissingReturn)
    }
}

/// 数值相加。
///
/// M0 只覆盖 Number；字符串拼接与 `ToPrimitive` 的完整谱系（对象、
/// BigInt 混用的 TypeError）在 M1 接入。
fn add(left: Value, right: Value) -> Value {
    match (left, right) {
        (Value::Number(a), Value::Number(b)) => Value::Number(a + b),
        _ => Value::Number(f64::NAN),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn runs_addition_and_returns_result() {
        let code = [
            Instr::new(Op::PushInt, 2),
            Instr::new(Op::PushInt, 3),
            Instr::new(Op::Add, 0),
            Instr::new(Op::Return, 0),
        ];
        let mut vm = Vm::new(0);
        match vm.run(&code) {
            Ok(Value::Number(n)) => assert_eq!(n, 5.0),
            other => panic!("expected Number(5), got {other:?}"),
        }
    }

    #[test]
    fn round_trips_a_value_through_a_local_slot() {
        let code = [
            Instr::new(Op::PushInt, 41),
            Instr::new(Op::StoreLocal, 0),
            Instr::new(Op::LoadLocal, 0),
            Instr::new(Op::PushInt, 1),
            Instr::new(Op::Add, 0),
            Instr::new(Op::Return, 0),
        ];
        let mut vm = Vm::new(1);
        match vm.run(&code) {
            Ok(Value::Number(n)) => assert_eq!(n, 42.0),
            other => panic!("expected Number(42), got {other:?}"),
        }
    }

    #[test]
    fn reports_underflow_instead_of_panicking() {
        let code = [Instr::new(Op::Add, 0), Instr::new(Op::Return, 0)];
        let mut vm = Vm::new(0);
        assert!(matches!(vm.run(&code), Err(VmError::StackUnderflow)));
    }

    #[test]
    fn reports_missing_return() {
        let code = [Instr::new(Op::PushInt, 1)];
        let mut vm = Vm::new(0);
        assert!(matches!(vm.run(&code), Err(VmError::MissingReturn)));
    }

    #[test]
    fn reports_local_out_of_range() {
        let code = [Instr::new(Op::LoadLocal, 5), Instr::new(Op::Return, 0)];
        let mut vm = Vm::new(1);
        assert!(matches!(vm.run(&code), Err(VmError::LocalOutOfRange)));
    }
}
