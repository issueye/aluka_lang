//! 内建原语补齐：`JSON.stringify`、字符串原型方法、`String`/`Symbol` 全局函数。
//!
//! 语义对齐 Go oracle（`aluka_g/bin/aluka.exe` 实测）：
//! - `JSON.stringify(undefined)` 返回字符串 `"null"`（Go 怪癖，非标准 undefined）；
//! - 对象键序按**字典序**输出（受 `Ordinary` 哈希存储限制，与 util.inspect 一致）；
//! - 字符串方法直接在 `CALL_METHOD` 链求值，不物化原型方法占位。

use crate::heap::HeapObject;
use crate::interpreter::{Vm, VmError};
use crate::value::Value;

impl Vm {
    /// 判断值是否为 JSON 全局对象（`_isJSON` 标记）。
    pub(crate) fn is_json_object(&self, val: Value) -> bool {
        matches!(
            val,
            Value::Object(r)
                if matches!(
                    self.heap.get(r.0 as usize),
                    Some(HeapObject::Ordinary { properties, .. })
                        if properties.contains_key("_isJSON")
                )
        )
    }

    /// `JSON.stringify(value[, replacer[, space]])`（replacer/space 忽略）。
    pub(crate) fn json_stringify(&mut self, value: Value) -> Result<Value, VmError> {
        let mut out = String::new();
        self.json_write(&mut out, value, &mut Vec::new());
        Ok(Value::Object(self.alloc_string(out)))
    }

    /// 递归序列化。`seen` 持有栈上对象句柄做循环引用检测（循环 → `"null"`，
    /// 对齐标准 `TypeError` 之外的常见降级；Go 侧实测无循环用例）。
    fn json_write(&self, out: &mut String, value: Value, seen: &mut Vec<u32>) {
        match value {
            // Go oracle 怪癖：undefined 序列化为 "null"（实测 console.log 输出 null）
            Value::Undefined | Value::Null => out.push_str("null"),
            Value::Boolean(b) => out.push_str(if b { "true" } else { "false" }),
            Value::Number(n) => {
                if n.is_nan() || n.is_infinite() {
                    out.push_str("null");
                } else {
                    out.push_str(&format!("{n}"));
                }
            }
            Value::Object(r) => {
                if seen.contains(&r.0) {
                    out.push_str("null");
                    return;
                }
                match self.heap.get(r.0 as usize) {
                    Some(HeapObject::String(text)) => out.push_str(&json_quote(text)),
                    Some(HeapObject::Array { elements, .. }) => {
                        seen.push(r.0);
                        out.push('[');
                        for (i, el) in elements.iter().enumerate() {
                            if i > 0 {
                                out.push(',');
                            }
                            // 数组内的 undefined/null 均序列化为 "null"（标准）
                            match el {
                                Value::Undefined => out.push_str("null"),
                                v => self.json_write(out, *v, seen),
                            }
                        }
                        out.push(']');
                        seen.pop();
                    }
                    Some(HeapObject::Ordinary { properties, .. }) => {
                        seen.push(r.0);
                        out.push('{');
                        // 字典序（Ordinary 为哈希存储，插入序不可得；与 util.inspect 一致）
                        // 符号键不参与 JSON 序列化（标准语义）
                        let mut keys: Vec<&String> = properties
                            .keys()
                            .filter(|k| !crate::symbol::is_symbol_key(k))
                            .collect();
                        keys.sort();
                        for (i, k) in keys.iter().enumerate() {
                            if i > 0 {
                                out.push(',');
                            }
                            out.push_str(&json_quote(k));
                            out.push(':');
                            // 对象属性值为 undefined 时整键剔除（标准）；此处简化
                            // 与 Go 对齐：undefined 值 → "null"（实测行为一致）
                            match properties.get(*k) {
                                Some(Value::Undefined) => out.push_str("null"),
                                Some(v) => self.json_write(out, *v, seen),
                                None => out.push_str("null"),
                            }
                        }
                        out.push('}');
                        seen.pop();
                    }
                    _ => out.push_str("null"),
                }
            }
        }
    }

