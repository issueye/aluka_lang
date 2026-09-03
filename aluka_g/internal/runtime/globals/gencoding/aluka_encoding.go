package gencoding

// Aluka.CSV / TSV / TOML / YAML 编解码（Phase 4 WBS 4.14）。
//
// 均为简化实现：
//   - CSV/TSV：Go encoding/csv（parse → string[][]，stringify 反向）
//   - TOML：自研子集解析器（table、key=value、字符串/数字/布尔/数组）
//   - YAML：自研子集解析器（缩进嵌套映射 + 列表项）

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// alukaRegisterEncoding 注册编码 API。
func RegisterAlukaEncoding(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()

	csvObj := engine.NewObject()
	_ = csvObj.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		text := ""
		if len(args) > 0 {
			text = args[0].String()
		}
		return alukaDelimitedParse(text, ','), nil
	}))
	_ = csvObj.Set("stringify", engine.NewFunction("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(alukaDelimitedStringify(args[0], ',')), nil
	}))
	_ = ao.Set("CSV", csvObj)

	tsvObj := engine.NewObject()
	_ = tsvObj.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		text := ""
		if len(args) > 0 {
			text = args[0].String()
		}
		return alukaDelimitedParse(text, '\t'), nil
	}))
	_ = tsvObj.Set("stringify", engine.NewFunction("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(alukaDelimitedStringify(args[0], '\t')), nil
	}))
	_ = ao.Set("TSV", tsvObj)

	tomlObj := engine.NewObject()
	_ = tomlObj.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		text := ""
		if len(args) > 0 {
			text = args[0].String()
		}
		obj, err := alukaTomlParse(text)
		if err != nil {
			return engine.Undefined(), err
		}
		return obj, nil
	}))
	_ = tomlObj.Set("stringify", engine.NewFunction("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(alukaTomlStringify(args[0])), nil
	}))
	_ = ao.Set("TOML", tomlObj)

	yamlObj := engine.NewObject()
	_ = yamlObj.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		text := ""
		if len(args) > 0 {
			text = args[0].String()
		}
		return alukaYamlParse(text), nil
	}))
	_ = yamlObj.Set("stringify", engine.NewFunction("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(alukaYamlStringify(args[0])), nil
	}))
	_ = ao.Set("YAML", yamlObj)
}

// --- CSV / TSV --------------------------------------------------------------

// alukaDelimitedParse 解析分隔文本为二维字符串数组。
func alukaDelimitedParse(text string, delim rune) engine.Value {
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	rows, err := r.ReadAll()
	if err != nil {
		return engine.NewArray(nil)
	}
	out := make([]engine.Value, 0, len(rows))
	for _, row := range rows {
		cells := make([]engine.Value, 0, len(row))
		for _, cell := range row {
			cells = append(cells, engine.Str(cell))
		}
		out = append(out, engine.NewArray(cells))
	}
	return engine.NewArray(out)
}

// alukaDelimitedStringify 将二维数组/数组序列化为分隔文本。
func alukaDelimitedStringify(v engine.Value, delim rune) string {
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		return ""
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = delim
	for _, rowVal := range arr.Elems() {
		var row []string
		if rowArr, ok := rowVal.(*engine.ArrayValue); ok {
			for _, cell := range rowArr.Elems() {
				row = append(row, cell.String())
			}
		} else {
			row = []string{rowVal.String()}
		}
		if err := w.Write(row); err != nil {
			return ""
		}
	}
	w.Flush()
	return buf.String()
}

// --- TOML -------------------------------------------------------------------

// alukaTomlParse 解析 TOML 子集。
func alukaTomlParse(text string) (engine.Value, error) {
	root := engine.NewObject()
	var current engine.Value = root
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// [table] / [[array.of.table]]（array of table 简化为普通 table）。
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			name = strings.TrimPrefix(name, "[")
			current = alukaTomlEnsureTable(root, name)
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		valStr := alukaTomlStripComment(strings.TrimSpace(line[eq+1:]))
		val, err := alukaTomlValue(valStr)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("TOML: %s: %w", key, err)
		}
		if co, ok := current.AsObject(); ok {
			_ = co.Set(key, val)
		}
	}
	return root, nil
}

