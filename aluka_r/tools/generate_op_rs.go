package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 指令事实条目
type 指令事实条目 struct {
	Opcode       uint8  `json:"opcode"`
	Ident        string `json:"ident"`
	Name         string `json:"name"`
	OperandKindId uint8 `json:"operand_kind_id"`
	OperandKind  string `json:"operand_kind"`
	OperandDesc  string `json:"operand_desc"`
	HasOperand   bool   `json:"has_operand"`
	Pops         uint8  `json:"pops"`
	Pushes       uint8  `json:"pushes"`
	StackEffect  string `json:"stack_effect"`
	PurePush     bool   `json:"pure_push"`
	IsJump       bool   `json:"is_jump"`
	IsTerminal   bool   `json:"is_terminal"`
	StackCond    bool   `json:"stack_cond"`
	VarStack     bool   `json:"var_stack"`
	SpecialNotes string `json:"special_notes"`
}

func main() {
	fmt.Println("开始基于 isa-facts.json 生成 aluka-bytecode::op 模块源码...")

	根目录, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取路径失败: %v\n", err)
		os.Exit(1)
	}

	json路径 := filepath.Join(根目录, ".work", "evidence", "20260904", "isa-facts.json")
	json数据, err := os.ReadFile(json路径)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 isa-facts.json 失败: %v\n", err)
		os.Exit(1)
	}

	var 事实列表 []指令事实条目
	if err := json.Unmarshal(json数据, &事实列表); err != nil {
		fmt.Fprintf(os.Stderr, "解析 JSON 失败: %v\n", err)
		os.Exit(1)
	}

	if len(事实列表) != 106 {
		fmt.Fprintf(os.Stderr, "事实条目数不等于 106，实为 %d\n", len(事实列表))
		os.Exit(1)
	}

	var 代码 strings.Builder

	代码.WriteString(`//! 指令定义、操作数语义与栈效果契约。
//!
//! 本文件是 aluka 虚拟机的单一事实来源（由 isa-facts 反推生成）。
//! 严格遵循 AGENTS.md 规范：所有元数据查询使用穷尽 match，禁止任何兜底分支，
//! 确保在编译期阻断未登记或形状不匹配的指令。

/// 指令操作数语义分类（对应 11 种 OperandKind）。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum OperandKind {
    /// 不读操作数域
    None,
    /// 常量池索引
    ConstIdx,
    /// 24位内联无符号整数
    Int,
    /// 局部槽位索引
    Slot,
    /// 闭包上值索引
    UpvalueIdx,
    /// 模块级函数/类模板索引
    TemplateIdx,
    /// Try 表索引
    TryIdx,
    /// 有符号相对跳转字节偏移
    SignedOff,
    /// 参数或元素数量
    Count,
    /// 打包槽位与属性名 (slot<<16 | nameIdx)
    PackedSlotName,
    /// 打包实参数量与方法名 (numArgs<<16 | nameIdx)
    PackedCall,
}

/// 栈效果类别。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StackEffect {
    /// 确定的净入栈/出栈差值（pushes - pops）
    Fixed(i32),
    /// 依赖运行时条件或跳转是否发生的条件变化
    Conditional,
    /// 依赖操作数或实参/元素个数的动态变化
    Variable,
}

/// 106 条完整 ISA 字节码操作码。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum Op {
`)

	for _, f := range 事实列表 {
		注释 := f.Name
		if f.SpecialNotes != "" {
			注释 += " - " + f.SpecialNotes
		}
		// 将 Go Ident 去掉前缀 "Op"，如 OpPushUndefined -> PushUndefined
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("    /// [%03d] %s (操作数: %s)\n", f.Opcode, 注释, f.OperandKind))
		代码.WriteString(fmt.Sprintf("    %s = %d,\n", rust变体名, f.Opcode))
	}

	代码.WriteString(`}

impl Op {
    /// 获取操作码的数值编码（0..105）。
    #[must_use]
    pub const fn opcode(self) -> u8 {
        self as u8
    }

    /// 从数值解码为操作码。
    #[must_use]
    pub fn from_opcode(byte: u8) -> Option<Self> {
        match byte {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            %d => Some(Op::%s),\n", f.Opcode, rust变体名))
	}

	代码.WriteString(`            _ => None,
        }
    }

    /// 规范大写指令名。
    #[must_use]
    pub const fn name(self) -> &'static str {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => \"%s\",\n", rust变体名, f.Name))
	}

	代码.WriteString(`        }
    }

    /// 操作数语义类别。
    #[must_use]
    pub const fn operand_kind(self) -> OperandKind {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		kind名 := strings.TrimPrefix(f.OperandKind, "Operand")
		代码.WriteString(fmt.Sprintf("            Op::%s => OperandKind::%s,\n", rust变体名, kind名))
	}

	代码.WriteString(`        }
    }

    /// 是否含有 3 字节有效操作数。
    #[must_use]
    pub fn has_operand(self) -> bool {
        !matches!(self.operand_kind(), OperandKind::None)
    }

    /// 该指令对操作数栈深度的静态净影响（压入数 − 弹出数）。
    ///
    /// 对于条件跳转短路（如 JMP_TRUE_KEEP）或动态操作数指令（如 CALL），
    /// 该静态函数返回 0。如需更精细类型请调用 Self::stack_effect_detailed。
    #[must_use]
    pub fn stack_effect(self) -> i32 {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		净变动 := int(f.Pushes) - int(f.Pops)
		if f.StackCond || f.VarStack {
			净变动 = 0
		}
		代码.WriteString(fmt.Sprintf("            Op::%s => %d,\n", rust变体名, 净变动))
	}

	代码.WriteString(`        }
    }

    /// 详细结构化栈效果。
    #[must_use]
    pub fn stack_effect_detailed(self) -> StackEffect {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		if f.StackCond {
			代码.WriteString(fmt.Sprintf("            Op::%s => StackEffect::Conditional,\n", rust变体名))
		} else if f.VarStack {
			代码.WriteString(fmt.Sprintf("            Op::%s => StackEffect::Variable,\n", rust变体名))
		} else {
			净变动 := int(f.Pushes) - int(f.Pops)
			代码.WriteString(fmt.Sprintf("            Op::%s => StackEffect::Fixed(%d),\n", rust变体名, 净变动))
		}
	}

	代码.WriteString(`        }
    }

    /// 指令弹出的操作数个数。
    #[must_use]
    pub const fn pops(self) -> u8 {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => %d,\n", rust变体名, f.Pops))
	}

	代码.WriteString(`        }
    }

    /// 指令压入的操作数个数。
    #[must_use]
    pub const fn pushes(self) -> u8 {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => %d,\n", rust变体名, f.Pushes))
	}

	代码.WriteString(`        }
    }

    /// 是否为纯字面量压栈（无副作用，可安全做死代码消除）。
    #[must_use]
    pub const fn is_pure_push(self) -> bool {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => %t,\n", rust变体名, f.PurePush))
	}

	代码.WriteString(`        }
    }

    /// 是否为相对跳转指令。
    #[must_use]
    pub const fn is_jump(self) -> bool {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => %t,\n", rust变体名, f.IsJump))
	}

	代码.WriteString(`        }
    }

    /// 是否为控制流终结指令（其后顺序指令不可达）。
    #[must_use]
    pub const fn is_terminal(self) -> bool {
        match self {
`)

	for _, f := range 事实列表 {
		rust变体名 := strings.TrimPrefix(f.Ident, "Op")
		代码.WriteString(fmt.Sprintf("            Op::%s => %t,\n", rust变体名, f.IsTerminal))
	}

	代码.WriteString(`        }
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
    pub const fn new(op: Op, operand: u32) -> Self {
        Self { op, operand }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn opcodes_roundtrip_all_106_variants() {
        for b in 0..=105u8 {
            let op = Op::from_opcode(b).expect("必须成功解码有效操作码");
            assert_eq!(op.opcode(), b);
        }
        assert_eq!(Op::from_opcode(106), None);
        assert_eq!(Op::from_opcode(255), None);
    }

    #[test]
    fn stack_effects_balance_a_simple_addition() {
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

    #[test]
    fn meta_properties_integrity() {
        assert!(Op::PushUndefined.is_pure_push());
        assert!(Op::Jmp.is_jump());
        assert!(Op::Return.is_terminal());
        assert_eq!(Op::GetPropLocal.operand_kind(), OperandKind::PackedSlotName);
        assert_eq!(Op::CallMethod.operand_kind(), OperandKind::PackedCall);
        assert_eq!(Op::End.opcode(), 105);
    }
}
`)

	目标文件 := filepath.Join(根目录, "aluka_r", "crates", "aluka-bytecode", "src", "op.rs")
	if err := os.WriteFile(目标文件, []byte(代码.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入目标文件失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("成功生成 106 条指令的 aluka-bytecode/src/op.rs (%s)\n", 目标文件)
}
