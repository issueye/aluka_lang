//! 对象属性读写、访问器触发、原型链遍历与 Instanceof 语义。

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::Value;
use aluka_core::ObjectRef;

impl Vm {
    /// 获取常量池字符串。
    pub fn get_const_string(&self, idx: usize) -> Result<String, VmError> {
        match self.current_constants.get(idx) {
            Some(aluka_bytecode::Constant::String(s)) => Ok(s.clone()),
            _ => Ok(format!("{idx}")),
        }
    }

    /// 将任意值转换为属性键。
    pub fn to_property_key(&self, val: Value) -> String {
        match val {
            Value::Number(n) => {
                if n.fract() == 0.0 {
                    format!("{}", n as i64)
                } else {
                    format!("{n}")
                }
            }
            Value::Boolean(b) => format!("{b}"),
            Value::Null => "null".to_owned(),
            Value::Undefined => "undefined".to_owned(),
            Value::Object(r) => {
                let idx = r.0 as usize;
                if idx < self.heap.len() {
                    match &self.heap[idx] {
                        HeapObject::String(s) => return s.clone(),
                        HeapObject::BigInt(s) => return s.clone(),
                        _ => {}
                    }
                }
                if let Some(aluka_bytecode::Constant::String(s)) = self.current_constants.get(idx) {
                    s.clone()
                } else {
                    format!("[Object {:?}]", r)
                }
            }
        }
    }

    /// 读取属性（含原型链查找、getter 触发与数组元素读取）。
    pub fn get_property(&mut self, obj: Value, key: &str) -> Result<Value, VmError> {
        // 内置对象的方法按需物化（process.nextTick 等属性访问先于调用）
        if key == "env" && self.process_object.is_some_and(|p| obj == Value::Object(p)) {
            // process.env：物化为环境变量对象
            let env_obj = self.alloc_ordinary();
            for (k, v) in std::env::vars() {
                let s_ref = self.alloc_string(v);
                let _ = self.set_property(Value::Object(env_obj), &k, Value::Object(s_ref));
            }
            return Ok(Value::Object(env_obj));
        }
        if key == "nextTick" && self.process_object.is_some_and(|p| obj == Value::Object(p)) {
            return Ok(Value::Object(self.alloc_native_fn("nextTick")));
        }
        // 闭包函数：`name` / `length` 读模板元数据（Go 前端编译产物携带函数名）
        if let Value::Object(r) = obj {
            let fn_meta = match self.heap.get(r.0 as usize) {
                Some(HeapObject::Closure { func_idx, .. }) => self
                    .module_functions
                    .get(*func_idx)
                    .map(|t| (t.name.clone(), t.num_params)),
                _ => None,
            };
            if let Some((name, num_params)) = fn_meta {
                if key == "name" {
                    return Ok(Value::Object(self.alloc_string(name)));
                }
                if key == "length" {
                    return Ok(Value::Number(num_params as f64));
                }
            }
        }
        // 字符串接收者：`length` 与数字下标访问（原型方法由 CALL_METHOD 链求值）
        if let Value::Object(r) = obj {
            let str_len = match self.heap.get(r.0 as usize) {
                Some(HeapObject::String(text)) => Some(text.chars().count()),
                _ => None,
            };
            if let Some(len) = str_len {
                if key == "length" {
                    return Ok(Value::Number(len as f64));
                }
                if let Ok(i) = key.parse::<usize>() {
                    if i < len {
                        if let Some(HeapObject::String(text)) = self.heap.get(r.0 as usize) {
                            let ch = text
                                .chars()
                                .nth(i)
                                .map(|c| c.to_string())
                                .unwrap_or_default();
                            return Ok(Value::Object(self.alloc_string(ch)));
                        }
                    }
                    return Ok(Value::Undefined);
                }
            }
        }
        let mut cur = obj;
        let mut depth = 0;
        while let Value::Object(r) = cur {
            if depth > 100 {
                break;
            }
            depth += 1;
            let idx = r.0 as usize;
            if idx >= self.heap.len() {
                break;
            }
            match &self.heap[idx] {
                HeapObject::Ordinary {
                    properties,
                    getters,
                    proto,
                    ..
                } => {
                    if let Some(&g_idx) = getters.get(key) {
                        return self.invoke_function(g_idx, obj, &[], Vec::new());
                    }
                    if let Some(v) = properties.get(key) {
                        return Ok(*v);
                    }
                    if let Some(parent) = *proto {
                        cur = Value::Object(parent);
                    } else {
                        break;
                    }
                }
                HeapObject::Closure {
                    properties, proto, ..
                } => {
                    if let Some(v) = properties.get(key) {
                        return Ok(*v);
                    }
                    if let Some(parent) = *proto {
                        cur = Value::Object(parent);
                    } else {
                        break;
                    }
                }
                HeapObject::NativeCtor { properties, .. } => {
                    if let Some(v) = properties.get(key) {
                        return Ok(*v);
                    }
                    break;
                }
                HeapObject::Array {
                    elements,
                    properties,
                    proto,
                } => {
                    if key == "length" {
                        return Ok(Value::Number(elements.len() as f64));
                    }
                    if let Ok(i) = key.parse::<usize>() {
                        return Ok(elements.get(i).copied().unwrap_or(Value::Undefined));
                    }
                    if let Some(v) = properties.get(key) {
                        return Ok(*v);
                    }
                    if let Some(parent) = *proto {
                        cur = Value::Object(parent);
                    } else {
                        break;
                    }
                }
                _ => break,
            }
        }
        Ok(Value::Undefined)
    }

