package builtin

// node:util 内置模块——提供工具函数。
// inspect 实现为简化版（复用引擎的 String() 格式化）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewUtil 构造 node:util 模块的导出对象。
func NewUtil(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("inspect", engine.NewFunction("inspect", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str("undefined"), nil
		}
		// 简化：直接用引擎的 String() 格式化（与 console.log 一致）。
		return engine.Str(args[0].String()), nil
	}))

	_ = m.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		// Node.js util.format：第一个参数含 %s/%d/%j/%o/%O/%% 占位符。
		return engine.Str(utilFormat(args)), nil
	}))

	// util.formatWithOptions(options, ...args)：与 format 相同但忽略首参 options。
	_ = m.Set("formatWithOptions", engine.NewFunction("formatWithOptions", func(args []engine.Value) (engine.Value, error) {
		rest := args
		if len(rest) > 0 {
			rest = rest[1:]
		}
		return engine.Str(utilFormat(rest)), nil
	}))

	_ = m.Set("debuglog", engine.NewFunction("debuglog", func(args []engine.Value) (engine.Value, error) {
		section := strings.ToLower(strArg(args, 0))
		enabled := debugSectionEnabled(section, os.Getenv("NODE_DEBUG"))
		debugFn := engine.NewFunction(section, func(callArgs []engine.Value) (engine.Value, error) {
			if enabled {
				fmt.Fprintf(os.Stderr, "%s %d: %s\n", strings.ToUpper(section), os.Getpid(), utilFormat(callArgs))
			}
			return engine.Undefined(), nil
		})
		if obj, ok := debugFn.AsObject(); ok {
			_ = obj.Set("enabled", engine.Boolean(enabled))
		}
		return debugFn, nil
	}))

	// util.inherits(ctor, superCtor)：令 ctor.prototype 的原型为
	// superCtor.prototype（Node 语义）。send 等模块依赖它建立继承链。
	_ = m.Set("inherits", engine.NewFunction("inherits", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		ctor, cok := args[0].AsObject()
		superCtor, sok := args[1].AsObject()
		if !cok || !sok {
			return engine.Undefined(), nil
		}
		ctorProto, _ := ctor.Get("prototype")
		superProto, _ := superCtor.Get("prototype")
		if cp, ok := ctorProto.(engine.Object); ok {
			if sp, ok := superProto.(engine.Object); ok {
				engine.SetProto(cp, sp)
			}
		}
		return engine.Undefined(), nil
	}))

	_ = m.Set("promisify", engine.NewFunction("promisify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("util.promisify: argument required")
		}
		original := args[0]
		// 返回一个新函数，把 callback 风格转为 Promise。
		return engine.NewFunction("promisified", func(callArgs []engine.Value) (engine.Value, error) {
			// 简化：直接调用原函数并包装为 Promise.resolve。
			// 完整实现需检测 (err, result) 回调模式。
			if f, ok := original.AsFunction(); ok {
				// 追加一个回调参数（Node.js 约定最后一个参数是回调）。
				result, err := f.Call(callArgs)
				if err != nil {
					return engine.Str(err.Error()), nil
				}
				return result, nil
			}
			return engine.Undefined(), nil
		}), nil
	}))

	_ = m.Set("deprecate", engine.NewFunction("deprecate", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		// 简化：返回原函数（不打印 deprecation 警告）。
		return args[0], nil
	}))

	_ = m.Set("callbackify", engine.NewFunction("callbackify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		original := args[0]
		return engine.NewFunction("callbackified", func(callArgs []engine.Value) (engine.Value, error) {
			if f, ok := original.AsFunction(); ok {
				result, err := f.Call(callArgs)
				if err != nil {
					return engine.Undefined(), err
				}
				return result, nil
			}
			return engine.Undefined(), nil
		}), nil
	}))

	_ = m.Set("isDeepStrictEqual", engine.NewFunction("isDeepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(deepStrictEqual(args[0], args[1])), nil
	}))

	_ = m.Set("styleText", engine.NewFunction("styleText", func(args []engine.Value) (engine.Value, error) {
		// 简化：忽略颜色，直接返回文本。
		if len(args) < 2 {
			return engine.Str(""), nil
		}
		return engine.Str(args[1].String()), nil
	}))

	// util.parseArgs(config)：CLI 参数解析（Node 22 稳定语义，按
	// node v22.x lib/internal/util/parse_args 移植）。
	_ = m.Set("parseArgs", engine.NewFunction("parseArgs", func(args []engine.Value) (engine.Value, error) {
		return parseArgsImpl(ctx, args)
	}))

	_ = m.Set("stripVTControlCharacters", engine.NewFunction("stripVTControlCharacters", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(stripVTControlCharacters(args[0].String())), nil
	}))

	// util.types 子对象
	types := engine.NewObject()
	registerUtilTypes(types)
	_ = m.Set("types", types)

	// inspect.defaultOptions
	inspectOpts := engine.NewObject()
	_ = inspectOpts.Set("depth", engine.Number(2))
	_ = inspectOpts.Set("colors", engine.Boolean(false))
	_ = m.Set("defaultOptions", inspectOpts)

	return m, nil
}

