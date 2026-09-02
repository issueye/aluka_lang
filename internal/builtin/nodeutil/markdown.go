// Package builtin —— 内置 Markdown 解析与 HTML 渲染模块（aluka:markdown，M4-2）。
//
// 纯 Go 自研实现，提供 Markdown → HTML 渲染与 Frontmatter 元数据提取，
// 赋能零外部依赖的 SSG 静态站点与文档生成管线。
package nodeutil

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// MarkdownRenderer 提供纯 Go 的 Markdown 转 HTML 能力。
type MarkdownRenderer struct{}

var (
	// frontmatterRe 匹配顶部的 --- ... --- 元数据
	frontmatterRe = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n(.*)$`)
	// inlineLinkRe 匹配 [text](url)
	inlineLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// inlineImgRe 匹配 ![alt](url)
	inlineImgRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	// inlineCodeRe 匹配 `code`
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	// inlineBoldRe 匹配 **bold** 或 __bold__
	inlineBoldRe = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	// inlineItalicRe 匹配 *italic* 或 _italic_
	inlineItalicRe = regexp.MustCompile(`\*([^*]+)\*|_([^_]+)_`)
	// inlineDelRe 匹配 ~~strikethrough~~
	inlineDelRe = regexp.MustCompile(`~~([^~]+)~~`)
)

// ParseFrontmatter 解析 Markdown 文件头部的 YAML/键值元数据。
func ParseFrontmatter(content string) (data map[string]string, body string) {
	data = make(map[string]string)
	m := frontmatterRe.FindStringSubmatch(content)
	if len(m) < 3 {
		return data, content
	}

	rawYaml := m[1]
	body = m[2]

	lines := strings.Split(rawYaml, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if colon := strings.Index(line, ":"); colon > 0 {
			k := strings.TrimSpace(line[:colon])
			v := strings.TrimSpace(line[colon+1:])
			v = strings.Trim(v, `"'`)
			data[k] = v
		}
	}
	return data, body
}

// RenderInline 渲染行内元素（链接、图片、行内代码、粗体、斜体、删除线）。
func RenderInline(text string) string {
	// 1. 图片 ![alt](url)
	text = inlineImgRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineImgRe.FindStringSubmatch(m)
		if len(parts) >= 3 {
			alt := html.EscapeString(parts[1])
			src := html.EscapeString(parts[2])
			return fmt.Sprintf(`<img src="%s" alt="%s" />`, src, alt)
		}
		return m
	})

	// 2. 链接 [text](url)
	text = inlineLinkRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineLinkRe.FindStringSubmatch(m)
		if len(parts) >= 3 {
			label := parts[1]
			href := html.EscapeString(parts[2])
			return fmt.Sprintf(`<a href="%s">%s</a>`, href, label)
		}
		return m
	})

	// 3. 行内代码 `code`
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineCodeRe.FindStringSubmatch(m)
		if len(parts) >= 2 {
			code := html.EscapeString(parts[1])
			return fmt.Sprintf(`<code>%s</code>`, code)
		}
		return m
	})

	// 4. 粗体 **bold**
	text = inlineBoldRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineBoldRe.FindStringSubmatch(m)
		val := parts[1]
		if val == "" {
			val = parts[2]
		}
		return fmt.Sprintf(`<strong>%s</strong>`, val)
	})

	// 5. 斜体 *italic*
	text = inlineItalicRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineItalicRe.FindStringSubmatch(m)
		val := parts[1]
		if val == "" {
			val = parts[2]
		}
		return fmt.Sprintf(`<em>%s</em>`, val)
	})

	// 6. 删除线 ~~text~~
	text = inlineDelRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineDelRe.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return fmt.Sprintf(`<del>%s</del>`, parts[1])
		}
		return m
	})

	return text
}

