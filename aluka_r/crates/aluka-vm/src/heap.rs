//! 虚拟机堆内托管对象与分配器实现。

use crate::interpreter::Vm;
use crate::value::{Upvalue, Value};
use aluka_core::ObjectRef;
use std::collections::HashMap;

/// 虚拟机堆内托管对象。
#[derive(Debug, Clone)]
pub enum HeapObject {
    /// 普通对象：属性哈希表 + Getter/Setter 映射 + 隐式原型链
    Ordinary {
        /// 对象自有属性字典
        properties: HashMap<String, Value>,
        /// 访问器 Getter 映射表（属性名 -> 函数模板索引）
        getters: HashMap<String, usize>,
        /// 访问器 Setter 映射表（属性名 -> 函数模板索引）
        setters: HashMap<String, usize>,
        /// 隐式原型 [[Prototype]]
        proto: Option<ObjectRef>,
    },
    /// 数组对象：线性元素列表 + 隐式原型（`Array.prototype` 单例）
    Array {
        /// 数组内存储的值列表
        elements: Vec<Value>,
        /// 隐式原型 [[Prototype]]
        proto: Option<ObjectRef>,
    },
    /// 闭包函数对象：指向所属函数模板的索引与捕获的上值
    Closure {
        /// 目标函数模板索引
        func_idx: usize,
        /// 捕获的上值列表
        upvalues: Vec<Upvalue>,
        /// 闭包自有属性（例如 prototype、静态字段等）
        properties: HashMap<String, Value>,
        /// 闭包原型 [[Prototype]]（用于静态继承 superClass）
        proto: Option<ObjectRef>,
    },
    /// 堆内字符串对象（全局唯一跨函数句柄）
    String(String),
    /// 堆内 BigInt 对象（十进制字符串表示，对齐 Go 版常量池语义）
    BigInt(String),
    /// 原生构造器（Error / Array / Object 等内置构造函数，`new` 由解释器拦截求值）
    NativeCtor {
        /// 构造器名（亦为产出的错误实例 `name`）
        name: String,
        /// 构造器自有属性（如 `prototype`）
        properties: HashMap<String, Value>,
    },
    /// 生成器对象（执行状态存于 `Vm.generators` 注册表，此变体仅作身份标记）
    Generator,
    /// Promise 对象（当前仅同步完成语义：async 函数体同步执行后包装结果）
    Promise {
        /// 是否已完成（fulfilled）
        fulfilled: bool,
        /// 完成值
        value: Value,
    },
    /// 正则表达式对象（模式与标志原文；匹配经 `aluka-regex` 引擎求值）
    RegExp {
        /// 模式原文（不含定界符 `/`）
        pattern: String,
        /// 标志字符串（如 `i`、`g`）
        flags: String,
    },
}

impl Vm {
    /// 在堆上分配字符串对象，返回句柄。
    pub fn alloc_string(&mut self, s: String) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::String(s));
        ObjectRef(idx)
    }

    /// 在堆上分配 BigInt 对象（十进制字符串表示），返回句柄。
    pub fn alloc_bigint(&mut self, s: String) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::BigInt(s));
        ObjectRef(idx)
    }

    /// 在堆上分配普通对象（带可选隐式原型），返回句柄。
    ///
    /// `proto` 为 `None` 且全局 `Object.prototype` 单例已初始化时自动挂到单例上
    /// （`{} instanceof Object` 语义）。
    pub fn alloc_ordinary_with_proto(&mut self, proto: Option<ObjectRef>) -> ObjectRef {
        self.alloc_ordinary_with_exact_proto(proto.or(self.object_prototype))
    }

    /// 在堆上分配普通对象，隐式原型精确指定（不做单例回退，
    /// 供 `Object.create(null)` 等需要无原型对象的场景）。
    pub fn alloc_ordinary_with_exact_proto(&mut self, proto: Option<ObjectRef>) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Ordinary {
            properties: HashMap::new(),
            getters: HashMap::new(),
            setters: HashMap::new(),
            proto,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配无原型普通对象，返回句柄。
    pub fn alloc_ordinary(&mut self) -> ObjectRef {
        self.alloc_ordinary_with_proto(None)
    }

    /// 在堆上分配数组对象，返回句柄。
    ///
    /// 全局 `Array.prototype` 单例已初始化时自动挂为隐式原型
    /// （`[] instanceof Array` 语义）。
    pub fn alloc_array(&mut self, elements: Vec<Value>) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Array {
            elements,
            proto: self.array_prototype,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配闭包对象，返回句柄。
    ///
    /// JS 函数对象自动携带 `prototype` 属性（类机制在分配后会覆盖为自己的原型）。
    pub fn alloc_closure_with_upvalues(
        &mut self,
        func_idx: usize,
        upvalues: Vec<Upvalue>,
    ) -> ObjectRef {
        let default_proto = self.alloc_ordinary();
        let mut properties = HashMap::new();
        properties.insert("prototype".to_owned(), Value::Object(default_proto));
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Closure {
            func_idx,
            upvalues,
            properties,
            proto: None,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配无上值的闭包对象，返回句柄。
    pub fn alloc_closure(&mut self, func_idx: usize) -> ObjectRef {
        self.alloc_closure_with_upvalues(func_idx, Vec::new())
    }

    /// 在堆上分配原生构造器对象（自动挂 `prototype` 属性），返回句柄。
    pub fn alloc_native_ctor(&mut self, name: &str, prototype: Option<ObjectRef>) -> ObjectRef {
        let mut properties = HashMap::new();
        if let Some(p) = prototype {
            properties.insert("prototype".to_owned(), Value::Object(p));
        }
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::NativeCtor {
            name: name.to_owned(),
            properties,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配已完成（fulfilled）的 Promise 对象，返回句柄。
    pub fn alloc_fulfilled_promise(&mut self, value: Value) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Promise {
            fulfilled: true,
            value,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配 Error 实例（`message` / `name` 为自有属性），返回句柄。
    pub fn alloc_error_instance(&mut self, message: &str) -> ObjectRef {
        let message_ref = self.alloc_string(message.to_owned());
        let name_ref = self.alloc_string("Error".to_owned());
        let mut properties = HashMap::new();
        properties.insert("message".to_owned(), Value::Object(message_ref));
        properties.insert("name".to_owned(), Value::Object(name_ref));
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Ordinary {
            properties,
            getters: HashMap::new(),
            setters: HashMap::new(),
            proto: self.object_prototype,
        });
        ObjectRef(idx)
    }
}
