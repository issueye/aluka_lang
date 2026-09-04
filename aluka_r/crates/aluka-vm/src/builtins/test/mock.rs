//! node:test mock 面（Phase 8）：函数/方法 spy 与 MockTracker。
//!
//! 移植 Go oracle（`nodetest/test_mock.go`）的 MockTracker 表面：
//! `fn` / `method` / `getter` / `setter` / `property` / `restoreAll` / `reset`。
//! spy 函数以「固定槽位 trampoline 池」实现（Rust 处理器为 fn 指针、
//! 无闭包捕获——每槽位一个常量泛型实例化，状态存于线程局部表）。
//!
//! 已知限制：spy 为原生函数对象（无属性表），Node 的 `spy.mock.calls`
//! 观测面不可达——调用记录保存在引擎内侧（`MockTracker` 语义），`restore`
//! /委托/`mockImplementation` 行为完整。

use super::context;
use crate::builtins::BuiltinHandler;
use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::Value;
use std::cell::RefCell;

/// spy 池容量（每槽位一个 trampoline）。
const SPY_POOL_SIZE: usize = 32;

/// tracker 归属：模块级全局 or per-test 作用域。
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum TrackerScope {
    /// 模块级 `mock`（不自动还原）。
    Global,
    /// `t.mock`（测试结束时自动还原）。
    Scoped,
}

/// spy 状态（槽位表条目）。
#[derive(Clone)]
pub struct SpyState {
    /// spy 函数名（mock.method 的方法名）。
    pub method: String,
    /// 被替换的目标对象（`mock.fn` 创建的独立函数无 target）。
    pub target: Option<Value>,
    /// 原始属性值/实现。
    pub original: Value,
    /// mockImplementation 替换实现。
    pub impl_val: Value,
    /// mockImplementationOnce 单次实现。
    pub once_impl: Value,
    /// 是否 `mock.fn` 独立函数（restore 时不写回 target）。
    pub is_fn: bool,
    /// tracker 归属。
    pub scope: TrackerScope,
}

thread_local! {
    /// spy 槽位表（None = 空槽）。
    static SPY_STORE: RefCell<Vec<Option<SpyState>>> = const { RefCell::new(Vec::new()) };
}

/// 分配空 spy 槽位。
fn alloc_slot() -> Option<usize> {
    SPY_STORE.with(|s| {
        let mut guard = s.borrow_mut();
        if guard.len() < SPY_POOL_SIZE {
            guard.push(None);
            Some(guard.len() - 1)
        } else {
            guard.iter().position(|slot| slot.is_none())
        }
    })
}

/// 读取 spy 槽位。
fn with_slot<R>(slot: usize, f: impl FnOnce(&SpyState) -> R) -> Option<R> {
    SPY_STORE.with(|s| s.borrow().get(slot).and_then(|o| o.as_ref()).map(f))
}

/// 修改 spy 槽位（槽位须已占用）。
fn with_slot_mut<R>(slot: usize, f: impl FnOnce(&mut SpyState) -> R) -> Option<R> {
    SPY_STORE.with(|s| {
        let mut guard = s.borrow_mut();
        guard.get_mut(slot).and_then(|o| o.as_mut()).map(f)
    })
}

/// 写入 spy 槽位（覆盖占位 None）。
fn set_slot(slot: usize, state: SpyState) {
    SPY_STORE.with(|s| {
        let mut guard = s.borrow_mut();
        if let Some(o) = guard.get_mut(slot) {
            *o = Some(state);
        }
    });
}

/// 泄漏 spy 状态（克隆快照；槽位按值访问规避借用冲突）。
fn take_slot(slot: usize) -> Option<SpyState> {
    with_slot(slot, Clone::clone)
}

/// 常量泛型 trampoline：槽位 N 的 spy 调用入口。
fn spy_trampoline<const N: usize>(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    spy_call(vm, N, args)
}

macro_rules! spy_pool {
    ($($i:literal),*) => {
        /// 槽位 → 处理器表（构建期注册到分派表）。
        pub static SPY_TRAMPS: [BuiltinHandler; SPY_POOL_SIZE] = [
            $(spy_trampoline::<$i>),*
        ];
    };
}

spy_pool!(
    0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25,
    26, 27, 28, 29, 30, 31
);

