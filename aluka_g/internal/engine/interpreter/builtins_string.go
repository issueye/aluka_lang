// 内置字符串：String.prototype/String 构造器、UTF-16 code unit 索引 helper 与切片参数规范化。

package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aluka-lang/aluka/internal/engine"
)

func jsStringUnits(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

func jsStringFromUnits(units []uint16) string {
	return string(utf16.Decode(units))
}

func jsStringUnitAt(s string, index int) (string, bool) {
	units := jsStringUnits(s)
	if index < 0 || index >= len(units) {
		return "", false
	}
	return jsStringFromUnits(units[index : index+1]), true
}

func jsStringIndex(haystack, needle []uint16, from int) int {
	if from < 0 {
		from = 0
	}
	if len(needle) == 0 {
		if from > len(haystack) {
			return len(haystack)
		}
		return from
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func (interp *Interpreter) setupStringProto() {
	p := interp.stringProto
	_ = p.Set("charAt", interp.nativeMethod("charAt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		unit, ok := jsStringUnitAt(s, idx)
		if !ok {
			return engine.Str(""), nil
		}
		return engine.Str(unit), nil
	}))
	_ = p.Set("charCodeAt", interp.nativeMethod("charCodeAt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := jsStringUnits(this.String())
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		if idx < 0 || idx >= len(units) {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(float64(units[idx])), nil
	}))
	_ = p.Set("codePointAt", interp.nativeMethod("codePointAt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := utf16.Encode([]rune(this.String()))
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		if idx < 0 || idx >= len(units) {
			return engine.Undefined(), nil
		}
		first := units[idx]
		if first >= 0xD800 && first <= 0xDBFF && idx+1 < len(units) {
			second := units[idx+1]
			if second >= 0xDC00 && second <= 0xDFFF {
				codePoint := 0x10000 + (int(first)-0xD800)*0x400 + int(second) - 0xDC00
				return engine.IntValue(codePoint), nil
			}
		}
		return engine.IntValue(int(first)), nil
	}))
	_ = p.Set("toUpperCase", interp.nativeMethod("toUpperCase", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.ToUpper(this.String())), nil
	}))
	_ = p.Set("toLowerCase", interp.nativeMethod("toLowerCase", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.ToLower(this.String())), nil
	}))
	_ = p.Set("localeCompare", interp.nativeMethod("localeCompare", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		other := "undefined"
		if len(args) > 0 {
			other = args[0].String()
		}
		return engine.IntValue(strings.Compare(this.String(), other)), nil
	}))
	_ = p.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := jsStringUnits(this.String())
		start, end := normalizeSliceArgs(len(units), args)
		if start >= end {
			return engine.Str(""), nil
		}
		return engine.Str(jsStringFromUnits(units[start:end])), nil
	}))
	_ = p.Set("substring", interp.nativeMethod("substring", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := jsStringUnits(this.String())
		n := len(units)
		start, end := normalizeSubstringArgs(n, args)
		return engine.Str(jsStringFromUnits(units[start:end])), nil
	}))
	_ = p.Set("substr", interp.nativeMethod("substr", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := jsStringUnits(this.String())
		n := len(units)
		// start：负值从末尾倒数（至少 0）；length：省略则取到末尾，
		// 负值/NaN 视为 0。
		start := 0
		if len(args) > 0 {
			if v, ok := args[0].Int(); ok {
				start = v
				if start < 0 {
					start = n + start
					if start < 0 {
						start = 0
					}
				}
				if start > n {
					start = n
				}
			}
		}
		length := n - start
		if len(args) > 1 && !args[1].IsUndefined() {
			if v, ok := args[1].Int(); ok {
				length = v
				if length < 0 {
					length = 0
				}
			}
		}
		if end := start + length; end < n {
			n = end
		}
		return engine.Str(jsStringFromUnits(units[start:n])), nil
	}))
	_ = p.Set("indexOf", interp.nativeMethod("indexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		s := jsStringUnits(this.String())
		needle := jsStringUnits(args[0].String())
		from := 0
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok && n > 0 {
				from = n
			}
		}
		if from > len(s) {
			return engine.IntValue(-1), nil
		}
		return engine.IntValue(jsStringIndex(s, needle, from)), nil
	}))
	_ = p.Set("lastIndexOf", interp.nativeMethod("lastIndexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		s := jsStringUnits(this.String())
		needle := jsStringUnits(args[0].String())
		if len(needle) == 0 {
			return engine.IntValue(len(s)), nil
		}
		last := -1
		for from := 0; ; {
			idx := jsStringIndex(s, needle, from)
			if idx < 0 {
				break
			}
			last = idx
			from = idx + 1
		}
		return engine.IntValue(last), nil
	}))
	_ = p.Set("includes", interp.nativeMethod("includes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.Contains(this.String(), args[0].String())), nil
	}))
	_ = p.Set("startsWith", interp.nativeMethod("startsWith", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.HasPrefix(this.String(), args[0].String())), nil
	}))
	_ = p.Set("endsWith", interp.nativeMethod("endsWith", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.HasSuffix(this.String(), args[0].String())), nil
	}))
	_ = p.Set("split", interp.nativeMethod("split", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.NewArray([]engine.Value{engine.Str(s)}), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			limit := -1
			if len(args) > 1 {
				if n, ok := args[1].Float(); ok {
					limit = int(n)
				}
			}
			return regexpSplit(interp, r, s, limit)
		}
		sep := args[0].String()
		if sep == "" {
			units := jsStringUnits(s)
			elems := make([]engine.Value, 0, len(units))
			for _, unit := range units {
				elems = append(elems, engine.Str(jsStringFromUnits([]uint16{unit})))
			}
			return engine.NewArray(elems), nil
		}
		parts := strings.Split(s, sep)
		elems := make([]engine.Value, len(parts))
		for i, part := range parts {
			elems[i] = engine.Str(part)
		}
		return engine.NewArray(elems), nil
	}))
	_ = p.Set("replace", interp.nativeMethod("replace", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(this.String()), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			return regexpReplace(interp, r, this.String(), args[1], r.compiled.Flags.Global)
		}
		return engine.Str(strings.Replace(this.String(), args[0].String(), args[1].String(), 1)), nil
	}))
	_ = p.Set("replaceAll", interp.nativeMethod("replaceAll", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(this.String()), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			if !r.compiled.Flags.Global {
				return engine.Undefined(), fmt.Errorf("%w: String.prototype.replaceAll called with a non-global RegExp", engine.ErrTypeError)
			}
			return regexpReplace(interp, r, this.String(), args[1], true)
		}
		return engine.Str(strings.ReplaceAll(this.String(), args[0].String(), args[1].String())), nil
	}))
	_ = p.Set("match", interp.nativeMethod("match", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Null(), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			if !r.compiled.Flags.Global {
				return r.execString(s)
			}
			mt, err := r.compiled.AllMatches(s)
			if err != nil {
				return engine.Undefined(), regexpExecError(err)
			}
			if mt.Len() == 0 {
				return engine.Null(), nil
			}
			elems := make([]engine.Value, 0, mt.Len())
			for i := 0; i < mt.Len(); i++ {
				elems = append(elems, engine.Str(mt.Slice(i, 0)))

			}
			out := engine.NewArray(elems)
			engine.SetProto(out, interp.arrayProto)
			return out, nil
		}
		// 非正则：按规范，若存在 Symbol.match 则调用之；此处简化为字符串查找。
		return engine.Null(), nil
	}))
	_ = p.Set("search", interp.nativeMethod("search", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.IntValue(-1), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			m, err := r.compiled.Exec(s)
			if err != nil {
				return engine.Undefined(), regexpExecError(err)
			}
			if m == nil {
				return engine.IntValue(-1), nil
			}
			return engine.IntValue(m[0]), nil

		}
		idx := strings.Index(s, args[0].String())
		return engine.IntValue(idx), nil
	}))
	_ = p.Set("matchAll", interp.nativeMethod("matchAll", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll requires a global RegExp", engine.ErrTypeError)
		}
		r, ok := args[0].(*RegexpValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll requires a RegExp", engine.ErrTypeError)
		}
		if !r.compiled.Flags.Global {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll called with a non-global RegExp", engine.ErrTypeError)
		}
		mt, err := r.compiled.AllMatches(s)
		if err != nil {
			return engine.Undefined(), regexpExecError(err)
		}
		elems := make([]engine.Value, 0, mt.Len())
		for i := 0; i < mt.Len(); i++ {
			v, err := r.execStringAtMatch(mt, i)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		out := engine.NewArray(elems)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("trim", interp.nativeMethod("trim", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimSpace(this.String())), nil
	}))
	_ = p.Set("trimStart", interp.nativeMethod("trimStart", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimLeftFunc(this.String(), unicode.IsSpace)), nil
	}))
	_ = p.Set("trimEnd", interp.nativeMethod("trimEnd", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimRightFunc(this.String(), unicode.IsSpace)), nil
	}))
	_ = p.Set("repeat", interp.nativeMethod("repeat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		n, ok := args[0].Int()
		if !ok || n < 0 {
			return engine.Str(""), nil
		}
		return engine.Str(strings.Repeat(this.String(), n)), nil
	}))
	_ = p.Set("concat", interp.nativeMethod("concat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var b strings.Builder
		b.WriteString(this.String())
		for _, a := range args {
			b.WriteString(a.String())
		}
		return engine.Str(b.String()), nil
	}))
	// isWellFormed()：无孤立 surrogate（ES2024，N22-C3）。
	// 孤立 surrogate 在 Go string 中为无效 UTF-8 或 surrogate 码点。
	_ = p.Set("isWellFormed", interp.nativeMethod("isWellFormed", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Boolean(stringIsWellFormed(this.String())), nil
	}))
	// toWellFormed()：孤立 surrogate 替换为 U+FFFD（ES2024，N22-C3）。
	_ = p.Set("toWellFormed", interp.nativeMethod("toWellFormed", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(stringToWellFormed(this.String())), nil
	}))
	_ = p.Set("padStart", interp.nativeMethod("padStart", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 {
			return engine.Str(s), nil
		}
		targetLen, _ := args[0].Int()
		if targetLen <= len(s) {
			return engine.Str(s), nil
		}
		pad := " "
		if len(args) > 1 {
			pad = args[1].String()
		}
		if pad == "" {
			return engine.Str(s), nil
		}
		need := targetLen - len(s)
		rep := (need + len(pad) - 1) / len(pad)
		return engine.Str(strings.Repeat(pad, rep)[:need] + s), nil
	}))
	_ = p.Set("padEnd", interp.nativeMethod("padEnd", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 {
			return engine.Str(s), nil
		}
		targetLen, _ := args[0].Int()
		if targetLen <= len(s) {
			return engine.Str(s), nil
		}
		pad := " "
		if len(args) > 1 {
			pad = args[1].String()
		}
		if pad == "" {
			return engine.Str(s), nil
		}
		need := targetLen - len(s)
		rep := (need + len(pad) - 1) / len(pad)
		return engine.Str(s + strings.Repeat(pad, rep)[:need]), nil
	}))
	_ = p.Set("at", interp.nativeMethod("at", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		units := jsStringUnits(this.String())
		if len(args) == 0 {
			if len(units) == 0 {
				return engine.Undefined(), nil
			}
			return engine.Str(jsStringFromUnits(units[:1])), nil
		}
		idx, _ := args[0].Int()
		if idx < 0 {
			idx += len(units)
		}
		if idx < 0 || idx >= len(units) {
			return engine.Undefined(), nil
		}
		return engine.Str(jsStringFromUnits(units[idx : idx+1])), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	// [Symbol.iterator]() 字符串默认迭代器（逐码点产出）。缺失时
	// `''[Symbol.iterator]()` 报 "undefined is not a function"。
	_ = p.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return interp.currentVM.newStringIterator(this.String()), nil
	}))
}