    /// 字符串接收者的原型方法求值（`CALL_METHOD` 链调用）。
    ///
    /// 返回 `None` 表示方法未实现（调用方继续既有路径）。
    pub(crate) fn call_string_method(
        &mut self,
        method: &str,
        args: &[Value],
        text: &str,
    ) -> Option<Result<Value, VmError>> {
        use Value::Number;
        // 借用隔离：arg_str/arg_num 提为自由函数（self 顺序借用）
        fn arg_str(vm: &mut Vm, args: &[Value], i: usize) -> String {
            args.get(i).map(|v| vm.format_value(*v)).unwrap_or_default()
        }
        fn arg_num(args: &[Value], i: usize) -> Option<f64> {
            args.get(i).and_then(|v| match v {
                Value::Number(n) => Some(*n),
                _ => None,
            })
        }
        macro_rules! ret_str {
            ($v:expr) => {
                return Some(Ok(Value::Object(self.alloc_string($v))))
            };
        }
        let chars: Vec<char> = text.chars().collect();
        match method {
            "length" => Some(Ok(Number(chars.len() as f64))),
            "trim" => ret_str!(text.trim().to_string()),
            "trimStart" | "trimLeft" => ret_str!(text.trim_start().to_string()),
            "trimEnd" | "trimRight" => ret_str!(text.trim_end().to_string()),
            "toUpperCase" => ret_str!(text.to_uppercase()),
            "toLowerCase" => ret_str!(text.to_lowercase()),
            "charAt" => {
                let i = arg_num(args, 0).unwrap_or(0.0);
                let ch = chars
                    .get(i as usize)
                    .map(|c| c.to_string())
                    .unwrap_or_default();
                ret_str!(ch);
            }
            "charCodeAt" => {
                let i = arg_num(args, 0).unwrap_or(0.0);
                let code = chars
                    .get(i as usize)
                    .map(|c| (*c as u32) as f64)
                    .unwrap_or(f64::NAN);
                Some(Ok(Number(code)))
            }
            "indexOf" => {
                let needle = arg_str(self, args, 0);
                let from_f = arg_num(args, 1).unwrap_or(0.0);
                let from = if from_f < 0.0 {
                    0usize
                } else {
                    (from_f as usize).min(chars.len())
                };
                if needle.is_empty() {
                    return Some(Ok(Number(from as f64)));
                }
                let hay: String = chars.iter().skip(from).collect();
                // find 返回字节偏移：换算为字符索引（JS indexOf 为 UTF-16 下标，
                // 此处以 char 下标近似，BMP 内一致）
                let pos = hay
                    .find(&needle)
                    .map(|byte_p| hay[..byte_p].chars().count() + from);
                Some(Ok(Number(match pos {
                    Some(p) => p as f64,
                    None => -1.0,
                })))
            }
            "lastIndexOf" => {
                let needle = arg_str(self, args, 0);
                Some(Ok(Number(match text.rfind(&needle) {
                    Some(p) => text[..p].chars().count() as f64,
                    None => -1.0,
                })))
            }
            "includes" => {
                let needle = arg_str(self, args, 0);
                Some(Ok(Value::Boolean(text.contains(&needle))))
            }
            "startsWith" => {
                let needle = arg_str(self, args, 0);
                Some(Ok(Value::Boolean(text.starts_with(&needle))))
            }
            "endsWith" => {
                let needle = arg_str(self, args, 0);
                Some(Ok(Value::Boolean(text.ends_with(&needle))))
            }
            "slice" => {
                let len = chars.len() as f64;
                let norm = |v: f64| -> usize {
                    let start = if v < 0.0 {
                        (len + v).max(0.0)
                    } else {
                        v.min(len)
                    };
                    start as usize
                };
                let start = norm(arg_num(args, 0).unwrap_or(0.0));
                let end = match arg_num(args, 1) {
                    Some(e) => norm(e),
                    None => chars.len(),
                };
                let slice: String = if start < end {
                    chars[start..end].iter().collect()
                } else {
                    String::new()
                };
                ret_str!(slice);
            }
            "substring" => {
                let len = chars.len();
                let clamp = |v: f64| -> usize {
                    if v < 0.0 || v.is_nan() {
                        0
                    } else {
                        (v as usize).min(len)
                    }
                };
                let mut start = clamp(arg_num(args, 0).unwrap_or(0.0));
                let mut end = match arg_num(args, 1) {
                    Some(e) => clamp(e),
                    None => len,
                };
                if start > end {
                    std::mem::swap(&mut start, &mut end);
                }
                let sub: String = chars[start..end].iter().collect();
                ret_str!(sub);
            }
            "repeat" => {
                let n = arg_num(args, 0).unwrap_or(0.0).max(0.0) as usize;
                ret_str!(text.repeat(n));
            }
            "concat" => {
                let mut out = text.to_string();
                for i in 0..args.len() {
                    out.push_str(&arg_str(self, args, i));
                }
                ret_str!(out);
            }
            "replace" => {
                // 字符串实参：仅替换首次出现（标准语义；不支持模式）
                let from = arg_str(self, args, 0);
                let to = arg_str(self, args, 1);
                ret_str!(text.replacen(&from, &to, 1));
            }
            "replaceAll" => {
                let from = arg_str(self, args, 0);
                let to = arg_str(self, args, 1);
                ret_str!(text.replace(&from, &to));
            }
            "split" => {
                let sep = arg_str(self, args, 0);
                let parts: Vec<Value> = if sep.is_empty() {
                    chars
                        .iter()
                        .map(|c| {
                            let s = self.alloc_string(c.to_string());
                            Value::Object(s)
                        })
                        .collect()
                } else {
                    text.split(&sep)
                        .map(|p| {
                            let s = self.alloc_string(p.to_string());
                            Value::Object(s)
                        })
                        .collect()
                };
                Some(Ok(Value::Object(self.alloc_array(parts))))
            }
            _ => None,
        }
    }
}

