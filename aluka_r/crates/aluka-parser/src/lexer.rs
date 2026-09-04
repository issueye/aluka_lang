//! 词法分析：源码 → token 流。
//!
//! 支持 ECMAScript / TypeScript 关键字、多字符运算符、字符串转义、注释跳过。

/// Token 细分类别。
#[derive(Debug, Clone, PartialEq)]
pub enum TokenKind {
    /// 数值字面量
    Number(f64),
    /// 大整数字面量（如 123n）
    BigInt(String),
    /// 字符串字面量
    String(String),
    /// 标识符
    Ident(String),
    /// 关键字
    Keyword(String),
    /// 标点与运算符
    Punct(String),
    /// 输入结束
    Eof,
}

/// 一个 token 及其在源码中的起始字节偏移。
#[derive(Debug, Clone, PartialEq)]
pub struct Token {
    /// 类别
    pub kind: TokenKind,
    /// 原始文本
    pub text: String,
    /// 起始字节偏移
    pub start: usize,
}

impl Token {
    /// 判定是否为指定的标点符号
    #[must_use]
    pub fn is_punct(&self, p: &str) -> bool {
        matches!(&self.kind, TokenKind::Punct(s) if s == p)
    }
}

/// 词法分析器。
#[derive(Debug)]
pub struct Lexer<'src> {
    src: &'src str,
    pos: usize,
}

const KEYWORDS: &[&str] = &[
    "let",
    "const",
    "var",
    "function",
    "class",
    "extends",
    "if",
    "else",
    "while",
    "for",
    "break",
    "continue",
    "return",
    "try",
    "catch",
    "finally",
    "throw",
    "new",
    "this",
    "super",
    "true",
    "false",
    "null",
    "undefined",
    "interface",
    "type",
    "as",
    "typeof",
    "delete",
    "void",
    "in",
    "instanceof",
    "yield",
    "await",
    "async",
    "switch",
    "case",
    "default",
    "do",
    "import",
    "export",
    "from",
];

impl<'src> Lexer<'src> {
    /// 在源码上创建分析器。
    #[must_use]
    pub fn new(src: &'src str) -> Self {
        Self { src, pos: 0 }
    }

    /// 跳过空白字符与注释（单行 // 与多行 /* */）。
    fn skip_whitespace_and_comments(&mut self) {
        let bytes = self.src.as_bytes();
        while self.pos < bytes.len() {
            // 空白字符
            if bytes[self.pos].is_ascii_whitespace() {
                self.pos += 1;
                continue;
            }
            // 单行注释 //
            if self.pos + 1 < bytes.len() && bytes[self.pos] == b'/' && bytes[self.pos + 1] == b'/'
            {
                self.pos += 2;
                while self.pos < bytes.len() && bytes[self.pos] != b'\n' {
                    self.pos += 1;
                }
                continue;
            }
            // 多行注释 /* */
            if self.pos + 1 < bytes.len() && bytes[self.pos] == b'/' && bytes[self.pos + 1] == b'*'
            {
                self.pos += 2;
                while self.pos + 1 < bytes.len()
                    && !(bytes[self.pos] == b'*' && bytes[self.pos + 1] == b'/')
                {
                    self.pos += 1;
                }
                if self.pos + 1 < bytes.len() {
                    self.pos += 2; // 跳过 */
                }
                continue;
            }
            break;
        }
    }

