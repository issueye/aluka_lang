//! 虚拟机核心解释器：执行状态定义与操作码分派循环。

use crate::heap::HeapObject;
use crate::ops::{eq, strict_eq, to_boolean, to_number};
use crate::value::{Upvalue, Value};
use aluka_bytecode::{ClassTemplate, Constant, FuncTemplate, Instr, Op};
use std::collections::HashMap;

/// 执行期可能发生的错误。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VmError {
    /// 操作数栈下溢（Pop 时栈为空）
    StackUnderflow,
    /// 访问越界局部变量槽位
    LocalOutOfRange,
    /// 函数执行到达末尾但未返回
    MissingReturn,
    /// 整数除以零
    DivisionByZero,
    /// 遇到了当前里程碑尚未实现的操作码
    UnimplementedOpcode(Op),
}

impl std::fmt::Display for VmError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::StackUnderflow => write!(f, "操作数栈下溢"),
            Self::LocalOutOfRange => write!(f, "访问的局部变量槽位越界"),
            Self::MissingReturn => write!(f, "指令流在返回前结束"),
            Self::DivisionByZero => write!(f, "除以零错误"),
            Self::UnimplementedOpcode(op) => write!(f, "未实现的操作码: {op:?}"),
        }
    }
}

impl std::error::Error for VmError {}

/// 一次执行的状态：操作数栈与局部槽位。
#[derive(Debug, Default)]
pub struct Vm {
    /// 操作数栈
    pub stack: Vec<Value>,
    /// 局部变量槽位
    pub locals: Vec<Value>,
    /// 控制台打印捕获记录（供测试断言比对）
    pub stdout_records: Vec<String>,
    /// 当前执行函数的常量池副本（用于字符串解引用与打印）
    pub current_constants: Vec<Constant>,
    /// 堆对象存储
    pub heap: Vec<HeapObject>,
    /// 模块全部函数模板（供跨函数调用与 Getter 调度）
    pub module_functions: Vec<FuncTemplate>,
    /// 模块全部类模板（供 MakeClass 构造类对象与原型链）
    pub module_classes: Vec<ClassTemplate>,
    /// 当前函数帧所持有的上值列表（供 LoadUpvalue / StoreUpvalue 访问）
    pub current_upvalues: Vec<Upvalue>,
    /// 当前函数帧活跃的打开上值表（slot -> Upvalue），保证同一 slot 共享同一个 RefCell
    pub open_upvalues: HashMap<usize, Upvalue>,
}

impl Vm {
    /// 创建虚拟机，预留 `locals` 个局部槽位（初值 `undefined`）。
    #[must_use]
    pub fn new(locals: usize) -> Self {
        Self {
            stack: Vec::new(),
            locals: vec![Value::Undefined; locals],
            stdout_records: Vec::new(),
            current_constants: Vec::new(),
            heap: Vec::new(),
            module_functions: Vec::new(),
            module_classes: Vec::new(),
            current_upvalues: Vec::new(),
            open_upvalues: HashMap::new(),
        }
    }

    #[inline]
    pub(crate) fn pop(&mut self) -> Result<Value, VmError> {
        self.stack.pop().ok_or(VmError::StackUnderflow)
    }

    #[inline]
    pub(crate) fn peek(&self) -> Result<Value, VmError> {
        self.stack.last().copied().ok_or(VmError::StackUnderflow)
    }

    /// 格式化值为字符串（对齐 JS 的 String(...) 与 console.log 输出语义）。
    pub fn format_value(&self, val: Value) -> String {
        match val {
            Value::Undefined => "undefined".to_owned(),
            Value::Null => "null".to_owned(),
            Value::Boolean(b) => format!("{b}"),
            Value::Number(n) => {
                if n.is_nan() {
                    "NaN".to_owned()
                } else if n.is_infinite() {
                    if n > 0.0 {
                        "Infinity".to_owned()
                    } else {
                        "-Infinity".to_owned()
                    }
                } else if n.fract() == 0.0 {
                    format!("{}", n as i64)
                } else {
                    format!("{n}")
                }
            }
            Value::Object(r) => {
                let idx = r.0 as usize;
                if idx < self.heap.len() {
                    match &self.heap[idx] {
                        HeapObject::String(s) => s.clone(),
                        HeapObject::Array { elements } => {
                            let items: Vec<String> =
                                elements.iter().map(|e| self.format_value(*e)).collect();
                            items.join(",")
                        }
                        HeapObject::Ordinary { .. } => "[object Object]".to_owned(),
                        HeapObject::Closure { .. } => "[function Function]".to_owned(),
                    }
                } else if let Some(c) = self.current_constants.get(idx) {
                    match c {
                        Constant::String(s) => s.clone(),
                        Constant::BigInt(b) => b.clone(),
                        Constant::Number(n) => format!("{n}"),
                        Constant::Bool(b) => format!("{b}"),
                        Constant::Null => "null".to_owned(),
                    }
                } else {
                    format!("[Object {:?}]", r)
                }
            }
        }
    }

