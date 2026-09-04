//! CJS 模块系统最小集（M1）：`require` / `exports` / 循环依赖占位。
//!
//! 对齐 Go 版 CJS 包装约定（`internal/runtime/module/cjs.go`）：模块字节码的
//! `<main>` 返回 7 参数包装闭包
//! `(require, module, exports, __filename, __dirname, __import, __importMeta)`，
//! 宿主负责按位传参调用。加载流程：
//!
//! 1. `require(spec)` 解析 `spec.bc`（相对基准目录；`.js` 视为 `.bc`）；
//! 2. 读文件 → 反序列化 → Verifier 校验 → 执行 `<main>` 取模块闭包；
//! 3. 预建 `exports` / `module` 对象并**先登记缓存再执行**（循环依赖的经典
//!    CJS 行为：后加载方拿到未完成的 `exports`）；
//! 4. 以 7 参调用模块闭包，返回 `module.exports`（允许模块重赋值）。
//!
//! 字节码分发约定（M1）：`require("./dep")` 解析为基准目录下的 `dep.bc`；
//! `require("./dep.js")` 同样解析 `dep.bc`（`.js` → `.bc` 替换）。嵌套相对
//! 路径以入口目录为基准（子目录递归 require 的按模块目录解析留 M2）。

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::Value;
use aluka_bytecode::BytecodeModule;
use std::path::{Path, PathBuf};

impl Vm {
    /// 开启 CJS 模块上下文：注入 `require` 原生函数并记录入口基准目录。
    ///
    /// 之后 [`Vm::run_module`] 遇到「入口函数返回闭包」时按 7 参 CJS 签名
    /// 调用；未调用本方法时保持既有行为（无参调用，golden 语料零回归）。
    pub fn setup_cjs(&mut self, entry_path: &Path) {
        let base = entry_path
            .parent()
            .map(Path::to_path_buf)
            .unwrap_or_else(|| PathBuf::from("."));
        self.base_dir = Some(base);
        self.entry_file = entry_path.display().to_string();
        let require = self.alloc_native_fn("require");
        self.require_fn = Some(require);
    }

    /// 内置模块名 → 模块对象（`node:` 前缀剥离；M2：`fs`/`path`/`os`）。
    fn builtin_module(&self, name: &str) -> Option<Value> {
        let name = name.strip_prefix("node:").unwrap_or(name);
        match name {
            "fs" => self.fs_object.map(Value::Object),
            "path" => self.path_module.map(Value::Object),
            "os" => self.os_module.map(Value::Object),
            "stream" => self.stream_module.map(Value::Object),
            "events" => self.events_module.map(Value::Object),
            _ => self.builtin_registry.module(name).map(Value::Object),
        }
    }

    /// `require(specifier)`：解析、加载（带缓存与循环依赖占位）并返回 exports。
    pub(crate) fn call_require(&mut self, specifier: Value) -> Result<Value, VmError> {
        let spec = self.format_value(specifier);
        // 内置模块优先（fs/path/process 等不经文件系统）
        if let Some(m) = self.builtin_module(&spec) {
            return Ok(m);
        }
        let resolved = self
            .resolve_module(&spec)
            .ok_or_else(|| self.module_not_found(&spec))?;
        let key = resolved.display().to_string();
        if let Some(cached) = self.module_exports.get(&key) {
            return Ok(*cached);
        }

        // 读文件 → 反序列化 → 校验
        let data = std::fs::read(&resolved).map_err(|e| {
            let msg = self.alloc_string(format!("Cannot read module '{spec}': {e}"));
            VmError::Thrown(Value::Object(msg))
        })?;
        let module = BytecodeModule::deserialize_go(&data).map_err(|e| {
            let msg = self.alloc_string(format!("module '{spec}' deserialize: {e}"));
            VmError::Thrown(Value::Object(msg))
        })?;
        module.verify().map_err(|e| {
            let msg = self.alloc_string(format!("module '{spec}' verify: {e}"));
            VmError::Thrown(Value::Object(msg))
        })?;

        // 预建模块上下文（exports 先进缓存：循环依赖方拿到未完成 exports）
        let exports = Value::Object(self.alloc_ordinary());
        let module_obj = Value::Object(self.alloc_ordinary());
        self.set_property(module_obj, "exports", exports)?;
        self.module_exports.insert(key.clone(), exports);

        // <main> 执行取模块闭包；模块闭包的 7 参调用期间必须保持
        // `module_functions` 装载（func_idx 是本模块函数表的索引）
        let saved_functions = std::mem::take(&mut self.module_functions);
        let saved_module_constants = std::mem::take(&mut self.module_constants);
        self.module_functions = module
            .functions
            .iter()
            .map(|f| std::rc::Rc::new(f.clone()))
            .collect();
        self.module_constants = self
            .module_functions
            .iter()
            .map(|f| std::rc::Rc::new(f.constants.clone()))
            .collect();
        let invoke_result = (|| -> Result<Value, VmError> {
            let closure = self.run_func(&self.module_functions[0].clone())?;

            let (func_idx, upvalues) = match closure {
                Value::Object(r) => match self.heap.get(r.0 as usize) {
                    Some(HeapObject::Closure {
                        func_idx, upvalues, ..
                    }) => (*func_idx, upvalues.clone()),
                    // 非 CJS 包装形态（<main> 未返回闭包）：无模块体可调
                    _ => return Ok(exports),
                },
                _ => return Ok(exports),
            };

            // 7 参 CJS 签名调用模块闭包
            let filename = Value::Object(self.alloc_string(resolved.display().to_string()));
            let dirname = Value::Object(
                self.alloc_string(
                    resolved
                        .parent()
                        .map(Path::to_string_lossy)
                        .unwrap_or_default()
                        .to_string(),
                ),
            );
            let require_fn = self
                .require_fn
                .unwrap_or_else(|| self.alloc_native_fn("require"));
            self.invoke_function(
                func_idx,
                Value::Undefined,
                &[
                    Value::Object(require_fn),
                    module_obj,
                    exports,
                    filename,
                    dirname,
                    Value::Undefined, // __import
                    Value::Undefined, // __importMeta
                ],
                upvalues,
            )?;
            Ok(exports)
        })();

        // 恢复入口模块的函数表后，再读最终的 exports（module.exports 可重赋值）
        self.module_functions = saved_functions;
        self.module_constants = saved_module_constants;
        invoke_result?;
        let final_exports = self.get_property(module_obj, "exports")?;
        self.module_exports.insert(key, final_exports);
        Ok(final_exports)
    }

    /// 解析 `specifier` 为字节码文件路径（基准目录 + `.js`→`.bc` / 补 `.bc`）。
    fn resolve_module(&self, specifier: &str) -> Option<PathBuf> {
        let base = self.base_dir.clone().unwrap_or_else(|| PathBuf::from("."));
        let rel = specifier.strip_prefix("./").unwrap_or(specifier);
        let mut p = base.join(rel);
        match p.extension().and_then(|e| e.to_str()) {
            Some("bc") => {}
            _ => {
                p.set_extension("bc");
            }
        }
        p.is_file().then_some(p)
    }

    fn module_not_found(&mut self, spec: &str) -> VmError {
        let msg = self.alloc_string(format!("Cannot find module '{spec}'"));
        VmError::Thrown(Value::Object(msg))
    }
}