// NewUtilTypes constructs the standalone node:util/types module. Node exposes
// the same predicates both here and as util.types.
func NewUtilTypes(ctx engine.Context) (engine.Value, error) {
	types := engine.NewObject()
	registerUtilTypes(types)
	return types, nil
}

// --- util.parseArgs（Node 22 语义，按 node v22.x lib/internal/util/parse_args 移植）---

type parseArgsToken struct {
	kind        string // option | positional | option-terminator
	name        string
	rawName     string
	index       int
	value       engine.Value
	hasValue    bool
	inlineValue bool
}

type parseArgsOpt struct {
	typ      string // string | boolean
	short    string
	multiple bool
	hasDef   bool
	def      engine.Value
}

func parseArgsError(code, msg string) error {
	return &codedError{err: fmt.Errorf("%w: %s", engine.ErrTypeError, msg), code: code}
}

// parseArgsImpl 实现 util.parseArgs（Node 22 稳定语义）。
func parseArgsImpl(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	var config engine.Object
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			config = o
		}
	}
	getCfg := func(name string) engine.Value {
		if config == nil {
			return engine.Undefined()
		}
		v, _ := config.Get(name)
		return v
	}
	// ?? 语义：undefined 或缺省 → 默认值。
	cfgBool := func(name string, def bool) bool {
		v := getCfg(name)
		if v.IsUndefined() {
			return def
		}
		if b, ok := v.Bool(); ok {
			return b
		}
		return def
	}
	strict := cfgBool("strict", true)
	allowPositionals := cfgBool("allowPositionals", !strict)
	returnTokens := cfgBool("tokens", false)
	allowNegative := cfgBool("allowNegative", false)

	// args：默认 process.argv.slice(2)。
	var argsArr []engine.Value
	if v := getCfg("args"); !v.IsUndefined() {
		if av, ok := v.(*engine.ArrayValue); ok {
			for _, k := range av.Keys() {
				if k == "length" {
					continue
				}
				iv, _ := av.Get(k)
				argsArr = append(argsArr, iv)
			}
		}
	} else {
		if pv, err := ctx.Global().Get("process"); err == nil {
			if po, ok := pv.AsObject(); ok {
				if argv, err := po.Get("argv"); err == nil {
					if arr, ok := argv.(*engine.ArrayValue); ok {
						for _, k := range arr.Keys() {
							if k == "length" {
								continue
							}
							idx, aerr := strconv.Atoi(k)
							if aerr != nil || idx < 2 {
								continue
							}
							iv, _ := arr.Get(k)
							argsArr = append(argsArr, iv)
						}
					}
				}
			}
		}
	}

	// options 配置表。
	opts := map[string]parseArgsOpt{}
	if v := getCfg("options"); !v.IsUndefined() {
		if oo, ok := v.AsObject(); ok {
			for _, k := range oo.Keys() {
				if k == "__proto__" {
					continue
				}
				o := parseArgsOpt{typ: "boolean"}
				if oc, ok2 := oo.Get(k); ok2 == nil {
					if co, ok3 := oc.AsObject(); ok3 {
						if tv, e := co.Get("type"); e == nil && tv.Type() == engine.TypeString {
							o.typ = tv.String()
						}
						if sv, e := co.Get("short"); e == nil && sv.Type() == engine.TypeString {
							o.short = sv.String()
						}
						if mv, e := co.Get("multiple"); e == nil {
							if mb, ok4 := mv.Bool(); ok4 {
								o.multiple = mb
							}
						}
						if dv, e := co.Get("default"); e == nil && !dv.IsUndefined() {
							o.hasDef = true
							o.def = dv
						}
					}
				}
				opts[k] = o
			}
		}
	}

	tokens := parseArgsToTokens(argsArr, opts)

	// Phase 2：处理 tokens。
	values := engine.NewObject() // null-prototype（Node 语义）
	var positionals []engine.Value
	for _, t := range tokens {
		switch t.kind {
		case "option":
			if strict {
				if err := parseCheckOptionUsage(t, opts, allowNegative, allowPositionals); err != nil {
					return engine.Undefined(), err
				}
			}
			storeParseOption(t, opts, values, allowNegative)
		case "positional":
			if !allowPositionals {
				return engine.Undefined(), parseArgsError("ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL",
					fmt.Sprintf("Unexpected argument '%s'. This command does not take positional arguments", t.value.String()))
			}
			positionals = append(positionals, t.value)
		}
	}

	// Phase 3：默认值。
	for name, o := range opts {
		if o.hasDef {
			if v, err := values.Get(name); err == nil && v.IsUndefined() {
				_ = values.Set(name, o.def)
			}
		}
	}

	result := engine.NewObject()
	_ = result.Set("values", values)
	posArr := engine.NewArray(positionals)
	_ = result.Set("positionals", posArr)
	if returnTokens {
		tokArr := engine.NewArray(nil)
		for i, t := range tokens {
			tokObj := engine.NewObject()
			_ = tokObj.Set("kind", engine.Str(t.kind))
			switch t.kind {
			case "option":
				_ = tokObj.Set("name", engine.Str(t.name))
				_ = tokObj.Set("rawName", engine.Str(t.rawName))
				_ = tokObj.Set("index", engine.Number(float64(t.index)))
				if t.hasValue {
					_ = tokObj.Set("value", t.value)
				}
				_ = tokObj.Set("inlineValue", engine.Boolean(t.inlineValue))
			case "positional":
				_ = tokObj.Set("index", engine.Number(float64(t.index)))
				_ = tokObj.Set("value", t.value)
			case "option-terminator":
				_ = tokObj.Set("index", engine.Number(float64(t.index)))
			}
			_ = tokArr.Set(strconv.Itoa(i), tokObj)
		}
		_ = result.Set("tokens", tokArr)
	}
	return result, nil
}

