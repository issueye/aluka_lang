//! 内置库注册表：把内置模块的纯函数实现聚合为可独立并行开发的单元。
//!
//! 并行开发纪律（see `aluka_r/docs/builtins-plan.md`）：
//! - 每个模块一个独立文件（如 `builtins/querystring.rs`），实现一个
//!   [`ModuleBuilder`] 注册函数；
//! - **禁止修改** `interpreter.rs` / `call.rs` / `property.rs` 等核心解释器
//!   文件——本注册表经一道通用分派分支接入解释器（`CALL_METHOD` 时查找）；
//! - 方法以 `模块名.方法名` 命名（如 `querystring.parse`），native 属性
//!   （如 `os.EOL`）走模块对象属性物化（模块创建时写入）。
//!
//! 分派模型：`CALL_METHOD` 拦截链在 receiver 是模块单例且方法名命中
//! [`BuiltinRegistry::lookup`] 时，调 [`BuiltinHandler`]（纯函数指针，
//! 无借用问题），返回值直接压栈。

pub mod assert;
pub mod buffer;
pub mod constants;
pub mod fs;
pub mod os;
pub mod path_posix;
pub mod path_win32;
pub mod perf_hooks;
pub mod querystring;
pub mod string_decoder;
pub mod timers;
pub mod util;
pub mod v8;

pub(crate) use crate::microtask::{Job, PendingResume};

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::Value;
use aluka_core::ObjectRef;
use std::cell::RefCell;
use std::collections::HashMap;

thread_local! {
    static CURRENT_RECEIVER: RefCell<Value> = const { RefCell::new(Value::Undefined) };
}

/// 设置当前分派调用的接收者（this）。
pub fn set_current_receiver(v: Value) {
    CURRENT_RECEIVER.with(|r| *r.borrow_mut() = v);
}

/// 获取当前分派调用的接收者（this）。
pub fn current_receiver() -> Value {
    CURRENT_RECEIVER.with(|r| *r.borrow())
}

/// 内置方法处理器：`(vm, 实参) -> 返回值`。
pub type BuiltinHandler = fn(&mut Vm, &[Value]) -> Result<Value, VmError>;

/// 内置模块注册条目。
pub struct ModuleDef {
    /// 模块名（`require("name")` 与 `node:name` 命中）
    pub name: &'static str,
    /// 创建模块对象并登记方法到 `registry`（返回模块单例句柄）
    ///
    /// # Errors
    /// 实现失败时返回 VM 错误（正常实现不应失败）
    pub build: fn(&mut Vm, &mut BuiltinRegistry) -> Result<ObjectRef, VmError>,
}

/// 内置库注册表：模块对象工厂 + 方法名到处理器的分派表。
#[derive(Debug, Default)]
pub struct BuiltinRegistry {
    /// `模块名.方法名` → 处理器
    dispatch: HashMap<String, BuiltinHandler>,
    /// 模块单例句柄（模块名 → 堆句柄）
    modules: HashMap<&'static str, ObjectRef>,
}

/// 内置模块清单（新增模块在 `mod.rs` 内注册）。
macro_rules! builtin_modules {
    () => {
        &[
            crate::builtins::constants::MODULE,
            crate::builtins::path_posix::MODULE,
            crate::builtins::path_win32::MODULE,
            crate::builtins::querystring::MODULE,
            crate::builtins::string_decoder::MODULE,
            crate::builtins::fs::MODULE,
            crate::builtins::fs::STAT_MODULE,
            crate::builtins::os::MODULE,
            crate::builtins::util::MODULE,
            crate::builtins::util::TYPES_MODULE,
            crate::builtins::assert::MODULE,
            crate::builtins::buffer::MODULE,
            crate::builtins::buffer::BUFFER_CLASS_MODULE,
            crate::builtins::buffer::INSTANCE_MODULE,
            crate::builtins::perf_hooks::MODULE,
            crate::builtins::perf_hooks::PERFORMANCE_MODULE,
            crate::builtins::v8::MODULE,
            crate::builtins::timers::MODULE,
            crate::builtins::timers::PROMISES_MODULE,
        ]
    };
}

