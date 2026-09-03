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
                    if let HeapObject::String(s) = &self.heap[idx] {
                        return s.clone();
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
                HeapObject::Array { elements } => {
                    if key == "length" {
                        return Ok(Value::Number(elements.len() as f64));
                    }
                    if let Ok(i) = key.parse::<usize>() {
                        return Ok(elements.get(i).copied().unwrap_or(Value::Undefined));
                    }
                    break;
                }
                _ => break,
            }
        }
        Ok(Value::Undefined)
    }

    /// 设置属性（含数组下标写入与闭包对象属性写入）。
    pub fn set_property(&mut self, obj: Value, key: &str, val: Value) -> Result<(), VmError> {
        if let Value::Object(r) = obj {
            let idx = r.0 as usize;
            if idx < self.heap.len() {
                match &mut self.heap[idx] {
                    HeapObject::Ordinary { properties, .. } => {
                        properties.insert(key.to_owned(), val);
                    }
                    HeapObject::Closure { properties, .. } => {
                        properties.insert(key.to_owned(), val);
                    }
                    HeapObject::Array { elements } => {
                        if let Ok(i) = key.parse::<usize>() {
                            if i >= elements.len() {
                                elements.resize(i + 1, Value::Undefined);
                            }
                            elements[i] = val;
                        }
                    }
                    _ => {}
                }
            }
        }
        Ok(())
    }

    /// 读取对象的内部原型 [[Prototype]]。
    pub fn get_prototype(&self, val: Value) -> Option<ObjectRef> {
        if let Value::Object(r) = val {
            let idx = r.0 as usize;
            if let Some(obj) = self.heap.get(idx) {
                match obj {
                    HeapObject::Ordinary { proto, .. } => *proto,
                    HeapObject::Closure { proto, .. } => *proto,
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
