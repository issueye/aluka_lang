//! 虚拟机核心解释器：执行状态定义与操作码分派循环。

use crate::exception::{Completion, FinallyOutcome, PHASE_TRY, TryExitOutcome, TryHandler};
use crate::generator::GeneratorState;
use crate::heap::HeapObject;
use crate::ops::{eq, strict_eq, to_boolean, to_number};
use crate::value::{Upvalue, Value};
use aluka_bytecode::{ClassTemplate, Constant, FuncTemplate, Instr, Op, TryEntry};
use aluka_core::ObjectRef;
use std::collections::HashMap;

/// 执行期可能发生的错误。
#[derive(Debug, Clone, PartialEq)]
pub enum VmError {
    /// 操作数栈下溢（Pop 时栈为空）
    StackUnderflow,
    /// 访问越界局部变量槽位
    LocalOutOfRange,
    /// 函数执行到达末尾但未返回
    MissingReturn,
    /// 整数除以零
    DivisionByZero,
    /// JS 层抛出的异常值（`THROW` 或未捕获时沿调用链传播）
    Thrown(Value),
    /// 生成器 `YIELD` 挂起信号（携带产出的值，由生成器驱动层捕获）
    Yielded(Value),
    /// `AWAIT` 未完成 Promise 的挂起信号（携带 promise 句柄，由 async 驱动层捕获）
    Awaited(aluka_core::ObjectRef),
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
            Self::Thrown(_) => write!(f, "未捕获的 JS 异常"),
            Self::Yielded(_) => write!(f, "生成器挂起信号（不应逃逸到顶层）"),
            Self::Awaited(_) => write!(f, "async 挂起信号（不应逃逸到顶层）"),
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
    /// 当前执行函数的常量池（Rc 共享，帧切换零拷贝）
    pub current_constants: std::rc::Rc<Vec<Constant>>,
    /// 当前模块的常量池表（与 `module_functions` 平行，供 `invoke_function` 零拷贝换帧）
    pub(crate) module_constants: Vec<std::rc::Rc<Vec<Constant>>>,
    /// 当前模块的函数扩展标量头（arguments 槽位等；与函数表平行）
    pub(crate) module_header_extras: Vec<aluka_bytecode::FuncHeaderExtras>,
    /// 堆对象存储
    pub heap: Vec<HeapObject>,
    /// 模块全部函数模板（供跨函数调用与 Getter 调度；Rc 共享避免逐调用深拷贝）
    pub module_functions: Vec<std::rc::Rc<FuncTemplate>>,
    /// 模块全部类模板（供 MakeClass 构造类对象与原型链）
    pub module_classes: Vec<ClassTemplate>,
    /// 当前函数帧所持有的上值列表（供 LoadUpvalue / StoreUpvalue 访问）
    pub current_upvalues: Vec<Upvalue>,
    /// 当前函数帧活跃的打开上值表（slot -> Upvalue），保证同一 slot 共享同一个 RefCell
    pub open_upvalues: HashMap<usize, Upvalue>,
    /// 当前函数帧活跃的 try/catch/finally handler 栈（自底向内层递增）
    pub(crate) try_stack: Vec<TryHandler>,
    /// 当前函数模板的 Try 表（`TRY_ENTER` 按索引克隆条目入栈）
    pub(crate) current_try_table: Vec<TryEntry>,
    /// 全局变量表（`STORE_GLOBAL` 写入、`LOAD_GLOBAL` 优先读取）
    pub globals: HashMap<String, Value>,
    /// `Object.prototype` 单例（普通对象默认隐式原型）
    pub object_prototype: Option<ObjectRef>,
    /// `Array.prototype` 单例（数组默认隐式原型）
    pub array_prototype: Option<ObjectRef>,
    /// `Math` 内置对象单例
    pub math_object: Option<ObjectRef>,
    /// `Error` 原生构造器单例
    pub error_ctor: Option<ObjectRef>,
    /// `Array` 原生构造器单例
    pub array_ctor: Option<ObjectRef>,
    /// `Object` 原生构造器单例
    pub object_ctor: Option<ObjectRef>,
    /// `fs` 内置对象单例（readFileSync/writeFileSync 拦截）
    pub fs_object: Option<ObjectRef>,
    /// `require` 原生函数句柄（`setup_cjs` 后可用）
    pub require_fn: Option<ObjectRef>,
    /// CJS 模块缓存：规范化路径 → exports
    pub(crate) module_exports: HashMap<String, Value>,
    /// 模块解析基准目录（入口文件所在目录）
    pub(crate) base_dir: Option<std::path::PathBuf>,
    /// 入口文件路径（CJS `__filename`）
    pub(crate) entry_file: String,
    /// 最近执行指令的下标（错误定位用）
    pub(crate) last_pc: usize,
    /// 当前执行函数索引（错误定位用；-1 表示无）
    pub(crate) current_func_idx: i64,
    /// nextTick 优先微任务队列（回调函数）
    pub(crate) nexttick_queue: std::collections::VecDeque<Value>,
    /// Promise 微任务队列（Job：回调或帧恢复）
    pub(crate) microtask_queue: std::collections::VecDeque<crate::builtins::Job>,
    /// 宏任务队列（句柄 id + 到期累计毫秒 + 延迟 + 回调 + 是否周期）
    pub(crate) macro_tasks: std::collections::VecDeque<(u64, u64, u64, Value, bool)>,
    /// 定时器句柄计数器（setTimeout/setInterval 分配 id）
    timer_counter: u64,
    /// 已被 clear 的定时器句柄集合（drain 时跳过）
    pub(crate) active_timers: std::collections::HashSet<u64>,
    /// `Promise` 原生构造器单例（resolve/withResolvers 拦截）
    pub promise_ctor: Option<ObjectRef>,
    /// `Map` 原生构造器单例（groupBy 拦截）
    pub map_ctor: Option<ObjectRef>,
    /// `process` 全局对象单例（nextTick 拦截）
    pub process_object: Option<ObjectRef>,
    /// `path` 内置模块单例（join/basename/dirname/extname/resolve 拦截）
    pub path_module: Option<ObjectRef>,
    /// `os` 内置模块单例（platform/homedir/tmpdir 拦截；EOL 属性读取特判）
    pub os_module: Option<ObjectRef>,
    /// `stream` 内置模块单例（Readable 构造器属性物化）
    pub stream_module: Option<ObjectRef>,
    /// `events` 内置模块单例（EventEmitter 构造器属性物化）
    pub events_module: Option<ObjectRef>,
    /// 内置库注册表（querystring/constants 等并行开发模块的分派表）
    pub(crate) builtin_registry: crate::builtins::BuiltinRegistry,
    /// 挂起 async 帧的恢复登记：promise 句柄索引 → 恢复帧
    pub(crate) promise_resumes: HashMap<u32, crate::builtins::PendingResume>,
    /// 生成器对象注册表（堆句柄索引 → 执行状态）
    pub(crate) generators: HashMap<u32, GeneratorState>,
    /// 最近一次 `YIELD` 的恢复点（下一条指令索引）
    pub(crate) yield_pc: usize,
}

