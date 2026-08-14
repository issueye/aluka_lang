import { tokenize } from "./lexer.ts";
import { Parser } from "./parser.ts";
import { Compiler, StackVM } from "./vm.ts";

console.log("=== [Project 2] Custom Mini-Language Compiler & Stack VM ===");

const sourceCode = `
// 定义变量与算术计算
let a = 15 + 25 * 2; // a = 65
let b = (a - 5) / 2; // b = 30
let c = a + b * 3;   // c = 65 + 90 = 155
return c;
`;

console.log(`[Source Code]:\n${sourceCode.trim()}\n`);

// 1. 词法分析
const tokens = tokenize(sourceCode);
console.log(`[Lexer] Generated ${tokens.length} tokens.`);

// 2. 语法分析生成 AST
const parser = new Parser(tokens);
const ast = parser.parseProgram();
console.log(`[Parser] Generated AST with ${ast.length} top-level nodes:`);
ast.forEach((node, i) => console.log(`  [${i}] ${node.type}`));

// 3. 字节码编译
const compiler = new Compiler();
const compiled = compiler.compile(ast);
console.log(`[Compiler] Bytecode size: ${compiled.main.length} bytes, Constants: [${compiled.constants.join(", ")}]`);

// 4. 虚拟机执行
const vm = new StackVM(compiled.constants);
const result = vm.run(compiled.main);
console.log(`[VM Execution] Result = ${result}`);

if (result !== 155) {
  throw new Error(`Execution failed: expected 155, got ${result}`);
}

console.log("=== [Project 2] Completed Successfully! ===");
