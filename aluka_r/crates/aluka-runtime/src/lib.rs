//! 运行时装配：把各 crate 组装成可用的引擎实例。
//!
//! 这是嵌入方与 CLI 唯一需要接触的门面：创建 [`Runtime`]，喂源码，拿结果。
//! 内部按固定顺序装配——堆与值系统、全局能力域、内置模块注册表、模块
//! 解析器，最后才是编译与执行。

use aluka_builtins::Registry;
use aluka_compiler::compile;
use aluka_core::{Heap, Value};
use aluka_module::Resolver;
use aluka_parser::ast::Program;
use aluka_vm::{Vm, VmError};
use aluka_webapi::Capability;

/// 运行时装配或执行失败的原因。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RuntimeError {
    /// 执行期错误
    Vm(VmError),
}

impl std::fmt::Display for RuntimeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            RuntimeError::Vm(err) => write!(f, "{err}"),
        }
    }
}

impl std::error::Error for RuntimeError {}

impl From<VmError> for RuntimeError {
    fn from(err: VmError) -> Self {
        RuntimeError::Vm(err)
    }
}

/// 一个引擎实例：自带堆、全局能力、内置模块与模块解析器。
///
/// 实例之间完全隔离——包括模块解析条件（运行时用 Node 条件，打包器用
/// browser 条件），这一点是刻意的，见 `aluka-module` 的模块文档。
#[derive(Debug)]
pub struct Runtime {
    heap: Heap,
    builtins: Registry,
    resolver: Resolver,
    capabilities: Vec<Capability>,
}

impl Runtime {
    /// 按运行时默认配置装配实例。
    #[must_use]
    pub fn new() -> Self {
        Self {
            heap: Heap::new(),
            builtins: Registry::with_planned_modules(),
            resolver: Resolver::for_runtime(),
            capabilities: Capability::all().to_vec(),
        }
    }

    /// 执行一棵已解析的语法树，返回其值。
    ///
    /// M0 阶段直连 compiler 与 VM；模块加载、事件循环与微任务在 M1/M2
    /// 接入（见 devplan 的里程碑划分）。
    pub fn evaluate(&mut self, program: &Program) -> Result<Value, RuntimeError> {
        let unit = compile(program);
        let mut vm = Vm::new(unit.locals);
        let res = vm.run(&unit.code)?;
        Ok(res.into())
    }

    /// 内置模块注册表（迁移期兼作进度看板）。
    #[must_use]
    pub fn builtins(&self) -> &Registry {
        &self.builtins
    }

    /// 模块解析器。
    #[must_use]
    pub fn resolver(&self) -> &Resolver {
        &self.resolver
    }

    /// 已装配的全局能力域。
    #[must_use]
    pub fn capabilities(&self) -> &[Capability] {
        &self.capabilities
    }

    /// 堆统计快照（分配数、存活数、回收次数）。
    #[must_use]
    pub fn heap_stats(&self) -> aluka_core::gc::GcStats {
        self.heap.stats()
    }
}

impl Default for Runtime {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use aluka_parser::ast::{Expr, Stmt};

    #[test]
    fn evaluates_an_addition_end_to_end() {
        let program = Program {
            body: vec![Stmt::Expr(Expr::Binary {
                op: "+".to_owned(),
                left: Box::new(Expr::Number(20.0)),
                right: Box::new(Expr::Number(22.0)),
            })],
        };
        let mut runtime = Runtime::new();
        match runtime.evaluate(&program) {
            Ok(Value::Number(n)) => assert_eq!(n, 42.0),
            other => panic!("expected Number(42), got {other:?}"),
        }
    }

    #[test]
    fn assembles_builtins_resolver_and_capabilities() {
        let runtime = Runtime::new();
        assert!(!runtime.builtins().is_empty());
        assert!(runtime.resolver().has_condition("node"));
        assert!(!runtime.resolver().has_condition("browser"));
        assert_eq!(runtime.capabilities().len(), Capability::all().len());
    }

    #[test]
    fn fresh_runtime_has_empty_heap_stats() {
        let runtime = Runtime::new();
        assert_eq!(runtime.heap_stats().allocated, 0);
        assert_eq!(runtime.heap_stats().collections, 0);
    }
}