    /// 设置属性（含数组下标写入、闭包对象属性写入与 Setter 访问器触发）。
    pub fn set_property(&mut self, obj: Value, key: &str, val: Value) -> Result<(), VmError> {
        if let Value::Object(r) = obj {
            let idx = r.0 as usize;
            if idx < self.heap.len() {
                // Setter 访问器优先：命中则调用（不写数据属性，对齐 JS [[Set]] 语义）
                let setter = match &self.heap[idx] {
                    HeapObject::Ordinary { setters, .. } => setters.get(key).copied(),
                    _ => None,
                };
                if let Some(s_idx) = setter {
                    self.invoke_function(s_idx, obj, &[val], Vec::new())?;
                    return Ok(());
                }
                match &mut self.heap[idx] {
                    HeapObject::Ordinary { properties, .. } => {
                        properties.insert(key.to_owned(), val);
                    }
                    HeapObject::Closure { properties, .. } => {
                        properties.insert(key.to_owned(), val);
                    }
                    HeapObject::NativeCtor { properties, .. } => {
                        properties.insert(key.to_owned(), val);
                    }
                    HeapObject::Array {
                        elements,
                        properties,
                        ..
                    } => {
                        if let Ok(i) = key.parse::<usize>() {
                            if i >= elements.len() {
                                elements.resize(i + 1, Value::Undefined);
                            }
                            elements[i] = val;
                        } else if key != "length" {
                            properties.insert(key.to_owned(), val);
                        }
                    }
                    _ => {}
                }
            }
        }
        Ok(())
    }

    /// 判断属性（自有或沿原型链）是否存在于对象上（`in` 运算符语义）。
    pub fn has_property(&mut self, obj: Value, key: &str) -> bool {
        let mut cur = obj;
        let mut depth = 0;
        while let Value::Object(r) = cur {
            if depth > 100 {
                break;
            }
            depth += 1;
            let idx = r.0 as usize;
            if idx >= self.heap.len() {
                break;
            }
            match &self.heap[idx] {
                HeapObject::Ordinary {
                    properties,
                    getters,
                    setters,
                    proto,
                } => {
                    if properties.contains_key(key)
                        || getters.contains_key(key)
                        || setters.contains_key(key)
                    {
                        return true;
                    }
                    cur = match proto {
                        Some(p) => Value::Object(*p),
                        None => break,
                    };
                }
                HeapObject::Closure { properties, .. }
                | HeapObject::NativeCtor { properties, .. } => {
                    if properties.contains_key(key) {
                        return true;
                    }
                    break;
                }
                HeapObject::Array { elements, .. } => {
                    if key == "length" {
                        return true;
                    }
                    if let Ok(i) = key.parse::<usize>() {
                        return i < elements.len();
                    }
                    break;
                }
                _ => break,
            }
        }
        false
    }

