package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParse 验证 .npmrc 内容解析。
func TestParse(t *testing.T) {
	cfg := &Config{AuthTokens: map[string]string{}}
	data := `; 这是注释
# 这也是注释
registry=https://registry.npmmirror.com
//registry.npmjs.org/:_authToken=secret-token
_authToken=fallback
always-auth=true
`
	for _, line := range splitLines(data) {
		cfg.parseLine(line)
	}
	if cfg.Registry != "https://registry.npmmirror.com" {
		t.Errorf("Registry = %q", cfg.Registry)
	}
	if got := cfg.TokenFor("https://registry.npmjs.org"); got != "secret-token" {
		t.Errorf("TokenFor npmjs = %q, want secret-token", got)
	}
	if got := cfg.TokenFor("https://other.example.com"); got != "fallback" {
		t.Errorf("TokenFor other = %q, want fallback", got)
	}
}

// TestLoad 验证项目 .npmrc 覆盖用户级配置。
func TestLoad(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	os.Setenv("HOME", home) // 影响 os.UserHomeDir（Windows 下用 USERPROFILE）
	os.Setenv("USERPROFILE", home)
	t.Cleanup(func() {
		os.Unsetenv("HOME")
		os.Unsetenv("USERPROFILE")
	})
	writeFile(t, filepath.Join(home, ".npmrc"), "registry=https://user.example.com\n_authToken=user-token\n")
	writeFile(t, filepath.Join(root, ".npmrc"), "registry=https://project.example.com\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "https://project.example.com" {
		t.Errorf("Registry = %q, want project.example.com (project wins)", cfg.Registry)
	}
	if got := cfg.TokenFor("https://project.example.com"); got != "user-token" {
		t.Errorf("TokenFor = %q, want user-token (inherited from user config)", got)
	}
}

// TestTokenForHostVariants 验证主机级 token 的各种 URL 形态。
func TestTokenForHostVariants(t *testing.T) {
	cfg := &Config{AuthTokens: map[string]string{}}
	cfg.parseLine("//registry.npmjs.org/:_authToken=abc")
	cfg.parseLine("//npm.pkg.github.com:_authToken=def")

	cases := map[string]string{
		"https://registry.npmjs.org":  "abc",
		"https://registry.npmjs.org/": "abc",
		"http://registry.npmjs.org":   "abc",
		"https://npm.pkg.github.com":  "def",
		"https://unknown.example.com": "",
	}
	for reg, want := range cases {
		if got := cfg.TokenFor(reg); got != want {
			t.Errorf("TokenFor(%q) = %q, want %q", reg, got, want)
		}
	}
}

// TestNoFile 验证文件不存在不报错。
func TestNoFile(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "" {
		t.Errorf("Registry = %q, want empty", cfg.Registry)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