// parseFindLongForShort 查找 short 对应的 long 选项名；未配置则返回 short 本身。
func parseFindLongForShort(short string, opts map[string]parseArgsOpt) string {
	for name, o := range opts {
		if o.short == short {
			return name
		}
	}
	return short
}

// parseArgsToTokens 分词（argsToTokens 移植）。
func parseArgsToTokens(args []engine.Value, opts map[string]parseArgsOpt) []parseArgsToken {
	var tokens []parseArgsToken
	remaining := append([]engine.Value{}, args...)
	index := -1
	groupCount := 0

	for len(remaining) > 0 {
		arg := remaining[0]
		remaining = remaining[1:]
		var nextArg engine.Value = engine.Undefined()
		if len(remaining) > 0 {
			nextArg = remaining[0]
		}
		if groupCount > 0 {
			groupCount--
		} else {
			index++
		}
		argStr := arg.String()

		// 裸 '--'：其后全部为 positional。
		if argStr == "--" {
			tokens = append(tokens, parseArgsToken{kind: "option-terminator", index: index})
			for _, r := range remaining {
				index++
				tokens = append(tokens, parseArgsToken{kind: "positional", index: index, value: r})
			}
			break
		}

		// isLoneShortOption：'-f'。
		if len(argStr) == 2 && argStr[0] == '-' && argStr[1] != '-' {
			short := argStr[1:2]
			long := parseFindLongForShort(short, opts)
			var value engine.Value = engine.Undefined()
			hasValue := false
			if o, ok := opts[long]; ok && o.typ == "string" && !nextArg.IsUndefined() && !nextArg.IsNull() {
				value = nextArg
				hasValue = true
				remaining = remaining[1:]
			}
			tokens = append(tokens, parseArgsToken{kind: "option", name: long, rawName: argStr,
				index: index, value: value, hasValue: hasValue, inlineValue: false})
			if hasValue {
				index++
			}
			continue
		}

		// isShortOptionGroup：'-abc' 展开（首个 short 不是 string 类型）。
		if len(argStr) > 2 && argStr[0] == '-' && argStr[1] != '-' {
			first := argStr[1:2]
			long := parseFindLongForShort(first, opts)
			if o, ok := opts[long]; !ok || o.typ != "string" {
				var expanded []string
				for i := 1; i < len(argStr); i++ {
					c := argStr[i : i+1]
					l := parseFindLongForShort(c, opts)
					if oo, ok2 := opts[l]; ok2 && oo.typ == "string" && i != len(argStr)-1 {
						expanded = append(expanded, "-"+argStr[i:])
						break
					}
					expanded = append(expanded, "-"+c)
				}
				newRemaining := make([]engine.Value, 0, len(expanded)+len(remaining))
				for _, e := range expanded {
					newRemaining = append(newRemaining, engine.Str(e))
				}
				remaining = append(newRemaining, remaining...)
				groupCount = len(expanded)
				continue
			}
		}

		// isShortOptionAndValue：'-fFILE'。
		if len(argStr) > 2 && argStr[0] == '-' && argStr[1] != '-' {
			short := argStr[1:2]
			long := parseFindLongForShort(short, opts)
			if o, ok := opts[long]; ok && o.typ == "string" {
				tokens = append(tokens, parseArgsToken{kind: "option", name: long, rawName: "-" + short,
					index: index, value: engine.Str(argStr[2:]), hasValue: true, inlineValue: true})
				continue
			}
		}

		// isLoneLongOption：'--foo'。
		if len(argStr) > 2 && strings.HasPrefix(argStr, "--") && !strings.Contains(argStr[3:], "=") {
			long := argStr[2:]
			var value engine.Value = engine.Undefined()
			hasValue := false
			if o, ok := opts[long]; ok && o.typ == "string" && !nextArg.IsUndefined() && !nextArg.IsNull() {
				value = nextArg
				hasValue = true
				remaining = remaining[1:]
			}
			tokens = append(tokens, parseArgsToken{kind: "option", name: long, rawName: argStr,
				index: index, value: value, hasValue: hasValue, inlineValue: false})
			if hasValue {
				index++
			}
			continue
		}

		// isLongOptionAndValue：'--foo=bar'。
		if len(argStr) > 2 && strings.HasPrefix(argStr, "--") && strings.Contains(argStr[3:], "=") {
			eq := strings.Index(argStr[3:], "=") + 3
			long := argStr[2:eq]
			tokens = append(tokens, parseArgsToken{kind: "option", name: long, rawName: "--" + long,
				index: index, value: engine.Str(argStr[eq+1:]), hasValue: true, inlineValue: true})
			continue
		}

		tokens = append(tokens, parseArgsToken{kind: "positional", index: index, value: arg})
	}
	return tokens
}

