package globals

// Intl API：Intl.Segmenter（tui 的 grapheme/word 分割，UAX #29）。
//
// 实现基于 github.com/rivo/uniseg（纯 Go，无 CGO）。
// 对齐 Node/V8 语义：
//   - new Intl.Segmenter([locale], { granularity: 'grapheme' | 'word' | 'sentence' })
//   - segment(text) → Segments 迭代器（[Symbol.iterator] + containing(index)）
//   - 分割项 { segment, index, input, isWordLike }

import (
	"unicode"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/rivo/uniseg"
)

// IntlConfig 配置 Intl 全局。
type IntlConfig struct{}

// NewIntl 注册 Intl 全局对象。
func NewIntl(ctx engine.Context, cfg IntlConfig) error {
	intl := engine.NewObject()

	// Segmenter 构造器。
	segmenterCtor := engine.NewFunction("Segmenter", func(args []engine.Value) (engine.Value, error) {
		granularity := "grapheme"
		if len(args) > 1 {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("granularity"); err == nil && !v.IsUndefined() {
					granularity = v.String()
				}
			}
		}
		if granularity != "grapheme" && granularity != "word" && granularity != "sentence" {
			return engine.Undefined(), nil // RangeError 简化：回退 grapheme
		}
		inst := engine.NewObject()
		_ = inst.Set("granularity", engine.Str(granularity))

		// segment(text) → Segments。
		_ = inst.Set("segment", engine.NewFunction("segment", func(sa []engine.Value) (engine.Value, error) {
			text := ""
			if len(sa) > 0 {
				text = sa[0].String()
			}
			return newSegments(granularity, text), nil
		}))
		return inst, nil
	})
	segmenterObj, _ := segmenterCtor.AsObject()
	proto := engine.NewObject()
	_ = proto.Set("constructor", segmenterCtor)
	_ = segmenterObj.Set("prototype", proto)

	_ = intl.Set("Segmenter", segmenterCtor)
	return ctx.Global().Set("Intl", intl)
}

// newSegments 构造 Segments 对象（可迭代 + containing）。
func newSegments(granularity, text string) engine.Value {
	// 预计算分割项。index 为 UTF-16 码元索引（JS 字符串语义）。
	type segItem struct {
		seg        string
		index      int
		isWordLike bool
	}
	var items []segItem
	utf16Index := 0
	switch granularity {
	case "word":
		state := -1
		rest := text
		for len(rest) > 0 {
			word, rest2, newState := uniseg.FirstWordInString(rest, state)
			items = append(items, segItem{seg: word, index: utf16Index, isWordLike: wordHasLetterOrDigit(word)})
			utf16Index += utf16Len(word)
			rest = rest2
			state = newState
		}
	default:
		// grapheme / sentence（sentence 简化按 grapheme 处理）。
		// 用 Graphemes 迭代器（Step 完整实现，支持 GB11 emoji ZWJ 序列）。
		gr := uniseg.NewGraphemes(text)
		for gr.Next() {
			g := gr.Str()
			items = append(items, segItem{seg: g, index: utf16Index})
			utf16Index += utf16Len(g)
		}
	}

	segments := engine.NewObject()
	_ = segments.Set("input", engine.Str(text))

	// containing(index) → 包含该索引的分割项。
	_ = segments.Set("containing", engine.NewFunction("containing", func(args []engine.Value) (engine.Value, error) {
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		for _, it := range items {
			if idx >= it.index && idx < it.index+len(it.seg) {
				item := engine.NewObject()
				_ = item.Set("segment", engine.Str(it.seg))
				_ = item.Set("index", engine.IntValue(it.index))
				_ = item.Set("input", engine.Str(text))
				_ = item.Set("isWordLike", engine.Boolean(it.isWordLike))
				return item, nil
			}
		}
		return engine.Undefined(), nil
	}))

	// [Symbol.iterator]() → 迭代器。
	iterObj := engine.NewObject()
	i := 0
	_ = iterObj.Set("next", engine.NewFunction("next", func(args []engine.Value) (engine.Value, error) {
		res := engine.NewObject()
		if i >= len(items) {
			_ = res.Set("done", engine.Boolean(true))
			return res, nil
		}
		it := items[i]
		i++
		item := engine.NewObject()
		_ = item.Set("segment", engine.Str(it.seg))
		_ = item.Set("index", engine.IntValue(it.index))
		_ = item.Set("input", engine.Str(text))
		_ = item.Set("isWordLike", engine.Boolean(it.isWordLike))
		_ = res.Set("done", engine.Boolean(false))
		_ = res.Set("value", item)
		return res, nil
	}))
	_ = segments.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))

	return segments
}

// utf16Len 计算字符串的 UTF-16 码元长度（补充平面字符算 2）。
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// wordHasLetterOrDigit 判断词片段是否含字母/数字（isWordLike 语义）。
func wordHasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
