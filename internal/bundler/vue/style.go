package vue

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func rejectAdvancedScoped(name, css string) error {
	needles := []string{":deep", ":slotted", ":global", "v-bind("}
	lower := strings.ToLower(css)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return fmt.Errorf("%s: scoped CSS %s is not supported yet", name, n)
		}
	}
	return nil
}

// attachStyleModules 把 <style> 编成虚拟 CSS 模块。scoped 用纯 Go 选择器后缀
// （Vite 默认属性选择器），不走 compiler-sfc compileStyle：Aluka VM 里
// postcss-selector-parser 的 toString 会把选择器打成对象文本。
func attachStyleModules(name string, styles []sfcBlock, externals *sfcExternals, id string) ([]GeneratedModule, string, error) {
	if externals == nil {
		externals = &sfcExternals{}
	}
	base := filepath.Base(filepath.FromSlash(name))
	var mods []GeneratedModule
	var b strings.Builder
	extStyle := 0
	for i, st := range styles {
		css := st.Content
		if st.attr("src") != "" {
			if extStyle >= len(externals.Styles) {
				return nil, "", fmt.Errorf("%s: missing src content for <style>", name)
			}
			css = externals.Styles[extStyle].Content
			extStyle++
		}
		if st.has("scoped") {
			if err := rejectAdvancedScoped(name, css); err != nil {
				return nil, "", err
			}
			css = scopeCSS(css, id)
		}
		modName := styleModuleName(base, i)
		mods = append(mods, GeneratedModule{Name: modName, Source: css})
		b.WriteString("import " + strconv.Quote("./"+filepath.ToSlash(modName)) + ";\n")
	}
	return mods, b.String(), nil
}

// scopeCSS 给选择器加 [data-v-id] 后缀（Vue 默认属性选择器）。不处理
// :deep/:slotted/:global/v-bind（调用前已拒绝）。
func scopeCSS(css, id string) string {
	attr := "[data-v-" + id + "]"
	return scopeCSSChunk(css, attr)
}

func scopeCSSChunk(css, attr string) string {
	var b strings.Builder
	i := 0
	for i < len(css) {
		i = skipCSSTrivia(css, i, &b)
		if i >= len(css) {
			break
		}
		selStart := i
		for i < len(css) {
			if next := skipCSSStringOrComment(css, i); next != i {
				i = next
				continue
			}
			if css[i] == '{' {
				break
			}
			if css[i] == '}' {
				b.WriteString(css[selStart : i+1])
				i++
				selStart = i
				continue
			}
			i++
		}
		if i >= len(css) {
			b.WriteString(css[selStart:])
			break
		}
		selector := strings.TrimSpace(css[selStart:i])
		body, next := readCSSBraceBody(css, i+1)
		i = next
		if selector == "" {
			continue
		}
		if strings.HasPrefix(selector, "@") {
			at := atRuleName(selector)
			b.WriteString(selector)
			b.WriteByte('{')
			switch at {
			case "media", "supports", "layer", "container":
				b.WriteString(scopeCSSChunk(body, attr))
			default:
				b.WriteString(body)
			}
			b.WriteByte('}')
			continue
		}
		b.WriteString(scopeSelectors(selector, attr))
		b.WriteByte('{')
		b.WriteString(body)
		b.WriteByte('}')
	}
	return b.String()
}

func scopeSelectors(selector, attr string) string {
	parts := strings.Split(selector, ",")
	for i, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if !strings.Contains(s, attr) {
			s += attr
		}
		parts[i] = s
	}
	return strings.Join(parts, ",")
}

func atRuleName(selector string) string {
	s := strings.TrimSpace(selector)
	if len(s) < 2 || s[0] != '@' {
		return ""
	}
	s = s[1:]
	end := 0
	for end < len(s) && ((s[end] >= 'a' && s[end] <= 'z') || (s[end] >= 'A' && s[end] <= 'Z') || s[end] == '-') {
		end++
	}
	return strings.ToLower(s[:end])
}

func skipCSSTrivia(css string, i int, b *strings.Builder) int {
	for i < len(css) {
		if css[i] == '/' && i+1 < len(css) && css[i+1] == '*' {
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				b.WriteString(css[i:])
				return len(css)
			}
			b.WriteString(css[i : i+2+end+2])
			i += 2 + end + 2
			continue
		}
		if css[i] == ' ' || css[i] == '\t' || css[i] == '\n' || css[i] == '\r' {
			b.WriteByte(css[i])
			i++
			continue
		}
		break
	}
	return i
}

func skipCSSStringOrComment(css string, i int) int {
	if i >= len(css) {
		return i
	}
	if css[i] == '/' && i+1 < len(css) && css[i+1] == '*' {
		end := strings.Index(css[i+2:], "*/")
		if end < 0 {
			return len(css)
		}
		return i + 2 + end + 2
	}
	if css[i] == '"' || css[i] == '\'' {
		q := css[i]
		i++
		for i < len(css) && css[i] != q {
			if css[i] == '\\' && i+1 < len(css) {
				i += 2
				continue
			}
			i++
		}
		if i < len(css) {
			i++
		}
		return i
	}
	return i
}

func readCSSBraceBody(css string, i int) (string, int) {
	start := i
	depth := 1
	for i < len(css) && depth > 0 {
		if next := skipCSSStringOrComment(css, i); next != i {
			i = next
			continue
		}
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start:i], i + 1
			}
		}
		i++
	}
	return css[start:], len(css)
}

func styleModuleName(base string, index int) string {
	return fmt.Sprintf("%s.__aluka_style.%d.css", base, index)
}
