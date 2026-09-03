//! 算术、位运算与类型强制转换逻辑。

use crate::heap::HeapObject;
use crate::interpreter::Vm;
use crate::value::Value;

/// 将任意值强制转换为数值。
#[must_use]
pub fn to_number(val: Value) -> f64 {
    match val {
        Value::Number(n) => n,
        Value::Boolean(true) => 1.0,
        Value::Boolean(false) | Value::Null => 0.0,
        Value::Undefined => f64::NAN,
        Value::Object(_) => f64::NAN,
    }
}

/// 将任意值强制转换为布尔值。
#[must_use]
pub fn to_boolean(val: Value) -> bool {
    val.is_truthy()
}

fn get_string_repr<'a>(
    idx: usize,
    heap: &'a [HeapObject],
    constants: &'a [aluka_bytecode::Constant],
) -> Option<&'a str> {
    if idx < heap.len() {
        if let HeapObject::String(s) = &heap[idx] {
            return Some(s.as_str());
        }
    }
    if let Some(aluka_bytecode::Constant::String(s)) = constants.get(idx) {
        return Some(s.as_str());
    }
    None
}

/// 判定非严格相等（==）。
pub fn eq(
    left: Value,
    right: Value,
    heap: &[HeapObject],
    constants: &[aluka_bytecode::Constant],
) -> bool {
    match (left, right) {
        (Value::Number(a), Value::Number(b)) => a == b,
        (Value::Boolean(a), Value::Boolean(b)) => a == b,
        (Value::Null, Value::Null) | (Value::Undefined, Value::Undefined) => true,
        (Value::Null, Value::Undefined) | (Value::Undefined, Value::Null) => true,
        (Value::Number(n), Value::Object(r)) | (Value::Object(r), Value::Number(n)) => {
            if let Some(s) = get_string_repr(r.0 as usize, heap, constants) {
                if let Ok(sn) = s.trim().parse::<f64>() {
                    return n == sn;
                }
            }
            false
        }
        (Value::Object(a), Value::Object(b)) => {
            if a == b {
                true
            } else {
                let s_a = get_string_repr(a.0 as usize, heap, constants);
                let s_b = get_string_repr(b.0 as usize, heap, constants);
                match (s_a, s_b) {
                    (Some(sa), Some(sb)) => sa == sb,
                    _ => false,
                }
            }
        }
        _ => false,
    }
}

/// 判定严格相等（===）。
pub fn strict_eq(
    left: Value,
    right: Value,
    heap: &[HeapObject],
    constants: &[aluka_bytecode::Constant],
) -> bool {
    match (left, right) {
        (Value::Number(a), Value::Number(b)) => a == b,
        (Value::Boolean(a), Value::Boolean(b)) => a == b,
        (Value::Null, Value::Null) | (Value::Undefined, Value::Undefined) => true,
        (Value::Object(a), Value::Object(b)) => {
            if a == b {
                true
            } else {
                let s_a = get_string_repr(a.0 as usize, heap, constants);
                let s_b = get_string_repr(b.0 as usize, heap, constants);
                match (s_a, s_b) {
                    (Some(sa), Some(sb)) => sa == sb,
                    _ => false,
                }
            }
        }
        _ => false,
    }
}

impl Vm {
    /// 执行加法运算（支持数值相加与 ECMAScript 字符串自动拼接）。
    pub fn add_values(&mut self, left: Value, right: Value) -> Value {
        if let (Value::Number(a), Value::Number(b)) = (left, right) {
            return Value::Number(a + b);
        }
        let is_left_str = if let Value::Object(r) = left {
            matches!(self.heap.get(r.0 as usize), Some(HeapObject::String(_)))
        } else {
            false
        };
        let is_right_str = if let Value::Object(r) = right {
            matches!(self.heap.get(r.0 as usize), Some(HeapObject::String(_)))
        } else {
            false
        };

        if is_left_str || is_right_str {
            let s1 = self.format_value(left);
            let s2 = self.format_value(right);
            let combined = format!("{s1}{s2}");
            let s_ref = self.alloc_string(combined);
            return Value::Object(s_ref);
        }

        Value::Number(f64::NAN)
    }
}