impl Vm {
    /// `JSON.parse(text)`：递归下降解析器，错误消息复刻 Go `encoding/json`
    /// 实测形态（`SyntaxError` + Go rune 引号字符），与 oracle 对拍一致。
    pub(crate) fn json_parse(&mut self, args: &[Value]) -> Result<Value, VmError> {
        let src = match args.first() {
            Some(v) => self.format_value(*v),
            None => String::new(),
        };
        let mut p = JsonParser {
            b: src.as_bytes(),
            s: &src,
            pos: 0,
        };
        p.skip_ws();
        let value = match p.parse_value(self) {
            Ok(v) => v,
            Err(msg) => return Err(self.syntax_error(&msg)),
        };
        p.skip_ws();
        if p.pos < p.b.len() {
            return Err(self.syntax_error(&format!(
                "invalid character {} after top-level value",
                quote_rune(p.b[p.pos])
            )));
        }
        Ok(value)
    }

    /// 构造 `name === "SyntaxError"` 的错误实例并包装为 Thrown。
    fn syntax_error(&mut self, msg: &str) -> VmError {
        let err = self.alloc_error_instance(msg);
        let name = self.alloc_string("SyntaxError".to_owned());
        let _ = self.set_property(Value::Object(err), "name", Value::Object(name));
        VmError::Thrown(Value::Object(err))
    }
}

/// Go rune 字面量形态：普通字符 `'b'`，控制字符用转义序列。
fn quote_rune(b: u8) -> String {
    match b {
        0x0a => "'\\n'".to_owned(),
        0x0d => "'\\r'".to_owned(),
        0x09 => "'\\t'".to_owned(),
        _ => format!("'{}'", b as char),
    }
}

struct JsonParser<'a> {
    b: &'a [u8],
    s: &'a str,
    pos: usize,
}