    /// 取下一个 token；输入耗尽后恒返回 [`TokenKind::Eof`]。
    pub fn next_token(&mut self) -> Token {
        self.skip_whitespace_and_comments();
        let bytes = self.src.as_bytes();
        if self.pos >= bytes.len() {
            return Token {
                kind: TokenKind::Eof,
                text: String::new(),
                start: self.pos,
            };
        }

        let start = self.pos;
        let first = bytes[self.pos];

        // 1. 字符串字面量 ("..." 或 '...' 或 `...`)
        if first == b'"' || first == b'\'' || first == b'`' {
            let quote = first;
            self.pos += 1;
            let mut s = String::new();
            while self.pos < bytes.len() && bytes[self.pos] != quote {
                if bytes[self.pos] == b'\\' && self.pos + 1 < bytes.len() {
                    self.pos += 1;
                    match bytes[self.pos] {
                        b'n' => s.push('\n'),
                        b't' => s.push('\t'),
                        b'r' => s.push('\r'),
                        b'\\' => s.push('\\'),
                        b'"' => s.push('"'),
                        b'\'' => s.push('\''),
                        b'`' => s.push('`'),
                        other => s.push(other as char),
                    }
                } else {
                    s.push(bytes[self.pos] as char);
                }
                self.pos += 1;
            }
            if self.pos < bytes.len() && bytes[self.pos] == quote {
                self.pos += 1;
            }
            return Token {
                kind: TokenKind::String(s),
                text: self.src[start..self.pos].to_owned(),
                start,
            };
        }

        // 2. 数值字面量
        if first.is_ascii_digit() {
            while self.pos < bytes.len()
                && (bytes[self.pos].is_ascii_digit()
                    || bytes[self.pos] == b'.'
                    || bytes[self.pos] == b'_')
            {
                self.pos += 1;
            }
            if self.pos < bytes.len() && bytes[self.pos] == b'n' {
                let raw_digits: String = self.src[start..self.pos]
                    .chars()
                    .filter(|&c| c != '_')
                    .collect();
                self.pos += 1;
                return Token {
                    kind: TokenKind::BigInt(raw_digits),
                    text: self.src[start..self.pos].to_owned(),
                    start,
                };
            }
            let raw_str: String = self.src[start..self.pos]
                .chars()
                .filter(|&c| c != '_')
                .collect();
            let val = raw_str.parse::<f64>().unwrap_or(0.0);
            return Token {
                kind: TokenKind::Number(val),
                text: self.src[start..self.pos].to_owned(),
                start,
            };
        }

        // 3. 标识符与关键字
        if first.is_ascii_alphabetic() || first == b'_' || first == b'$' {
            while self.pos < bytes.len()
                && (bytes[self.pos].is_ascii_alphanumeric()
                    || bytes[self.pos] == b'_'
                    || bytes[self.pos] == b'$')
            {
                self.pos += 1;
            }
            let text = self.src[start..self.pos].to_owned();
            let kind = if KEYWORDS.contains(&text.as_str()) {
                TokenKind::Keyword(text.clone())
            } else {
                TokenKind::Ident(text.clone())
            };
            return Token { kind, text, start };
        }

        // 4. 多字符运算符与标点
        let multi_puncts = &[
            "...", ">>>=", "===", "!==", ">>>", "**=", "<<=", ">>=", "&&=", "||=", "??=", "==",
            "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "**", "<<", ">>", "+=", "-=",
            "*=", "/=", "%=", "=>",
        ];

        for &mp in multi_puncts {
            if self.src[self.pos..].starts_with(mp) {
                self.pos += mp.len();
                return Token {
                    kind: TokenKind::Punct(mp.to_owned()),
                    text: mp.to_owned(),
                    start,
                };
            }
        }

        // 单字符标点
        self.pos += 1;
        let text = self.src[start..self.pos].to_owned();
        Token {
            kind: TokenKind::Punct(text.clone()),
            text,
            start,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scans_numbers_idents_and_puncts() {
        let mut lexer = Lexer::new("let x1 = 42; // 注释\n/* 多行 */ x1 === \"hello\";");
        let tokens: Vec<Token> = std::iter::from_fn(|| {
            let token = lexer.next_token();
            if token.kind == TokenKind::Eof {
                None
            } else {
                Some(token)
            }
        })
        .collect();

        assert_eq!(tokens[0].kind, TokenKind::Keyword("let".to_owned()));
        assert_eq!(tokens[1].kind, TokenKind::Ident("x1".to_owned()));
        assert_eq!(tokens[2].kind, TokenKind::Punct("=".to_owned()));
        assert_eq!(tokens[3].kind, TokenKind::Number(42.0));
        assert_eq!(tokens[4].kind, TokenKind::Punct(";".to_owned()));
        assert_eq!(tokens[5].kind, TokenKind::Ident("x1".to_owned()));
        assert_eq!(tokens[6].kind, TokenKind::Punct("===".to_owned()));
        assert_eq!(tokens[7].kind, TokenKind::String("hello".to_owned()));
        assert_eq!(tokens[8].kind, TokenKind::Punct(";".to_owned()));
    }

    #[test]
    fn reports_eof_after_input_is_consumed() {
        let mut lexer = Lexer::new("  \n\t // trailing comment\n ");
        assert_eq!(lexer.next_token().kind, TokenKind::Eof);
        assert_eq!(lexer.next_token().kind, TokenKind::Eof);
    }
}
