// Explicit ESM Module (.mjs) bridging CJS, JS, and TS
export { add, multiply, Vector2D } from "./legacy_math.cjs";
export { CryptoService as KeyVault } from "./crypto_service.ts";
export { transformData, aggregate } from "./data_helper.js";

export const bridgeVersion = "1.0.0";
export const bridgeFormats = ["cjs", "esm-js", "ts", "mjs"];
