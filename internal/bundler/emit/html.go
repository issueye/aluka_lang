// Package emit —— HTML 入口解析与资源改写（M2-2）。
//
// 支持以 index.html 作为 Web Bundle 的入口，解析引用的 <script src> 与
// <link rel="stylesheet" href>，并在资源打包完成后将引用替换为最终产物文件名。
package emit

import (
	"regexp"
	"strings"
)

// HTMLAsset 是 HTML 中引用的外部静态资源。
type HTMLAsset struct {
	Type     string // "script" 或 "stylesheet"
	Original string // 原始引用路径（如 "./src/main.tsx" 或 "styles/app.css"）
	RawTag   string // 匹配到的完整 HTML 标签
}

// HTMLAssets 包含从 HTML 中提取出的资源。
type HTMLAssets struct {
	Scripts     []HTMLAsset
	Stylesheets []HTMLAsset
}

var (
	// scriptTagRe 匹配 <script ... src="..." ...></script> 或自闭合 <script ... src="..." />
	scriptTagRe = regexp.MustCompile(`(?i)<script\s+([^>]*?)src=["']([^"']+)["']([^>]*)>(?:\s*</script>)?`)
	// linkTagRe 匹配 <link ... href="..." ...>（限制为 stylesheet）
	linkTagRe = regexp.MustCompile(`(?i)<link\s+([^>]*?)href=["']([^"']+)["']([^>]*)/?>`)
)

// ParseHTMLEntry 解析 HTML 文本中的所有脚本与样式表引用。
func ParseHTMLEntry(html string) HTMLAssets {
	var assets HTMLAssets

	// 1. 查找所有 script 标签
	scriptMatches := scriptTagRe.FindAllStringSubmatch(html, -1)
	for _, m := range scriptMatches {
		if len(m) >= 3 {
			rawTag := m[0]
			src := m[2]
			// 过滤非 http/https 的本地文件引用
			if !isRemoteURL(src) {
				assets.Scripts = append(assets.Scripts, HTMLAsset{
					Type:     "script",
					Original: src,
					RawTag:   rawTag,
				})
			}
		}
	}

	// 2. 查找所有 stylesheet link 标签
	linkMatches := linkTagRe.FindAllStringSubmatch(html, -1)
	for _, m := range linkMatches {
		if len(m) >= 3 {
			rawTag := m[0]
			attrs := m[1] + " " + m[3]
			href := m[2]
			if strings.Contains(strings.ToLower(attrs), "stylesheet") && !isRemoteURL(href) {
				assets.Stylesheets = append(assets.Stylesheets, HTMLAsset{
					Type:     "stylesheet",
					Original: href,
					RawTag:   rawTag,
				})
			}
		}
	}

	return assets
}

// RewriteHTML 将 HTML 中的原始资源引用替换为产物路径。
// replacements 为 map[原始路径]产物相对路径。
func RewriteHTML(html string, replacements map[string]string) string {
	result := html

	// 替换 script src
	result = scriptTagRe.ReplaceAllStringFunc(result, func(tag string) string {
		m := scriptTagRe.FindStringSubmatch(tag)
		if len(m) >= 3 {
			orig := m[2]
			if rep, ok := replacements[orig]; ok {
				return strings.Replace(tag, orig, rep, 1)
			}
		}
		return tag
	})

	// 替换 link href
	result = linkTagRe.ReplaceAllStringFunc(result, func(tag string) string {
		m := linkTagRe.FindStringSubmatch(tag)
		if len(m) >= 3 {
			orig := m[2]
			if rep, ok := replacements[orig]; ok {
				return strings.Replace(tag, orig, rep, 1)
			}
		}
		return tag
	})

	return result
}

func isRemoteURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//")
}