// normalizeSliceArgs computes start/end indices for slice() (supports negatives).
func normalizeSliceArgs(n int, args []engine.Value) (int, int) {
	start := 0
	end := n
	if len(args) > 0 {
		if v, ok := args[0].Int(); ok {
			start = v
			if start < 0 {
				start += n
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
		}
	}
	if len(args) > 1 && !args[1].IsUndefined() {
		if v, ok := args[1].Int(); ok {
			end = v
			if end < 0 {
				end += n
			}
			if end < 0 {
				end = 0
			}
			if end > n {
				end = n
			}
		}
	}
	return start, end
}

// normalizeSubstringArgs computes start/end for substring() (swaps if start>end, no negatives).
func normalizeSubstringArgs(n int, args []engine.Value) (int, int) {
	start := 0
	end := n
	if len(args) > 0 {
		if v, ok := args[0].Int(); ok {
			start = v
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
		}
	}
	if len(args) > 1 && !args[1].IsUndefined() {
		if v, ok := args[1].Int(); ok {
			end = v
			if end < 0 {
				end = 0
			}
			if end > n {
				end = n
			}
		}
	}
	if start > end {
		start, end = end, start
	}
	return start, end
}

func (interp *Interpreter) setupStringCtor() {
	ctor := interp.makeFunc("String", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(args[0].String()), nil
	})
	_ = ctor.Set("fromCharCode", interp.makeFunc("fromCharCode", func(args []engine.Value) (engine.Value, error) {
		var b strings.Builder
		for _, a := range args {
			n, _ := a.Int()
			b.WriteRune(rune(n))
		}
		return engine.Str(b.String()), nil
	}))
	_ = ctor.Set("fromCodePoint", interp.makeFunc("fromCodePoint", func(args []engine.Value) (engine.Value, error) {
		var b strings.Builder
		for _, arg := range args {
			codePoint, ok := arg.Int()
			if !ok || codePoint < 0 || codePoint > utf8.MaxRune {
				return engine.Undefined(), fmt.Errorf("%w: Invalid code point %s", engine.ErrRangeError, arg.String())
			}
			b.WriteRune(rune(codePoint))
		}
		return engine.Str(b.String()), nil
	}))
	// String.raw`...`：按模板对象 .raw 数组拼接（raw 保留转义原文）。
	_ = ctor.Set("raw", interp.makeFunc("raw", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		tpl, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: template is not an object", engine.ErrTypeError)
		}
		rawVal, err := tpl.Get("raw")
		if err != nil {
			return engine.Undefined(), err
		}
		rawObj, ok := rawVal.AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: template.raw is not an object", engine.ErrTypeError)
		}
		lv, err := rawObj.Get("length")
		if err != nil {
			return engine.Undefined(), err
		}
		n, ok := lv.Int()
		if !ok || n < 0 {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: invalid raw length", engine.ErrTypeError)
		}
		var b strings.Builder
		for i := 0; i < n; i++ {
			qv, err := rawObj.Get(strconv.Itoa(i))
			if err != nil {
				return engine.Undefined(), err
			}
			b.WriteString(qv.String())
			if i+1 < n {
				sub := engine.Str("")
				if i < len(args)-1 {
					sub = args[i+1]
				}
				b.WriteString(sub.String())
			}
		}
		return engine.Str(b.String()), nil
	}))
	_ = ctor.Set("prototype", interp.stringProto)
	_ = interp.stringProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("String", ctor)
	interp.constructors["String"] = ctor
}

// stringIsWellFormed 判断字符串是否含孤立 surrogate（ES2024 String.prototype.isWellFormed）。
// 孤立 surrogate 在 Go string 中表现为：无效 UTF-8 序列（utf8.DecodeRuneInString
// 返回 RuneError+size1）或 surrogate 码点（U+D800-U+DFFF）。
func stringIsWellFormed(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r >= 0xD800 && r <= 0xDFFF {
			return false
		}
		i += size
	}
	return true
}

// stringToWellFormed 把孤立 surrogate 替换为 U+FFFD（ES2024 toWellFormed）。
func stringToWellFormed(s string) string {
	if stringIsWellFormed(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(0xFFFD)
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}
