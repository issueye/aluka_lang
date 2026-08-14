import type { Token, TokenType } from "./types.ts";

export class LexerError extends Error {
  constructor(message: string, public line: number, public col: number) {
    super(`[LexerError L${line}:C${col}] ${message}`);
    this.name = "LexerError";
  }
}

const KEYWORDS = new Map<string, TokenType>([
  ["let", "LET"],
  ["fn", "FN"],
  ["if", "IF"],
  ["else", "ELSE"],
  ["return", "RETURN"],
]);

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let cursor = 0;
  let line = 1;
  let col = 1;

  while (cursor < source.length) {
    const ch = source[cursor];

    if (ch === " " || ch === "\t" || ch === "\r") {
      cursor++;
      col++;
      continue;
    }

    if (ch === "\n") {
      cursor++;
      line++;
      col = 1;
      continue;
    }

    if (ch === "/" && source[cursor + 1] === "/") {
      while (cursor < source.length && source[cursor] !== "\n") {
        cursor++;
      }
      continue;
    }

    if (/\d/.test(ch)) {
      let numStr = "";
      const startCol = col;
      while (cursor < source.length && /[\d.]/.test(source[cursor])) {
        numStr += source[cursor++];
        col++;
      }
      tokens.push({ type: "NUMBER", value: numStr, line, col: startCol });
      continue;
    }

    if (/[a-zA-Z_]/.test(ch)) {
      let ident = "";
      const startCol = col;
      while (cursor < source.length && /[a-zA-Z0-9_]/.test(source[cursor])) {
        ident += source[cursor++];
        col++;
      }
      const kwType = KEYWORDS.get(ident);
      tokens.push({
        type: kwType ?? "IDENT",
        value: ident,
        line,
        col: startCol,
      });
      continue;
    }

    const singleOps: Record<string, TokenType> = {
      "+": "PLUS",
      "-": "MINUS",
      "*": "STAR",
      "/": "SLASH",
      "(": "LPAREN",
      ")": "RPAREN",
      "{": "LBRACE",
      "}": "RBRACE",
      "=": "ASSIGN",
      ";": "SEMICOLON",
      ",": "COMMA",
    };

    if (singleOps[ch]) {
      tokens.push({ type: singleOps[ch], value: ch, line, col });
      cursor++;
      col++;
      continue;
    }

    throw new LexerError(`Unexpected character: '${ch}'`, line, col);
  }

  tokens.push({ type: "EOF", value: "", line, col });
  return tokens;
}

// Tagged template string lexing helper
export function code(strings: TemplateStringsArray, ...values: any[]): Token[] {
  let raw = "";
  strings.forEach((str, i) => {
    raw += str + (values[i] !== undefined ? String(values[i]) : "");
  });
  return tokenize(raw);
}
