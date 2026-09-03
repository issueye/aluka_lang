//! 函数调用管理、帧上下文隔离与模块执行入口。

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::{Upvalue, Value};

impl Vm {
    /// 执行函数模板（自动隔离并保存/恢复局部槽位、上值环境与当前常量池）。
    pub fn invoke_function(
        &mut self,
        func_idx: usize,
        this_val: Value,
        args: &[Value],
        upvalues: Vec<Upvalue>,
    ) -> Result<Value, VmError> {
        if func_idx >= self.module_functions.len() {
            return Ok(Value::Undefined);
        }
        let tmpl = self.module_functions[func_idx].clone();
        let old_locals = std::mem::replace(
            &mut self.locals,
            vec![Value::Undefined; tmpl.num_locals as usize],
        );
        let old_constants = std::mem::replace(&mut self.current_constants, tmpl.constants.clone());
        let old_upvalues = std::mem::replace(&mut self.current_upvalues, upvalues);
        let old_open_upvalues = std::mem::take(&mut self.open_upvalues);

        if !self.locals.is_empty() {
            self.locals[0] = this_val;
        }
        for (i, arg) in args.iter().enumerate() {
            let slot = i + 1; // locals[0] 是 this
            if slot < self.locals.len() {
                self.locals[slot] = *arg;
            }
        }

        let ret = self.run_with_constants(&tmpl.code, &tmpl.constants);

        // 函数返回前，关闭当前帧所有未关闭的 open upvalues
        for (slot, uv) in &self.open_upvalues {
            if let Some(val) = self.locals.get(*slot) {
                *uv.0.borrow_mut() = *val;
            }
        }

        self.locals = old_locals;
        self.current_constants = old_constants;
        self.current_upvalues = old_upvalues;
        self.open_upvalues = old_open_upvalues;
        ret
    }

    /// 执行函数模板（自动根据常量池和局部变量槽位初始化执行环境）。
    pub fn run_func(&mut self, func: &aluka_bytecode::FuncTemplate) -> Result<Value, VmError> {
        let old_locals = std::mem::replace(
            &mut self.locals,
            vec![Value::Undefined; func.num_locals as usize],
        );
        let res = self.run_with_constants(&func.code, &func.constants);
        self.locals = old_locals;
        res
    }

    /// 执行编译模块：按顺序执行顶层及入口闭包。
    pub fn run_module(
        &mut self,
        module: &aluka_bytecode::BytecodeModule,
    ) -> Result<Value, VmError> {
        if module.functions.is_empty() {
            return Ok(Value::Undefined);
        }
        self.module_functions = module.functions.clone();
        self.module_classes = module.classes.clone();
        // 先执行 Func 0（主函数）
        let res = self.run_func(&module.functions[0])?;
        if let Value::Object(r) = res {
            let (target_func, uvs) = if let Some(HeapObject::Closure {
                func_idx, upvalues, ..
            }) = self.heap.get(r.0 as usize)
            {
                (Some(*func_idx), upvalues.clone())
            } else if (r.0 as usize) < module.functions.len() {
                (Some(r.0 as usize), Vec::new())
            } else {
                (None, Vec::new())
            };
            if let Some(fi) = target_func {
                if fi < module.functions.len() {
                    return self.invoke_function(fi, Value::Undefined, &[], uvs);
                }
            }
        }
        Ok(res)
    }
}