/// spy 调用：记录 arguments → 委托 once_impl/impl/original（保持 this——
/// Node 语义）→ 回填 result。
fn spy_call(vm: &mut Vm, slot: usize, args: &[Value]) -> Result<Value, VmError> {
    let state = match take_slot(slot) {
        Some(s) => s,
        None => return Ok(Value::Undefined),
    };
    // 实现解析：onceImpl（一次性，用后还原）> impl > original。
    let cur_impl = if !matches!(state.once_impl, Value::Undefined) {
        let once = state.once_impl;
        with_slot_mut(slot, |s| s.once_impl = Value::Undefined);
        once
    } else if !matches!(state.impl_val, Value::Undefined) {
        state.impl_val
    } else {
        Value::Undefined
    };
    let this_val = crate::builtins::current_receiver();
    let target = if !matches!(cur_impl, Value::Undefined) {
        cur_impl
    } else {
        state.original
    };
    if super::is_function_value(vm, target) {
        return vm.invoke_callable(target, this_val, args);
    }
    Ok(Value::Undefined)
}

/// MockTracker 构造（模块级 `mock` 与 per-test `t.mock` 共用）。
pub fn new_tracker(vm: &mut Vm, scope: TrackerScope) -> Value {
    let mock_obj = vm.alloc_ordinary();
    let ns_val = Value::Object(vm.alloc_string("test:mock".to_owned()));
    let _ = vm.set_property(Value::Object(mock_obj), "_builtinNs", ns_val);
    for (prop, name) in [
        ("fn", "test:mock.fn"),
        ("method", "test:mock.method"),
        ("getter", "test:mock.getter"),
        ("setter", "test:mock.setter"),
        ("property", "test:mock.property"),
        ("restoreAll", "test:mock.restoreAll"),
        ("reset", "test:mock.reset"),
    ] {
        let fn_ref = vm.alloc_native_fn(name);
        let _ = vm.set_property(Value::Object(mock_obj), prop, Value::Object(fn_ref));
    }
    let _ = scope;
    Value::Object(mock_obj)
}

/// 记录新 spy（分配槽位并登记分派）。
fn install_spy(vm: &mut Vm, state: SpyState) -> Result<Value, VmError> {
    let Some(slot) = alloc_slot() else {
        return Err(super::asserts::thrown_msg(
            vm,
            "mock tracker spy pool exhausted (max 32)",
        ));
    };
    set_slot(slot, state);
    register_handler_at(vm, slot);
    let fn_ref = vm.alloc_native_fn(&format!("test:mockSpy.{slot}"));
    // per-test tracker：挂到当前状态（测试结束自动还原——Node 语义）。
    if take_slot(slot).map(|s| s.scope) == Some(TrackerScope::Scoped) {
        context::current_add_mock_spy(slot);
    }
    Ok(Value::Object(fn_ref))
}

/// 运行期把槽位处理器登记进分派表（`test:mockSpy.{slot}` → trampoline）。
fn register_handler_at(vm: &mut Vm, slot: usize) {
    let handler = SPY_TRAMPS[slot];
    crate::builtins::register_handler(
        &mut vm.builtin_registry,
        "test:mockSpy",
        &slot.to_string(),
        handler,
    );
}

/// `mock.fn([impl])`：独立 spy 函数。
fn mock_fn(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    let impl_val = match args.first() {
        Some(v) if super::is_function_value(vm, *v) => *v,
        _ => Value::Undefined,
    };
    install_spy(
        vm,
        SpyState {
            method: String::new(),
            target: None,
            original: impl_val,
            impl_val,
            once_impl: Value::Undefined,
            is_fn: true,
            scope: tracker_scope_of(),
        },
    )
}

/// 读取「正在创建 tracker 的作用域」：t.mock 的创建发生在测试上下文构造中
/// （CURRENT 已绑定），模块级 mock 在其外。
fn tracker_scope_of() -> TrackerScope {
    if context::current_id().is_some() {
        TrackerScope::Scoped
    } else {
        TrackerScope::Global
    }
}

/// `mock.method(target, name[, impl])`：替换对象方法为 spy。
fn mock_method(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    if args.len() < 2 {
        return Err(super::asserts::type_fail(
            vm,
            "mock.method(target, methodName)",
        ));
    }
    let target = match args.first().copied() {
        Some(v) if super::is_function_value(vm, v) || is_ordinary(vm, v) => v,
        _ => {
            return Err(super::asserts::type_fail(
                vm,
                "mock.method target must be an object",
            ));
        }
    };
    let method = vm.format_value(args[1]);
    let original = vm.get_property(target, &method).unwrap_or(Value::Undefined);
    let impl_val = match args.get(2) {
        Some(v) if super::is_function_value(vm, *v) => *v,
        _ => Value::Undefined,
    };
    let spy = install_spy(
        vm,
        SpyState {
            method: method.clone(),
            target: Some(target),
            original,
            impl_val,
            once_impl: Value::Undefined,
            is_fn: false,
            scope: tracker_scope_of(),
        },
    )?;
    let _ = vm.set_property(target, &method, spy);
    Ok(spy)
}