impl<'a> JsonParser<'a> {
    fn skip_ws(&mut self) {
        while self.pos < self.b.len() && matches!(self.b[self.pos], b' ' | b'\t' | b'\n' | b'\r') {
            self.pos += 1;
        }
    }

    fn peek(&self) -> Option<u8> {
        self.b.get(self.pos).copied()
    }

    fn parse_value(&mut self, vm: &mut Vm) -> Result<Value, String> {
        self.skip_ws();
        let Some(c) = self.peek() else {
            return Err("unexpected end of JSON input".to_owned());
        };
        match c {
            b'{' => self.parse_object(vm),
            b'[' => self.parse_array(vm),
            b'"' => self
                .parse_string()
                .map(|s| Value::Object(vm.alloc_string(s))),
            b't' => self.parse_literal("true", b"rue", Value::Boolean(true)),
            b'f' => self.parse_literal("false", b"alse", Value::Boolean(false)),
            b'n' => self.parse_literal("null", b"ull", Value::Null),
            b'-' | b'0'..=b'9' => self.parse_number(),
            _ => Err(format!(
                "invalid character {} looking for beginning of value",
                quote_rune(c)
            )),
        }
    }

    fn parse_literal(&mut self, word: &str, rest: &[u8], value: Value) -> Result<Value, String> {
        // 首字符已消费；逐字符校验余下部分（Go 逐字符报错形态）
        for (i, expected) in rest.iter().enumerate() {
            let at = self.pos + 1 + i;
            match self.b.get(at) {
                None => return Err("unexpected end of JSON input".to_owned()),
                Some(&got) if got != *expected => {
                    return Err(format!(
                        "invalid character {} in literal {}",
                        quote_rune(got),
                        word
                    ));
                }
                _ => {}
            }
        }
        self.pos += 1 + rest.len();
        Ok(value)
    }

    fn parse_number(&mut self) -> Result<Value, String> {
        let start = self.pos;
        if self.peek() == Some(b'-') {
            self.pos += 1;
        }
        match self.peek() {
            Some(b'0') => self.pos += 1,
            Some(c) if c.is_ascii_digit() => {
                while self.peek().is_some_and(|c| c.is_ascii_digit()) {
                    self.pos += 1;
                }
            }
            Some(c) => {
                return Err(format!(
                    "invalid character {} in numeric literal",
                    quote_rune(c)
                ));
            }
            None => return Err("unexpected end of JSON input".to_owned()),
        }
        if self.peek() == Some(b'.') {
            self.pos += 1;
            if !self.peek().is_some_and(|c| c.is_ascii_digit()) {
                return Err(match self.peek() {
                    Some(c) => format!("invalid character {} in numeric literal", quote_rune(c)),
                    None => "unexpected end of JSON input".to_owned(),
                });
            }
            while self.peek().is_some_and(|c| c.is_ascii_digit()) {
                self.pos += 1;
            }
        }
        if matches!(self.peek(), Some(b'e') | Some(b'E')) {
            self.pos += 1;
            if matches!(self.peek(), Some(b'+') | Some(b'-')) {
                self.pos += 1;
            }
            if !self.peek().is_some_and(|c| c.is_ascii_digit()) {
                return Err(match self.peek() {
                    Some(c) => format!("invalid character {} in numeric literal", quote_rune(c)),
                    None => "unexpected end of JSON input".to_owned(),
                });
            }
            while self.peek().is_some_and(|c| c.is_ascii_digit()) {
                self.pos += 1;
            }
        }
        let text = &self.s[start..self.pos];
        text.parse::<f64>()
            .map(Value::Number)
            .map_err(|_| "unexpected end of JSON input".to_owned())
    }

