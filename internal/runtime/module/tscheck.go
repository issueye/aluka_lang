package module

// TypeScript strip-only 模式诊断（M7-6）。
//
// Node 22 的 type stripping 仅支持类型注解擦除；非 declare 的 enum 与
// namespace 声明抛 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX。这里在模块加载时对
// .ts/.mts/.cts 文件做 token 级检测，命中即报与 Node 一致的诊断。
// declare enum / declare namespace 属于环境声明（无运行时语义），允许。

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

// tsDeclMarker 是 strip-only 不支持的类型声明关键字。
var tsDeclMarkers = map[string]string{
	"enum":      "TypeScript enum is not supported in strip-only mode",
	"namespace": "TypeScript namespace declaration is not supported in strip-only mode",
	"module":    "TypeScript namespace declaration is not supported in strip-only mode",
}

// checkUnsupportedTS 扫描 TS 源码，检测不支持的 enum/namespace 声明。
// 返回 nil 表示通过；否则返回带行号的 SyntaxError 语义错误。
func checkUnsupportedTS(src, path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".ts" && ext != ".mts" && ext != ".cts" {
		return nil
	}
	lx := lexer.New(src)
	var toks []lexer.Token
	for {
		tok, err := lx.Next()
		if err != nil {
			return nil // 词法错误交给解析器报
		}
		if tok.Type == lexer.TokenEOF {
			break
		}
		toks = append(toks, tok)
	}
	for i := 0; i < len(toks); i++ {
		v := toks[i].Value
		msg, isMarker := tsDeclMarkers[v]
		if !isMarker {
			continue
		}
		// declare 前缀保护（支持的环境声明）：declare enum / declare namespace。
		if i > 0 && toks[i-1].Value == "declare" {
			continue
		}
		// 声明位置判定：前一个有效 token（跳过 const/export/abstract）必须是
		// 语句边界（文件开头、; { }），且后随 标识符 + "{"（声明形态）。
		prev := i - 1
		for prev >= 0 && (toks[prev].Value == "const" || toks[prev].Value == "export" || toks[prev].Value == "abstract") {
			prev--
		}
		atBoundary := prev < 0
		if prev >= 0 {
			switch toks[prev].Value {
			case ";", "{", "}", ",", ":":
				atBoundary = true
			}
		}
		if !atBoundary {
			continue
		}
		// 后随 标识符 + "{"：声明形态（排除对象字面量键 `{ enum: ... }`）。
		if i+2 < len(toks) && toks[i+1].Type != lexer.TokenEOF && toks[i+2].Value == "{" {
			return fmt.Errorf("SyntaxError [ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX]: %s (line %d)", msg, toks[i].Line)
		}
	}
	return nil
}
