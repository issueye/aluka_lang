export type TokenType =
  | "NUMBER"
  | "IDENT"
  | "PLUS"
  | "MINUS"
  | "STAR"
  | "SLASH"
  | "LPAREN"
  | "RPAREN"
  | "LET"
  | "FN"
  | "IF"
  | "ELSE"
  | "ASSIGN"
  | "SEMICOLON"
  | "COMMA"
  | "LBRACE"
  | "RBRACE"
  | "RETURN"
  | "EOF";

export interface Token {
  type: TokenType;
  value: string;
  line: number;
  col: number;
}

export type ASTNode =
  | { type: "NumLit"; value: number }
  | { type: "Var"; name: string }
  | { type: "BinOp"; op: string; left: ASTNode; right: ASTNode }
  | { type: "VarDecl"; name: string; init: ASTNode }
  | { type: "Assign"; name: string; value: ASTNode }
  | { type: "FnDecl"; name: string; params: string[]; body: ASTNode[] }
  | { type: "Call"; callee: ASTNode; args: ASTNode[] }
  | { type: "IfStmt"; test: ASTNode; consequent: ASTNode[]; alternate?: ASTNode[] }
  | { type: "Return"; value: ASTNode }
  | { type: "Block"; body: ASTNode[] };

export const Opcode = {
  OP_CONST: 0x01,
  OP_LOAD: 0x02,
  OP_STORE: 0x03,
  OP_ADD: 0x04,
  OP_SUB: 0x05,
  OP_MUL: 0x06,
  OP_DIV: 0x07,
  OP_CALL: 0x08,
  OP_RET: 0x09,
  OP_JMP_IF_FALSE: 0x0A,
  OP_JMP: 0x0B,
  OP_CLOSURE: 0x0C,
  OP_HALT: 0xFF,
} as const;

export type Opcode = (typeof Opcode)[keyof typeof Opcode];