// parseCheckOptionUsage strict 模式下的用法校验（checkOptionUsage +
// checkOptionLikeValue 移植）。
func parseCheckOptionUsage(t parseArgsToken, opts map[string]parseArgsOpt, allowNegative, allowPositionals bool) error {
	tokName := t.name
	if _, ok := opts[tokName]; !ok {
		if allowNegative && strings.HasPrefix(tokName, "no-") {
			base := tokName[3:]
			if o, ok := opts[base]; !ok || o.typ != "boolean" {
				return parseArgsError("ERR_PARSE_ARGS_UNKNOWN_OPTION", parseUnknownOptionMsg(t.rawName, allowPositionals))
			}
		} else {
			return parseArgsError("ERR_PARSE_ARGS_UNKNOWN_OPTION", parseUnknownOptionMsg(t.rawName, allowPositionals))
		}
	}
	o, _ := opts[tokName]
	shortAndLong := "--" + tokName
	if o.short != "" {
		shortAndLong = "-" + o.short + ", --" + tokName
	}
	if o.typ == "string" && !t.hasValue {
		return parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE",
			fmt.Sprintf("Option '%s <value>' argument missing", shortAndLong))
	}
	if o.typ == "boolean" && t.hasValue {
		return parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE",
			fmt.Sprintf("Option '%s' does not take an argument", shortAndLong))
	}
	// checkOptionLikeValue：--port --verbose 类歧义。
	if !t.inlineValue && t.hasValue {
		vs := t.value.String()
		if len(vs) > 1 && vs[0] == '-' {
			example := fmt.Sprintf("'%s=-XYZ'", t.rawName)
			if !strings.HasPrefix(t.rawName, "--") {
				example = fmt.Sprintf("'--%s=-XYZ' or '%s-XYZ'", t.name, t.rawName)
			}
			return parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE", fmt.Sprintf(
				"Option '%s' argument is ambiguous.\nDid you forget to specify the option argument for '%s'?\nTo specify an option argument starting with a dash use %s.",
				t.rawName, t.rawName, example))
		}
	}
	return nil
}

