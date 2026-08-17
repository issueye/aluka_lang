package vue

import (
	"fmt"
	"strings"
	"unicode"
)

// sfcBlock 是 SFC 顶层块（script / template / style / custom）。
type sfcBlock struct {
	Tag     string
	Attrs   map[string]string
	Content string
}

func (b sfcBlock) has(name string) bool {
	_, ok := b.Attrs[name]
	return ok
}

func (b sfcBlock) attr(name string) string {
	if b.Attrs == nil {
		return ""
	}
	return b.Attrs[name]
}

func (b sfcBlock) isSetup() bool {
	return b.Tag == "script" && b.has("setup")
}

// parseSFCBlocks 扫描 SFC 顶层块。顶层未知标签视为 custom block。
func parseSFCBlocks(src string) ([]sfcBlock, error) {
	var blocks []sfcBlock
	i := 0
	for i < len(src) {
		for i < len(src) && isHTMLSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			break
		}
		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("unterminated HTML comment")
			}
			i += 4 + end + 3
			continue
		}
		if src[i] != '<' {
			// 顶层文本忽略到下一 '<'。
			j := strings.IndexByte(src[i:], '<')
			if j < 0 {
				break
			}
			i += j
			continue
		}
		block, next, err := parseOneBlock(src, i)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
		i = next
	}
	return blocks, nil
}

func parseOneBlock(src string, start int) (sfcBlock, int, error) {
	if start >= len(src) || src[start] != '<' {
		return sfcBlock{}, start, fmt.Errorf("expected tag")
	}
	i := start + 1
	if i < len(src) && src[i] == '/' {
		return sfcBlock{}, start, fmt.Errorf("unexpected closing tag")
	}
	tagStart := i
	for i < len(src) && isTagNameChar(src[i]) {
		i++
	}
	if i == tagStart {
		return sfcBlock{}, start, fmt.Errorf("expected tag name")
	}
	tag := strings.ToLower(src[tagStart:i])
	attrs := map[string]string{}
	selfClose := false
	for {
		for i < len(src) && isHTMLSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			return sfcBlock{}, start, fmt.Errorf("unterminated <%s", tag)
		}
		if src[i] == '>' {
			i++
			break
		}
		if src[i] == '/' {
			i++
			for i < len(src) && isHTMLSpace(src[i]) {
				i++
			}
			if i < len(src) && src[i] == '>' {
				i++
				selfClose = true
				break
			}
			return sfcBlock{}, start, fmt.Errorf("stray '/' in <%s", tag)
		}
		nameStart := i
		for i < len(src) && isAttrNameChar(src[i]) {
			i++
		}
		if i == nameStart {
			return sfcBlock{}, start, fmt.Errorf("expected attribute in <%s", tag)
		}
		name := strings.ToLower(src[nameStart:i])
		for i < len(src) && isHTMLSpace(src[i]) {
			i++
		}
		value := ""
		if i < len(src) && src[i] == '=' {
			i++
			for i < len(src) && isHTMLSpace(src[i]) {
				i++
			}
			value, i = readQuotedOrBare(src, i)
		}
		attrs[name] = value
	}
	if selfClose {
		return sfcBlock{Tag: tag, Attrs: attrs}, i, nil
	}
	closeTag := "</" + tag + ">"
	end := indexCloseTag(src, i, tag)
	if end < 0 {
		return sfcBlock{}, start, fmt.Errorf("missing %s", closeTag)
	}
	content := src[i:end]
	return sfcBlock{Tag: tag, Attrs: attrs, Content: content}, end + len(closeTag), nil
}

// indexCloseTag 找与 start 处内容匹配的闭合标签；template 允许一层同名嵌套。
func indexCloseTag(src string, start int, tag string) int {
	closeTag := "</" + tag + ">"
	openTag := "<" + tag
	depth := 0
	i := start
	for i < len(src) {
		if strings.HasPrefix(src[i:], closeTag) {
			if depth == 0 {
				return i
			}
			depth--
			i += len(closeTag)
			continue
		}
		if tag == "template" && strings.HasPrefix(strings.ToLower(src[i:]), openTag) {
			j := i + len(openTag)
			if j < len(src) && (src[j] == '>' || src[j] == '/' || isHTMLSpace(src[j])) {
				depth++
			}
		}
		i++
	}
	return -1
}

func readQuotedOrBare(src string, i int) (string, int) {
	if i >= len(src) {
		return "", i
	}
	if src[i] == '"' || src[i] == '\'' {
		q := src[i]
		i++
		start := i
		for i < len(src) && src[i] != q {
			i++
		}
		val := src[start:i]
		if i < len(src) {
			i++
		}
		return val, i
	}
	start := i
	for i < len(src) && !isHTMLSpace(src[i]) && src[i] != '>' {
		i++
	}
	return src[start:i], i
}

func isHTMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isTagNameChar(b byte) bool {
	return b == '-' || b == '_' || unicode.IsLetter(rune(b)) || unicode.IsDigit(rune(b))
}

func isAttrNameChar(b byte) bool {
	return !isHTMLSpace(b) && b != '=' && b != '>' && b != '/'
}

func classifyBlocks(blocks []sfcBlock) (script, template *sfcBlock, styles []sfcBlock, custom []sfcBlock) {
	for i := range blocks {
		b := &blocks[i]
		switch b.Tag {
		case "script":
			if script == nil {
				script = b
			}
		case "template":
			if template == nil {
				template = b
			}
		case "style":
			styles = append(styles, *b)
		default:
			custom = append(custom, *b)
		}
	}
	return
}