/// 是否普通对象。
fn is_ordinary(vm: &Vm, v: Value) -> bool {
    matches!(v, Value::Object(r)
        if matches!(vm.heap.get(r.index()), Some(HeapObject::Ordinary { .. })))
}

/// `mock.getter(target, name[, impl])`：近似移植——以当前值/实现写入
/// 数据属性（引擎访问器仅支持编译期函数模板）。
fn mock_getter(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    if args.len() < 2 {
        return Err(super::asserts::type_fail(
            vm,
            "mock.getter(target, property)",
        ));
    }
    let target = match args.first().copied() {
        Some(v) if is_ordinary(vm, v) => v,
        _ => {
            return Err(super::asserts::type_fail(
                vm,
                "mock.getter target must be an object",
            ));
        }
    };
    let name = vm.format_value(args[1]);
    let original = vm.get_property(target, &name).unwrap_or(Value::Undefined);
    let impl_val = args.get(2).copied().unwrap_or(Value::Undefined);
    let effective = if !matches!(impl_val, Value::Undefined) {
        impl_val
    } else {
        original
    };
    let _ = vm.set_property(target, &name, effective);
    Ok(Value::Undefined)
}

/// `mock.setter(target, name[, impl])`：近似移植（同 getter 限制）。
fn mock_setter(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    mock_getter(vm, args)
}

/// `mock.property(target, name, value)`：以 value 写入属性（读取可观测）。
fn mock_property(vm: &mut Vm, args: &[Value]) -> Result<Value, VmError> {
    if args.len() < 3 {
        return Err(super::asserts::type_fail(
            vm,
            "mock.property(target, property, value)",
        ));
    }
    let target = match args.first().copied() {
        Some(v) if is_ordinary(vm, v) => v,
        _ => {
            return Err(super::asserts::type_fail(
                vm,
                "mock.property target must be an object",
            ));
        }
    };
    let name = vm.format_value(args[1]);
    let _ = vm.set_property(target, &name, args[2]);
    Ok(Value::Undefined)
}

/// 还原指定槽位（写回 original；独立函数不写回——对齐 Go `restoreAll`）。
pub fn restore_slot(vm: &mut Vm, slot: usize) {
    let Some(state) = take_slot(slot) else {
        return;
    };
    if !state.is_fn {
        if let Some(target) = state.target {
            let _ = vm.set_property(target, &state.method, state.original);
        }
    }
    with_slot_mut(slot, |s| {
        s.impl_val = Value::Undefined;
        s.once_impl = Value::Undefined;
    });
}

/// `mock.restoreAll()`：还原全部并清空（per-test tracker 的还原由
/// 测试结束钩子逐槽位完成，不清理全局槽位表条目以外的状态）。
fn mock_restore_all(vm: &mut Vm, _args: &[Value]) -> Result<Value, VmError> {
    let slots: Vec<usize> = SPY_STORE.with(|s| {
        s.borrow()
            .iter()
            .enumerate()
            .filter(|(_, o)| o.is_some())
            .map(|(i, _)| i)
            .collect()
    });
    for slot in slots {
        restore_slot(vm, slot);
    }
    SPY_STORE.with(|s| {
        s.borrow_mut().clear();
    });
    Ok(Value::Undefined)
}

/// `mock.reset()`：恢复全部原始实现（调用历史保留——Node 22.23 语义）。
fn mock_reset(vm: &mut Vm, _args: &[Value]) -> Result<Value, VmError> {
    let slots: Vec<usize> = SPY_STORE.with(|s| {
        s.borrow()
            .iter()
            .enumerate()
            .filter(|(_, o)| o.is_some())
            .map(|(i, _)| i)
            .collect()
    });
    for slot in slots {
        restore_slot(vm, slot);
    }
    Ok(Value::Undefined)
}

/// 注册 mock 系列处理器（模块 build 时调用一次）。
pub fn register_handlers(registry: &mut crate::builtins::BuiltinRegistry) {
    use crate::builtins::register_handler;
    register_handler(registry, "test:mock", "fn", mock_fn);
    register_handler(registry, "test:mock", "method", mock_method);
    register_handler(registry, "test:mock", "getter", mock_getter);
    register_handler(registry, "test:mock", "setter", mock_setter);
    register_handler(registry, "test:mock", "property", mock_property);
    register_handler(registry, "test:mock", "restoreAll", mock_restore_all);
    register_handler(registry, "test:mock", "reset", mock_reset);
}
