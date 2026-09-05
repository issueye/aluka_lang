//! M5 JIT（J1 阶段，ADR 0005）：字节码数值子集 → Cranelift 机器码。
//!
//! 子集操作码：`PushConst / LoadLocal / StoreLocal / Add / Sub / Mul / Div /
//! Lt / Gt / Eq / Jmp / JmpFalsePop / Return / Pop / Nop`。语义约束：
//! - 值域为 f64（布尔以 1.0/0.0 表示，与 VM `to_boolean` 对齐：非零且非
//!   NaN 为真）；
//! - 函数签名统一 `num_params × f64 → f64`；
//! - 遇子集外操作码 → 编译期拒绝（[`JitError::UnsupportedOpcode`]），
//!   调用方回退解释器（无部分去优化需求，见 ADR 0005 决定 4）。
//!
//! Quick IR：编译前先跑 [`peephole::const_fold`] 常量折叠 peephole。

pub mod peephole;

use aluka_bytecode::{Constant, FuncTemplate, Op};
use cranelift::prelude::*;
use cranelift_jit::{JITBuilder, JITModule};
use cranelift_module::{Linkage, Module};
use std::collections::{BTreeMap, BTreeSet};

/// JIT 编译错误。
#[derive(Debug)]
pub enum JitError {
    /// 子集外操作码（调用方回退解释器）
    UnsupportedOpcode {
        /// 函数名（含诊断信息）
        func: String,
        /// 折叠后指令流中的位置
        pc: usize,
    },
    /// Cranelift 编译失败
    Codegen(String),
}

/// 已编译的可执行函数：`params × f64 → f64`。
pub struct JittedFn {
    ptr: *const u8,
    /// 持有以保活已 finalize 的代码内存
    #[allow(dead_code)]
    module: JITModule,
    /// peephole 折叠后的指令数（诊断用）
    pub folded_len: usize,
}

// SAFETY: `ptr` 指向已 finalize 的只读可执行代码，其生命周期由同结构体
// 持有的 `module` 保证；代码页不含线程局部状态、执行期不改写自身，因此
// 跨线程转移所有权后调用仍安全。
unsafe impl Send for JittedFn {}

impl JittedFn {
    /// 执行：按参数个数调用机器码。
    ///
    /// # Safety 论证（SAFETY）
    /// `self.ptr` 指向 Cranelift `finalize_definitions` 之后的可执行代码，
    /// 生命周期由 `self.module` 保活；签名 `(*const f64, usize) -> f64` 与
    /// 编译期声明的 Cranelift 签名逐字段一致（指针 + I32 长度，返回 F64），
    /// 调用即机器码入口的正常控制流转移。
    pub fn call(&self, args: &[f64]) -> f64 {
        // SAFETY: 见方法级论证——指针有效性与签名一致性由 JITModule 保证
        let f = unsafe { std::mem::transmute::<*const u8, fn(*const f64, usize) -> f64>(self.ptr) };
        f(args.as_ptr(), args.len())
    }
}