// Render 将 Markdown 文本渲染为 HTML 片段。
func Render(md string) string {
	lines := strings.Split(md, "\n")
	var sb strings.Builder

	inCodeBlock := false
	codeLang := ""
	var codeLines []string

	inList := false
	listType := "" // "ul" or "ol"

	inBlockquote := false
	var bqLines []string

	flushList := func() {
		if inList {
			sb.WriteString("</" + listType + ">\n")
			inList = false
			listType = ""
		}
	}

	flushBlockquote := func() {
		if inBlockquote {
			sb.WriteString("<blockquote><p>")
			sb.WriteString(RenderInline(strings.Join(bqLines, " ")))
			sb.WriteString("</p></blockquote>\n")
			inBlockquote = false
			bqLines = nil
		}
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)

		// 代码块处理 ```
		if strings.HasPrefix(trimmed, "```") {
			flushList()
			flushBlockquote()
			if inCodeBlock {
				sb.WriteString("<pre><code")
				if codeLang != "" {
					sb.WriteString(fmt.Sprintf(` class="language-%s"`, html.EscapeString(codeLang)))
				}
				sb.WriteString(">")
				sb.WriteString(html.EscapeString(strings.Join(codeLines, "\n")))
				sb.WriteString("</code></pre>\n")
				inCodeBlock = false
				codeLines = nil
				codeLang = ""
			} else {
				inCodeBlock = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				codeLines = nil
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		// 空行
		if trimmed == "" {
			flushList()
			flushBlockquote()
			continue
		}

		// 水平线 ---, ***, ___
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			flushList()
			flushBlockquote()
			sb.WriteString("<hr />\n")
			continue
		}

		// 引用 >
		if strings.HasPrefix(trimmed, ">") {
			flushList()
			inBlockquote = true
			bqContent := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			bqLines = append(bqLines, bqContent)
			continue
		} else {
			flushBlockquote()
		}

		// 标题 # ~ ######
		if strings.HasPrefix(trimmed, "#") {
			flushList()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
				headerText := strings.TrimSpace(trimmed[level:])
				sb.WriteString(fmt.Sprintf("<h%d>%s</h%d>\n", level, RenderInline(headerText), level))
				continue
			}
		}

		// 无序列表 - / * / +
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			if !inList || listType != "ul" {
				flushList()
				inList = true
				listType = "ul"
				sb.WriteString("<ul>\n")
			}
			itemText := strings.TrimSpace(trimmed[2:])
			sb.WriteString(fmt.Sprintf("<li>%s</li>\n", RenderInline(itemText)))
			continue
		}

		// 有序列表 1. / 2.
		if isOrderedList(trimmed) {
			if !inList || listType != "ol" {
				flushList()
				inList = true
				listType = "ol"
				sb.WriteString("<ol>\n")
			}
			dot := strings.Index(trimmed, ".")
			itemText := strings.TrimSpace(trimmed[dot+1:])
			sb.WriteString(fmt.Sprintf("<li>%s</li>\n", RenderInline(itemText)))
			continue
		}

		flushList()

		// 普通段落
		sb.WriteString(fmt.Sprintf("<p>%s</p>\n", RenderInline(trimmed)))
	}

	flushList()
	flushBlockquote()
	if inCodeBlock {
		sb.WriteString("<pre><code>")
		sb.WriteString(html.EscapeString(strings.Join(codeLines, "\n")))
		sb.WriteString("</code></pre>\n")
	}

	return strings.TrimSpace(sb.String())
}

// RenderFullDocument 将 Markdown 渲染为包含完整 HTML 骨架的文档。
func RenderFullDocument(md, title string) string {
	body := Render(md)
	if title == "" {
		title = "Aluka Static Document"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
</head>
<body>
    <main class="markdown-body">
%s
    </main>
</body>
</html>`, html.EscapeString(title), body)
}

func isOrderedList(s string) bool {
	dot := strings.Index(s, ". ")
	if dot <= 0 {
		return false
	}
	for i := 0; i < dot; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NewMarkdownModule 创建暴露给 JS 引擎的 aluka:markdown 模块对象。
func NewMarkdownModule(ctx engine.Context) (engine.Value, error) {
	obj := engine.NewObject()

	// render(md: string): string
	_ = obj.Set("render", engine.NewFunction("render", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		htmlStr := Render(args[0].String())
		return engine.Str(htmlStr), nil
	}))

	// renderToHTML(md: string, options?: { title?: string }): string
	_ = obj.Set("renderToHTML", engine.NewFunction("renderToHTML", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		title := "Aluka Site"
		if len(args) > 1 {
			if optObj, ok := args[1].AsObject(); ok {
				if tVal, err := optObj.Get("title"); err == nil && !tVal.IsUndefined() {
					title = tVal.String()
				}
			}
		}
		htmlDoc := RenderFullDocument(args[0].String(), title)
		return engine.Str(htmlDoc), nil
	}))

	// parseFrontmatter(content: string): { data: Record<string, string>, content: string }
	_ = obj.Set("parseFrontmatter", engine.NewFunction("parseFrontmatter", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			res := engine.NewObject()
			_ = res.Set("data", engine.NewObject())
			_ = res.Set("content", engine.Str(""))
			return res, nil
		}
		data, body := ParseFrontmatter(args[0].String())
		dataObj := engine.NewObject()
		for k, v := range data {
			_ = dataObj.Set(k, engine.Str(v))
		}
		res := engine.NewObject()
		_ = res.Set("data", dataObj)
		_ = res.Set("content", engine.Str(body))
		return res, nil
	}))

	return obj, nil
}
