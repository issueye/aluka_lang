// Package emit —— CSS 模块打包与压缩（M2-1）。
//
// 支持 CSS 文件的拓扑拼接、依赖去重与轻量 Minify（去注释、压缩空白、精简符号间隔）。
package emit

import (
	"regexp"
	"strings"
)

// CSSFile 代表一个 CSS 文件输入。
type CSSFile struct {
	ID      string // 模块虚拟路径（如 "src/style.css"）
	Content string // CSS 文本内容
}

var (
	// cssCommentRe 匹配 CSS 多行注释 /* ... */
	cssCommentRe = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	// cssSpaceRe 匹配连续的空白字符
	cssSpaceRe = regexp.MustCompile(`\s+`)
	// cssSymbolsRe 匹配符号周围的空格
	cssSymbolsColonRe     = regexp.MustCompile(`\s*:\s*`)
	cssSymbolsSemiRe      = regexp.MustCompile(`\s*;\s*`)
	cssSymbolsBraceOpenRe = regexp.MustCompile(`\s*\{\s*`)
	cssSymbolsBraceCloseRe = regexp.MustCompile(`\s*\}\s*`)
	cssSymbolsCommaRe     = regexp.MustCompile(`\s*,\s*`)
	cssTrailingSemiRe     = regexp.MustCompile(`;\}`)
)

// MinifyCSS 对 CSS 源码进行纯 Go 极速压缩。
func MinifyCSS(css string) string {
	// 1. 去除注释
	s := cssCommentRe.ReplaceAllString(css, "")

	// 2. 将连续空白压缩为单空格
	s = cssSpaceRe.ReplaceAllString(s, " ")

	// 3. 去除符号周围的多余空格
	s = cssSymbolsColonRe.ReplaceAllString(s, ":")
	s = cssSymbolsSemiRe.ReplaceAllString(s, ";")
	s = cssSymbolsBraceOpenRe.ReplaceAllString(s, "{")
	s = cssSymbolsBraceCloseRe.ReplaceAllString(s, "}")
	s = cssSymbolsCommaRe.ReplaceAllString(s, ",")

	// 4. 去除规则块末尾冗余分号 ;} -> }
	s = cssTrailingSemiRe.ReplaceAllString(s, "}")

	return strings.TrimSpace(s)
}

// BundleCSS 拼接多个 CSS 模块，按输入顺序去重并可选执行压缩。
func BundleCSS(files []CSSFile, minify bool) (string, error) {
	seen := make(map[string]bool, len(files))
	var sb strings.Builder

	for _, f := range files {
		if seen[f.ID] {
			continue
		}
		seen[f.ID] = true

		content := f.Content
		if minify {
			content = MinifyCSS(content)
		} else {
			content = strings.TrimSpace(content)
		}

		if content == "" {
			continue
		}

		if sb.Len() > 0 {
			if minify {
				sb.WriteString("")
			} else {
				sb.WriteString("\n\n")
			}
		}
		sb.WriteString(content)
	}

	return sb.String(), nil
}
