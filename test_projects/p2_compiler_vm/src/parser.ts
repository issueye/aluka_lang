import type { ASTNode, Token, TokenType } from "./types.ts";

export class Parser {
  private pos = 0;

  constructor(private tokens: Token[]) {}

  private peek(): Token {
    return this.tokens[this.pos] ?? { type: "EOF", value: "", line: 0, col: 0 };
  }

  private match(type: TokenType): boolean {
    if (this.peek().type === type) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expect(type: TokenType): Token {
    const t = this.peek();
    if (t.type !== type) {
      throw new Error(`[ParserError L${t.line}:C${t.col}] Expected token '${type}', got '${t.type}' ('${t.value}')`);
    }
    this.pos++;
    return t;
  }

  parseProgram(): ASTNode[] {
    const stmts: ASTNode[] = [];
    while (this.peek().type !== "EOF") {
      stmts.push(this.parseStatement());
    }
    return stmts;
  }

  private parseStatement(): ASTNode {
    if (this.match("LET")) {
      const name = this.expect("IDENT").value;
      this.expect("ASSIGN");
      const init = this.parseExpression();
      this.match("SEMICOLON");
      return { type: "VarDecl", name, init };
    }

    if (this.match("FN")) {
      const name = this.expect("IDENT").value;
      this.expect("LPAREN");
      const params: string[] = [];
      if (this.peek().type !== "RPAREN") {
        do {
          params.push(this.expect("IDENT").value);
        } while (this.match("COMMA"));
      }
      this.expect("RPAREN");
      this.expect("LBRACE");
      const body: ASTNode[] = [];
      while (!this.match("RBRACE") && this.peek().type !== "EOF") {
        body.push(this.parseStatement());
      }
      return { type: "FnDecl", name, params, body };
    }

    if (this.match("IF")) {
      this.expect("LPAREN");
      const test = this.parseExpression();
      this.expect("RPAREN");
      this.expect("LBRACE");
      const consequent: ASTNode[] = [];
      while (!this.match("RBRACE") && this.peek().type !== "EOF") {
        consequent.push(this.parseStatement());
      }
      let alternate: ASTNode[] | undefined;
      if (this.match("ELSE")) {
        this.expect("LBRACE");
        alternate = [];
        while (!this.match("RBRACE") && this.peek().type !== "EOF") {
          alternate.push(this.parseStatement());
        }
      }
      return { type: "IfStmt", test, consequent, alternate };
    }

    if (this.match("RETURN")) {
      const value = this.parseExpression();
      this.match("SEMICOLON");
      return { type: "Return", value };
    }

    const expr = this.parseExpression();
    this.match("SEMICOLON");
    return expr;
  }

  private parseExpression(): ASTNode {
    return this.parseAdditive();
  }

  private parseAdditive(): ASTNode {
    let left = this.parseMultiplicative();
    while (this.peek().type === "PLUS" || this.peek().type === "MINUS") {
      const op = this.tokens[this.pos++].value;
      const right = this.parseMultiplicative();
      left = { type: "BinOp", op, left, right };
    }
    return left;
  }

  private parseMultiplicative(): ASTNode {
    let left = this.parsePrimary();
    while (this.peek().type === "STAR" || this.peek().type === "SLASH") {
      const op = this.tokens[this.pos++].value;
      const right = this.parsePrimary();
      left = { type: "BinOp", op, left, right };
    }
    return left;
  }

  private parsePrimary(): ASTNode {
    if (this.match("LPAREN")) {
      const expr = this.parseExpression();
      this.expect("RPAREN");
      return expr;
    }

    if (this.peek().type === "NUMBER") {
      const val = Number(this.expect("NUMBER").value);
      return { type: "NumLit", value: val };
    }

    if (this.peek().type === "IDENT") {
      const ident = this.expect("IDENT").value;
      if (this.match("LPAREN")) {
        const args: ASTNode[] = [];
        if (this.peek().type !== "RPAREN") {
          do {
            args.push(this.parseExpression());
          } while (this.match("COMMA"));
        }
        this.expect("RPAREN");
        return { type: "Call", callee: { type: "Var", name: ident }, args };
      }
      return { type: "Var", name: ident };
    }

    throw new Error(`Unexpected primary token: ${this.peek().type}`);
  }
}