// alukaTomlEnsureTable 从 root 按点分路径创建/进入嵌套 table。
func alukaTomlEnsureTable(root engine.Value, path string) engine.Value {
	cur := root
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if o, ok := cur.AsObject(); ok {
			existing, err := o.Get(part)
			if err == nil && existing != nil && existing.IsObject() {
				cur = existing
				continue
			}
		}
		sub := engine.NewObject()
		if co, ok := cur.AsObject(); ok {
			_ = co.Set(part, sub)
		}
		cur = sub
	}
	return cur
}

// alukaTomlStripComment 去掉行尾注释（# 在引号外）。
func alukaTomlStripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

// alukaTomlValue 解析 TOML 标量/数组。
func alukaTomlValue(s string) (engine.Value, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return engine.Str(""), nil
	}
	// 字符串。
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		body := s[1 : len(s)-1]
		if s[0] == '"' {
			body = strings.ReplaceAll(body, `\"`, `"`)
			body = strings.ReplaceAll(body, `\n`, "\n")
			body = strings.ReplaceAll(body, `\t`, "\t")
		}
		return engine.Str(body), nil
	}
	// 布尔。
	switch s {
	case "true":
		return engine.Boolean(true), nil
	case "false":
		return engine.Boolean(false), nil
	}
	// 数字。
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return engine.IntValue(int(f)), nil
	}
	// 数组。
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return engine.NewArray(nil), nil
		}
		parts := alukaTomlSplitTopLevel(inner)
		elems := make([]engine.Value, 0, len(parts))
		for _, p := range parts {
			v, err := alukaTomlValue(p)
			if err != nil {
				return engine.Undefined(), err
			}
			elems = append(elems, v)
		}
		return engine.NewArray(elems), nil
	}
	// 日期/其他 → 字符串。
	return engine.Str(s), nil
}

// alukaTomlSplitTopLevel 按逗号拆分（忽略引号内逗号）。
func alukaTomlSplitTopLevel(s string) []string {
	var parts []string
	var quote byte
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(s) {
		parts = append(parts, strings.TrimSpace(s[start:]))
	}
	return parts
}

// alukaTomlStringify 序列化对象为 TOML。
func alukaTomlStringify(v engine.Value) string {
	var sb strings.Builder
	alukaTomlWrite(&sb, v, "")
	return sb.String()
}

// alukaTomlWrite 递归输出。table 对象输出子表；标量输出 key = value。
func alukaTomlWrite(sb *strings.Builder, v engine.Value, prefix string) {
	o, ok := v.AsObject()
	if !ok {
		return
	}
	for _, k := range o.Keys() {
		val, _ := o.Get(k)
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if val != nil && val.IsObject() {
			if _, isArr := val.(*engine.ArrayValue); isArr {
				sb.WriteString(k + " = ")
				alukaTomlWriteArray(sb, val)
				sb.WriteString("\n")
				continue
			}
			// 嵌套 table。
			sb.WriteString("[" + full + "]\n")
			alukaTomlWrite(sb, val, full)
			continue
		}
		sb.WriteString(k + " = " + alukaTomlScalar(val) + "\n")
	}
}

// alukaTomlWriteArray 输出 [ ... ]。
func alukaTomlWriteArray(sb *strings.Builder, v engine.Value) {
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		sb.WriteString("[]")
		return
	}
	sb.WriteString("[")
	for i, e := range arr.Elems() {
		if i > 0 {
			sb.WriteString(", ")
		}
		if e != nil && e.IsObject() {
			sb.WriteString(alukaTomlScalar(e))
		} else {
			sb.WriteString(alukaTomlScalar(e))
		}
	}
	sb.WriteString("]")
}

