package builtin

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// TestMarkdownRender 验证纯 Go Markdown 渲染基本语法
func TestMarkdownRender(t *testing.T) {
	md := `# Hello World

This is a **bold** and *italic* paragraph with ` + "`inline code`" + ` and a [Link](https://aluka.dev).

## Lists
- Item 1
- Item 2

## Code Block
` + "```typescript\nconst x: number = 42;\nconsole.log(x);\n```" + `

> This is a quote

---
`

	html := Render(md)

	mustContain := []string{
		"<h1>Hello World</h1>",
		"<h2>Lists</h2>",
		"<h2>Code Block</h2>",
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<code>inline code</code>",
		`<a href="https://aluka.dev">Link</a>`,
		"<ul>\n<li>Item 1</li>\n<li>Item 2</li>\n</ul>",
		`<pre><code class="language-typescript">const x: number = 42;`,
		"<blockquote><p>This is a quote</p></blockquote>",
		"<hr />",
	}

	for _, str := range mustContain {
		if !strings.Contains(html, str) {
			t.Errorf("rendered HTML missing %q\nHTML:\n%s", str, html)
		}
	}
}

// TestParseFrontmatter 验证 Frontmatter 元数据提取
func TestParseFrontmatter(t *testing.T) {
	doc := `---
title: "Getting Started"
date: "2026-08-15"
author: 'Aluka Team'
---
# Main Content
Hello from SSG!
`
	data, body := ParseFrontmatter(doc)
	if data["title"] != "Getting Started" {
		t.Errorf("title = %q, want 'Getting Started'", data["title"])
	}
	if data["date"] != "2026-08-15" {
		t.Errorf("date = %q, want '2026-08-15'", data["date"])
	}
	if data["author"] != "Aluka Team" {
		t.Errorf("author = %q, want 'Aluka Team'", data["author"])
	}
	if !strings.Contains(body, "# Main Content") {
		t.Errorf("body missing main content: %q", body)
	}
}

// TestMarkdownJSModule 验证 aluka:markdown 模块在 JS 引擎中的调用
func TestMarkdownJSModule(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	mod, err := NewMarkdownModule(ctx)
	if err != nil {
		t.Fatalf("NewMarkdownModule: %v", err)
	}
	_ = ctx.Global().Set("markdown", mod)

	jsSrc := `
		var md = "# Title\n**Aluka SSG**";
		var res = markdown.render(md);
		var fm = markdown.parseFrontmatter("---\ntitle: Doc\n---\nBody");
		globalThis.res = res;
		globalThis.fmTitle = fm.data.title;
		globalThis.fmBody = fm.content;
	`
	if _, err := ctx.Eval(jsSrc, "test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	resV, _ := ctx.Global().Get("res")
	if !strings.Contains(resV.String(), "<h1>Title</h1>") || !strings.Contains(resV.String(), "<strong>Aluka SSG</strong>") {
		t.Errorf("unexpected res = %q", resV.String())
	}

	fmTitleV, _ := ctx.Global().Get("fmTitle")
	if fmTitleV.String() != "Doc" {
		t.Errorf("unexpected fmTitle = %q", fmTitleV.String())
	}
}
