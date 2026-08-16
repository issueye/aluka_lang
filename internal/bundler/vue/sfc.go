// Package vue 实现 .vue 单文件组件（SFC）到 JS 模块的构建期转换
// （web bundle v1 子集），架构对齐 Vite / @vitejs/plugin-vue：
//
//   - 编译器只产出「import 运行时 helper 的代码」，不内嵌任何运行时：
//     生成的 render 调用 `vue` 包导出的公开 API（h / toDisplayString /
//     unref），`vue` 是用户项目的 node_modules 依赖，经 graph 正常解析；
//     编译器演进只改变所调用的 helper，不与任何内嵌实现锁版本；
//   - <template> 编译为 render(_ctx)，vnode 经 _h(...) 构造——形状由
//     运行时的 h 唯一定义，编译产物不手写数据结构；
//   - <script> 内容原样保留，其 `export default` 对象改写为 const __sfc__
//     并以 `__sfc__.render = render` 挂接后导出（Vite 同款模式）；
//   - <style> / <script setup> 暂不支持，构建期明确报错；
//   - 模板子集：元素嵌套 / 自闭合 / void 元素、静态属性、`:prop` 绑定、
//     `@event` 处理器（标识符引用或含调用的表达式）、`{{ expr }}` 插值；
//   - 表达式中的裸标识符重写为 _ctx.<id>（关键字与内置全局除外）。
package vue

import (
	"fmt"
	"strconv"
	"strings"
)

// runtimeImports 是编译产物依赖的 vue 运行时 helper（Vite 风格下划线别名，
// 避免与用户标识符冲突）。'vue' 说明符由 graph 按 node_modules 正常解析。
const runtimeImports = "import { h as _h, toDisplayString as _toDisplayString, unref as _unref } from 'vue';\n"

// TransformSFC 将 SFC 源码编译为等价 JS 模块源码。name 仅用于错误信息。
func TransformSFC(src, name string) (string, error) {
	if strings.Contains(src, "<script setup") {
		return "", fmt.Errorf("%s: <script setup> is not supported yet", name)
	}
	if _, has := extractBlock(src, "style"); has {
		return "", fmt.Errorf("%s: <style> is not supported yet; move styles into the entry CSS", name)
	}
	tmpl, ok := extractBlock(src, "template")
	if !ok {
		return "", fmt.Errorf("%s: missing <template> block", name)
	}
	script, _ := extractBlock(src, "script")

	render, err := compileTemplate(tmpl)
	if err != nil {
		return "", fmt.Errorf("%s: template: %w", name, err)
	}

	var b strings.Builder
	b.WriteString(runtimeImports)
	if strings.TrimSpace(script) != "" {
		const marker = "export default"
		idx := strings.Index(script, marker)
		if idx < 0 {
			return "", fmt.Errorf("%s: <script> must `export default` the component options", name)
		}
		b.WriteString(script[:idx])
		b.WriteString("const __sfc__ =")
		b.WriteString(script[idx+len(marker):])
		b.WriteString("\n")
	} else {
		// 无 script：模板直接消费 props（setup 透传）。
		b.WriteString("const __sfc__ = { setup: (props) => props };\n")
	}
	b.WriteString(render)
	b.WriteString("\n__sfc__.render = render;\nexport default __sfc__;\n")
	return b.String(), nil
}

// extractBlock 提取首个 <tag ...>...</tag> 块的内部内容。
func extractBlock(src, tag string) (string, bool) {
	open := "<" + tag
	i := strings.Index(src, open)
	if i < 0 {
		return "", false
	}
	j := strings.IndexByte(src[i:], '>')
	if j < 0 {
		return "", false
	}
	start := i + j + 1
	end := strings.Index(src[start:], "</"+tag+">")
	if end < 0 {
		return "", false
	}
	return src[start : start+end], true
}

// ---------- 模板编译 ----------

type attrKind int

const (
	attrStatic attrKind = iota
	attrBind            // :name="expr"
	attrEvent           // @name="expr"
)

type attr struct {
	kind  attrKind
	name  string
	value string
}

type elemNode struct {
	tag      string
	attrs    []attr
	children []any // string（文本）/ *interp / *elemNode
}

type interp struct{ expr string }

type sfcParser struct {
	src []rune
	pos int
}

var voidElements = map[string]bool{
	"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true,
}

func compileTemplate(tmpl string) (string, error) {
	p := &sfcParser{src: []rune(tmpl)}
	kids, err := p.parseNodes("")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("export function render(_ctx){return ")
	writeChildren(&b, kids)
	b.WriteString(";}")
	return b.String(), nil
}

