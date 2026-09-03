//! 词法分析：源码 → token 流。
//!
//! JS 的词法有两处与直觉不同，实现时必须照顾：
//!
//! - 正则字面量与除法运算符共享 `/`，需要靠前一个 token 的类别消歧；
//! - 模板字面量里的裸 CRLF/CR 必须规范化为 LF（ES 的 TV/TRV 语义）。
//!   Go 版曾因漏掉这条让 CRLF 行尾的 vendored 包生成错误代码，见
//!   `AGENTS.md` 的词法行终止符规范。

/// token 的类别。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TokenKind {
    /// 数值字面量
    Number,
    /// 字符串字面量
    String,
    /// 标识符或关键字
    Ident,
    /// 运算符与标点
    Punct,
    /// 输入结束
    Eof,
}

/// 一个 token 及其在源码中的起始字节偏移。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Token {
    /// 类别
    pub kind: TokenKind,
    /// 原始文本
    pub text: String,
    /// 起始字节偏移
    pub start: usize,
}

/// 词法分析器。
///
/// M0 阶段仅识别数字、标识符与单字符标点，用来把 crate 间的接口跑通；
/// 字符串、模板、正则与全部关键字在 M1 落地。
#[derive(Debug)]
pub struct Lexer<'src> {
    src: &'src str,
    pos: usize,
}

impl<'src> Lexer<'src> {
    /// 在源码上创建分析器。
    #[must_use]
    pub fn new(src: &'src str) -> Self {
        Self { src, pos: 0 }
    }

    /// 取下一个 token；输入耗尽后恒返回 [`TokenKind::Eof`]。
    pub fn next_token(&mut self) -> Token {
        let bytes = self.src.as_bytes();
        while self.pos < bytes.len() && bytes[self.pos].is_ascii_whitespace() {
            self.pos += 1;
        }
        if self.pos >= bytes.len() {
            return Token {
                kind: TokenKind::Eof,
                text: String::new(),
                start: self.pos,
            };
        }
        let start = self.pos;
        let first = bytes[self.pos];
        if first.is_ascii_digit() {
            while self.pos < bytes.len()
                && (bytes[self.pos].is_ascii_digit() || bytes[self.pos] == b'.')
            {
                self.pos += 1;
            }
            return Token {
                kind: TokenKind::Number,
                text: self.src[start..self.pos].to_owned(),
                start,
            };
        }
        if first.is_ascii_alphabetic() || first == b'_' || first == b'$' {
            while self.pos < bytes.len()
                && (bytes[self.pos].is_ascii_alphanumeric()
                    || bytes[self.pos] == b'_'
                    || bytes[self.pos] == b'$')
            {
                self.pos += 1;
            }
            return Token {
                kind: TokenKind::Ident,
                text: self.src[start..self.pos].to_owned(),
                start,
            };
        }
        self.pos += 1;
        Token {
            kind: TokenKind::Punct,
            text: self.src[start..self.pos].to_owned(),
            start,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scans_numbers_idents_and_puncts() {
        let mut lexer = Lexer::new("let x1 = 42;");
        let kinds: Vec<TokenKind> = std::iter::from_fn(|| {
            let token = lexer.next_token();
            if token.kind == TokenKind::Eof {
                None
            } else {
                Some(token.kind)
            }
        })
        .collect();
        assert_eq!(
            kinds,
            vec![
                TokenKind::Ident,
                TokenKind::Ident,
                TokenKind::Punct,
                TokenKind::Number,
                TokenKind::Punct
            ]
        );
    }

    #[test]
    fn reports_eof_after_input_is_consumed() {
        let mut lexer = Lexer::new("  ");
        assert_eq!(lexer.next_token().kind, TokenKind::Eof);
        assert_eq!(lexer.next_token().kind, TokenKind::Eof);
    }
}