/// 向解释器注册内置库：调用全部模块的 `build` 并预热注册表。
///
/// 在 `Vm::new` 中调用一次；模块可多次 `require`（命中各自单例）。
pub fn register_all(vm: &mut Vm) -> Result<(), VmError> {
    let defs: &[ModuleDef] = builtin_modules!();
    let mut registry = BuiltinRegistry::default();
    for def in defs {
        let module_ref = (def.build)(vm, &mut registry)?;
        registry.modules.insert(def.name, module_ref);
    }
    vm.builtin_registry = registry;
    Ok(())
}

/// 模块注册表便捷宏：声明模块与方法的处理器映射。
///
/// 用法（模块文件内）：
/// ```ignore
/// use crate::builtins::{register_all, BuiltinRegistry};
/// pub const MODULE: ModuleDef = ModuleDef {
///     name: "querystring",
///     build,
/// };
/// fn build(vm: &mut Vm) -> Result<ObjectRef, VmError> { /* 建对象+登记 */ }
/// ```
#[macro_export]
macro_rules! builtin_module {
    ($name:literal, $build:path, $handlers:expr) => {
        pub const MODULE: $crate::builtins::ModuleDef = $crate::builtins::ModuleDef {
            name: $name,
            build: $build,
        };
    };
}

/// 注册模块时把方法名登记进分派表（模块 build 内部调用）。
pub fn register_handler(
    registry: &mut BuiltinRegistry,
    module: &str,
    method: &str,
    handler: BuiltinHandler,
) {
    registry.dispatch.insert(join_key(module, method), handler);
}

fn join_key(module: &str, method: &str) -> String {
    format!("{module}.{method}")
}

impl BuiltinRegistry {
    /// 以「模块名.方法名」`full` 查询分派表。
    #[must_use]
    pub fn lookup(&self, full: &str) -> Option<BuiltinHandler> {
        self.dispatch.get(full).copied()
    }

    /// 模块单例句柄。
    #[must_use]
    pub fn module(&self, name: &str) -> Option<ObjectRef> {
        self.modules.get(name).copied()
    }

    /// 反查句柄所属模块名。
    #[must_use]
    pub fn module_of(&self, r: ObjectRef) -> Option<&'static str> {
        self.modules
            .iter()
            .find(|(_, m)| **m == r)
            .map(|(name, _)| *name)
    }

    /// 判断值是否为本注册表管理的模块单例。
    #[must_use]
    pub fn is_module_object(&self, val: Value) -> bool {
        matches!(val, Value::Object(r) if self.modules.values().any(|m| *m == r))
    }
}

/// 统一分派入口：`receiver` 是注册表模块单例且 `模块名.方法名` 命中时调用。
///
/// 返回 `None` 表示未命中（调用方走既有路径）；`Some(Ok(v))`/`Some(Err(e))`
/// 表示已处理。
pub fn try_dispatch(
    vm: &mut Vm,
    receiver: Value,
    method: &str,
    args: &[Value],
) -> Option<Result<Value, VmError>> {
    let Value::Object(r) = receiver else {
        return None;
    };
    let key = match &vm.heap[r.index()] {
        // 形态一：GET_PROP 后调用（receiver 是 NativeFn "模块.方法"）
        HeapObject::NativeFn { name } if name.contains('.') => name.clone(),
        // 形态二：模块单例直调（receiver 是模块对象或类构造器）
        HeapObject::Ordinary { properties, .. } | HeapObject::NativeCtor { properties, .. } => {
            if properties.contains_key("_isBuffer") {
                format!("buffer:instance.{method}")
            } else if let Some(module_name) = vm.builtin_registry.module_of(r) {
                format!("{module_name}.{method}")
            } else {
                return None;
            }
        }
        _ => return None,
    };
    let handler = vm.builtin_registry.lookup(&key)?;
    set_current_receiver(receiver);
    Some(handler(vm, args))
}

/// 供其它文件安全地给单例挂属性（绕过借用拆分）。
pub fn set_module_prop(
    vm: &mut Vm,
    obj: ObjectRef,
    key: &str,
    value: Value,
) -> Result<(), VmError> {
    let _ = vm.set_property(Value::Object(obj), key, value);
    Ok(())
}

/// 模块对象识别辅助（供解释器与测试）。
pub fn is_module_heap_obj(vm: &Vm, r: ObjectRef) -> bool {
    // 只是形状辅助：模块对象都是 Ordinary；不做额外区分
    matches!(vm.heap.get(r.index()), Some(HeapObject::Ordinary { .. }))
}