    /// 执行指令序列，返回 `Return` 或 `ReturnUndef` 携带的值。
    pub fn run(&mut self, code: &[Instr]) -> Result<Value, VmError> {
        self.run_with_constants(code, &[])
    }

    /// 携带常量池执行指令序列。
    pub fn run_with_constants(
        &mut self,
        code: &[Instr],
        constants: &[Constant],
    ) -> Result<Value, VmError> {
        self.current_constants = constants.to_vec();
        let mut pc = 0;
        let num_instrs = code.len();

        while pc < num_instrs {
            let instr = code[pc];

            match instr.op {
                // 1. 标量字面量与常量加载
                Op::Nop => {}
                Op::PushUndefined => self.stack.push(Value::Undefined),
                Op::PushNull => self.stack.push(Value::Null),
                Op::PushTrue => self.stack.push(Value::Boolean(true)),
                Op::PushFalse => self.stack.push(Value::Boolean(false)),
                Op::PushInt => self.stack.push(Value::Number(f64::from(instr.operand))),
                Op::PushNegInt => self.stack.push(Value::Number(-(f64::from(instr.operand)))),
                Op::PushConst => {
                    let idx = instr.operand as usize;
                    let c = constants.get(idx).ok_or(VmError::LocalOutOfRange)?;
                    match c {
                        Constant::Number(n) => self.stack.push(Value::Number(*n)),
                        Constant::String(s) => {
                            let s_ref = self.alloc_string(s.clone());
                            self.stack.push(Value::Object(s_ref));
                        }
                        Constant::BigInt(b) => {
                            let b_ref = self.alloc_string(b.clone());
                            self.stack.push(Value::Object(b_ref));
                        }
                        Constant::Bool(b) => {
                            self.stack.push(Value::Boolean(*b));
                        }
                        Constant::Null => {
                            self.stack.push(Value::Null);
                        }
                    }
                }

                // 2. 栈操作
                Op::Pop => {
                    self.pop()?;
                }
                Op::Dup => {
                    let top = self.peek()?;
                    self.stack.push(top);
                }
                Op::Swap => {
                    let a = self.pop()?;
                    let b = self.pop()?;
                    self.stack.push(a);
                    self.stack.push(b);
                }
                // 3. 算术运算
                Op::Add => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = self.add_values(left, right);
                    self.stack.push(res);
                }
                Op::Sub => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Number(to_number(left) - to_number(right)));
                }
                Op::Mul => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Number(to_number(left) * to_number(right)));
                }
                Op::Div => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Number(to_number(left) / to_number(right)));
                }
                Op::Mod => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Number(to_number(left) % to_number(right)));
                }
                Op::Pow => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Number(to_number(left).powf(to_number(right))));
                }
                Op::Neg => {
                    let top = self.pop()?;
                    self.stack.push(Value::Number(-to_number(top)));
                }
                Op::UnaryPlus => {
                    let top = self.pop()?;
                    self.stack.push(Value::Number(to_number(top)));
                }
                Op::Inc => {
                    let top = self.pop()?;
                    self.stack.push(Value::Number(to_number(top) + 1.0));
                }
                Op::Dec => {
                    let top = self.pop()?;
                    self.stack.push(Value::Number(to_number(top) - 1.0));
                }

                // 4. 位运算与逻辑非
                Op::Not => {
                    let top = self.pop()?;
                    self.stack.push(Value::Boolean(!to_boolean(top)));
                }
                Op::BitNot => {
                    let top = self.pop()?;
                    let n = to_number(top) as i32;
                    self.stack.push(Value::Number(f64::from(!n)));
                }
                Op::BitAnd => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = (to_number(left) as i32) & (to_number(right) as i32);
                    self.stack.push(Value::Number(f64::from(res)));
                }
                Op::BitOr => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = (to_number(left) as i32) | (to_number(right) as i32);
                    self.stack.push(Value::Number(f64::from(res)));
                }
                Op::BitXor => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = (to_number(left) as i32) ^ (to_number(right) as i32);
                    self.stack.push(Value::Number(f64::from(res)));
                }
                Op::Shl => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let shift = (to_number(right) as u32) & 0x1f;
                    let res = (to_number(left) as i32).wrapping_shl(shift);
                    self.stack.push(Value::Number(f64::from(res)));
                }
                Op::Shr => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let shift = (to_number(right) as u32) & 0x1f;
                    let res = (to_number(left) as i32).wrapping_shr(shift);
                    self.stack.push(Value::Number(f64::from(res)));
                }
                Op::UShr => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let shift = (to_number(right) as u32) & 0x1f;
                    let res = ((to_number(left) as u32).wrapping_shr(shift)) as f64;
                    self.stack.push(Value::Number(res));
                }

                // 5. 比较运算
                Op::Eq => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = eq(left, right, &self.heap, &self.current_constants);
                    self.stack.push(Value::Boolean(res));
                }
                Op::Ne => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = !eq(left, right, &self.heap, &self.current_constants);
                    self.stack.push(Value::Boolean(res));
                }
                Op::StrictEq => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = strict_eq(left, right, &self.heap, &self.current_constants);
                    self.stack.push(Value::Boolean(res));
                }
                Op::StrictNe => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    let res = !strict_eq(left, right, &self.heap, &self.current_constants);
                    self.stack.push(Value::Boolean(res));
                }
                Op::Lt => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Boolean(to_number(left) < to_number(right)));
                }
                Op::Le => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Boolean(to_number(left) <= to_number(right)));
                }
                Op::Gt => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Boolean(to_number(left) > to_number(right)));
                }
                Op::Ge => {
                    let right = self.pop()?;
                    let left = self.pop()?;
                    self.stack
                        .push(Value::Boolean(to_number(left) >= to_number(right)));
                }

                // 6. 局部变量与全局变量
                Op::LoadLocal => {
                    let slot = instr.operand as usize;
                    let val = self
                        .locals
                        .get(slot)
                        .copied()
                        .ok_or(VmError::LocalOutOfRange)?;
                    self.stack.push(val);
                }
                Op::StoreLocal => {
                    let slot = instr.operand as usize;
                    let val = self.peek()?;
                    if slot >= self.locals.len() {
                        return Err(VmError::LocalOutOfRange);
                    }
                    self.locals[slot] = val;
                    if let Some(uv) = self.open_upvalues.get(&slot) {
                        *uv.0.borrow_mut() = val;
                    }
                }
                Op::LoadGlobal => {
                    self.stack.push(Value::Undefined);
                }

                // 7. 控制流跳转
                Op::Jmp => {
                    pc = compute_jump_target(pc, instr.operand);
                    continue;
                }
                Op::JmpTruePop => {
                    let top = self.pop()?;
                    if to_boolean(top) {
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    }
                }
                Op::JmpFalsePop => {
                    let top = self.pop()?;
                    if !to_boolean(top) {
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    }
                }
                Op::JmpTrueKeep => {
                    let top = self.peek()?;
                    if to_boolean(top) {
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    } else {
                        self.pop()?;
                    }
                }
                Op::JmpFalseKeep => {
                    let top = self.peek()?;
                    if !to_boolean(top) {
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    } else {
                        self.pop()?;
                    }
                }
                Op::JmpNullishKeep => {
                    let top = self.peek()?;
                    if matches!(top, Value::Null | Value::Undefined) {
                        self.pop()?;
                    } else {
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    }
                }
                Op::OptionalJump => {
                    let top = self.peek()?;
                    if matches!(top, Value::Null | Value::Undefined) {
                        self.pop()?;
                        self.stack.push(Value::Undefined);
                        pc = compute_jump_target(pc, instr.operand);
                        continue;
                    }
                }
                // 8. 函数调用与方法调度
                Op::CallMethod => {
                    let num_args = (instr.operand >> 16) as usize;
                    let name_idx = (instr.operand & 0xFFFF) as usize;
                    let method_name = self.get_const_string(name_idx)?;

                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let receiver = self.pop()?;

                    if method_name == "log" {
                        let line = args
                            .iter()
                            .map(|v| self.format_value(*v))
                            .collect::<Vec<_>>()
                            .join(" ");
                        self.stdout_records.push(line);
                        self.stack.push(Value::Undefined);
                    } else if let Value::Object(r) = receiver {
                        let idx = r.0 as usize;
                        if idx < self.heap.len()
                            && matches!(self.heap[idx], HeapObject::Array { .. })
                        {
                            match method_name.as_str() {
                                "push" => {
                                    if let Some(HeapObject::Array { elements }) =
                                        self.heap.get_mut(idx)
                                    {
                                        elements.extend(args);
                                        let len = elements.len() as f64;
                                        self.stack.push(Value::Number(len));
                                    } else {
                                        self.stack.push(Value::Undefined);
                                    }
                                }
                                "map" => {
                                    let cb = args.first().copied().unwrap_or(Value::Undefined);
                                    let elems = if let Some(HeapObject::Array { elements }) =
                                        self.heap.get(idx)
                                    {
                                        elements.clone()
                                    } else {
                                        Vec::new()
                                    };
                                    let mut new_elems = Vec::with_capacity(elems.len());
                                    if let Value::Object(cb_ref) = cb {
                                        let (cb_fidx, cb_uvs) = if let Some(HeapObject::Closure {
                                            func_idx,
                                            upvalues,
                                            ..
                                        }) =
                                            self.heap.get(cb_ref.0 as usize)
                                        {
                                            (Some(*func_idx), upvalues.clone())
                                        } else {
                                            (None, Vec::new())
                                        };
                                        if let Some(fi) = cb_fidx {
                                            for (elem_idx, elem) in elems.iter().enumerate() {
                                                let item_res = self.invoke_function(
                                                    fi,
                                                    Value::Undefined,
                                                    &[*elem, Value::Number(elem_idx as f64)],
                                                    cb_uvs.clone(),
                                                )?;
                                                new_elems.push(item_res);
                                            }
                                        }
                                    }
                                    let new_arr = self.alloc_array(new_elems);
                                    self.stack.push(Value::Object(new_arr));
                                }
                                "join" => {
                                    let sep = if let Some(sep_val) = args.first() {
                                        self.to_property_key(*sep_val)
                                    } else {
                                        ",".to_owned()
                                    };
                                    let parts: Vec<String> =
                                        if let Some(HeapObject::Array { elements }) =
                                            self.heap.get(idx)
                                        {
                                            elements.iter().map(|e| self.format_value(*e)).collect()
                                        } else {
                                            Vec::new()
                                        };
                                    let joined = parts.join(&sep);
                                    let s_ref = self.alloc_string(joined);
                                    self.stack.push(Value::Object(s_ref));
                                }
                                "slice" => {
                                    let elems = if let Some(HeapObject::Array { elements }) =
                                        self.heap.get(idx)
                                    {
                                        elements.clone()
                                    } else {
                                        Vec::new()
                                    };
                                    let len = elems.len() as i64;
                                    let start_raw = match args.first() {
                                        Some(Value::Number(n)) => *n as i64,
                                        _ => 0,
                                    };
                                    let start = if start_raw < 0 {
                                        (len + start_raw).max(0) as usize
                                    } else {
                                        start_raw.min(len) as usize
                                    };
                                    let end = if let Some(Value::Number(n)) = args.get(1) {
                                        let end_raw = *n as i64;
                                        if end_raw < 0 {
                                            (len + end_raw).max(0) as usize
                                        } else {
                                            end_raw.min(len) as usize
                                        }
                                    } else {
                                        len as usize
                                    };
                                    let sliced = if start < end && start < elems.len() {
                                        elems[start..end.min(elems.len())].to_vec()
                                    } else {
                                        Vec::new()
                                    };
                                    let new_arr = self.alloc_array(sliced);
                                    self.stack.push(Value::Object(new_arr));
                                }
                                _ => self.stack.push(Value::Undefined),
                            }
                        } else {
                            // 普通对象方法调用
                            let method_val = self.get_property(receiver, &method_name)?;
                            if let Value::Object(m_ref) = method_val {
                                let (f_idx, uvs) =
                                    if let Some(HeapObject::Closure {
                                        func_idx, upvalues, ..
                                    }) = self.heap.get(m_ref.0 as usize)
                                    {
                                        (Some(*func_idx), upvalues.clone())
                                    } else if (m_ref.0 as usize) < self.module_functions.len() {
                                        (Some(m_ref.0 as usize), Vec::new())
                                    } else {
                                        (None, Vec::new())
                                    };

                                if let Some(fi) = f_idx {
                                    let ret = self.invoke_function(fi, receiver, &args, uvs)?;
                                    self.stack.push(ret);
                                } else {
                                    self.stack.push(Value::Undefined);
                                }
                            } else {
                                self.stack.push(Value::Undefined);
                            }
                        }
                    } else {
                        self.stack.push(Value::Undefined);
                    }
                }
                Op::Call => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let callee = self.pop()?;
                    if let Value::Object(r) = callee {
                        let (f_idx, uvs) =
                            if let Some(HeapObject::Closure {
                                func_idx, upvalues, ..
                            }) = self.heap.get(r.0 as usize)
                            {
                                (Some(*func_idx), upvalues.clone())
                            } else if (r.0 as usize) < self.module_functions.len() {
                                (Some(r.0 as usize), Vec::new())
                            } else {
                                (None, Vec::new())
                            };

                        if let Some(fi) = f_idx {
                            let ret = self.invoke_function(fi, Value::Undefined, &args, uvs)?;
                            self.stack.push(ret);
                        } else {
                            self.stack.push(Value::Undefined);
                        }
                    } else {
                        self.stack.push(Value::Undefined);
                    }
                }
                Op::CallWithThis => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let this_val = self.pop()?;
                    let callee = self.pop()?;
                    if let Value::Object(r) = callee {
                        let (f_idx, uvs) =
                            if let Some(HeapObject::Closure {
                                func_idx, upvalues, ..
                            }) = self.heap.get(r.0 as usize)
                            {
                                (Some(*func_idx), upvalues.clone())
                            } else if (r.0 as usize) < self.module_functions.len() {
                                (Some(r.0 as usize), Vec::new())
                            } else {
                                (None, Vec::new())
                            };

                        if let Some(fi) = f_idx {
                            let ret = self.invoke_function(fi, this_val, &args, uvs)?;
                            self.stack.push(ret);
                        } else {
                            self.stack.push(Value::Undefined);
                        }
                    } else {
                        self.stack.push(Value::Undefined);
                    }
                }

                // 9. 闭包与 Upvalues
                Op::MakeClosure => {
                    let target_func_idx = instr.operand as usize;
                    let tmpl = self
                        .module_functions
                        .get(target_func_idx)
                        .cloned()
                        .ok_or(VmError::LocalOutOfRange)?;

                    let mut captured = Vec::with_capacity(tmpl.upvalues.len());
                    for cap in &tmpl.upvalues {
                        if cap.is_local {
                            let slot = cap.index as usize;
                            let uv = self
                                .open_upvalues
                                .entry(slot)
                                .or_insert_with(|| {
                                    let val =
                                        self.locals.get(slot).copied().unwrap_or(Value::Undefined);
                                    Upvalue(std::rc::Rc::new(std::cell::RefCell::new(val)))
                                })
                                .clone();
                            captured.push(uv);
                        } else {
                            let inherited = self
                                .current_upvalues
                                .get(cap.index as usize)
                                .cloned()
                                .unwrap_or_else(|| {
                                    Upvalue(std::rc::Rc::new(std::cell::RefCell::new(
                                        Value::Undefined,
                                    )))
                                });
                            captured.push(inherited);
                        }
                    }
                    let closure_ref = self.alloc_closure_with_upvalues(target_func_idx, captured);
                    self.stack.push(Value::Object(closure_ref));
                }
                Op::LoadUpvalue => {
                    let uv_idx = instr.operand as usize;
                    let val = self
                        .current_upvalues
                        .get(uv_idx)
                        .map(|uv| *uv.0.borrow())
                        .unwrap_or(Value::Undefined);
                    self.stack.push(val);
                }
                Op::StoreUpvalue => {
                    let val = self.pop()?;
                    let uv_idx = instr.operand as usize;
                    if let Some(uv) = self.current_upvalues.get(uv_idx) {
                        *uv.0.borrow_mut() = val;
                    }
                    self.stack.push(val);
                }
                Op::CloseUpvalues => {
                    let from_slot = instr.operand as usize;
                    self.open_upvalues.retain(|&slot, _| slot < from_slot);
                }

                // 10. 对象与数组字面量
                Op::NewObject => {
                    let prop_count = instr.operand as usize;
                    let obj_ref = self.alloc_ordinary();
                    if prop_count > 0 {
                        let mut pairs = Vec::with_capacity(prop_count * 2);
                        for _ in 0..(prop_count * 2) {
                            pairs.push(self.pop()?);
                        }
                        pairs.reverse();
                        for i in (0..pairs.len()).step_by(2) {
                            let k = self.to_property_key(pairs[i]);
                            let v = pairs[i + 1];
                            self.set_property(Value::Object(obj_ref), &k, v)?;
                        }
                    }
                    self.stack.push(Value::Object(obj_ref));
                }
                Op::NewArray | Op::BuildArray => {
                    let n = instr.operand as usize;
                    let mut elements = Vec::with_capacity(n);
                    for _ in 0..n {
                        elements.push(self.pop()?);
                    }
                    elements.reverse();
                    let arr_ref = self.alloc_array(elements);
                    self.stack.push(Value::Object(arr_ref));
                }
                Op::ArrayPush => {
                    let val = self.pop()?;
                    let arr_val = self.peek()?;
                    if let Value::Object(r) = arr_val {
                        if let Some(HeapObject::Array { elements }) =
                            self.heap.get_mut(r.0 as usize)
                        {
                            elements.push(val);
                        }
                    }
                }
                Op::ArraySpread => {
                    let spread_val = self.pop()?;
                    let target_arr = self.peek()?;
                    let to_append: Vec<Value> = if let Value::Object(s_ref) = spread_val {
                        if let Some(HeapObject::Array { elements }) =
                            self.heap.get(s_ref.0 as usize)
                        {
                            elements.clone()
                        } else {
                            Vec::new()
                        }
                    } else {
                        Vec::new()
                    };
                    if let Value::Object(t_ref) = target_arr {
                        if let Some(HeapObject::Array { elements }) =
                            self.heap.get_mut(t_ref.0 as usize)
                        {
                            elements.extend(to_append);
                        }
                    }
                }
                // 11. 属性操作
                Op::SetProp => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let val = self.pop()?;
                    let obj = self.pop()?;
                    self.set_property(obj, &key, val)?;
                    self.stack.push(val);
                }
                Op::SetPropObj => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let val = self.pop()?;
                    let obj = self.peek()?;
                    self.set_property(obj, &key, val)?;
                }
                Op::SetPropTop => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let obj = self.pop()?;
                    let val = self.pop()?;
                    self.set_property(obj, &key, val)?;
                }
                Op::SetPropComputedObj => {
                    let val = self.pop()?;
                    let key_val = self.pop()?;
                    let key = self.to_property_key(key_val);
                    let obj = self.peek()?;
                    self.set_property(obj, &key, val)?;
                }
                Op::GetProp => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let obj = self.pop()?;
                    let val = self.get_property(obj, &key)?;
                    self.stack.push(val);
                }
                Op::GetPropLocal => {
                    let slot = (instr.operand >> 16) as usize;
                    let name_idx = (instr.operand & 0xFFFF) as usize;
                    let key = self.get_const_string(name_idx)?;
                    let obj = *self.locals.get(slot).ok_or(VmError::LocalOutOfRange)?;
                    let val = self.get_property(obj, &key)?;
                    self.stack.push(val);
                }
                Op::GetElem => {
                    let key_val = self.pop()?;
                    let obj = self.pop()?;
                    let key = self.to_property_key(key_val);
                    let val = self.get_property(obj, &key)?;
                    self.stack.push(val);
                }
                Op::SetElem => {
                    let val = self.pop()?;
                    let key_val = self.pop()?;
                    let obj = self.pop()?;
                    let key = self.to_property_key(key_val);
                    self.set_property(obj, &key, val)?;
                    self.stack.push(val);
                }
                Op::SetElemTop => {
                    let key_val = self.pop()?;
                    let obj = self.pop()?;
                    let val = self.pop()?;
                    let key = self.to_property_key(key_val);
                    self.set_property(obj, &key, val)?;
                }
                Op::SetGetterObj => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let fn_val = self.pop()?;
                    let obj = self.peek()?;
                    if let (Value::Object(o_ref), Value::Object(f_ref)) = (obj, fn_val) {
                        let f_idx = if let Some(HeapObject::Closure { func_idx, .. }) =
                            self.heap.get(f_ref.0 as usize)
                        {
                            *func_idx
                        } else {
                            f_ref.0 as usize
                        };
                        if let Some(HeapObject::Ordinary { getters, .. }) =
                            self.heap.get_mut(o_ref.0 as usize)
                        {
                            getters.insert(key, f_idx);
                        }
                    }
                }
                Op::SetSetterObj => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let fn_val = self.pop()?;
                    let obj = self.peek()?;
                    if let (Value::Object(o_ref), Value::Object(f_ref)) = (obj, fn_val) {
                        let f_idx = if let Some(HeapObject::Closure { func_idx, .. }) =
                            self.heap.get(f_ref.0 as usize)
                        {
                            *func_idx
                        } else {
                            f_ref.0 as usize
                        };
                        if let Some(HeapObject::Ordinary { setters, .. }) =
                            self.heap.get_mut(o_ref.0 as usize)
                        {
                            setters.insert(key, f_idx);
                        }
                    }
                }
                Op::SetGetterComputedObj => {
                    let fn_val = self.pop()?;
                    let key_val = self.pop()?;
                    let key = self.to_property_key(key_val);
                    let obj = self.peek()?;
                    if let (Value::Object(o_ref), Value::Object(f_ref)) = (obj, fn_val) {
                        let f_idx = if let Some(HeapObject::Closure { func_idx, .. }) =
                            self.heap.get(f_ref.0 as usize)
                        {
                            *func_idx
                        } else {
                            f_ref.0 as usize
                        };
                        if let Some(HeapObject::Ordinary { getters, .. }) =
                            self.heap.get_mut(o_ref.0 as usize)
                        {
                            getters.insert(key, f_idx);
                        }
                    }
                }
                Op::SetSetterComputedObj => {
                    let fn_val = self.pop()?;
                    let key_val = self.pop()?;
                    let key = self.to_property_key(key_val);
                    let obj = self.peek()?;
                    if let (Value::Object(o_ref), Value::Object(f_ref)) = (obj, fn_val) {
                        let f_idx = if let Some(HeapObject::Closure { func_idx, .. }) =
                            self.heap.get(f_ref.0 as usize)
                        {
                            *func_idx
                        } else {
                            f_ref.0 as usize
                        };
                        if let Some(HeapObject::Ordinary { setters, .. }) =
                            self.heap.get_mut(o_ref.0 as usize)
                        {
                            setters.insert(key, f_idx);
                        }
                    }
                }
                Op::DelProp => {
                    let key = self.get_const_string(instr.operand as usize)?;
                    let obj = self.pop()?;
                    if let Value::Object(r) = obj {
                        if let Some(HeapObject::Ordinary { properties, .. }) =
                            self.heap.get_mut(r.0 as usize)
                        {
                            properties.remove(&key);
                        }
                    }
                    self.stack.push(Value::Boolean(true));
                }
                Op::DelElem => {
                    let key_val = self.pop()?;
                    let key = self.to_property_key(key_val);
                    let obj = self.pop()?;
                    if let Value::Object(r) = obj {
                        if let Some(HeapObject::Ordinary { properties, .. }) =
                            self.heap.get_mut(r.0 as usize)
                        {
                            properties.remove(&key);
                        }
                    }
                    self.stack.push(Value::Boolean(true));
                }

                // 12. 返回指令
                Op::Return => {
                    return self.pop();
                }
                Op::ReturnUndef => {
                    return Ok(Value::Undefined);
                }

                // 13. ES6 类与面向对象指令
                Op::MakeClass => {
                    let class_idx = instr.operand as usize;
                    self.exec_make_class(class_idx)?;
                }
                Op::New => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let callee = self.pop()?;
                    let proto_ref = match self.get_property(callee, "prototype") {
                        Ok(Value::Object(p)) => Some(p),
                        _ => None,
                    };
                    let instance_ref = self.alloc_ordinary_with_proto(proto_ref);
                    let instance_val = Value::Object(instance_ref);

                    if let Value::Object(c_ref) = callee {
                        let (f_idx, uvs) =
                            if let Some(HeapObject::Closure {
                                func_idx, upvalues, ..
                            }) = self.heap.get(c_ref.0 as usize)
                            {
                                (Some(*func_idx), upvalues.clone())
                            } else if (c_ref.0 as usize) < self.module_functions.len() {
                                (Some(c_ref.0 as usize), Vec::new())
                            } else {
                                (None, Vec::new())
                            };
                        if let Some(fi) = f_idx {
                            let res = self.invoke_function(fi, instance_val, &args, uvs)?;
                            if matches!(res, Value::Object(_)) {
                                self.stack.push(res);
                            } else {
                                self.stack.push(instance_val);
                            }
                        } else {
                            self.stack.push(instance_val);
                        }
                    } else {
                        self.stack.push(instance_val);
                    }
                }
                Op::ConstructThis => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let callee = self.pop()?;
                    let this_val = *self.locals.first().unwrap_or(&Value::Undefined);
                    if let Value::Object(c_ref) = callee {
                        let (f_idx, uvs) =
                            if let Some(HeapObject::Closure {
                                func_idx, upvalues, ..
                            }) = self.heap.get(c_ref.0 as usize)
                            {
                                (Some(*func_idx), upvalues.clone())
                            } else if (c_ref.0 as usize) < self.module_functions.len() {
                                (Some(c_ref.0 as usize), Vec::new())
                            } else {
                                (None, Vec::new())
                            };
                        if let Some(fi) = f_idx {
                            let res = self.invoke_function(fi, this_val, &args, uvs)?;
                            self.stack.push(res);
                        } else {
                            self.stack.push(this_val);
                        }
                    } else {
                        self.stack.push(this_val);
                    }
                }
                Op::CallThis => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let callee = self.pop()?;
                    let this_val = *self.locals.first().unwrap_or(&Value::Undefined);
                    if let Value::Object(c_ref) = callee {
                        let (f_idx, uvs) =
                            if let Some(HeapObject::Closure {
                                func_idx, upvalues, ..
                            }) = self.heap.get(c_ref.0 as usize)
                            {
                                (Some(*func_idx), upvalues.clone())
                            } else if (c_ref.0 as usize) < self.module_functions.len() {
                                (Some(c_ref.0 as usize), Vec::new())
                            } else {
                                (None, Vec::new())
                            };
                        if let Some(fi) = f_idx {
                            let res = self.invoke_function(fi, this_val, &args, uvs)?;
                            self.stack.push(res);
                        } else {
                            self.stack.push(Value::Undefined);
                        }
                    } else {
                        self.stack.push(Value::Undefined);
                    }
                }
                Op::GetProto => {
                    let obj = self.pop()?;
                    let proto = self.get_prototype(obj);
                    if let Some(p) = proto {
                        self.stack.push(Value::Object(p));
                    } else {
                        self.stack.push(Value::Null);
                    }
                }
                Op::Instanceof => {
                    let r = self.pop()?;
                    let l = self.pop()?;
                    let res = self.check_instanceof(l, r);
                    self.stack.push(Value::Boolean(res));
                }

                // 其它高级对象与协程操作码（后续阶段扩展）
                Op::CallWithThisArgs
                | Op::CallArgs
                | Op::CallMethodArgs
                | Op::NewArgs
                | Op::SpreadObject
                | Op::Typeof
                | Op::TypeofGlobal
                | Op::TryEnter
                | Op::TryExit
                | Op::TryExitFinally
                | Op::TryExitJmp
                | Op::Throw
                | Op::In
                | Op::ForInNext
                | Op::CallThisArgs
                | Op::ConstructThisArgs
                | Op::GetIterator
                | Op::Yield
                | Op::GetAsyncIterator
                | Op::Await
                | Op::MakeRegexp
                | Op::StoreGlobal
                | Op::EnumKeys
                | Op::End => return Err(VmError::UnimplementedOpcode(instr.op)),
            }
            pc += 1;
        }
        Err(VmError::MissingReturn)
    }
}

#[inline]
fn compute_jump_target(pc: usize, operand: u32) -> usize {
    let signed_off = if operand & 0x80_0000 != 0 {
        (operand | 0xFF00_0000) as i32
    } else {
        operand as i32
    };
    (((pc as i32 * 4) + 4 + signed_off) / 4) as usize
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