// parseNodes 解析同级子节点序列；closing 非空时遇到 </closing> 停止并消费。
func (p *sfcParser) parseNodes(closing string) ([]any, error) {
	var out []any
	for p.pos < len(p.src) {
		if strings.HasPrefix(p.rest(), "</") {
			name, err := p.readCloseTag()
			if err != nil {
				return nil, err
			}
			if closing == "" {
				return nil, fmt.Errorf("unexpected </%s>", name)
			}
			if name != closing {
				return nil, fmt.Errorf("mismatched closing </%s>, want </%s>", name, closing)
			}
			return out, nil
		}
		if strings.HasPrefix(p.rest(), "<!--") {
			end := strings.Index(p.rest(), "-->")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			p.pos += end + 3
			continue
		}
		if strings.HasPrefix(p.rest(), "<") {
			el, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			if el != nil {
				out = append(out, el)
			}
			continue
		}
		// 文本节点（含 {{ }} 插值），直到下一个 '<'。
		pieces := p.parseText()
		out = append(out, pieces...)
	}
	if closing != "" {
		return nil, fmt.Errorf("missing </%s>", closing)
	}
	return out, nil
}

func (p *sfcParser) rest() string { return string(p.src[p.pos:]) }

func (p *sfcParser) skipWS() {
	for p.pos < len(p.src) && isSpace(p.src[p.pos]) {
		p.pos++
	}
}

// parseText 消费到下一个 '<'，返回文本/插值子节点（空白按 Vue condense 近似：
// 含换行的空白段折叠为空，纯内联空白折叠为单空格）。
func (p *sfcParser) parseText() []any {
	var out []any
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != '<' {
		p.pos++
	}
	raw := string(p.src[start:p.pos])
	for len(raw) > 0 {
		if i := strings.Index(raw, "{{"); i >= 0 {
			if text := condenseText(raw[:i]); text != "" {
				out = append(out, text)
			}
			j := strings.Index(raw[i:], "}}")
			if j < 0 {
				out = append(out, &interp{expr: strings.TrimSpace(raw[i+2:])})
				raw = ""
				break
			}
			expr := strings.TrimSpace(raw[i+2 : i+j])
			out = append(out, &interp{expr: expr})
			raw = raw[i+j+2:]
			continue
		}
		if text := condenseText(raw); text != "" {
			out = append(out, text)
		}
		raw = ""
	}
	return out
}

// condenseText：Vue condense 近似——含换行的空白段整体删除（元素间分隔），
// 纯内联空白折叠为单空格（保留 "x2 = {{ x }}" 中插值旁的间距）。
func condenseText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			hasNL := false
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				if s[i] == '\n' {
					hasNL = true
				}
				i++
			}
			if !hasNL {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// parseElement 解析一个元素；void/自闭合元素返回无子节点。
func (p *sfcParser) parseElement() (*elemNode, error) {
	p.pos++ // consume '<'
	name := p.readIdent()
	if name == "" {
		return nil, fmt.Errorf("expected tag name")
	}
	el := &elemNode{tag: name}
	selfClose := false
	for {
		p.skipWS()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("unterminated tag <%s", name)
		}
		c := p.src[p.pos]
		if c == '>' {
			p.pos++
			break
		}
		if c == '/' {
			p.pos++
			if p.pos < len(p.src) && p.src[p.pos] == '>' {
				p.pos++
				selfClose = true
				break
			}
			return nil, fmt.Errorf("stray '/' in <%s", name)
		}
		attrName := p.readAttrName()
		if attrName == "" {
			return nil, fmt.Errorf("expected attribute in <%s", name)
		}
		a := attr{name: attrName}
		switch {
		case strings.HasPrefix(attrName, ":"):
			a.kind, a.name = attrBind, attrName[1:]
		case strings.HasPrefix(attrName, "@"):
			a.kind, a.name = attrEvent, attrName[1:]
		}
		p.skipWS()
		if p.pos < len(p.src) && p.src[p.pos] == '=' {
			p.pos++
			p.skipWS()
			a.value = p.readAttrValue()
		}
		el.attrs = append(el.attrs, a)
	}
	if selfClose || voidElements[name] {
		return el, nil
	}
	kids, err := p.parseNodes(name)
	if err != nil {
		return nil, err
	}
	el.children = kids
	return el, nil
}

func (p *sfcParser) readCloseTag() (string, error) {
	p.pos += 2 // '</'
	name := p.readIdent()
	p.skipWS()
	if p.pos >= len(p.src) || p.src[p.pos] != '>' {
		return "", fmt.Errorf("malformed </%s", name)
	}
	p.pos++
	return name, nil
}