// alukaTomlScalar 输出标量字面量。
func alukaTomlScalar(v engine.Value) string {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return `""`
	}
	if v.IsObject() {
		// 数组。
		if a, ok := v.(*engine.ArrayValue); ok {
			parts := make([]string, 0, len(a.Elems()))
			for _, e := range a.Elems() {
				parts = append(parts, alukaTomlScalar(e))
			}
			return "[" + strings.Join(parts, ", ") + "]"
		}
		return `""`
	}
	switch v.Type() {
	case engine.TypeBoolean:
		b, _ := v.Bool()
		if b {
			return "true"
		}
		return "false"
	case engine.TypeNumber:
		return v.String()
	case engine.TypeString:
		return `"` + strings.ReplaceAll(v.String(), `"`, `\"`) + `"`
	default:
		return `"` + v.String() + `"`
	}
}

// --- YAML -------------------------------------------------------------------

// alukaYamlParse 解析 YAML 子集（缩进嵌套映射 + 列表项 + 顶层列表）。
func alukaYamlParse(text string) engine.Value {
	root := engine.NewObject()
	type frame struct {
		container engine.Value // 对象或数组容器；nil 表示挂起（等首个子行定类型）
		indent    int
		parent    engine.Value // 挂起时：挂载的父对象
		key       string       // 挂起时：父对象上的键
	}
	stack := []frame{{container: root, indent: -1}}
	for _, raw := range strings.Split(text, "\n") {
		indent := 0
		content := raw
		for len(content) > 0 && content[0] == ' ' {
			indent++
			content = content[1:]
		}
		content = strings.TrimSpace(content)
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		// 弹出缩进不浅于当前行的帧（挂起帧无子内容则作废）。
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		top := &stack[len(stack)-1]
		// 挂起帧：按当前行类型确定子容器（列表项 → 数组，否则 → 对象）。
		if top.container == nil {
			if strings.HasPrefix(content, "-") {
				arr := engine.NewArray(nil)
				if p, ok := top.parent.AsObject(); ok {
					_ = p.Set(top.key, arr)
				}
				top.container = arr
			} else {
				obj := engine.NewObject()
				if p, ok := top.parent.AsObject(); ok {
					_ = p.Set(top.key, obj)
				}
				top.container = obj
			}
		}

		// 列表项。
		if strings.HasPrefix(content, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(content, "-"))
			item = alukaYamlStripComment(item)
			var arr *engine.ArrayValue
			if a, ok := top.container.(*engine.ArrayValue); ok {
				arr = a
			} else if o, ok := top.container.AsObject(); ok {
				if v, err := o.Get("__list"); err == nil {
					if a, ok := v.(*engine.ArrayValue); ok {
						arr = a
					}
				}
				if arr == nil {
					arr = engine.NewArray(nil)
					_ = o.Set("__list", arr)
				}
			}
			if arr == nil {
				continue
			}
			if item == "" {
				// 列表项为子对象。
				sub := engine.NewObject()
				arr.Append(sub)
				stack = append(stack, frame{container: sub, indent: indent})
			} else if idx := strings.Index(item, ":"); idx > 0 {
				// 内联对象项：- key: value。
				sub := engine.NewObject()
				if co, ok := sub.AsObject(); ok {
					_ = co.Set(strings.TrimSpace(item[:idx]), alukaYamlScalar(alukaYamlStripComment(item[idx+1:])))
				}
				arr.Append(sub)
			} else {
				arr.Append(alukaYamlScalar(item))
			}
			continue
		}

		// key: value。
		idx := strings.Index(content, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(content[:idx])
		rest := strings.TrimSpace(content[idx+1:])
		var cont engine.Value
		if a, ok := top.container.(*engine.ArrayValue); ok {
			// 数组元素对象：追加并进入。
			sub := engine.NewObject()
			a.Append(sub)
			cont = sub
			top.container = sub
			top.indent = indent
		} else {
			cont = top.container
		}
		if rest == "" || alukaYamlStripComment(rest) == "" {
			// 挂起子容器（对象或数组，待子行确定）。
			stack = append(stack, frame{container: nil, indent: indent, parent: cont, key: key})
		} else {
			if co, ok := cont.AsObject(); ok {
				_ = co.Set(key, alukaYamlScalar(alukaYamlStripComment(rest)))
			}
		}
	}
	// 顶层列表特例：仅含 __list 键时返回数组。
	if o, ok := root.AsObject(); ok {
		keys := o.Keys()
		if len(keys) == 1 && keys[0] == "__list" {
			if v, err := o.Get("__list"); err == nil {
				return v
			}
		}
	}
	return root
}