    fn parse_string(&mut self) -> Result<String, String> {
        self.pos += 1; // 开引号
        let mut out = String::new();
        while let Some(c) = self.peek() {
            match c {
                b'"' => {
                    self.pos += 1;
                    return Ok(out);
                }
                0x5c => {
                    self.pos += 1;
                    let Some(esc) = self.peek() else {
                        return Err("unexpected end of JSON input".to_owned());
                    };
                    self.pos += 1;
                    match esc {
                        0x22 => out.push('"'),
                        0x5c => out.push('\\'),
                        0x2f => out.push('/'),
                        0x08 => out.push('\u{8}'),
                        0x0c => out.push('\u{c}'),
                        b'n' => out.push('\n'),
                        b'r' => out.push('\r'),
                        b't' => out.push('\t'),
                        b'u' => {
                            if self.pos + 4 > self.b.len() {
                                return Err("unexpected end of JSON input".to_owned());
                            }
                            let hex = &self.s[self.pos..self.pos + 4];
                            let cp = u32::from_str_radix(hex, 16)
                                .map_err(|_| "invalid Unicode escape".to_owned())?;
                            self.pos += 4;
                            out.push(char::from_u32(cp).unwrap_or('\u{FFFD}'));
                        }
                        other => {
                            return Err(format!(
                                "invalid character {} in string escape code",
                                quote_rune(other)
                            ));
                        }
                    }
                }
                c if c < 0x20 => {
                    return Err(format!(
                        "invalid character {} in string literal",
                        quote_rune(c)
                    ));
                }
                _ => {
                    let ch = self.s[self.pos..].chars().next().unwrap_or('\u{FFFD}');
                    out.push(ch);
                    self.pos += ch.len_utf8();
                }
            }
        }
        Err("unexpected end of JSON input".to_owned())
    }

    fn parse_array(&mut self, vm: &mut Vm) -> Result<Value, String> {
        self.pos += 1; // '['
        let mut elements = Vec::new();
        self.skip_ws();
        if self.peek() == Some(b']') {
            self.pos += 1;
            return Ok(Value::Object(vm.alloc_array(elements)));
        }
        loop {
            let v = self.parse_value(vm)?;
            elements.push(v);
            self.skip_ws();
            match self.peek() {
                Some(b',') => self.pos += 1,
                Some(b']') => {
                    self.pos += 1;
                    return Ok(Value::Object(vm.alloc_array(elements)));
                }
                Some(c) => {
                    return Err(format!(
                        "invalid character {} after array element",
                        quote_rune(c)
                    ));
                }
                None => return Err("unexpected end of JSON input".to_owned()),
            }
        }
    }

    fn parse_object(&mut self, vm: &mut Vm) -> Result<Value, String> {
        self.pos += 1; // '{'
        let obj = vm.alloc_ordinary();
        self.skip_ws();
        if self.peek() == Some(b'}') {
            self.pos += 1;
            return Ok(Value::Object(obj));
        }
        loop {
            self.skip_ws();
            let Some(key) = self.peek() else {
                return Err("unexpected end of JSON input".to_owned());
            };
            if key != 0x22 {
                return Err(format!(
                    "invalid character {} looking for beginning of object key string",
                    quote_rune(key)
                ));
            }
            let key = self.parse_string()?;
            self.skip_ws();
            match self.peek() {
                Some(b':') => self.pos += 1,
                Some(c) => {
                    return Err(format!(
                        "invalid character {} after object key",
                        quote_rune(c)
                    ));
                }
                None => return Err("unexpected end of JSON input".to_owned()),
            }
            let value = self.parse_value(vm)?;
            let _ = vm.set_property(Value::Object(obj), &key, value);
            self.skip_ws();
            match self.peek() {
                Some(b',') => self.pos += 1,
                Some(b'}') => {
                    self.pos += 1;
                    return Ok(Value::Object(obj));
                }
                Some(c) => {
                    return Err(format!(
                        "invalid character {} after object key:value pair",
                        quote_rune(c)
                    ));
                }
                None => return Err("unexpected end of JSON input".to_owned()),
            }
        }
    }
}

/// JSON 字符串引号包裹与转义（对齐 `JSON.stringify` 的字符串形态）。
fn json_quote(text: &str) -> String {
    let mut out = String::with_capacity(text.len() + 2);
    out.push('"');
    for c in text.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}
