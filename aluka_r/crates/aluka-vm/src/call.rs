//! 函数调用管理、帧上下文隔离与模块执行入口。

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::{Upvalue, Value};

impl Vm {
    /// 解析可调用对象：闭包返回 (函数模板索引, 上值)；裸函数模板索引返回 (索引, 空上值)。
    pub(crate) fn resolve_callable(&self, callee: Value) -> (Option<usize>, Vec<Upvalue>) {
        if let Value::Object(r) = callee {
            if let Some(HeapObject::Closure {
                func_idx, upvalues, ..
            }) = self.heap.get(r.0 as usize)
            {
                return (Some(*func_idx), upvalues.clone());
            }
            if (r.0 as usize) < self.module_functions.len() {
                return (Some(r.0 as usize), Vec::new());
            }
        }
        (None, Vec::new())
    }

    /// 调用可调用对象；不可解析时返回 `undefined`（对齐现有 `CALL` 臂语义）。
    pub(crate) fn invoke_callable(
        &mut self,
        callee: Value,
        this_val: Value,
        args: &[Value],
    ) -> Result<Value, VmError> {
        let (f_idx, uvs) = self.resolve_callable(callee);
        if let Some(fi) = f_idx {
            return self.invoke_function(fi, this_val, args, uvs);
        }
        Ok(Value::Undefined)
    }

    /// 构造调用（`new X(args)`）：分配实例（挂 `callee.prototype`）并以 `this`=实例
    /// 调用构造器；构造器返回对象则采用之，否则采用实例。原生构造器由解释器拦截。
    pub(crate) fn do_construct(&mut self, callee: Value, args: &[Value]) -> Result<Value, VmError> {
        if let Value::Object(r) = callee {
            let ctor_name = match self.heap.get(r.0 as usize) {
                Some(HeapObject::NativeCtor { name, .. }) => Some(name.clone()),
                _ => None,
            };
            match ctor_name.as_deref() {
                Some("Error") => {
                    // message 未传或为 undefined 时按规范置空串
                    let message = match args.first() {
                        Some(Value::Undefined) | None => String::new(),
                        Some(v) => self.format_value(*v),
                    };
                    return Ok(Value::Object(self.alloc_error_instance(&message)));
                }
                Some("Array") => return Ok(Value::Object(self.alloc_array(Vec::new()))),
                Some("Object") => return Ok(Value::Object(self.alloc_ordinary())),
                _ => {}
            }
        }
        let proto_ref = match self.get_property(callee, "prototype") {
            Ok(Value::Object(p)) => Some(p),
            _ => None,
        };
        let instance_ref = self.alloc_ordinary_with_proto(proto_ref);
        let instance_val = Value::Object(instance_ref);
        let (f_idx, uvs) = self.resolve_callable(callee);
        if let Some(fi) = f_idx {
            let res = self.invoke_function(fi, instance_val, args, uvs)?;
            if matches!(res, Value::Object(_)) {
                return Ok(res);
            }
        }
        Ok(instance_val)
    }

    /// `super(args)` 语义：在当前帧 `this` 槽（`locals[0]`，即派生实例）上调用父类构造器。
    pub(crate) fn do_construct_this(
        &mut self,
        callee: Value,
        args: &[Value],
    ) -> Result<Value, VmError> {
        let this_val = *self.locals.first().unwrap_or(&Value::Undefined);
        let (f_idx, uvs) = self.resolve_callable(callee);
        if let Some(fi) = f_idx {
            return self.invoke_function(fi, this_val, args, uvs);
        }
        Ok(this_val)
    }

    /// 将 spread 参数表转为参数列表（对齐 Go 版 `toArrayValues`）：
    /// 数组取元素列表，普通对象取自有属性值集，其余为空。
    pub(crate) fn to_array_values(&self, val: Value) -> Vec<Value> {
        if let Value::Object(r) = val {
            let idx = r.0 as usize;
            if idx < self.heap.len() {
                match &self.heap[idx] {
                    HeapObject::Array { elements, .. } => return elements.clone(),
                    HeapObject::Ordinary { properties, .. } => {
                        return properties.values().copied().collect();
                    }
                    _ => {}
                }
            }
        }
        Vec::new()
    }

    /// 绑定调用实参到当前帧局部槽位（`self.locals` 须已初始化为全 undefined）。
    ///
    /// 对齐 Go 版：固定参数位只绑前 `num_params` 个；varargs 函数把多余实参
    /// 打包成 rest 数组写在 `locals[1 + num_params]`（不足为空数组）。
    pub(crate) fn bind_call_args(
        &mut self,
        this_val: Value,
        args: &[Value],
        num_params: usize,
        is_var_args: bool,
    ) {
        if !self.locals.is_empty() {
            self.locals[0] = this_val;
        }
        for (i, arg) in args.iter().take(num_params).enumerate() {
            let slot = i + 1; // locals[0] 是 this
            if slot < self.locals.len() {
                self.locals[slot] = *arg;
            }
        }
        if is_var_args {
            let rest: Vec<Value> = if args.len() > num_params {
                args[num_params..].to_vec()
            } else {
                Vec::new()
            };
            let rest_ref = self.alloc_array(rest);
            let rest_slot = 1 + num_params;
            if rest_slot < self.locals.len() {
                self.locals[rest_slot] = Value::Object(rest_ref);
            }
        }
    }

    /// 执行函数模板（自动隔离并保存/恢复局部槽位、上值环境与当前常量池）。
    ///
    /// 生成器函数**不执行函数体**：仅创建生成器对象并登记初始状态（JS 语义，
    /// 由 `next()` 驱动）；async 函数同步执行函数体并把结果包装为 fulfilled Promise。
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
        if tmpl.is_generator {
            return Ok(self.make_generator(&tmpl, func_idx, this_val, args, upvalues));
        }
        let old_locals = std::mem::replace(
            &mut self.locals,
            vec![Value::Undefined; tmpl.num_locals as usize],
        );
        let old_constants = std::mem::replace(&mut self.current_constants, tmpl.constants.clone());
        let old_upvalues = std::mem::replace(&mut self.current_upvalues, upvalues);
        let old_open_upvalues = std::mem::take(&mut self.open_upvalues);
        // 每帧独立的 try handler 栈与 Try 表（异常只在所属帧内查找 handler）
        let old_try_stack = std::mem::take(&mut self.try_stack);
        let old_try_table = std::mem::replace(&mut self.current_try_table, tmpl.try_table.clone());

        self.bind_call_args(this_val, args, tmpl.num_params as usize, tmpl.is_var_args);

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
        self.try_stack = old_try_stack;
        self.current_try_table = old_try_table;
        match ret {
            // async 函数：同步执行体后把结果包装为 fulfilled Promise（对齐语料语义）
            Ok(v) if tmpl.is_async => {
                let p = self.alloc_fulfilled_promise(v);
                Ok(Value::Object(p))
            }
            other => other,
        }
    }

    /// 执行函数模板（自动根据常量池和局部变量槽位初始化执行环境）。
    pub fn run_func(&mut self, func: &aluka_bytecode::FuncTemplate) -> Result<Value, VmError> {
        let old_locals = std::mem::replace(
            &mut self.locals,
            vec![Value::Undefined; func.num_locals as usize],
        );
        self.current_try_table = func.try_table.clone();
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