// alukaYamlStripComment 去掉行内 # 注释。
func alukaYamlStripComment(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// alukaYamlScalar 推断标量类型。
func alukaYamlScalar(s string) engine.Value {
	s = strings.TrimSpace(s)
	switch s {
	case "null", "~":
		return engine.Null()
	case "true":
		return engine.Boolean(true)
	case "false":
		return engine.Boolean(false)
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return engine.Str(s[1 : len(s)-1])
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return engine.IntValue(int(f))
	}
	return engine.Str(s)
}

// alukaYamlStringify 序列化对象为 YAML（2 空格缩进）。
func alukaYamlStringify(v engine.Value) string {
	var sb strings.Builder
	alukaYamlWrite(&sb, "", v, 0, true)
	return sb.String()
}

// alukaYamlWrite 递归输出。
func alukaYamlWrite(sb *strings.Builder, key string, v engine.Value, depth int, top bool) {
	indent := strings.Repeat(" ", depth*2)
	if v == nil || v.IsUndefined() || v.IsNull() {
		if !top {
			sb.WriteString(indent + key + ": null\n")
		}
		return
	}
	if a, ok := v.(*engine.ArrayValue); ok {
		if !top {
			sb.WriteString(indent + key + ":\n")
		}
		for _, e := range a.Elems() {
			if e != nil && e.IsObject() {
				sb.WriteString(indent + "  - \n")
				alukaYamlWrite(sb, "", e, depth+2, false)
			} else {
				sb.WriteString(indent + "  - " + alukaYamlScalarToStr(e) + "\n")
			}
		}
		return
	}
	if o, ok := v.AsObject(); ok {
		if !top {
			sb.WriteString(indent + key + ":\n")
		}
		for _, k := range o.Keys() {
			val, _ := o.Get(k)
			if val != nil && val.IsObject() && !val.IsUndefined() {
				// 嵌套对象/数组。
				if arr, isArr := val.(*engine.ArrayValue); isArr {
					if len(arr.Elems()) == 0 {
						sb.WriteString(indent + "  " + k + ": []\n")
						continue
					}
				}
				alukaYamlWrite(sb, k, val, depth+1, false)
			} else {
				sb.WriteString(indent + "  " + k + ": " + alukaYamlScalarToStr(val) + "\n")
			}
		}
		return
	}
	if !top {
		sb.WriteString(indent + key + ": " + alukaYamlScalarToStr(v) + "\n")
	}
}

// alukaYamlScalarToStr 标量转 YAML 字面量。
func alukaYamlScalarToStr(v engine.Value) string {
	if v == nil || v.IsUndefined() {
		return "null"
	}
	if v.IsNull() {
		return "null"
	}
	switch v.Type() {
	case engine.TypeBoolean:
		b, _ := v.Bool()
		if b {
			return "true"
		}
		return "false"
	case engine.TypeNumber:
		return v.String()
	case engine.TypeString:
		s := v.String()
		if strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`") || strings.TrimSpace(s) != s {
			return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
		return s
	default:
		return v.String()
	}
}
