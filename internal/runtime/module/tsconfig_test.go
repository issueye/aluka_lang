package module

import "testing"

// 本文件覆盖 tsconfig.json 路径别名解析（1C.12 tsconfig 读取 + 1C.13 paths/baseUrl）。
// 风格对齐 module_test.go：newTestEnv 写临时文件 → loader.Run → globalGet 验证。
//
// 覆盖场景：
//  1. paths 通配符别名（@/* → src/*）
//  2. 多别名 + 精确匹配（无通配符）
//  3. baseUrl 单独作用（无 paths 时 bare specifier 相对 baseUrl）
//  4. tsconfig 向上查找（子目录模块也能用根 tsconfig）
//  5. jsconfig.json 回退
//  6. jsonc 注释容错
//  7. TS 扩展名（.ts）解析
//  8. 回退到 node_modules（别名未匹配时）

// TestTsconfigPathsWildcard: @/* → src/* 通配符别名。
func TestTsconfigPathsWildcard(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": {
				"baseUrl": ".",
				"paths": { "@/*": ["src/*"] }
			}
		}`,
		"src/main.ts":   `import { greet } from "@/utils"; globalThis.__r = greet("Aluka");`,
		"src/utils.ts":  `export function greet(name: string): string { return "Hi, " + name; }`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "Hi, Aluka" {
		t.Errorf("paths wildcard: got %q, want Hi, Aluka", got)
	}
}

// TestTsconfigPathsMultiple: 多别名，含精确匹配与通配符。
func TestTsconfigPathsMultiple(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": {
				"baseUrl": ".",
				"paths": {
					"@config": ["src/config.ts"],
					"@lib/*": ["src/lib/*"]
				}
			}
		}`,
		"src/main.ts": `import { cfg } from "@config";
import { helper } from "@lib/helper";
globalThis.__r = cfg + ":" + helper();`,
		"src/config.ts":    `export const cfg: string = "prod";`,
		"src/lib/helper.ts": `export function helper(): string { return "ok"; }`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "prod:ok" {
		t.Errorf("paths multiple: got %q, want prod:ok", got)
	}
}

// TestTsconfigBaseURLOnly: 仅设 baseUrl 无 paths 时，bare specifier 相对 baseUrl 解析。
func TestTsconfigBaseURLOnly(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": { "baseUrl": "src" }
		}`,
		"src/main.ts":  `import { val } from "mod"; globalThis.__r = val;`,
		"src/mod.ts":   `export const val = 42;`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("baseUrl only: got %q, want 42", got)
	}
}

// TestTsconfigNestedDirLookup: 子目录模块也能命中根 tsconfig（向上查找）。
func TestTsconfigNestedDirLookup(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } }
		}`,
		"src/main.ts":      `import { dep } from "@/deep/inner"; globalThis.__r = dep;`,
		"src/deep/inner.ts": `import { val } from "@/shared"; export const dep = val;`,
		"src/shared.ts":     `export const val = "deep-ok";`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "deep-ok" {
		t.Errorf("nested dir lookup: got %q, want deep-ok", got)
	}
}

// TestJsconfigFallback: 无 tsconfig.json 时回退到 jsconfig.json。
func TestJsconfigFallback(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"jsconfig.json": `{
			"compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } }
		}`,
		"src/main.mjs": `import { x } from "@/mod"; globalThis.__r = x;`,
		"src/mod.mjs":  `export const x = "jsconfig-works";`,
	})
	env.run(t, "src/main.mjs")
	if got := env.globalGet("__r"); got != "jsconfig-works" {
		t.Errorf("jsconfig fallback: got %q, want jsconfig-works", got)
	}
}

// TestTsconfigJSONCComments: tsconfig.json 含 // 与 /* */ 注释仍可解析。
func TestTsconfigJSONCComments(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			// 这是行注释
			"compilerOptions": {
				"baseUrl": ".", /* 块注释 */
				"paths": { "@/*": ["src/*"] }
			}
		}`,
		"src/main.ts":  `import { v } from "@/mod"; globalThis.__r = v;`,
		"src/mod.ts":   `export const v = "jsonc";`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "jsonc" {
		t.Errorf("jsonc comments: got %q, want jsonc", got)
	}
}

// TestTsconfigFallbackToNodeModules: 别名未匹配时回退到 node_modules 查找。
func TestTsconfigFallbackToNodeModules(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } }
		}`,
		"src/main.ts": `import { v } from "pkg"; globalThis.__r = v;`,
		"node_modules/pkg/index.mjs": `export const v = "from-npm";`,
	})
	env.run(t, "src/main.ts")
	if got := env.globalGet("__r"); got != "from-npm" {
		t.Errorf("fallback to node_modules: got %q, want from-npm", got)
	}
}

// TestTsconfigResolutionViaRequire: CJS 模块的 require 也应走 tsconfig paths。
func TestTsconfigResolutionViaRequire(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"tsconfig.json": `{
			"compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } }
		}`,
		"src/main.cjs": `var mod = require("@/mod"); globalThis.__r = mod.val;`,
		"src/mod.js":   `module.exports.val = "require-path";`,
	})
	env.run(t, "src/main.cjs")
	if got := env.globalGet("__r"); got != "require-path" {
		t.Errorf("require with paths: got %q, want require-path", got)
	}
}
