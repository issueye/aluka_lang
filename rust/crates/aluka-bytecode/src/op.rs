//! 指令定义与栈效果。

/// 操作码。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Op {
    /// 压入 `undefined`
    PushUndefined,
    /// 压入小整数（操作数为立即值）
    PushInt,
    /// 读局部槽位（操作数为槽位号）
    LoadLocal,
    /// 写局部槽位（操作数为槽位号）
    StoreLocal,
    /// 弹出栈顶
    Pop,
    /// 相加栈顶两值
    Add,
    /// 从函数返回栈顶值
    Return,
}

impl Op {
    /// 该指令对操作数栈深度的净影响（压入数 − 弹出数）。
    ///
    /// 优化器与校验器据此推导每个程序点的栈深。新增指令必须在这里登记，
    /// 否则 `match` 不穷尽、编译失败——这是"元数据是单一事实来源"的
    /// 编译期保证。
    #[must_use]
    pub fn stack_effect(self) -> i32 {
        match self {
            Op::PushUndefined | Op::PushInt | Op::LoadLocal => 1,
            Op::StoreLocal | Op::Pop | Op::Return | Op::Add => -1,
        }
    }
}

/// 一条指令：操作码 + 24 位操作数。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Instr {
    /// 操作码
    pub op: Op,
    /// 操作数（槽位号、立即值或常量池下标）
    pub operand: u32,
}

impl Instr {
    /// 构造一条指令。
    #[must_use]
    pub fn new(op: Op, operand: u32) -> Self {
        Self { op, operand }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stack_effects_balance_a_simple_addition() {
        // PushInt, PushInt, Add：压两个、合成一个，栈净增 1。
        let code = [
            Instr::new(Op::PushInt, 1),
            Instr::new(Op::PushInt, 2),
            Instr::new(Op::Add, 0),
        ];
        let depth: i32 = code.iter().map(|instr| instr.op.stack_effect()).sum();
        assert_eq!(depth, 1);
    }

    #[test]
    fn return_consumes_its_value() {
        assert_eq!(Op::Return.stack_effect(), -1);
    }
}
