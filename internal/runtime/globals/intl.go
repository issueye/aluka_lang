package globals

// Intl API：全量国际化标准支持
// 包含：
//   - Intl.DateTimeFormat (日期与时间格式化)
//   - Intl.NumberFormat (数字/货币/百分比格式化)
//   - Intl.RelativeTimeFormat (相对时间格式化：3天前/下个月)
//   - Intl.ListFormat (列表格式化：A, B, and C)
//   - Intl.PluralRules (单复数规则)
//   - Intl.Collator (多语言字符串排序与比较)
//   - Intl.Segmenter (文本与字素分割)
//   - Intl.getCanonicalLocales

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/rivo/uniseg"
)

// IntlConfig 配置 Intl 全局。
type IntlConfig struct{}

// NewIntl 注册 Intl 全局对象及其全量构造器。
func NewIntl(ctx engine.Context, cfg IntlConfig) error {
	intl := engine.NewObject()

	// 1. getCanonicalLocales(locales)
	_ = intl.Set("getCanonicalLocales", engine.NewFunction("getCanonicalLocales", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		locales := normalizeLocales(args[0])
		vals := make([]engine.Value, len(locales))
		for i, l := range locales {
			vals[i] = engine.Str(l)
		}
		return engine.NewArray(vals), nil
	}))

	// 2. Intl.DateTimeFormat
	dtfCtor := engine.NewFunction("DateTimeFormat", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newDateTimeFormatInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(dtfCtor, "DateTimeFormat")
	_ = intl.Set("DateTimeFormat", dtfCtor)

	// 3. Intl.NumberFormat
	nfCtor := engine.NewFunction("NumberFormat", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newNumberFormatInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(nfCtor, "NumberFormat")
	_ = intl.Set("NumberFormat", nfCtor)

	// 4. Intl.RelativeTimeFormat
	rtfCtor := engine.NewFunction("RelativeTimeFormat", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newRelativeTimeFormatInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(rtfCtor, "RelativeTimeFormat")
	_ = intl.Set("RelativeTimeFormat", rtfCtor)

	// 5. Intl.ListFormat
	lfCtor := engine.NewFunction("ListFormat", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newListFormatInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(lfCtor, "ListFormat")
	_ = intl.Set("ListFormat", lfCtor)

	// 6. Intl.PluralRules
	prCtor := engine.NewFunction("PluralRules", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newPluralRulesInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(prCtor, "PluralRules")
	_ = intl.Set("PluralRules", prCtor)

	// 7. Intl.Collator
	colCtor := engine.NewFunction("Collator", func(args []engine.Value) (engine.Value, error) {
		locale := "en-US"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			locs := normalizeLocales(args[0])
			if len(locs) > 0 {
				locale = locs[0]
			}
		}
		opts := extractOptions(args, 1)
		inst := newCollatorInstance(locale, opts)
		return inst, nil
	})
	registerConstructorPrototype(colCtor, "Collator")
	_ = intl.Set("Collator", colCtor)

	// 8. Intl.Segmenter (既有保持)
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
			granularity = "grapheme"
		}
		inst := engine.NewObject()
		_ = inst.Set("granularity", engine.Str(granularity))

		_ = inst.Set("segment", engine.NewFunction("segment", func(sa []engine.Value) (engine.Value, error) {
			text := ""
			if len(sa) > 0 {
				text = sa[0].String()
			}
			return newSegments(granularity, text), nil
		}))
		return inst, nil
	})
	registerConstructorPrototype(segmenterCtor, "Segmenter")
	_ = intl.Set("Segmenter", segmenterCtor)

	return ctx.Global().Set("Intl", intl)
}

func registerConstructorPrototype(ctor engine.Function, name string) {
	if obj, ok := ctor.AsObject(); ok {
		proto := engine.NewObject()
		_ = proto.Set("constructor", ctor)
		_ = obj.Set("prototype", proto)
	}
}

// --- 实例实现 ---

func newDateTimeFormatInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()
	_ = inst.Set("resolvedOptions", engine.NewFunction("resolvedOptions", func(args []engine.Value) (engine.Value, error) {
		res := engine.NewObject()
		_ = res.Set("locale", engine.Str(locale))
		_ = res.Set("calendar", engine.Str("gregory"))
		_ = res.Set("numberingSystem", engine.Str("latn"))
		for k, v := range opts {
			_ = res.Set(k, engine.Str(v))
		}
		return res, nil
	}))

	_ = inst.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		t := time.Now()
		if len(args) > 0 && !args[0].IsUndefined() {
			if ms, ok := args[0].Float(); ok {
				t = time.UnixMilli(int64(ms))
			} else if dateStr := args[0].String(); dateStr != "" {
				if parsed, err := time.Parse(time.RFC3339, dateStr); err == nil {
					t = parsed
				}
			}
		}

		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			// 2026/8/14 或 2026年8月14日
			if opts["year"] != "" && opts["month"] != "" && opts["day"] != "" {
				return engine.Str(fmt.Sprintf("%d年%d月%d日", t.Year(), int(t.Month()), t.Day())), nil
			}
			return engine.Str(fmt.Sprintf("%d/%d/%d", t.Year(), int(t.Month()), t.Day())), nil
		}

		// 默认 en-US: M/D/YYYY
		return engine.Str(fmt.Sprintf("%d/%d/%d", int(t.Month()), t.Day(), t.Year())), nil
	}))

	return inst
}

func newNumberFormatInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()

	style := opts["style"]
	if style == "" {
		style = "decimal"
	}
	currency := opts["currency"]
	if currency == "" {
		currency = "USD"
	}

	_ = inst.Set("resolvedOptions", engine.NewFunction("resolvedOptions", func(args []engine.Value) (engine.Value, error) {
		res := engine.NewObject()
		_ = res.Set("locale", engine.Str(locale))
		_ = res.Set("style", engine.Str(style))
		if style == "currency" {
			_ = res.Set("currency", engine.Str(currency))
		}
		return res, nil
	}))

	_ = inst.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		var val float64
		if len(args) > 0 {
			if n, ok := args[0].Float(); ok {
				val = n
			}
		}

		switch style {
		case "percent":
			p := val * 100
			return engine.Str(fmt.Sprintf("%.0f%%", p)), nil
		case "currency":
			sign := "$"
			switch strings.ToUpper(currency) {
			case "CNY", "RMB":
				sign = "¥"
			case "EUR":
				sign = "€"
			case "GBP":
				sign = "£"
			case "JPY":
				sign = "¥"
			}
			return engine.Str(fmt.Sprintf("%s%.2f", sign, val)), nil
		default:
			// decimal: 支持千分位格式化
			return engine.Str(formatNumberWithCommas(val)), nil
		}
	}))

	return inst
}

func newRelativeTimeFormatInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()
	numeric := opts["numeric"]
	if numeric == "" {
		numeric = "always"
	}

	_ = inst.Set("resolvedOptions", engine.NewFunction("resolvedOptions", func(args []engine.Value) (engine.Value, error) {
		res := engine.NewObject()
		_ = res.Set("locale", engine.Str(locale))
		_ = res.Set("numeric", engine.Str(numeric))
		return res, nil
	}))

	_ = inst.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(""), nil
		}
		val := 0
		if n, ok := args[0].Int(); ok {
			val = n
		}
		unit := strings.ToLower(args[1].String())
		unit = strings.TrimSuffix(unit, "s") // normalize "days" -> "day"

		isZh := strings.HasPrefix(strings.ToLower(locale), "zh")

		if numeric == "auto" {
			if unit == "day" {
				if val == 0 {
					if isZh {
						return engine.Str("今天"), nil
					}
					return engine.Str("today"), nil
				} else if val == 1 {
					if isZh {
						return engine.Str("明天"), nil
					}
					return engine.Str("tomorrow"), nil
				} else if val == -1 {
					if isZh {
						return engine.Str("昨天"), nil
					}
					return engine.Str("yesterday"), nil
				}
			}
		}

		if isZh {
			if val < 0 {
				return engine.Str(fmt.Sprintf("%d%s前", -val, zhUnitName(unit))), nil
			}
			return engine.Str(fmt.Sprintf("%d%s后", val, zhUnitName(unit))), nil
		}

		// en-US
		if val < 0 {
			abs := -val
			s := ""
			if abs != 1 {
				s = "s"
			}
			return engine.Str(fmt.Sprintf("%d %s%s ago", abs, unit, s)), nil
		}
		s := ""
		if val != 1 {
			s = "s"
		}
		return engine.Str(fmt.Sprintf("in %d %s%s", val, unit, s)), nil
	}))

	return inst
}

func newListFormatInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()
	_ = inst.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		var list []string
		if arr, ok := args[0].(*engine.ArrayValue); ok {
			for _, el := range arr.Elems() {
				list = append(list, el.String())
			}
		}

		if len(list) == 0 {
			return engine.Str(""), nil
		}
		if len(list) == 1 {
			return engine.Str(list[0]), nil
		}
		if len(list) == 2 {
			if strings.HasPrefix(strings.ToLower(locale), "zh") {
				return engine.Str(list[0] + "和" + list[1]), nil
			}
			return engine.Str(list[0] + " and " + list[1]), nil
		}

		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			return engine.Str(strings.Join(list[:len(list)-1], "、") + "和" + list[len(list)-1]), nil
		}

		return engine.Str(strings.Join(list[:len(list)-1], ", ") + ", and " + list[len(list)-1]), nil
	}))
	return inst
}

func newPluralRulesInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()
	_ = inst.Set("select", engine.NewFunction("select", func(args []engine.Value) (engine.Value, error) {
		var n float64
		if len(args) > 0 {
			if num, ok := args[0].Float(); ok {
				n = num
			}
		}
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			return engine.Str("other"), nil
		}
		if n == 1 {
			return engine.Str("one"), nil
		}
		if n == 0 {
			return engine.Str("zero"), nil
		}
		return engine.Str("other"), nil
	}))
	return inst
}

func newCollatorInstance(locale string, opts map[string]string) engine.Value {
	inst := engine.NewObject()
	numeric := opts["numeric"] == "true"
	sensitivity := opts["sensitivity"]
	caseInsensitive := sensitivity == "base" || sensitivity == "accent"

	_ = inst.Set("compare", engine.NewFunction("compare", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.IntValue(0), nil
		}
		s1 := args[0].String()
		s2 := args[1].String()

		res := naturalCompare(s1, s2, numeric, caseInsensitive)
		return engine.IntValue(res), nil
	}))
	return inst
}

func naturalCompare(s1, s2 string, numeric bool, caseInsensitive bool) int {
	if caseInsensitive {
		s1 = strings.ToLower(s1)
		s2 = strings.ToLower(s2)
	}
	if !numeric {
		if s1 < s2 {
			return -1
		} else if s1 > s2 {
			return 1
		}
		return 0
	}

	i1, i2 := 0, 0
	len1, len2 := len(s1), len(s2)

	for i1 < len1 && i2 < len2 {
		ch1 := s1[i1]
		ch2 := s2[i2]

		if ch1 >= '0' && ch1 <= '9' && ch2 >= '0' && ch2 <= '9' {
			j1 := i1
			for j1 < len1 && s1[j1] >= '0' && s1[j1] <= '9' {
				j1++
			}
			j2 := i2
			for j2 < len2 && s2[j2] >= '0' && s2[j2] <= '9' {
				j2++
			}
			numStr1 := s1[i1:j1]
			numStr2 := s2[i2:j2]
			n1, _ := strconv.ParseUint(numStr1, 10, 64)
			n2, _ := strconv.ParseUint(numStr2, 10, 64)
			if n1 < n2 {
				return -1
			} else if n1 > n2 {
				return 1
			}
			i1 = j1
			i2 = j2
		} else {
			if ch1 < ch2 {
				return -1
			} else if ch1 > ch2 {
				return 1
			}
			i1++
			i2++
		}
	}

	if i1 < len1 {
		return 1
	} else if i2 < len2 {
		return -1
	}
	return 0
}

// --- 辅助工具 ---

func normalizeLocales(v engine.Value) []string {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return []string{"en-US"}
	}
	if v.Type() == engine.TypeString {
		return []string{v.String()}
	}
	if arr, ok := v.(*engine.ArrayValue); ok {
		var res []string
		for _, el := range arr.Elems() {
			if s := el.String(); s != "" {
				res = append(res, s)
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	return []string{"en-US"}
}

func extractOptions(args []engine.Value, idx int) map[string]string {
	res := make(map[string]string)
	if len(args) > idx {
		if o, ok := args[idx].AsObject(); ok {
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil && !val.IsUndefined() {
					res[k] = val.String()
				}
			}
		}
	}
	return res
}

func zhUnitName(u string) string {
	switch u {
	case "second":
		return "秒"
	case "minute":
		return "分钟"
	case "hour":
		return "小时"
	case "day":
		return "天"
	case "week":
		return "周"
	case "month":
		return "个月"
	case "year":
		return "年"
	default:
		return u
	}
}

func formatNumberWithCommas(val float64) string {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return fmt.Sprintf("%v", val)
	}
	str := fmt.Sprintf("%.2f", val)
	str = strings.TrimSuffix(str, ".00")
	parts := strings.Split(str, ".")
	intPart := parts[0]
	isNegative := false
	if strings.HasPrefix(intPart, "-") {
		isNegative = true
		intPart = intPart[1:]
	}

	var formatted []byte
	n := len(intPart)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			formatted = append(formatted, ',')
		}
		formatted = append(formatted, intPart[i])
	}
	res := string(formatted)
	if isNegative {
		res = "-" + res
	}
	if len(parts) > 1 {
		res += "." + parts[1]
	}
	return res
}

// newSegments 构造 Segments 对象（保留既有 Uniseg 实现）
func newSegments(granularity, text string) engine.Value {
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
		gr := uniseg.NewGraphemes(text)
		for gr.Next() {
			g := gr.Str()
			items = append(items, segItem{seg: g, index: utf16Index})
			utf16Index += utf16Len(g)
		}
	}

	segments := engine.NewObject()
	_ = segments.Set("input", engine.Str(text))

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

func wordHasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