impl Vm {
    /// 创建虚拟机，预留 `locals` 个局部槽位（初值 `undefined`）。
    ///
    /// 同时在堆上预建内置原型与构造器单例（`Object.prototype`、`Array.prototype`、
    /// `Math`、`Error`/`Array`/`Object` 构造器），后续所有分配自动挂接正确的原型链。
    #[must_use]
    pub fn new(locals: usize) -> Self {
        let mut vm = Self {
            stack: Vec::new(),
            locals: vec![Value::Undefined; locals],
            stdout_records: Vec::new(),
            current_constants: std::rc::Rc::new(Vec::new()),
            module_constants: Vec::new(),
            module_header_extras: Vec::new(),
            heap: Vec::new(),
            module_functions: Vec::new(),
            module_classes: Vec::new(),
            current_upvalues: Vec::new(),
            open_upvalues: HashMap::new(),
            try_stack: Vec::new(),
            current_try_table: Vec::new(),
            globals: HashMap::new(),
            object_prototype: None,
            array_prototype: None,
            math_object: None,
            error_ctor: None,
            array_ctor: None,
            object_ctor: None,
            generators: HashMap::new(),
            yield_pc: 0,
            last_pc: 0,
            current_func_idx: -1,
            nexttick_queue: std::collections::VecDeque::new(),
            microtask_queue: std::collections::VecDeque::new(),
            macro_tasks: std::collections::VecDeque::new(),
            timer_counter: 0,
            active_timers: std::collections::HashSet::new(),
            promise_ctor: None,
            map_ctor: None,
            process_object: None,
            path_module: None,
            os_module: None,
            stream_module: None,
            events_module: None,
            builtin_registry: std::default::Default::default(),
            promise_resumes: HashMap::new(),
            module_exports: HashMap::new(),
            base_dir: None,
            entry_file: String::new(),
            require_fn: None,
            fs_object: None,
        };
        // Object.prototype：原型链顶端（[[Prototype]] 为 null）
        vm.object_prototype = Some(vm.alloc_ordinary());
        // Array.prototype：沿原型链挂到 Object.prototype，并填充已实现的
        // 数组方法（NativeFn 占位：CALL_METHOD 按名字分派求值，属性存在性
        // 查询（`arr.forEach ? ...`）经原型链命中）
        vm.array_prototype = vm
            .object_prototype
            .map(|p| vm.alloc_ordinary_with_proto(Some(p)));
        if let Some(ap) = vm.array_prototype {
            let methods = [
                "map",
                "filter",
                "find",
                "some",
                "forEach",
                "reduce",
                "reduceRight",
                "join",
                "push",
                "pop",
                "shift",
                "unshift",
                "slice",
                "sort",
            ];
            for m in methods {
                let fn_ref = vm.alloc_native_fn(m);
                let _ = vm.set_property(Value::Object(ap), m, Value::Object(fn_ref));
            }
        }
        // Math 内置对象
        vm.math_object = Some(vm.alloc_ordinary());
        // fs 内置对象
        vm.fs_object = Some(vm.alloc_ordinary());
        // 三个原生构造器（`new` 由解释器拦截求值；instanceof 经 prototype 属性判定）
        let obj_proto = vm.object_prototype;
        vm.error_ctor = Some(vm.alloc_native_ctor("Error", obj_proto));
        vm.array_ctor = Some(vm.alloc_native_ctor("Array", vm.array_prototype));
        vm.object_ctor = Some(vm.alloc_native_ctor("Object", obj_proto));
        // Promise / Map 构造器与 process 全局（微任务与异步基建）
        vm.promise_ctor = Some(vm.alloc_native_ctor("Promise", obj_proto));
        vm.map_ctor = Some(vm.alloc_native_ctor("Map", obj_proto));
        vm.process_object = Some(vm.alloc_ordinary());
        // path 内置模块（方法经 CALL_METHOD 拦截求值）
        let path_mod = vm.alloc_ordinary();
        let join_fn = vm.alloc_native_fn("path.join");
        let basename_fn = vm.alloc_native_fn("path.basename");
        let dirname_fn = vm.alloc_native_fn("path.dirname");
        let extname_fn = vm.alloc_native_fn("path.extname");
        let resolve_fn = vm.alloc_native_fn("path.resolve");
        let _ = vm.set_property(Value::Object(path_mod), "join", Value::Object(join_fn));
        let _ = vm.set_property(
            Value::Object(path_mod),
            "basename",
            Value::Object(basename_fn),
        );
        let _ = vm.set_property(
            Value::Object(path_mod),
            "dirname",
            Value::Object(dirname_fn),
        );
        let _ = vm.set_property(
            Value::Object(path_mod),
            "extname",
            Value::Object(extname_fn),
        );
        let _ = vm.set_property(
            Value::Object(path_mod),
            "resolve",
            Value::Object(resolve_fn),
        );
        vm.path_module = Some(path_mod);
        // stream 内置模块
        let stream_mod = vm.alloc_ordinary();
        vm.stream_module = Some(stream_mod);
        // events 内置模块
        let events_mod = vm.alloc_ordinary();
        vm.events_module = Some(events_mod);
        // os 内置模块
        let os_mod = vm.alloc_ordinary();
        let platform_fn = vm.alloc_native_fn("os.platform");
        let homedir_fn = vm.alloc_native_fn("os.homedir");
        let tmpdir_fn = vm.alloc_native_fn("os.tmpdir");
        let eol = if cfg!(windows) { "\r\n" } else { "\n" };
        let eol_str = vm.alloc_string(eol.to_owned());
        let _ = vm.set_property(
            Value::Object(os_mod),
            "platform",
            Value::Object(platform_fn),
        );
        let _ = vm.set_property(Value::Object(os_mod), "homedir", Value::Object(homedir_fn));
        let _ = vm.set_property(Value::Object(os_mod), "tmpdir", Value::Object(tmpdir_fn));
        let _ = vm.set_property(Value::Object(os_mod), "EOL", Value::Object(eol_str));
        vm.os_module = Some(os_mod);
        // 内置库注册表：必须在全部单例（fs/path/os/process/构造器）初始化之后
        // 预热（内置模块如 fs/os 的 build 复用已建单例）
        let _ = crate::builtins::register_all(&mut vm);
        vm
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
                        HeapObject::BigInt(s) => s.clone(),
                        HeapObject::Array { elements, .. } => {
                            let items: Vec<String> =
                                elements.iter().map(|e| self.format_value(*e)).collect();
                            items.join(",")
                        }
                        HeapObject::Ordinary { .. }
                        | HeapObject::Generator
                        | HeapObject::Promise { .. }
                        | HeapObject::Map { .. }
                        | HeapObject::Readable { .. }
                        | HeapObject::EventEmitter { .. } => "[object Object]".to_owned(),
                        HeapObject::Closure { .. }
                        | HeapObject::NativeCtor { .. }
                        | HeapObject::NativeFn { .. }
                        | HeapObject::PromiseResolver { .. } => "[function Function]".to_owned(),
                        HeapObject::RegExp { pattern, flags } => {
                            format!("/{pattern}/{flags}")
                        }
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

    /// console.log 专用格式化（对齐 Go 版 `inspectValue` 的数组输出）。
    ///
    /// 数组呈现为 `[ a, b ]`（空数组 `[]`，元素 `, ` 分隔、递归同规则），
    /// 其余值与 [`Vm::format_value`] 一致。
    pub fn format_console_value(&self, val: Value) -> String {
        if let Value::Object(r) = val {
            if let Some(HeapObject::Array { elements, .. }) = self.heap.get(r.0 as usize) {
                if elements.is_empty() {
                    return "[]".to_owned();
                }
                let items: Vec<String> = elements
                    .iter()
                    .map(|e| self.format_console_value(*e))
                    .collect();
                return format!("[ {} ]", items.join(", "));
            }
        }
        self.format_value(val)
    }

    /// JS `typeof` 语义的字符串化。
    fn typeof_value(&self, val: Value) -> String {
        match val {
            Value::Undefined => "undefined".to_owned(),
            Value::Null => "object".to_owned(),
            Value::Boolean(_) => "boolean".to_owned(),
            Value::Number(_) => "number".to_owned(),
            Value::Object(r) => match self.heap.get(r.0 as usize) {
                Some(HeapObject::String(_)) => "string".to_owned(),
                Some(HeapObject::BigInt(_)) => "bigint".to_owned(),
                Some(
                    HeapObject::Closure { .. }
                    | HeapObject::NativeCtor { .. }
                    | HeapObject::NativeFn { .. },
                ) => "function".to_owned(),
                _ => "object".to_owned(),
            },
        }
    }

    /// `++` / `--` 的 ToNumeric 递增（BigInt 保持 BigInt，对齐 Go 版 `updateNumeric`）。
    fn update_numeric(&mut self, val: Value, delta: i128) -> Value {
        if let Value::Object(r) = val {
            if let Some(HeapObject::BigInt(s)) = self.heap.get(r.0 as usize) {
                let n: i128 = s.parse().unwrap_or(0);
                let updated = n + delta;
                return Value::Object(self.alloc_bigint(updated.to_string()));
            }
        }
        Value::Number(to_number(val) + delta as f64)
    }

    /// 解析全局名：全局变量表优先，其次内置对象，未知名返回 `undefined`。
    fn resolve_global(&mut self, name: &str) -> Value {
        if let Some(v) = self.globals.get(name) {
            return *v;
        }
        match name {
            "undefined" => Value::Undefined,
            "Math" => self
                .math_object
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "Error" => self
                .error_ctor
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "Array" => self
                .array_ctor
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "Object" => self
                .object_ctor
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "fs" => self
                .fs_object
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "Promise" => self
                .promise_ctor
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "Map" => self.map_ctor.map(Value::Object).unwrap_or(Value::Undefined),
            "process" => self
                .process_object
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "os" => self
                .os_module
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            "URL" => {
                let c = self.alloc_native_ctor("URL", None);
                Value::Object(c)
            }
            "setTimeout" => {
                let f = self.alloc_native_fn("setTimeout");
                Value::Object(f)
            }
            "setInterval" => {
                let f = self.alloc_native_fn("setInterval");
                Value::Object(f)
            }
            "clearTimeout" => {
                let f = self.alloc_native_fn("clearTimeout");
                Value::Object(f)
            }
            "clearInterval" => {
                let f = self.alloc_native_fn("clearInterval");
                Value::Object(f)
            }
            "queueMicrotask" => {
                let f = self.alloc_native_fn("queueMicrotask");
                Value::Object(f)
            }
            "require" => self
                .require_fn
                .map(Value::Object)
                .unwrap_or(Value::Undefined),
            _ => Value::Undefined,
        }
    }

    /// 判断值是否为指定名称的原生函数。
    pub(crate) fn is_native_fn(&self, val: Value, name: &str) -> bool {
        matches!(
            val,
            Value::Object(r)
                if matches!(
                    self.heap.get(r.0 as usize),
                    Some(HeapObject::NativeFn { name: n }) if n == name
                )
        )
    }

    /// 数组回调上下文：`(回调, thisArg)`（thisArg 为第二参数，未传为 undefined）。
    fn array_cb_ctx(&self, args: &[Value]) -> (Value, Value) {
        (
            args.first().copied().unwrap_or(Value::Undefined),
            args.get(1).copied().unwrap_or(Value::Undefined),
        )
    }

    /// 调用数组原型方法的回调：this=thisArg，实参按 JS 规范 `(elem, idx, arr)`。
    fn invoke_array_cb(
        &mut self,
        cb: Value,
        this_arg: Value,
        cb_args: &[Value],
    ) -> Result<Value, VmError> {
        self.invoke_callable(cb, this_arg, cb_args)
    }

    /// `node:path` 轻量方法实现（平台分隔符语义；符号参数规范化处理）。
    fn path_method(&self, method: &str, args: &[Value]) -> String {
        use std::path::{Path, PathBuf};
        let parts: Vec<String> = args.iter().map(|v| self.format_value(*v)).collect();
        match method {
            "join" => {
                let parts: Vec<String> = parts
                    .into_iter()
                    .filter(|p| !p.is_empty() && *p != "undefined" && *p != "null")
                    .collect();
                let mut buf = PathBuf::new();
                for p in &parts {
                    buf.push(p);
                }
                self.win_leading_slash(&buf.to_string_lossy())
            }
            "basename" => {
                let p = Path::new(&parts[0]);
                let name = p
                    .file_name()
                    .map(|n| n.to_string_lossy().to_string())
                    .unwrap_or_default();
                match args.get(1) {
                    None | Some(Value::Undefined) => name,
                    // 第二参为扩展名（字符串对象）：剥离（如 ".txt"）
                    Some(Value::Object(_)) => name
                        .strip_suffix(&self.format_value(*args.get(1).expect("已确认存在")))
                        .unwrap_or(&name)
                        .to_string(),
                    _ => name,
                }
            }
            "dirname" => Path::new(&parts[0])
                .parent()
                .map(|p| self.win_leading_slash(&p.to_string_lossy()))
                .unwrap_or_default(),
            "extname" => Path::new(&parts[0])
                .extension()
                .map(|e| format!(".{}", e.to_string_lossy()))
                .unwrap_or_default(),
            _ => {
                // resolve：当前目录为基座
                let mut buf = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
                for p in &parts {
                    buf.push(p);
                }
                self.win_leading_slash(&buf.to_string_lossy())
            }
        }
    }

    /// 路径输出的前导 `/` 转 `\`（对齐 Windows 语义的 Go filepath 输出）。
    fn win_leading_slash(&self, s: &str) -> String {
        // Windows 分隔符语义：路径输出统一为 `\`（Go filepath 对齐）
        s.replace('/', "\\")
    }

    /// `new URL(href)`：轻量解析并物化属性（protocol/host/hostname/port/pathname/
    /// search/hash/href/origin），对齐 Go `node:url` 输出。
    pub(crate) fn url_constructor(&mut self, args: &[Value]) -> Value {
        let href = match args.first() {
            Some(v) => self.format_value(*v),
            None => String::new(),
        };
        let mut properties: Vec<(&str, String)> = Vec::new();
        properties.push(("href", href.clone()));

        let (scheme, rest) = match href.split_once(':') {
            Some((s, r)) if !r.is_empty() => (format!("{s}:"), r.strip_prefix("//").unwrap_or(r)),
            _ => ("".to_owned(), href.as_str()),
        };
        properties.push(("protocol", scheme.clone()));

        // authority 到首个 / ? #
        let authority_end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
        let authority = &rest[..authority_end];
        let tail = &rest[authority_end..];
        let (pathname, search, hash) = {
            let q = tail.find('?');
            let h = tail.find('#');
            let path_end = q.or(h).unwrap_or(tail.len());
            let pathname = &tail[..path_end];
            let search = match q {
                Some(qi) => {
                    let se = h.map(|hi| hi.min(tail.len())).unwrap_or(tail.len());
                    &tail[qi..se]
                }
                None => "",
            };
            let hash = match h {
                Some(hi) => &tail[hi..],
                None => "",
            };
            (pathname, search, hash)
        };
        properties.push(("pathname", pathname.to_owned()));
        properties.push(("search", search.to_owned()));
        properties.push(("hash", hash.to_owned()));

        let userinfo_end = authority.find('@').map(|i| i + 1).unwrap_or(0);
        let host_port = &authority[userinfo_end..];
        let (host, port) = match host_port.split_once(':') {
            Some((h, p)) => (h, p.to_owned()),
            None => (host_port, String::new()),
        };
        properties.push(("hostname", host.to_owned()));
        properties.push(("port", port.clone()));
        properties.push((
            "host",
            if port.is_empty() {
                host.to_owned()
            } else {
                format!("{host}:{port}")
            },
        ));
        properties.push((
            "origin",
            if scheme.is_empty() {
                String::new()
            } else if port.is_empty() {
                format!("{scheme}//{host}")
            } else {
                format!("{scheme}//{host}:{port}")
            },
        ));

        let obj = self.alloc_ordinary();
        for (k, v) in properties {
            let s_ref = self.alloc_string(v);
            let _ = self.set_property(Value::Object(obj), k, Value::Object(s_ref));
        }
        Value::Object(obj)
    }

    /// 判断值是否为可读流实例。
    pub(crate) fn is_readable_obj(&self, val: Value) -> bool {
        matches!(
            val,
            Value::Object(r)
                if matches!(self.heap.get(r.0 as usize), Some(HeapObject::Readable { .. }))
        )
    }

    /// 判断值是否为正则表达式对象。
    pub(crate) fn is_regexp_obj(&self, val: Value) -> bool {
        matches!(
            val,
            Value::Object(r)
                if matches!(self.heap.get(r.0 as usize), Some(HeapObject::RegExp { .. }))
        )
    }

    /// `RegExp.prototype.exec` 求值：成功返回结果数组元素
    /// `[全匹配, 组1, …]`（未参与的组为 `undefined`），无匹配返回 `None`。
    ///
    /// 语法错误与回溯超限都以 JS 异常值上抛（`VmError::Thrown`）。
    fn regexp_exec(&mut self, re: Value, subject: &str) -> Result<Option<Vec<Value>>, VmError> {
        let (pattern, flags) = match re {
            Value::Object(r) => match self.heap.get(r.0 as usize) {
                Some(HeapObject::RegExp { pattern, flags }) => (pattern.clone(), flags.clone()),
                _ => return Err(VmError::LocalOutOfRange),
            },
            _ => return Err(VmError::LocalOutOfRange),
        };
        let compiled = aluka_regex::Regex::compile(&pattern, &flags).map_err(|e| {
            let msg = self.alloc_string(e.to_string());
            VmError::Thrown(Value::Object(msg))
        })?;
        let matched = compiled.find(subject).map_err(|e| {
            let msg = self.alloc_string(e.to_string());
            VmError::Thrown(Value::Object(msg))
        })?;
        let Some(m) = matched else {
            return Ok(None);
        };
        let chars: Vec<char> = subject.chars().collect();
        let slice = |a: usize, b: usize| -> String { chars[a..b].iter().collect() };
        let mut elems = vec![Value::Object(self.alloc_string(slice(m.start, m.end)))];
        for group in &m.groups {
            let elem = match group {
                Some((a, b)) => Value::Object(self.alloc_string(slice(*a, *b))),
                None => Value::Undefined,
            };
            elems.push(elem);
        }
        Ok(Some(elems))
    }

    /// 执行指令序列，返回 `Return` 或 `ReturnUndef` 携带的值。
    pub fn run(&mut self, code: &[Instr]) -> Result<Value, VmError> {
        self.run_with_constants(code, &[])
    }

    /// 携带常量池执行指令序列。
    ///
    /// 扮演异常展开边界：本帧无 handler 接住的 [`VmError::Thrown`] 会继续向上
    /// （调用者帧的 `invoke_function` 调用点）传播，与 Go 版 `jsThrow` 逐帧上抛一致。
    pub fn run_with_constants(
        &mut self,
        code: &[Instr],
        constants: &[Constant],
    ) -> Result<Value, VmError> {
        // 外部低频入口：借用切片包装为 Rc（内部热路径走 run_with_constants_rc）
        self.run_with_constants_rc(code, std::rc::Rc::new(constants.to_vec()), 0)
    }

    /// 内部热路径：常量池以 `Rc` 持有（帧切换零拷贝）。
    pub(crate) fn run_with_constants_rc(
        &mut self,
        code: &[Instr],
        constants: std::rc::Rc<Vec<Constant>>,
        start_pc: usize,
    ) -> Result<Value, VmError> {
        self.run_with_constants_at(code, constants, start_pc)
    }

    /// 携带常量池从 `start_pc` 起执行指令序列（生成器挂起恢复的入口）。
    ///
    /// 扮演异常展开边界：本帧无 handler 接住的 [`VmError::Thrown`] 会继续向上
    /// （调用者帧的 `invoke_function` 调用点）传播，与 Go 版 `jsThrow` 逐帧上抛一致；
    /// [`VmError::Yielded`] 直接上抛，由生成器驱动层捕获。
    pub(crate) fn run_with_constants_at(
        &mut self,
        code: &[Instr],
        constants: std::rc::Rc<Vec<Constant>>,
        start_pc: usize,
    ) -> Result<Value, VmError> {
        self.current_constants = constants;
        let mut pc = start_pc;
        loop {
            match self.exec_frame(code, pc) {
                Ok(value) => return Ok(value),
                Err(VmError::Thrown(exc)) => match self.find_handler_in_frame(exc) {
                    // 本帧接住：从 handler 入口（catch 压入异常 / finally 直跳）续跑
                    Some(next_pc) => pc = next_pc,
                    None => return Err(VmError::Thrown(exc)),
                },
                Err(err) => {
                    eprintln!(
                        "[vm-err] func={} pc={} stack={} err={err:?}",
                        self.current_func_idx,
                        self.last_pc,
                        self.stack.len()
                    );
                    return Err(err);
                }
            }
        }
    }

    /// 从 `start_pc` 起单遍执行当前帧指令流。
    ///
    /// 遇到未接住的 `Thrown` 即返回，由 [`Vm::run_with_constants`] 查找 handler
    /// 后重入续跑；嵌套调用（`invoke_function`）在返回前已恢复本帧上下文。
    fn exec_frame(&mut self, code: &[Instr], start_pc: usize) -> Result<Value, VmError> {
        let num_instrs = code.len();
        let mut pc = start_pc;

        while pc < num_instrs {
            self.last_pc = pc;
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
                    let c = self
                        .current_constants
                        .get(idx)
                        .ok_or(VmError::LocalOutOfRange)?;
                    match c {
                        Constant::Number(n) => self.stack.push(Value::Number(*n)),
                        Constant::String(s) => {
                            let s_ref = self.alloc_string(s.clone());
                            self.stack.push(Value::Object(s_ref));
                        }
                        Constant::BigInt(b) => {
                            let b_ref = self.alloc_bigint(b.clone());
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
                    let updated = self.update_numeric(top, 1);
                    self.stack.push(updated);
                }
                Op::Dec => {
                    let top = self.pop()?;
                    let updated = self.update_numeric(top, -1);
                    self.stack.push(updated);
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
                    // ISA 契约：STORE_LOCAL 净栈效果 -1（弹出栈顶写入槽位）
                    let val = self.pop()?;
                    if slot >= self.locals.len() {
                        return Err(VmError::LocalOutOfRange);
                    }
                    self.locals[slot] = val;
                    // 快路径：无打开上值时跳过哈希查找（绝大多数帧）
                    if !self.open_upvalues.is_empty()
                        && let Some(uv) = self.open_upvalues.get(&slot)
                    {
                        *uv.0.borrow_mut() = val;
                    }
                }
                Op::LoadGlobal => {
                    // 操作数是常量池索引，解引用出全局对象名（对齐 Go 版 OpLoadGlobal）
                    let name = self.get_const_string(instr.operand as usize)?;
                    let val = self.resolve_global(&name);
                    self.stack.push(val);
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
                            .map(|v| self.format_console_value(*v))
                            .collect::<Vec<_>>()
                            .join(" ");
                        self.stdout_records.push(line);
                        self.stack.push(Value::Undefined);
                    } else if method_name == "sqrt"
                        && self
                            .math_object
                            .is_some_and(|m| receiver == Value::Object(m))
                    {
                        // Math.sqrt(x)：原生方法（receiver 是 Math 单例）
                        let n = to_number(args.first().copied().unwrap_or(Value::Undefined));
                        self.stack.push(Value::Number(n.sqrt()));
                    } else if matches!(method_name.as_str(), "exec" | "test")
                        && self.is_regexp_obj(receiver)
                    {
                        // RegExp 原型方法：exec 返回结果数组或 null，test 返回布尔
                        let subject = args
                            .first()
                            .map(|v| self.format_value(*v))
                            .unwrap_or_default();
                        let result = self.regexp_exec(receiver, &subject)?;
                        if method_name == "test" {
                            self.stack.push(Value::Boolean(result.is_some()));
                        } else {
                            match result {
                                Some(m) => {
                                    let arr = self.alloc_array(m);
                                    self.stack.push(Value::Object(arr));
                                }
                                None => self.stack.push(Value::Null),
                            }
                        }
                    } else if method_name == "next" && self.is_generator_obj(receiver) {
                        // 生成器迭代协议：gen.next(v) 驱动到下一个 YIELD/结束
                        let injected = args.first().copied().unwrap_or(Value::Undefined);
                        let gen_ref = match receiver {
                            Value::Object(r) => r,
                            _ => unreachable!("is_generator_obj 已确认 receiver 是对象"),
                        };
                        let result = self.drive_generator(gen_ref, Some(injected))?;
                        self.stack.push(result);
                    } else if matches!(
                        method_name.as_str(),
                        "readFileSync" | "writeFileSync" | "existsSync"
                    ) && self.fs_object.is_some_and(|f| receiver == Value::Object(f))
                    {
                        // fs 最小内置（M1）：同步读写文本文件
                        let path = args
                            .first()
                            .map(|v| self.format_value(*v))
                            .unwrap_or_default();
                        match method_name.as_str() {
                            "existsSync" => {
                                self.stack
                                    .push(Value::Boolean(std::path::Path::new(&path).exists()));
                            }
                            "readFileSync" => match std::fs::read_to_string(&path) {
                                Ok(content) => {
                                    let s = self.alloc_string(content);
                                    self.stack.push(Value::Object(s));
                                }
                                Err(e) => {
                                    let msg = self.alloc_string(format!("fs.readFileSync: {e}"));
                                    return Err(VmError::Thrown(Value::Object(msg)));
                                }
                            },
                            _ => {
                                let data = args
                                    .get(1)
                                    .map(|v| self.format_value(*v))
                                    .unwrap_or_default();
                                match std::fs::write(&path, data) {
                                    Ok(()) => self.stack.push(Value::Undefined),
                                    Err(e) => {
                                        let msg =
                                            self.alloc_string(format!("fs.writeFileSync: {e}"));
                                        return Err(VmError::Thrown(Value::Object(msg)));
                                    }
                                }
                            }
                        }
                    } else if method_name == "nextTick" && {
                        let c1 = self
                            .process_object
                            .is_some_and(|p| receiver == Value::Object(p));
                        let c2 = matches!(
                            receiver,
                            Value::Object(rr)
                                if matches!(
                                    self.heap.get(rr.0 as usize),
                                    Some(HeapObject::NativeFn { name })
                                        if name == "nextTick"
                                )
                        );
                        let _ = (c1, c2);
                        c1 || c2
                    } {
                        // process.nextTick(cb)：nextTick 优先微任务队列
                        let cb = args.first().copied().unwrap_or(Value::Undefined);
                        self.nexttick_queue.push_back(cb);
                        self.stack.push(Value::Undefined);
                    } else if matches!(method_name.as_str(), "then" | "catch")
                        && matches!(
                            receiver,
                            Value::Object(rr)
                                if matches!(
                                    self.heap.get(rr.0 as usize),
                                    Some(HeapObject::Promise { .. })
                                )
                        )
                    {
                        // promise.then(onF)：登记处理器，返回自身；已完成时立即调度。
                        // promise.catch(onR)：pending 时登记（reject 简化同 fulfill——
                        // 本引擎无 reject 语义，fulfilled 完成不触发 catch）
                        if let Value::Object(rr) = receiver {
                            let cb = args.first().copied().unwrap_or(Value::Undefined);
                            let is_pending = matches!(
                                self.heap.get(rr.0 as usize),
                                Some(HeapObject::Promise { pending: true, .. })
                            );
                            if is_pending {
                                if method_name == "then" {
                                    if let Some(HeapObject::Promise { handlers, .. }) =
                                        self.heap.get_mut(rr.0 as usize)
                                    {
                                        handlers.push(cb);
                                    }
                                } else if let Some(HeapObject::Promise { rejected, .. }) =
                                    self.heap.get_mut(rr.0 as usize)
                                {
                                    // catch：只在 reject 时调度（fulfill 不触发）
                                    rejected.push(cb);
                                }
                            } else if method_name == "then" {
                                let value = match self.heap.get(rr.0 as usize) {
                                    Some(HeapObject::Promise { value, .. }) => *value,
                                    _ => Value::Undefined,
                                };
                                self.microtask_queue
                                    .push_back(crate::builtins::Job::Call(cb, value));
                            }
                        }
                        self.stack.push(receiver);
                    } else if method_name == "resolve"
                        && self
                            .promise_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Promise.resolve(v)：直接完成
                        let value = args.first().copied().unwrap_or(Value::Undefined);
                        let p = self.alloc_fulfilled_promise(value);
                        self.stack.push(Value::Object(p));
                    } else if method_name == "withResolvers"
                        && self
                            .promise_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Promise.withResolvers()：{ promise, resolve, reject }
                        let promise = self.alloc_pending_promise();
                        let resolve = self.alloc_promise_resolver(promise, true);
                        let reject = self.alloc_promise_resolver(promise, false);
                        let result = self.alloc_ordinary();
                        let _ = self.set_property(
                            Value::Object(result),
                            "promise",
                            Value::Object(promise),
                        );
                        let _ = self.set_property(
                            Value::Object(result),
                            "resolve",
                            Value::Object(resolve),
                        );
                        let _ = self.set_property(
                            Value::Object(result),
                            "reject",
                            Value::Object(reject),
                        );
                        self.stack.push(Value::Object(result));
                    } else if method_name == "fromAsync"
                        && self
                            .array_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Array.fromAsync(iterable)：同步数组直接收集；
                        // 生成器按 next() 同步驱动（async 生成器在语料中同步产值）
                        let iterable = args.first().copied().unwrap_or(Value::Undefined);
                        let mut elems: Vec<Value> = Vec::new();
                        if let Value::Object(it) = iterable {
                            match self.heap.get(it.0 as usize) {
                                Some(HeapObject::Array { elements, .. }) => {
                                    elems.extend(elements.iter().copied());
                                }
                                Some(HeapObject::Generator) => {
                                    let mut done = false;
                                    let re = it;
                                    while !done {
                                        let result = self.drive_generator(re, None)?;
                                        let (val, is_done) = match result {
                                            Value::Object(res) => {
                                                let v =
                                                    self.get_property(Value::Object(res), "value")?;
                                                let d =
                                                    self.get_property(Value::Object(res), "done")?;
                                                (v, matches!(d, Value::Boolean(true)))
                                            }
                                            _ => (Value::Undefined, true),
                                        };
                                        if is_done {
                                            done = true;
                                        } else {
                                            elems.push(val);
                                        }
                                    }
                                }
                                _ => {}
                            }
                        }
                        let arr = self.alloc_array(elems);
                        let p = self.alloc_fulfilled_promise(Value::Object(arr));
                        self.stack.push(Value::Object(p));
                    } else if method_name == "groupBy"
                        && self
                            .object_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Object.groupBy(arr, cb)：分组到普通对象
                        let cb = args.get(1).copied().unwrap_or(Value::Undefined);
                        let mut groups: std::collections::HashMap<String, Vec<Value>> =
                            std::collections::HashMap::new();
                        let elems: Vec<Value> =
                            match args.first().copied().unwrap_or(Value::Undefined) {
                                Value::Object(rr) => match self.heap.get(rr.0 as usize) {
                                    Some(HeapObject::Array { elements, .. }) => elements.clone(),
                                    _ => Vec::new(),
                                },
                                _ => Vec::new(),
                            };
                        for (i, elem) in elems.iter().enumerate() {
                            let key_val = self.invoke_array_cb(
                                cb,
                                Value::Undefined,
                                &[*elem, Value::Number(i as f64), Value::Undefined],
                            )?;
                            let key = self.to_property_key(key_val);
                            groups.entry(key).or_default().push(*elem);
                        }
                        let result = self.alloc_ordinary();
                        for (key, items) in groups {
                            let arr = self.alloc_array(items);
                            let _ =
                                self.set_property(Value::Object(result), &key, Value::Object(arr));
                        }
                        self.stack.push(Value::Object(result));
                    } else if method_name == "groupBy"
                        && self.map_ctor.is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Map.groupBy(arr, cb)：分组到 Map（键字符串化）
                        let cb = args.get(1).copied().unwrap_or(Value::Undefined);
                        let mut groups: std::collections::HashMap<String, Vec<Value>> =
                            std::collections::HashMap::new();
                        let elems: Vec<Value> =
                            match args.first().copied().unwrap_or(Value::Undefined) {
                                Value::Object(rr) => match self.heap.get(rr.0 as usize) {
                                    Some(HeapObject::Array { elements, .. }) => elements.clone(),
                                    _ => Vec::new(),
                                },
                                _ => Vec::new(),
                            };
                        for (i, elem) in elems.iter().enumerate() {
                            let key_val = self.invoke_array_cb(
                                cb,
                                Value::Undefined,
                                &[*elem, Value::Number(i as f64), Value::Undefined],
                            )?;
                            let key = self.to_property_key(key_val);
                            groups.entry(key).or_default().push(*elem);
                        }
                        let mut map_entries: Vec<(String, Value)> = Vec::new();
                        for (k, v) in groups.into_iter() {
                            let arr = self.alloc_array(v);
                            map_entries.push((k, Value::Object(arr)));
                        }
                        let map = self.alloc_map(map_entries);
                        self.stack.push(Value::Object(map));
                    } else if method_name == "get"
                        && matches!(
                            receiver,
                            Value::Object(rr)
                                if matches!(
                                    self.heap.get(rr.0 as usize),
                                    Some(HeapObject::Map { .. })
                                )
                        )
                    {
                        // map.get(key)
                        let key = args
                            .first()
                            .map(|v| self.to_property_key(*v))
                            .unwrap_or_default();
                        let val = if let Value::Object(rr) = receiver {
                            if let Some(HeapObject::Map { entries }) = self.heap.get(rr.0 as usize)
                            {
                                entries.get(&key).copied()
                            } else {
                                None
                            }
                        } else {
                            None
                        };
                        self.stack.push(val.unwrap_or(Value::Undefined));
                    } else if matches!(
                        method_name.as_str(),
                        "on" | "once" | "off" | "removeListener" | "emit"
                    ) && matches!(
                        receiver,
                        Value::Object(rr)
                            if matches!(
                                self.heap.get(rr.0 as usize),
                                Some(HeapObject::EventEmitter { .. })
                            )
                    ) {
                        // EventEmitter：on/once 注册监听器，emit 触发，off/removeListener 移除
                        if let Value::Object(rr) = receiver {
                            match method_name.as_str() {
                                "on" | "once" => {
                                    let name = args
                                        .first()
                                        .map(|v| self.to_property_key(*v))
                                        .unwrap_or_default();
                                    let cb = args.get(1).copied().unwrap_or(Value::Undefined);
                                    let once = method_name == "once";
                                    if let Some(HeapObject::EventEmitter { listeners }) =
                                        self.heap.get_mut(rr.0 as usize)
                                    {
                                        listeners.entry(name).or_default().push((cb, once));
                                    }
                                    self.stack.push(receiver);
                                }
                                "emit" => {
                                    let name = args
                                        .first()
                                        .map(|v| self.to_property_key(*v))
                                        .unwrap_or_default();
                                    // 触发瞬间收集监听器：普通监听器保持并触发，
                                    // once 的触发前移除（只触发一次）
                                    let mut all: Vec<Value> = Vec::new();
                                    if let Some(HeapObject::EventEmitter { listeners }) =
                                        self.heap.get_mut(rr.0 as usize)
                                    {
                                        if let Some(list) = listeners.get_mut(&name) {
                                            let mut fired = Vec::new();
                                            let mut keep = Vec::with_capacity(list.len());
                                            for (cb, once) in std::mem::take(list) {
                                                if once {
                                                    fired.push(cb);
                                                } else {
                                                    keep.push((cb, once));
                                                    all.push(cb);
                                                }
                                            }
                                            *list = keep;
                                            all.extend(fired);
                                        }
                                    }
                                    let emit_args: Vec<Value> =
                                        args.iter().skip(1).copied().collect();
                                    for cb in all {
                                        self.invoke_callable(cb, receiver, &emit_args)?;
                                    }
                                    self.stack.push(Value::Boolean(!emit_args.is_empty()));
                                }
                                _ => {
                                    // off / removeListener：移除匹配的监听器
                                    let name = args
                                        .first()
                                        .map(|v| self.to_property_key(*v))
                                        .unwrap_or_default();
                                    let cb = args.get(1).copied().unwrap_or(Value::Undefined);
                                    if let Some(HeapObject::EventEmitter { listeners }) =
                                        self.heap.get_mut(rr.0 as usize)
                                    {
                                        if let Some(list) = listeners.get_mut(&name) {
                                            list.retain(|(c, _)| *c != cb);
                                        }
                                    }
                                    self.stack.push(receiver);
                                }
                            }
                        }
                    } else if matches!(method_name.as_str(), "push" | "next")
                        && self.is_readable_obj(receiver)
                    {
                        // 可读流：push 追加数据（null=结束）；next 消费（空读挂起等待）
                        match method_name.as_str() {
                            "push" => {
                                let v = args.first().copied().unwrap_or(Value::Undefined);
                                let is_end = matches!(v, Value::Null);
                                let waiting = if let Value::Object(rr) = receiver {
                                    if let Some(HeapObject::Readable {
                                        buffer,
                                        ended,
                                        waiting,
                                    }) = self.heap.get_mut(rr.0 as usize)
                                    {
                                        if is_end {
                                            *ended = true;
                                        } else if waiting.is_none() {
                                            // 无等待读取者：数据入缓冲；有等待者时
                                            // 数据直接交给等待的 next（避免双读）
                                            buffer.push_back(v);
                                        }
                                        waiting.take()
                                    } else {
                                        None
                                    }
                                } else {
                                    None
                                };
                                // 有等待中的 promise：兑现为 {value, done} 结果对象
                                if let Some(wp) = waiting {
                                    let res_obj = self.alloc_ordinary();
                                    let done = is_end;
                                    let val = if done { Value::Undefined } else { v };
                                    let _ = self.set_property(Value::Object(res_obj), "value", val);
                                    let _ = self.set_property(
                                        Value::Object(res_obj),
                                        "done",
                                        Value::Boolean(done),
                                    );
                                    self.fulfill_promise(wp, Value::Object(res_obj))?;
                                }
                                self.stack.push(Value::Boolean(true));
                            }
                            "next" => {
                                // 先取动作：Some(值) / Done / NeedWait(等待 promise)
                                enum NextAction {
                                    Data(Value),
                                    Done,
                                    NeedWait,
                                }
                                // 无条件先建 pending promise（NeedWait 时登记等待；
                                // Data/Done 时弃用——堆对象无副作用）
                                let pending_promise = self.alloc_pending_promise();
                                let action = if let Value::Object(rr) = receiver {
                                    match self.heap.get_mut(rr.0 as usize) {
                                        Some(HeapObject::Readable {
                                            buffer,
                                            ended,
                                            waiting,
                                        }) => {
                                            if let Some(v) = buffer.pop_front() {
                                                NextAction::Data(v)
                                            } else if *ended {
                                                NextAction::Done
                                            } else {
                                                // 空读未结束：登记等待 promise（挂起等待 push）
                                                *waiting = Some(pending_promise);
                                                NextAction::NeedWait
                                            }
                                        }
                                        _ => NextAction::Done,
                                    }
                                } else {
                                    NextAction::Done
                                };
                                let result = match action {
                                    NextAction::Data(v) => {
                                        let res_obj = self.alloc_ordinary();
                                        let _ =
                                            self.set_property(Value::Object(res_obj), "value", v);
                                        let _ = self.set_property(
                                            Value::Object(res_obj),
                                            "done",
                                            Value::Boolean(false),
                                        );
                                        Some(res_obj)
                                    }
                                    NextAction::Done => {
                                        let res_obj = self.alloc_ordinary();
                                        let _ = self.set_property(
                                            Value::Object(res_obj),
                                            "value",
                                            Value::Undefined,
                                        );
                                        let _ = self.set_property(
                                            Value::Object(res_obj),
                                            "done",
                                            Value::Boolean(true),
                                        );
                                        Some(res_obj)
                                    }
                                    NextAction::NeedWait => None, // pending：等待 push 兑现
                                };
                                match result {
                                    Some(obj) => self.stack.push(Value::Object(obj)),
                                    None => {
                                        // 空读未结束：next 返回等待 promise 本身
                                        // （与 waiting 登记同一句柄——push 兑现它来
                                        // 恢复 async 帧），AWAIT 挂起等待 push
                                        self.stack.push(Value::Object(pending_promise));
                                    }
                                }
                            }
                            _ => self.stack.push(Value::Undefined),
                        }
                    } else if matches!(method_name.as_str(), "platform" | "homedir" | "tmpdir")
                        && self.os_module.is_some_and(|m| receiver == Value::Object(m))
                    {
                        let result = match method_name.as_str() {
                            "platform" => if cfg!(windows) { "win32" } else { "linux" }.to_owned(),
                            "homedir" => std::env::var("USERPROFILE")
                                .or_else(|_| std::env::var("HOME"))
                                .unwrap_or_default(),
                            _ => std::env::var("TEMP")
                                .or_else(|_| std::env::var("TMPDIR"))
                                .unwrap_or_else(|_| "/tmp".to_owned()),
                        };
                        let r = self.alloc_string(result);
                        self.stack.push(Value::Object(r));
                    } else if matches!(
                        method_name.as_str(),
                        "join" | "basename" | "dirname" | "extname" | "resolve"
                    ) && self
                        .path_module
                        .is_some_and(|m| receiver == Value::Object(m))
                    {
                        // node:path 轻量内置（平台分隔符，对齐 Go `filepath` 语义）
                        let result = self.path_method(method_name.as_str(), &args);
                        let r = self.alloc_string(result);
                        self.stack.push(Value::Object(r));
                    } else if matches!(method_name.as_str(), "isWellFormed" | "toWellFormed")
                        && matches!(
                            receiver,
                            Value::Object(rr)
                                if matches!(
                                    self.heap.get(rr.0 as usize),
                                    Some(HeapObject::String(_))
                                )
                        )
                    {
                        // 字符串完整性（Rust String 恒为合法 UTF-8）
                        if method_name == "isWellFormed" {
                            self.stack.push(Value::Boolean(true));
                        } else {
                            self.stack.push(receiver);
                        }
                    } else if matches!(
                        method_name.as_str(),
                        "toSorted" | "toReversed" | "toSpliced" | "with"
                    ) && matches!(
                        receiver,
                        Value::Object(rr)
                            if matches!(self.heap.get(rr.0 as usize), Some(HeapObject::Array { .. }))
                    ) {
                        // ES2023 不可变数组方法：返回新数组
                        let mut elems: Vec<Value> = if let Value::Object(rr) = receiver {
                            if let Some(HeapObject::Array { elements, .. }) =
                                self.heap.get(rr.0 as usize)
                            {
                                elements.clone()
                            } else {
                                Vec::new()
                            }
                        } else {
                            Vec::new()
                        };
                        match method_name.as_str() {
                            "toSorted" => {
                                elems.sort_by(|a, b| {
                                    self.format_value(*a).cmp(&self.format_value(*b))
                                });
                            }
                            "toReversed" => elems.reverse(),
                            "toSpliced" => {
                                let start = args
                                    .first()
                                    .and_then(|v| match v {
                                        Value::Number(n) => Some(*n as usize),
                                        _ => None,
                                    })
                                    .unwrap_or(0)
                                    .min(elems.len());
                                let del = args
                                    .get(1)
                                    .and_then(|v| match v {
                                        Value::Number(n) => Some(*n as usize),
                                        _ => None,
                                    })
                                    .unwrap_or(0)
                                    .min(elems.len() - start);
                                elems.splice(start..start + del, args[2..].to_vec());
                            }
                            _ => {
                                // with(idx, val)
                                let idx = args
                                    .first()
                                    .and_then(|v| match v {
                                        Value::Number(n) => Some(*n as usize),
                                        _ => None,
                                    })
                                    .unwrap_or(0);
                                let val = args.get(1).copied().unwrap_or(Value::Undefined);
                                if idx < elems.len() {
                                    elems[idx] = val;
                                }
                            }
                        }
                        let new_arr = self.alloc_array(elems);
                        self.stack.push(Value::Object(new_arr));
                    } else if method_name == "hasOwn"
                        && self
                            .object_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Object.hasOwn(obj, key)：自有属性判定（不沿原型链）
                        let result = match (
                            args.first().copied().unwrap_or(Value::Undefined),
                            args.get(1)
                                .map(|v| self.to_property_key(*v))
                                .unwrap_or_default(),
                        ) {
                            (Value::Object(rr), key) => match self.heap.get(rr.0 as usize) {
                                Some(HeapObject::Ordinary { properties, .. }) => {
                                    properties.contains_key(&key)
                                }
                                Some(HeapObject::Array { properties, .. }) => {
                                    key == "length" || properties.contains_key(&key)
                                }
                                _ => false,
                            },
                            _ => false,
                        };
                        self.stack.push(Value::Boolean(result));
                    } else if let Some(dispatched) =
                        crate::builtins::try_dispatch(self, receiver, &method_name, &args)
                    {
                        // 内置库注册表模块方法（querystring 等并行开发模块）
                        match dispatched {
                            Ok(v) => self.stack.push(v),
                            Err(e) => return Err(e),
                        }
                    } else if method_name == "create"
                        && self
                            .object_ctor
                            .is_some_and(|c| receiver == Value::Object(c))
                    {
                        // Object.create(proto)：以精确原型分配新对象（null → 无原型）
                        let proto_val = args.first().copied().unwrap_or(Value::Undefined);
                        let proto = match proto_val {
                            Value::Object(p) => Some(p),
                            _ => None,
                        };
                        let obj = self.alloc_ordinary_with_exact_proto(proto);
                        self.stack.push(Value::Object(obj));
                    } else if let Value::Object(r) = receiver {
                        let idx = r.0 as usize;
                        if idx < self.heap.len()
                            && matches!(self.heap[idx], HeapObject::Array { .. })
                        {
                            match method_name.as_str() {
                                "push" => {
                                    if let Some(HeapObject::Array { elements, .. }) =
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
                                    let (cb, this_arg) = self.array_cb_ctx(&args);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let mut new_elems = Vec::with_capacity(elems.len());
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        let item_res = self.invoke_array_cb(
                                            cb,
                                            this_arg,
                                            &[*elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                        new_elems.push(item_res);
                                    }
                                    let new_arr = self.alloc_array(new_elems);
                                    self.stack.push(Value::Object(new_arr));
                                }
                                "filter" => {
                                    let (cb, this_arg) = self.array_cb_ctx(&args);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    let mut kept = Vec::new();
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        let keep = self.invoke_array_cb(
                                            cb,
                                            this_arg,
                                            &[*elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                        if to_boolean(keep) {
                                            kept.push(*elem);
                                        }
                                    }
                                    let new_arr = self.alloc_array(kept);
                                    self.stack.push(Value::Object(new_arr));
                                }
                                "find" => {
                                    let (cb, this_arg) = self.array_cb_ctx(&args);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    let mut found = Value::Undefined;
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        let hit = self.invoke_array_cb(
                                            cb,
                                            this_arg,
                                            &[*elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                        if to_boolean(hit) {
                                            found = *elem;
                                            break;
                                        }
                                    }
                                    self.stack.push(found);
                                }
                                "some" => {
                                    let (cb, this_arg) = self.array_cb_ctx(&args);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    let mut any = false;
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        let hit = self.invoke_array_cb(
                                            cb,
                                            this_arg,
                                            &[*elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                        if to_boolean(hit) {
                                            any = true;
                                            break;
                                        }
                                    }
                                    self.stack.push(Value::Boolean(any));
                                }
                                "forEach" => {
                                    let (cb, this_arg) = self.array_cb_ctx(&args);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        self.invoke_array_cb(
                                            cb,
                                            this_arg,
                                            &[*elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                    }
                                    self.stack.push(Value::Undefined);
                                }
                                "reduce" => {
                                    let cb = args.first().copied().unwrap_or(Value::Undefined);
                                    let mut acc = args.get(1).copied().unwrap_or(Value::Undefined);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    for (elem_idx, elem) in elems.iter().enumerate() {
                                        acc = self.invoke_array_cb(
                                            cb,
                                            Value::Undefined,
                                            &[acc, *elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                    }
                                    self.stack.push(acc);
                                }
                                "reduceRight" => {
                                    let cb = args.first().copied().unwrap_or(Value::Undefined);
                                    let mut acc = args.get(1).copied().unwrap_or(Value::Undefined);
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    let arr_obj = Value::Object(ObjectRef(idx as u32));
                                    for (elem_idx, elem) in elems.iter().enumerate().rev() {
                                        acc = self.invoke_array_cb(
                                            cb,
                                            Value::Undefined,
                                            &[acc, *elem, Value::Number(elem_idx as f64), arr_obj],
                                        )?;
                                    }
                                    self.stack.push(acc);
                                }
                                "join" => {
                                    let sep = if let Some(sep_val) = args.first() {
                                        self.to_property_key(*sep_val)
                                    } else {
                                        ",".to_owned()
                                    };
                                    let parts: Vec<String> =
                                        if let Some(HeapObject::Array { elements, .. }) =
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
                                    let elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
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
                                "sort" => {
                                    // 无比较器排序：元素字符串化后按字典序原地排序（JS 默认语义）
                                    let mut elems =
                                        if let Some(HeapObject::Array { elements, .. }) =
                                            self.heap.get(idx)
                                        {
                                            elements.clone()
                                        } else {
                                            Vec::new()
                                        };
                                    elems.sort_by(|a, b| {
                                        self.format_value(*a).cmp(&self.format_value(*b))
                                    });
                                    if let Some(HeapObject::Array { elements, .. }) =
                                        self.heap.get_mut(idx)
                                    {
                                        *elements = elems;
                                    }
                                    self.stack.push(receiver);
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
                    if self.is_native_fn(callee, "require") {
                        // require(spec)：CJS 模块加载（缓存 + 循环依赖占位）
                        let spec = args.first().copied().unwrap_or(Value::Undefined);
                        let exports = self.call_require(spec)?;
                        self.stack.push(exports);
                    } else if let Value::Object(r) = callee {
                        let callee_ref = r.0 as usize;
                        if let Some(HeapObject::PromiseResolver { promise, .. }) =
                            self.heap.get(callee_ref)
                        {
                            // resolve(value)/reject：fulfill 目标 promise 并调度处理器
                            let value = args.first().copied().unwrap_or(Value::Undefined);
                            self.fulfill_promise(*promise, value)?;
                            self.stack.push(Value::Undefined);
                        } else if self.is_native_fn(Value::Object(r), "setTimeout")
                            || self.is_native_fn(Value::Object(r), "setInterval")
                        {
                            let delay = args
                                .get(1)
                                .and_then(|v| match v {
                                    Value::Number(n) => Some(*n as u64),
                                    _ => None,
                                })
                                .unwrap_or(0);
                            let cb = args.first().copied().unwrap_or(Value::Undefined);
                            let repeating = self.is_native_fn(Value::Object(r), "setInterval");
                            self.timer_counter += 1;
                            let id = self.timer_counter;
                            // 到期时间 = 队尾累计到期 + 延迟（同批注册按时间序）
                            let last_due = self
                                .macro_tasks
                                .back()
                                .map(|(_, d, _, _, _)| *d)
                                .unwrap_or(0);
                            let due = last_due + delay;
                            self.macro_tasks.push_back((id, due, delay, cb, repeating));
                            // Node 返回 Timeout/Interval 句柄；简化返回数字 id
                            // （clear* 接受数字或对象，数字自洽）
                            self.stack.push(Value::Number(id as f64));
                        } else if self.is_native_fn(Value::Object(r), "clearTimeout")
                            || self.is_native_fn(Value::Object(r), "clearInterval")
                        {
                            let id = args
                                .first()
                                .and_then(|v| match v {
                                    Value::Number(n) => Some(*n as u64),
                                    _ => None,
                                })
                                .unwrap_or(0);
                            self.active_timers.insert(id);
                            self.stack.push(Value::Undefined);
                        } else if self.is_native_fn(Value::Object(r), "queueMicrotask") {
                            let cb = args.first().copied().unwrap_or(Value::Undefined);
                            self.microtask_queue
                                .push_back(crate::builtins::Job::Call(cb, Value::Undefined));
                            self.stack.push(Value::Undefined);
                        } else {
                            let (f_idx, uvs) =
                                if let Some(HeapObject::Closure {
                                    func_idx, upvalues, ..
                                }) = self.heap.get(callee_ref)
                                {
                                    (Some(*func_idx), upvalues.clone())
                                } else if callee_ref < self.module_functions.len() {
                                    (Some(callee_ref), Vec::new())
                                } else {
                                    (None, Vec::new())
                                };

                            if let Some(fi) = f_idx {
                                let ret = self.invoke_function(fi, Value::Undefined, &args, uvs)?;
                                self.stack.push(ret);
                            } else {
                                self.stack.push(Value::Undefined);
                            }
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
                    // ISA 契约：STORE_UPVALUE 净栈效果 -1（弹出栈顶写入上值）
                    let val = self.pop()?;
                    let uv_idx = instr.operand as usize;
                    if let Some(uv) = self.current_upvalues.get(uv_idx) {
                        *uv.0.borrow_mut() = val;
                    }
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
                        if let Some(HeapObject::Array { elements, .. }) =
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
                        if let Some(HeapObject::Array { elements, .. }) =
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
                        if let Some(HeapObject::Array { elements, .. }) =
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

                // 12. 返回指令（return 穿越带 finally 的区域时先挂起、跑完 finally 再返回）
                Op::Return => {
                    let val = self.pop()?;
                    match self.exit_try(Completion::Return(val)) {
                        TryExitOutcome::Continue(next_pc) => {
                            pc = next_pc;
                            continue;
                        }
                        TryExitOutcome::Return(v) => return Ok(v),
                    }
                }
                Op::ReturnUndef => match self.exit_try(Completion::Return(Value::Undefined)) {
                    TryExitOutcome::Continue(next_pc) => {
                        pc = next_pc;
                        continue;
                    }
                    TryExitOutcome::Return(v) => return Ok(v),
                },

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
                    let res = self.do_construct(callee, &args)?;
                    self.stack.push(res);
                }
                Op::ConstructThis => {
                    let num_args = instr.operand as usize;
                    let mut args = Vec::with_capacity(num_args);
                    for _ in 0..num_args {
                        args.push(self.pop()?);
                    }
                    args.reverse();
                    let callee = self.pop()?;
                    let res = self.do_construct_this(callee, &args)?;
                    self.stack.push(res);
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

                // 14. 异常与 try 语义（状态机移植自 Go 版 vm_exception.go）
                Op::TryEnter => {
                    let try_idx = instr.operand as usize;
                    let entry = self
                        .current_try_table
                        .get(try_idx)
                        .copied()
                        .ok_or(VmError::LocalOutOfRange)?;
                    self.try_stack.push(TryHandler {
                        try_idx,
                        entry,
                        exc: None,
                        phase: PHASE_TRY,
                        completion: None,
                    });
                }
                Op::TryExit => {
                    self.handle_try_exit(instr.operand as usize);
                }
                Op::TryExitFinally => match self.handle_try_exit_finally(instr.operand as usize) {
                    FinallyOutcome::Continue => {}
                    FinallyOutcome::ContinueAt(next_pc) => {
                        pc = next_pc;
                        continue;
                    }
                    FinallyOutcome::Rethrow(exc) => return Err(VmError::Thrown(exc)),
                    FinallyOutcome::Return(val) => return Ok(val),
                },
                Op::TryExitJmp => {
                    // break/continue 位于 try 区域内：跳转穿出区域前先运行 finally
                    let target = compute_jump_target(pc, instr.operand);
                    match self.exit_try(Completion::Jump(target)) {
                        TryExitOutcome::Continue(next_pc) => {
                            pc = next_pc;
                            continue;
                        }
                        // 不可达：Jump 完成动作永远解析为跳转而非 return
                        TryExitOutcome::Return(_) => return Ok(Value::Undefined),
                    }
                }
                Op::Throw => {
                    let exc = self.pop()?;
                    return Err(VmError::Thrown(exc));
                }

                // 15. 全局赋值与一元运算符
                Op::StoreGlobal => {
                    // 不带声明符的全局赋值写入全局变量表（对齐 Go 版 globalObj.Set）
                    let name = self.get_const_string(instr.operand as usize)?;
                    let val = self.pop()?;
                    self.globals.insert(name, val);
                }
                Op::In => {
                    let r = self.pop()?;
                    let l = self.pop()?;
                    let key = self.to_property_key(l);
                    let res = self.has_property(r, &key);
                    self.stack.push(Value::Boolean(res));
                }
                Op::Typeof => {
                    let v = self.pop()?;
                    let s = self.typeof_value(v);
                    let r = self.alloc_string(s);
                    self.stack.push(Value::Object(r));
                }
                Op::TypeofGlobal => {
                    let name = self.get_const_string(instr.operand as usize)?;
                    let v = self.resolve_global(&name);
                    let s = self.typeof_value(v);
                    let r = self.alloc_string(s);
                    self.stack.push(Value::Object(r));
                }

                // 16. 展开调用家族（f(...args) / obj.m(...args) / new X(...args) / super(...args)）
                Op::CallArgs => {
                    // 栈序 ... callee argsArray
                    let args_arr = self.pop()?;
                    let callee = self.pop()?;
                    let args = self.to_array_values(args_arr);
                    let ret = self.invoke_callable(callee, Value::Undefined, &args)?;
                    self.stack.push(ret);
                }
                Op::CallWithThisArgs => {
                    // 栈序 ... callee this argsArray
                    let args_arr = self.pop()?;
                    let this_val = self.pop()?;
                    let callee = self.pop()?;
                    let args = self.to_array_values(args_arr);
                    let ret = self.invoke_callable(callee, this_val, &args)?;
                    self.stack.push(ret);
                }
                Op::CallMethodArgs => {
                    // 栈序 ... receiver argsArray；操作数 = 方法名常量索引
                    let name = self.get_const_string(instr.operand as usize)?;
                    let args_arr = self.pop()?;
                    let receiver = self.pop()?;
                    let args = self.to_array_values(args_arr);
                    let method = self.get_property(receiver, &name)?;
                    let ret = self.invoke_callable(method, receiver, &args)?;
                    self.stack.push(ret);
                }
                Op::NewArgs => {
                    // 栈序 ... callee argsArray
                    let args_arr = self.pop()?;
                    let callee = self.pop()?;
                    let args = self.to_array_values(args_arr);
                    let res = self.do_construct(callee, &args)?;
                    self.stack.push(res);
                }
                Op::ConstructThisArgs => {
                    // super(...args)：参数表在栈顶，this 取当前帧 locals[0]
                    let args_arr = self.pop()?;
                    let callee = self.pop()?;
                    let args = self.to_array_values(args_arr);
                    let res = self.do_construct_this(callee, &args)?;
                    self.stack.push(res);
                }
                Op::SpreadObject => {
                    // { ...src }：把 src 自有属性逐个写入栈顶 dst（dst 不弹出）
                    let src = self.pop()?;
                    let dst = self.peek()?;
                    for (k, v) in self.own_properties(src) {
                        self.set_property(dst, &k, v)?;
                    }
                }
                Op::EnumKeys => {
                    // for-in 头部：快照原型链可枚举键为字符串数组（对齐 Go OpEnumKeys）
                    let src = self.pop()?;
                    let keys = self.enumerate_for_in_keys(src);
                    let key_refs: Vec<Value> = keys
                        .into_iter()
                        .map(|k| Value::Object(self.alloc_string(k)))
                        .collect();
                    let arr = self.alloc_array(key_refs);
                    self.stack.push(Value::Object(arr));
                }

                // 17. 生成器 / async 协程
                Op::Yield => {
                    // 挂起生成器帧：记录恢复点并以 Yielded 信号上抛（携带产出值）；
                    // 恢复时注入值压栈，成为 yield 表达式的求值结果
                    let produced = self.pop()?;
                    self.yield_pc = pc + 1;
                    return Err(VmError::Yielded(produced));
                }
                Op::Await => {
                    // await 是让出点：Node 语义下先把已排队的微任务跑完
                    self.drain_microtasks()?;
                    let awaited = self.pop()?;
                    let resolved = match awaited {
                        Value::Object(r) => match self.heap.get(r.0 as usize) {
                            Some(HeapObject::Promise {
                                pending: false,
                                value,
                                ..
                            }) => Some(*value),
                            Some(HeapObject::Promise { pending: true, .. }) => {
                                // 真异步挂起：记录恢复点并以 Awaited 信号上抛，
                                // 由 async 驱动层捕获后挂起整帧（M2 事件循环模型）
                                self.yield_pc = pc + 1;
                                return Err(VmError::Awaited(r));
                            }
                            _ => Some(awaited),
                        },
                        _ => Some(awaited),
                    };
                    match resolved {
                        Some(v) => self.stack.push(v),
                        None => return Err(VmError::UnimplementedOpcode(instr.op)),
                    }
                }
                Op::GetIterator | Op::GetAsyncIterator => {
                    let val = self.pop()?;
                    if self.is_generator_obj(val) || self.is_readable_obj(val) {
                        // 生成器对象自身即（async）迭代器
                        self.stack.push(val);
                    } else {
                        let msg = self.alloc_string("TypeError: value is not iterable".to_owned());
                        return Err(VmError::Thrown(Value::Object(msg)));
                    }
                }
                Op::MakeRegexp => {
                    // 正则字面量：弹 flags + pattern，构造 RegExp 对象（对齐 Go OpMakeRegexp）
                    let flags_val = self.pop()?;
                    let pattern_val = self.pop()?;
                    let regexp = HeapObject::RegExp {
                        pattern: self.format_value(pattern_val),
                        flags: self.to_property_key(flags_val),
                    };
                    let idx = self.heap.len() as u32;
                    self.heap.push(regexp);
                    self.stack.push(Value::Object(ObjectRef(idx)));
                }

                // 其它高级对象与协程操作码（后续阶段扩展）
                Op::ForInNext | Op::CallThisArgs | Op::End => {
                    return Err(VmError::UnimplementedOpcode(instr.op));
                }
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
