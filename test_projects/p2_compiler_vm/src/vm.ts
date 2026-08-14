import { Opcode, type ASTNode } from "./types.ts";

export interface CompiledFunction {
  name: string;
  params: string[];
  code: Uint8Array;
  constants: number[];
}

export class Compiler {
  private constants: number[] = [];
  private bytecode: number[] = [];
  private varSlots = new Map<string, number>();
  private functions = new Map<string, CompiledFunction>();

  private addConstant(val: number): number {
    const idx = this.constants.length;
    this.constants.push(val);
    return idx;
  }

  private getSlot(name: string): number {
    let slot = this.varSlots.get(name);
    if (slot === undefined) {
      slot = this.varSlots.size;
      this.varSlots.set(name, slot);
    }
    return slot;
  }

  private emit(op: Opcode, ...operands: number[]) {
    this.bytecode.push(op, ...operands);
  }

  compile(ast: ASTNode[]): { main: Uint8Array; constants: number[]; functions: Map<string, CompiledFunction> } {
    for (const node of ast) {
      this.compileNode(node);
    }
    this.emit(Opcode.OP_HALT);
    return {
      main: new Uint8Array(this.bytecode),
      constants: [...this.constants],
      functions: this.functions,
    };
  }

  private compileNode(node: ASTNode) {
    switch (node.type) {
      case "NumLit": {
        const idx = this.addConstant(node.value);
        this.emit(Opcode.OP_CONST, idx);
        break;
      }
      case "BinOp": {
        this.compileNode(node.left);
        this.compileNode(node.right);
        switch (node.op) {
          case "+":
            this.emit(Opcode.OP_ADD);
            break;
          case "-":
            this.emit(Opcode.OP_SUB);
            break;
          case "*":
            this.emit(Opcode.OP_MUL);
            break;
          case "/":
            this.emit(Opcode.OP_DIV);
            break;
        }
        break;
      }
      case "VarDecl": {
        this.compileNode(node.init);
        const slot = this.getSlot(node.name);
        this.emit(Opcode.OP_STORE, slot);
        break;
      }
      case "Var": {
        const slot = this.getSlot(node.name);
        this.emit(Opcode.OP_LOAD, slot);
        break;
      }
      case "Return": {
        this.compileNode(node.value);
        this.emit(Opcode.OP_RET);
        break;
      }
    }
  }
}

export class StackVM {
  private stack: number[] = [];
  private memory = new Map<number, number>();

  constructor(private constants: number[]) {}

  run(code: Uint8Array): number {
    let ip = 0;
    while (ip < code.length) {
      const op = code[ip++];
      switch (op) {
        case Opcode.OP_CONST: {
          const constIdx = code[ip++];
          this.stack.push(this.constants[constIdx]);
          break;
        }
        case Opcode.OP_ADD: {
          const b = this.stack.pop()!;
          const a = this.stack.pop()!;
          this.stack.push(a + b);
          break;
        }
        case Opcode.OP_SUB: {
          const b = this.stack.pop()!;
          const a = this.stack.pop()!;
          this.stack.push(a - b);
          break;
        }
        case Opcode.OP_MUL: {
          const b = this.stack.pop()!;
          const a = this.stack.pop()!;
          this.stack.push(a * b);
          break;
        }
        case Opcode.OP_DIV: {
          const b = this.stack.pop()!;
          const a = this.stack.pop()!;
          this.stack.push(Math.floor(a / b));
          break;
        }
        case Opcode.OP_STORE: {
          const slot = code[ip++];
          const val = this.stack.pop()!;
          this.memory.set(slot, val);
          break;
        }
        case Opcode.OP_LOAD: {
          const slot = code[ip++];
          const val = this.memory.get(slot) ?? 0;
          this.stack.push(val);
          break;
        }
        case Opcode.OP_HALT:
        case Opcode.OP_RET:
          return this.stack.length > 0 ? this.stack[this.stack.length - 1] : 0;
        default:
          throw new Error(`Unknown VM opcode: 0x${op.toString(16)} at ip=${ip - 1}`);
      }
    }
    return this.stack.pop() ?? 0;
  }
}
