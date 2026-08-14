// Main Entry Point: TypeScript ESM orchestrating CJS, JS, TS, MJS, and JSON
import config from "./config.json" with { type: "json" };
import legacyMath from "./legacy_math.cjs";
import DataFormatter, { transformData, JS_MODULE_TAG } from "./data_helper.js";
import { CryptoService } from "./crypto_service.ts";
import { add as bridgedAdd, multiply as bridgedMul, Vector2D, KeyVault, bridgeVersion, bridgeFormats } from "./reexport_bridge.mjs";

console.log("=== [Project 4] Multi-Type Hybrid (TS + JS + CJS + MJS + JSON) ===");

// 1. 验证 JSON 资源导入
console.log(`[JSON Resource] App: ${config.appName} v${config.version}, Env: ${config.environment}`);
console.log(`[JSON Resource] Supported modes: [${config.supportedModes.join(", ")}]`);

if (config.appName !== "HybridMatrixEngine" || config.version !== "2.4.0") {
  throw new Error("JSON import data mismatch!");
}

// 2. 验证 CommonJS 导入与方法调用
const cjsSum = legacyMath.add(100, 250);
const cjsProduct = legacyMath.multiply(6, 7);
const vec = new legacyMath.Vector2D(3, 4);
console.log(`[CommonJS] add(100, 250) = ${cjsSum}, multiply(6, 7) = ${cjsProduct}`);
console.log(`[CommonJS] Vector2D(3, 4) magnitude = ${vec.magnitude()}, format info = ${legacyMath.info.format}`);

if (cjsSum !== 350 || cjsProduct !== 42 || vec.magnitude() !== 5) {
  throw new Error("CommonJS function execution failed!");
}

// 3. 验证标准 JS ESM 模块与 Default Export
const formatter = new DataFormatter("METRIC");
const rawItems = [10, 20, 30, 40, 50];
const processed = transformData(rawItems, (x: number) => x * 2);
console.log(`[JS ESM] Tag: ${JS_MODULE_TAG}, Processed count: ${processed.length}`);
console.log(`[JS ESM] Sample formatted: ${formatter.format(processed[0])}`);

if (processed.length !== 5 || processed[0].processed !== 20) {
  throw new Error("JS ESM transformation failed!");
}

// 4. 验证 Strict TypeScript 泛型服务
const vault = new CryptoService<number>("matrix_seed", 5);
const hashRes = await vault.hash(123456);
console.log(`[TypeScript] Hashed 123456 -> ${hashRes.digest} (${hashRes.algorithm}, ${hashRes.rounds} rounds)`);
const isValid = await vault.validate(123456, hashRes.digest);
console.log(`[TypeScript] Hash validation = ${isValid}`);

if (!isValid) {
  throw new Error("TypeScript crypto service validation failed!");
}

// 5. 验证 MJS Re-export Bridge (跨模块聚合)
console.log(`[MJS Bridge] Bridge v${bridgeVersion}, Formats: [${bridgeFormats.join(", ")}]`);
const bAdd = bridgedAdd(50, 50);
const bMul = bridgedMul(9, 9);
const vA = new Vector2D(1, 2);
const vB = new Vector2D(3, 4);
const vC = vA.add(vB);
console.log(`[MJS Bridge] Re-exported CJS math: add=${bAdd}, mul=${bMul}, vecAdd=(${vC.x}, ${vC.y})`);

const kv = new KeyVault("bridge_seed", 2);
const kvHash = await kv.hash("hello_aluka");
console.log(`[MJS Bridge] Re-exported TS class KeyVault: digest=${kvHash.digest}`);

if (bAdd !== 100 || bMul !== 81 || vC.x !== 4 || vC.y !== 6) {
  throw new Error("MJS re-export bridge math execution failed!");
}

// 6. 验证动态 Import (TS + CJS + JSON)
const dynamicCJS = await import("./legacy_math.cjs");
const dynamicSum = dynamicCJS.default?.add ? dynamicCJS.default.add(11, 22) : (dynamicCJS as any).add(11, 22);
console.log(`[Dynamic Import] Dynamically loaded legacy_math.cjs: add(11, 22) = ${dynamicSum}`);

console.log(`[import.meta] url: ${import.meta.url}, main: ${import.meta.main}`);
console.log("=== [Project 4] Multi-Type Hybrid Completed Successfully! ===");
