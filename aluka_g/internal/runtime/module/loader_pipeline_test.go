package module

import (
	"strings"
	"testing"
)

// TestRunTSUnsupportedDiagnostic：run 路径（loadModuleFile → ParseSourceUnit）
// 对 .ts/.mts/.cts 中非 declare enum 报 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX，
// 与 build 编译期诊断一致（P1-2/P3-4 对拍）。
func TestRunTSUnsupportedDiagnostic(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"bad.ts":   "enum E { A, B }\n",
		"bad.mts":  "enum E { A, B }\n",
		"bad.cts":  "enum E { A, B }\n",
		"good.ts":  "declare enum E { A, B }\nconst x = 1;\nglobalThis.__ok = x;\n",
		"plain.ts": "const x: number = 42;\nglobalThis.__ok = x;\n",
	})

	for _, name := range []string{"bad.ts", "bad.mts", "bad.cts"} {
		if err := env.loader.Run(env.dir + "/" + name); err == nil {
			t.Errorf("Run(%s) = nil error, want TS diagnostic", name)
		} else if !strings.Contains(err.Error(), "ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX") {
			t.Errorf("Run(%s) error = %v, want ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX", name, err)
		}
	}

	// 环境声明与合法 TS 正常执行。
	env.run(t, "good.ts")
	if got := env.globalGet("__ok"); got != "1" {
		t.Errorf("good.ts __ok = %q, want 1", got)
	}
	env.run(t, "plain.ts")
	if got := env.globalGet("__ok"); got != "42" {
		t.Errorf("plain.ts __ok = %q, want 42", got)
	}
}

// TestRunCJSWithESMSyntaxDiagnostic：显式 .cjs/.cts 含 ESM 语法给出明确诊断，
// 不静默回退 ESM（P3-2）。
func TestRunCJSWithESMSyntaxDiagnostic(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"bad.cjs": "export const x = 1;\n",
		"bad.cts": "export const x = 1;\n",
	})
	for _, name := range []string{"bad.cjs", "bad.cts"} {
		if err := env.loader.Run(env.dir + "/" + name); err == nil {
			t.Errorf("Run(%s) = nil error, want CJS/ESM diagnostic", name)
		} else if !strings.Contains(err.Error(), "commonjs") {
			t.Errorf("Run(%s) error = %v, want commonjs diagnostic", name, err)
		}
	}
}