func parseUnknownOptionMsg(option string, allowPositionals bool) string {
	if allowPositionals {
		return fmt.Sprintf("Unknown option '%s'. To specify a positional argument starting with a '-', place it at the end of the command after '--', as in '-- \"%s\"", option, option)
	}
	return fmt.Sprintf("Unknown option '%s'", option)
}

// storeParseOption 存储选项值（storeOption 移植）。
func storeParseOption(t parseArgsToken, opts map[string]parseArgsOpt, values engine.Object, allowNegative bool) {
	longName := t.name
	val := engine.Undefined()
	if t.hasValue {
		val = t.value
	}
	if longName == "__proto__" {
		return
	}
	if allowNegative && strings.HasPrefix(longName, "no-") && !t.hasValue {
		// --no-foo → foo=false。
		longName = longName[3:]
		val = engine.Boolean(false)
	}
	newVal := val
	if newVal.IsUndefined() {
		newVal = engine.Boolean(true)
	}
	if o, ok := opts[longName]; ok && o.multiple {
		if existing, err := values.Get(longName); err == nil && !existing.IsUndefined() {
			if arr, ok := existing.(*engine.ArrayValue); ok {
				_ = arr.Set(strconv.Itoa(len(arr.Keys())-1), newVal)
			}
		} else {
			arr := engine.NewArray([]engine.Value{newVal})
			_ = values.Set(longName, arr)
		}
	} else {
		_ = values.Set(longName, newVal)
	}
}

func debugSectionEnabled(section, setting string) bool {
	for _, pattern := range strings.FieldsFunc(setting, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		matched, err := filepath.Match(strings.ToLower(pattern), section)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// utilFormat 实现 Node.js util.format 的占位符替换。
func utilFormat(args []engine.Value) string {
	if len(args) == 0 {
		return ""
	}
	format := args[0].String()
	if !strings.Contains(format, "%") {
		// 无占位符：用空格连接所有参数。
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		return strings.Join(parts, " ")
	}
	var b strings.Builder
	argIdx := 1
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) {
			switch format[i+1] {
			case 's':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case 'd':
				if argIdx < len(args) {
					if n, ok := args[argIdx].Int(); ok {
						b.WriteString(fmt.Sprintf("%d", n))
					} else if f, ok := args[argIdx].Float(); ok {
						b.WriteString(fmt.Sprintf("%g", f))
					} else {
						b.WriteString(args[argIdx].String())
					}
					argIdx++
				}
				i++
			case 'j':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case 'o', 'O':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case '%':
				b.WriteByte('%')
				i++
			default:
				b.WriteByte(format[i])
			}
		} else {
			b.WriteByte(format[i])
		}
	}
	// 剩余参数用空格追加
	for ; argIdx < len(args); argIdx++ {
		b.WriteString(" ")
		b.WriteString(args[argIdx].String())
	}
	return b.String()
}