func (p *sfcParser) readIdent() string {
	start := p.pos
	for p.pos < len(p.src) && (isIdentPart(p.src[p.pos])) {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func (p *sfcParser) readAttrName() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if isSpace(c) || c == '=' || c == '>' || c == '/' {
			break
		}
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func (p *sfcParser) readAttrValue() string {
	if p.pos < len(p.src) && (p.src[p.pos] == '"' || p.src[p.pos] == '\'') {
		quote := p.src[p.pos]
		p.pos++
		start := p.pos
		for p.pos < len(p.src) && p.src[p.pos] != quote {
			p.pos++
		}
		val := string(p.src[start:p.pos])
		if p.pos < len(p.src) {
			p.pos++ // closing quote
		}
		return val
	}
	start := p.pos
	for p.pos < len(p.src) && !isSpace(p.src[p.pos]) && p.src[p.pos] != '>' {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

// ---------- 代码生成 ----------

func writeChildren(b *strings.Builder, kids []any) {
	if len(kids) == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteString("[")
	for i, k := range kids {
		if i > 0 {
			b.WriteString(",")
		}
		switch t := k.(type) {
		case string:
			b.WriteString(strconv.Quote(t))
		case *interp:
			b.WriteString("_toDisplayString(_unref(" + rewriteIdents(t.expr) + "))")
		case *elemNode:
			writeElement(b, t)
		}
	}
	b.WriteString("]")
}

// writeElement 生成 _h(tag, props, children) 调用——vnode 形状由
// 运行时的 h 唯一定义，编译产物不手写数据结构（Vite 同款思路）。
func writeElement(b *strings.Builder, el *elemNode) {
	b.WriteString("_h(")
	b.WriteString(strconv.Quote(el.tag))
	b.WriteString(",{")
	for i, a := range el.attrs {
		if i > 0 {
			b.WriteString(",")
		}
		switch a.kind {
		case attrStatic:
			b.WriteString(strconv.Quote(a.name) + ":" + strconv.Quote(a.value))
		case attrBind:
			b.WriteString(strconv.Quote(a.name) + ":_unref(" + rewriteIdents(a.value) + ")")
		case attrEvent:
			b.WriteString(strconv.Quote(eventProp(a.name)) + ":" + eventHandler(a.value))
		}
	}
	b.WriteString("},")
	writeChildren(b, el.children)
	b.WriteString(")")
}

// eventProp：click → onClick。
func eventProp(name string) string {
	if name == "" {
		return "on"
	}
	return "on" + strings.ToUpper(name[:1]) + name[1:]
}

// eventHandler：纯标识符引用直接取 ctx 成员；含调用/表达式包成箭头。
func eventHandler(value string) string {
	if isSimpleRef(value) {
		return rewriteIdents(value)
	}
	return "($event)=>(" + rewriteIdents(value) + ")"
}

func isSimpleRef(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 0 && r >= '0' && r <= '9') || (i > 0 && r == '.')
		if !ok {
			return false
		}
	}
	return true
}

// rewriteIdents：裸标识符前缀 _ctx.（'.' 后的属性、字符串字面量内容与
// 关键字/内置全局除外）。
func rewriteIdents(expr string) string {
	var jsGlobals = map[string]bool{
		"true": true, "false": true, "null": true, "undefined": true,
		"typeof": true, "new": true, "this": true, "in": true, "of": true,
		"instanceof": true, "delete": true, "void": true,
		"Math": true, "Date": true, "String": true, "Number": true,
		"Boolean": true, "JSON": true, "Object": true, "Array": true,
		"window": true, "document": true, "console": true,
	}
	var b strings.Builder
	rs := []rune(expr)
	for i := 0; i < len(rs); {
		r := rs[i]
		// 字符串字面量原样保留。
		if r == '\'' || r == '"' {
			b.WriteRune(r)
			i++
			for i < len(rs) && rs[i] != r {
				if rs[i] == '\\' && i+1 < len(rs) {
					b.WriteRune(rs[i])
					i++
				}
				b.WriteRune(rs[i])
				i++
			}
			if i < len(rs) {
				b.WriteRune(r)
				i++
			}
			continue
		}
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			j := i
			for j < len(rs) && isIdentPart(rs[j]) {
				j++
			}
			word := string(rs[i:j])
			prevDot := i > 0 && rs[i-1] == '.'
			if prevDot || jsGlobals[word] {
				b.WriteString(word)
			} else {
				b.WriteString("_ctx." + word)
			}
			i = j
			continue
		}
		b.WriteRune(r)
		i++
	}
	return b.String()
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isIdentPart(r rune) bool {
	return r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