    /// 枚举对象自有属性（键 + 值），供 `{ ...src }` 展开使用。
    ///
    /// 普通对象取属性字典；数组产出索引键与 `length`。其余类型为空集。
    pub(crate) fn own_properties(&self, obj: Value) -> Vec<(String, Value)> {
        if let Value::Object(r) = obj {
            let idx = r.0 as usize;
            if idx < self.heap.len() {
                match &self.heap[idx] {
                    HeapObject::Ordinary { properties, .. } => {
                        return properties.iter().map(|(k, v)| (k.clone(), *v)).collect();
                    }
                    HeapObject::Array { elements, .. } => {
                        let mut out = Vec::with_capacity(elements.len() + 1);
                        for (i, v) in elements.iter().enumerate() {
                            out.push((i.to_string(), *v));
                        }
                        out.push(("length".to_owned(), Value::Number(elements.len() as f64)));
                        return out;
                    }
                    _ => {}
                }
            }
        }
        Vec::new()
    }

    /// `for-in` 键枚举（对齐 Go 版 `EnumerateForInKeys`）。
    ///
    /// 沿原型链（≤128 层）收集自有键并去重（先到先得，自有键优先）；
    /// 字符串产出索引键（按 UTF-16 code unit 计）；原始值为空集。
    pub(crate) fn enumerate_for_in_keys(&self, val: Value) -> Vec<String> {
        let mut out: Vec<String> = Vec::new();
        let mut seen = std::collections::HashSet::new();
        let mut cur = Some(val);
        for _ in 0..128 {
            let Some(v) = cur.take() else { break };
            let Value::Object(r) = v else { break };
            let idx = r.0 as usize;
            let Some(h) = self.heap.get(idx) else { break };
            let (keys, proto) = match h {
                HeapObject::Ordinary {
                    properties, proto, ..
                } => (properties.keys().cloned().collect::<Vec<_>>(), *proto),
                HeapObject::Array {
                    elements,
                    properties,
                    proto,
                } => {
                    let mut ks = (0..elements.len())
                        .map(|i| i.to_string())
                        .collect::<Vec<_>>();
                    ks.extend(properties.keys().cloned());
                    (ks, *proto)
                }
                HeapObject::Closure { properties, .. } => {
                    (properties.keys().cloned().collect::<Vec<_>>(), None)
                }
                HeapObject::String(s) => {
                    // 索引键按 UTF-16 code unit 计（星面字符占 2 个）
                    let units: usize = s.chars().map(|c| if c > '\u{FFFF}' { 2 } else { 1 }).sum();
                    let ks = (0..units).map(|i| i.to_string()).collect::<Vec<_>>();
                    (ks, None)
                }
                _ => (Vec::new(), None),
            };
            for k in keys {
                if seen.insert(k.clone()) {
                    out.push(k);
                }
            }
            cur = proto.map(Value::Object);
        }
        out
    }

    /// 读取对象的内部原型 [[Prototype]]。
    pub fn get_prototype(&self, val: Value) -> Option<ObjectRef> {
        if let Value::Object(r) = val {
            let idx = r.0 as usize;
            if let Some(obj) = self.heap.get(idx) {
                match obj {
                    HeapObject::Ordinary { proto, .. } => *proto,
                    HeapObject::Closure { proto, .. } => *proto,
                    HeapObject::Array { proto, .. } => *proto,
                    _ => None,
                }
            } else {
                None
            }
        } else {
            None
        }
    }

    /// 检查 l instanceof r（沿着 l 的原型链查找 r.prototype）。
    pub fn check_instanceof(&mut self, l: Value, r: Value) -> bool {
        let target_proto = match self.get_property(r, "prototype") {
            Ok(Value::Object(p)) => p,
            _ => return false,
        };
        let mut cur = self.get_prototype(l);
        let mut depth = 0;
        while let Some(proto_ref) = cur {
            if depth > 100 {
                break;
            }
            depth += 1;
            if proto_ref == target_proto {
                return true;
            }
            cur = self.get_prototype(Value::Object(proto_ref));
        }
        false
    }
}