/// 编译函数为机器码。
///
/// # Errors
/// 子集外操作码或 Cranelift 失败时返回 [`JitError`]。
pub fn jit_compile(func: &FuncTemplate) -> Result<JittedFn, JitError> {
    let folded = peephole::const_fold(&func.code, &func.constants);
    let code = &folded.code;
    let constants = &folded.constants;

    let isa_builder =
        cranelift_native::builder().map_err(|e| JitError::Codegen(format!("native isa: {e}")))?;
    let mut flag_builder = settings::builder();
    flag_builder
        .set("use_colocated_libcalls", "false")
        .map_err(|e| JitError::Codegen(format!("flag: {e}")))?;
    flag_builder
        .set("is_pic", "false")
        .map_err(|e| JitError::Codegen(format!("flag: {e}")))?;
    let flags = settings::Flags::new(flag_builder);
    let isa = isa_builder
        .finish(flags)
        .map_err(|e| JitError::Codegen(format!("isa finish: {e}")))?;
    let builder = JITBuilder::with_isa(isa, cranelift_module::default_libcall_names());
    let mut module = JITModule::new(builder);
    let mut ctx = module.make_context();
    let ptr_type = module.target_config().pointer_type();
    // 签名：(args_ptr, arg_count) -> f64
    ctx.func.signature.params.push(AbiParam::new(ptr_type));
    ctx.func.signature.params.push(AbiParam::new(types::I32));
    ctx.func.signature.returns.push(AbiParam::new(types::F64));

    let mut fb_ctx = FunctionBuilderContext::new();
    let mut fb = FunctionBuilder::new(&mut ctx.func, &mut fb_ctx);

    // 块集合：0、全部跳转目标/落点、末尾出口
    let mut starts: BTreeSet<usize> = BTreeSet::new();
    starts.insert(0);
    for (pc, instr) in code.iter().enumerate() {
        if matches!(instr.op, Op::Jmp | Op::JmpFalsePop) {
            starts.insert(jump_target(pc, instr.operand));
            starts.insert(pc + 1);
        }
    }
    starts.insert(code.len());
    let mut blocks: BTreeMap<usize, Block> = BTreeMap::new();
    let entry = *blocks.entry(0).or_insert_with(|| fb.create_block());
    for &s in starts.iter().skip(1) {
        blocks.entry(s).or_insert_with(|| fb.create_block());
    }

    fb.append_block_params_for_function_params(entry);
    fb.switch_to_block(entry);
    fb.seal_block(entry);

    // 局部槽位 → Cranelift Variable（槽 0 = this = NaN 占位）
    let num_locals = func.num_locals.max(1) as usize;
    let mut vars: Vec<Variable> = Vec::with_capacity(num_locals);
    for i in 0..num_locals {
        let v = Variable::from_u32(i as u32);
        fb.declare_var(v, types::F64);
        let init = if i == 0 { f64::NAN } else { 0.0 };
        let cst = { fb.ins().f64const(init) };
        fb.def_var(v, cst);
        vars.push(v);
    }
    // 实参装载：args[i] → vars[1+i]
    let args_ptr = fb.block_params(entry)[0];
    let param_count = (func.num_params as usize).min(num_locals.saturating_sub(1));
    for (i, &var) in vars.iter().enumerate().skip(1).take(param_count) {
        let idx = { fb.ins().iconst(types::I32, (i - 1) as i64) };
        let idx64 = { fb.ins().sextend(types::I64, idx) };
        let eight = { fb.ins().iconst(types::I64, 8) };
        let off = { fb.ins().imul(idx64, eight) };
        let addr = { fb.ins().iadd(args_ptr, off) };
        let loaded = { fb.ins().load(types::F64, MemFlags::new(), addr, 0) };
        fb.def_var(var, loaded);
    }

    let mut value_stack: Vec<Value> = Vec::new();
    let mut terminated = false;
    let mut current_block = entry;

    let mut pc = 0usize;
    while pc < code.len() {
        // 块边界：切到本指令所属块（若与当前块不同）
        if let Some(&b) = blocks.get(&pc) {
            if b != current_block {
                if !terminated {
                    fb.ins().jump(b, &[]);
                }
                fb.switch_to_block(b);
                current_block = b;
                terminated = false;
                value_stack.clear();
            }
        } else if terminated {
            // 不可达且非块起点：跳过
            pc += 1;
            continue;
        }
        let instr = &code[pc];
        match instr.op {
            Op::PushConst => {
                let v = match constants.get(instr.operand as usize) {
                    Some(Constant::Number(n)) => *n,
                    Some(Constant::Bool(b)) => {
                        if *b {
                            1.0
                        } else {
                            0.0
                        }
                    }
                    Some(Constant::Null) => f64::NAN,
                    _ => return Err(unsupported(func, pc)),
                };
                value_stack.push(fb.ins().f64const(v));
            }
            Op::LoadLocal => {
                let slot = instr.operand as usize;
                let Some(&var) = vars.get(slot) else {
                    return Err(JitError::Codegen(format!("槽位越界 {slot}")));
                };
                value_stack.push(fb.use_var(var));
            }
            Op::StoreLocal => {
                let slot = instr.operand as usize;
                let Some(v) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let Some(&var) = vars.get(slot) else {
                    return Err(JitError::Codegen(format!("槽位越界 {slot}")));
                };
                fb.def_var(var, v);
            }
            Op::Add | Op::Sub | Op::Mul | Op::Div => {
                let Some(b) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let Some(a) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let r = match instr.op {
                    Op::Add => fb.ins().fadd(a, b),
                    Op::Sub => fb.ins().fsub(a, b),
                    Op::Mul => fb.ins().fmul(a, b),
                    _ => fb.ins().fdiv(a, b),
                };
                value_stack.push(r);
            }
            Op::Lt | Op::Gt | Op::Eq => {
                let Some(b) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let Some(a) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let cc = match instr.op {
                    Op::Lt => FloatCC::LessThan,
                    Op::Gt => FloatCC::GreaterThan,
                    _ => FloatCC::Equal,
                };
                let cmp = fb.ins().fcmp(cc, a, b);
                let one = fb.ins().f64const(1.0);
                let zero = fb.ins().f64const(0.0);
                value_stack.push(fb.ins().select(cmp, one, zero));
            }
            Op::Jmp => {
                let target = jump_target(pc, instr.operand);
                let b = *blocks
                    .get(&target)
                    .ok_or_else(|| JitError::Codegen(format!("跳转目标越界 {target}")))?;
                fb.ins().jump(b, &[]);
                terminated = true;
            }
            Op::JmpFalsePop => {
                let Some(cond) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                let target = jump_target(pc, instr.operand);
                let tb = *blocks
                    .get(&target)
                    .ok_or_else(|| JitError::Codegen(format!("跳转目标越界 {target}")))?;
                let zero = fb.ins().f64const(0.0);
                let is_true = fb.ins().fcmp(FloatCC::NotEqual, cond, zero);
                let ordered = fb.ins().fcmp(FloatCC::Ordered, cond, cond);
                let truthy = fb.ins().band(is_true, ordered);
                let fallthrough = *blocks
                    .get(&(pc + 1))
                    .ok_or_else(|| JitError::Codegen("JmpFalsePop 落点缺块".into()))?;
                fb.ins().brif(truthy, fallthrough, &[], tb, &[]);
                terminated = true;
            }
            Op::Return => {
                let Some(v) = value_stack.pop() else {
                    return Err(JitError::Codegen("栈下溢".into()));
                };
                fb.ins().return_(&[v]);
                terminated = true;
            }
            Op::Pop => {
                value_stack.pop();
            }
            Op::Nop => {}
            _ => return Err(unsupported(func, pc)),
        }
        pc += 1;
        // 已终结且下一 pc 非块起点：切换到出口占位（后续不可达跳过）
        if terminated {
            if let Some(&b) = blocks.get(&pc) {
                fb.switch_to_block(b);
                current_block = b;
                terminated = false;
                value_stack.clear();
            }
        }
    }
    // 出口块：一切未终结路径跳入；统一返回 NaN（正常路径已在 Return 处终结）
    let exit = *blocks.get(&code.len()).expect("出口块已创建");
    if current_block != exit && !terminated {
        fb.ins().jump(exit, &[]);
    }
    fb.switch_to_block(exit);
    let nan = fb.ins().f64const(f64::NAN);
    fb.ins().return_(&[nan]);
    fb.seal_all_blocks();

    let func_id = module
        .declare_function(&func.name, Linkage::Local, &ctx.func.signature)
        .map_err(|e| JitError::Codegen(format!("declare: {e}")))?;
    module
        .define_function(func_id, &mut ctx)
        .map_err(|e| JitError::Codegen(format!("define: {e:?}")))?;
    module.clear_context(&mut ctx);
    module
        .finalize_definitions()
        .map_err(|e| JitError::Codegen(format!("finalize: {e}")))?;
    let ptr = module.get_finalized_function(func_id);
    Ok(JittedFn {
        ptr,
        module,
        folded_len: code.len(),
    })
}

fn unsupported(func: &FuncTemplate, pc: usize) -> JitError {
    let op = func.code.get(pc).map(|i| i.op).unwrap_or(Op::Nop);
    JitError::UnsupportedOpcode {
        func: format!("{} (orig pc {pc}, op {op:?})", func.name),
        pc,
    }
}

/// 跳转目标指令索引（对齐 VM `compute_jump_target`：相对下一指令的字节偏移）。
#[must_use]
pub fn jump_target(pc: usize, operand: u32) -> usize {
    let signed = if operand & 0x80_0000 != 0 {
        (operand | 0xFF00_0000) as i32
    } else {
        operand as i32
    };
    (((pc as i32 * 4) + 4 + signed) / 4) as usize
}
