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
    /// 数组对象：线性元素列表
    Array {
        /// 数组内存储的值列表
        elements: Vec<Value>,
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
}

impl Vm {
    /// 在堆上分配字符串对象，返回句柄。
    pub fn alloc_string(&mut self, s: String) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::String(s));
        ObjectRef(idx)
    }

    /// 在堆上分配普通对象（带可选隐式原型），返回句柄。
    pub fn alloc_ordinary_with_proto(&mut self, proto: Option<ObjectRef>) -> ObjectRef {
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
    pub fn alloc_array(&mut self, elements: Vec<Value>) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Array { elements });
        ObjectRef(idx)
    }

    /// 在堆上分配闭包对象，返回句柄。
    pub fn alloc_closure_with_upvalues(
        &mut self,
        func_idx: usize,
        upvalues: Vec<Upvalue>,
    ) -> ObjectRef {
        let idx = self.heap.len() as u32;
        self.heap.push(HeapObject::Closure {
            func_idx,
            upvalues,
            properties: HashMap::new(),
            proto: None,
        });
        ObjectRef(idx)
    }

    /// 在堆上分配无上值的闭包对象，返回句柄。
    pub fn alloc_closure(&mut self, func_idx: usize) -> ObjectRef {
        self.alloc_closure_with_upvalues(func_idx, Vec::new())
    }
}