// registerUtilTypes 注册 util.types 子对象的方法。
func registerUtilTypes(types engine.Object) {
	typeCheck := func(name string, pred func(engine.Value) bool) {
		_ = types.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Boolean(false), nil
			}
			return engine.Boolean(pred(args[0])), nil
		}))
	}

	typeCheck("isPromise", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Promise")
	})
	typeCheck("isArray", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.HasPrefix(v.String(), "[")
	})
	typeCheck("isMap", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Map")
	})
	typeCheck("isSet", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Set")
	})
	typeCheck("isRegExp", func(v engine.Value) bool {
		if v.Type() != engine.TypeObject {
			return false
		}
		// RegExp 实例 String() 为 "/source/flags" 且带可写 lastIndex 自有属性。
		if !strings.HasPrefix(v.String(), "/") {
			return false
		}
		if o, ok := v.AsObject(); ok {
			if li, _ := o.Get("lastIndex"); !li.IsUndefined() {
				return true
			}
		}
		return false
	})
	typeCheck("isDate", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Date")
	})
	typeCheck("isError", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Error")
	})
	typeCheck("isBoolean", func(v engine.Value) bool { return v.Type() == engine.TypeBoolean })
	typeCheck("isNumber", func(v engine.Value) bool { return v.Type() == engine.TypeNumber })
	typeCheck("isString", func(v engine.Value) bool { return v.Type() == engine.TypeString })
	typeCheck("isSymbol", func(v engine.Value) bool { return v.Type() == engine.TypeSymbol })
	typeCheck("isBigInt", func(v engine.Value) bool { return v.Type() == engine.TypeBigInt })
	typeCheck("isFunction", func(v engine.Value) bool { return v.Type() == engine.TypeFunction })
	typeCheck("isObject", func(v engine.Value) bool { return v.Type() == engine.TypeObject })
	typeCheck("isNull", func(v engine.Value) bool { return v.IsNull() })
	typeCheck("isUndefined", func(v engine.Value) bool { return v.IsUndefined() })
	typeCheck("isPrimitive", func(v engine.Value) bool {
		t := v.Type()
		return t != engine.TypeObject && t != engine.TypeFunction
	})
	typeCheck("isArrayBuffer", func(v engine.Value) bool {
		_, ok := engine.AsArrayBufferValue(v)
		return ok
	})
	typeCheck("isSharedArrayBuffer", func(v engine.Value) bool { return false })
	typeCheck("isAnyArrayBuffer", func(v engine.Value) bool {
		_, ok := engine.AsArrayBufferValue(v)
		return ok
	})
	typeCheck("isTypedArray", func(v engine.Value) bool {
		if _, ok := engine.AsBuffer(v); ok {
			return true
		}
		_, ok := engine.AsTypedArray(v)
		return ok
	})
	typeCheck("isUint8Array", func(v engine.Value) bool {
		if _, ok := engine.AsBuffer(v); ok {
			return true
		}
		typed, ok := engine.AsTypedArray(v)
		return ok && typed.Kind() == engine.KindUint8
	})
	typeCheck("isArrayBufferView", func(v engine.Value) bool {
		if _, ok := engine.AsBuffer(v); ok {
			return true
		}
		if _, ok := engine.AsTypedArray(v); ok {
			return true
		}
		_, ok := engine.AsDataView(v)
		return ok
	})
	typeCheck("isDataView", func(v engine.Value) bool {
		_, ok := engine.AsDataView(v)
		return ok
	})
}

var ansiEscapePattern = regexp.MustCompile(`[\x1b\x9b](?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[P^_][^\x07\x1b]*(?:\x07|\x1b\\))`)

func stripVTControlCharacters(str string) string {
	return ansiEscapePattern.ReplaceAllString(str, "")
}
