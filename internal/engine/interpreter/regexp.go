// RegExp 内置对象实现（JS RegExp → Go regexp 翻译层内核）。
//
// 设计遵循 MapValue/PromiseValue 惯例：专用 Go struct 包装一个 backing
// engine.Object（原型链挂在 backing 上），实例的 lastIndex 作为可写数据属性
// 存在 backing 上，exec/test 按 g/y 标志驱动 lastIndex 状态机。
package interpreter

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/regex"
)

// RegexpValue 是 JS RegExp 实例。
type RegexpValue struct {
	obj      engine.Object
	interp   *Interpreter
	compiled *regex.Compiled
	source   string // 原始 pattern（未经规范化，保留 \/ 原文）
	flags    string // 规范顺序的 flags 字符串
}

// NewRegexpValue 创建绑定 regexpProto 的 RegExp 实例。
func NewRegexpValue(interp *Interpreter, compiled *regex.Compiled, source, flags string) *RegexpValue {
	obj := engine.NewObject()
	engine.SetProto(obj, interp.regexpProto)
	r := &RegexpValue{obj: obj, interp: interp, compiled: compiled, source: source, flags: flags}
	_ = obj.Set("lastIndex", engine.IntValue(0))
	return r
}

func (r *RegexpValue) SetProto(proto engine.Object)     { engine.SetProto(r.obj, proto) }
func (r *RegexpValue) Proto() engine.Object             { return engine.GetProto(r.obj) }

func (r *RegexpValue) Type() engine.ValueType             { return engine.TypeObject }
func (r *RegexpValue) String() string                     { return "/" + escapeRegExpSource(r.source) + "/" + r.flags }
func (r *RegexpValue) Int() (int, bool)                   { return 0, false }
func (r *RegexpValue) Float() (float64, bool)             { return 0, false }
func (r *RegexpValue) Bool() (bool, bool)                 { return true, true }
func (r *RegexpValue) IsUndefined() bool                  { return false }
func (r *RegexpValue) IsNull() bool                       { return false }
func (r *RegexpValue) IsObject() bool                     { return true }
func (r *RegexpValue) IsFunction() bool                   { return false }
func (r *RegexpValue) AsObject() (engine.Object, bool)    { return r, true }
func (r *RegexpValue) AsFunction() (engine.Function, bool) { return nil, false }

func (r *RegexpValue) Get(key string) (engine.Value, error) { return r.obj.Get(key) }
func (r *RegexpValue) Set(key string, v engine.Value) error { return r.obj.Set(key, v) }
func (r *RegexpValue) Keys() []string                       { return r.obj.Keys() }
func (r *RegexpValue) Delete(key string) bool               { return r.obj.Delete(key) }

// isRegexpValue 判断一个值是否为 RegExp 实例。
func isRegexpValue(v engine.Value) bool {
	_, ok := v.(*RegexpValue)
	return ok
}

// makeRegexp 编译 source/flags 并创建 RegExp 实例。编译失败返回 SyntaxError。
func (interp *Interpreter) makeRegexp(source, flags string) (engine.Value, error) {
	c, err := regex.Compile(source, flags)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrSyntaxError, err)
	}
	return NewRegexpValue(interp, c, source, c.Flags.String()), nil
}

// lastIndex 读取实例的 lastIndex（非数字视为 0）。
func (r *RegexpValue) lastIndex() int {
	v, _ := r.obj.Get("lastIndex")
	if n, ok := v.Float(); ok {
		return int(n)
	}
	return 0
}

// matchIndex 在 str 中执行匹配并返回 Go 索引数组（整体 + 捕获组）。
// 遵循 g/y 的 lastIndex 语义：命中后更新 lastIndex，失败时重置为 0。
func (r *RegexpValue) matchIndex(str string) ([]int, bool) {
	f := r.compiled.Flags
	if !f.Global && !f.Sticky {
		m := r.compiled.MatchIndex(str)
		return m, m != nil
	}
	li := r.lastIndex()
	if li < 0 {
		li = 0
	}
	if li > len(str) {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false
	}
	m := r.compiled.MatchIndex(str[li:])
	if m == nil {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false
	}
	// sticky：匹配必须从 lastIndex 开始。
	if f.Sticky && m[0] != 0 {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false
	}
	// 索引偏移回原串。
	for j := range m {
		if m[j] >= 0 {
			m[j] += li
		}
	}
	// 更新 lastIndex；零宽匹配前进一个字符避免死循环。
	end := m[1]
	if end == m[0] {
		end++
	}
	_ = r.obj.Set("lastIndex", engine.IntValue(end))
	return m, true
}

// execString 执行匹配并构造 RegExp.exec 的结果数组。
func (r *RegexpValue) execString(str string) (engine.Value, error) {
	m, ok := r.matchIndex(str)
	if !ok {
		return engine.Null(), nil
	}
	return r.execStringAt(str, m)
}

// execStringAt 由给定的匹配索引数组构造 exec 结果数组（不产生 lastIndex 副作用）。
func (r *RegexpValue) execStringAt(str string, m []int) (engine.Value, error) {
	elems := make([]engine.Value, 0, len(m)/2)
	var groups map[string]engine.Value
	for i := 0; i+1 < len(m); i += 2 {
		var v engine.Value
		if m[i] < 0 {
			v = engine.Undefined()
		} else {
			v = engine.Str(str[m[i]:m[i+1]])
		}
		elems = append(elems, v)
		if i > 0 {
			if name := r.compiled.GroupName(i / 2); name != "" {
				if groups == nil {
					groups = make(map[string]engine.Value)
				}
				groups[name] = v
			}
		}
	}
	result := engine.NewArray(elems)
	engine.SetProto(result, r.interp.arrayProto)
	_ = result.Set("index", engine.IntValue(m[0]))
	_ = result.Set("input", engine.Str(str))
	if groups != nil {
		_ = result.Set("groups", engine.NewObjectFrom(groups))
	} else {
		_ = result.Set("groups", engine.Undefined())
	}
	return result, nil
}

// testString 返回是否有匹配（与 exec 相同的 lastIndex 语义）。
func (r *RegexpValue) testString(str string) bool {
	_, ok := r.matchIndex(str)
	return ok
}

// escapeRegExpSource 转义 source 中未转义的 '/' 为 '\/'（RegExp.prototype.source）。
// 空 pattern 显示为 (?:)。
func escapeRegExpSource(src string) string {
	if src == "" {
		return "(?:)"
	}
	var b strings.Builder
	backslashes := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\\' {
			backslashes++
			b.WriteByte(c)
			continue
		}
		if c == '/' && backslashes%2 == 0 {
			b.WriteString(`\/`)
		} else {
			b.WriteByte(c)
		}
		backslashes = 0
	}
	return b.String()
}

// setupRegexp 注册 RegExp 构造器、原型与全局对象。
func (interp *Interpreter) setupRegexp() {
	p := engine.NewObject()
	engine.SetProto(p, interp.objectProto)
	interp.regexpProto = p

	notRegexp := func() error {
		return fmt.Errorf("%w: RegExp.prototype method called on non-RegExp", engine.ErrTypeError)
	}
	getRegexp := func(this engine.Value) (*RegexpValue, error) {
		r, ok := this.(*RegexpValue)
		if !ok {
			return nil, notRegexp()
		}
		return r, nil
	}

	// --- 访问器属性 ---
	engine.UpdateAccessor(p, "source", true, interp.nativeMethod("source", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(escapeRegExpSource(r.source)), nil
	}))
	engine.UpdateAccessor(p, "flags", true, interp.nativeMethod("flags", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(r.flags), nil
	}))
	engine.UpdateAccessor(p, "global", true, interp.nativeMethod("global", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.Global), nil
	}))
	engine.UpdateAccessor(p, "ignoreCase", true, interp.nativeMethod("ignoreCase", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.IgnoreCase), nil
	}))
	engine.UpdateAccessor(p, "multiline", true, interp.nativeMethod("multiline", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.Multiline), nil
	}))
	engine.UpdateAccessor(p, "dotAll", true, interp.nativeMethod("dotAll", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.DotAll), nil
	}))
	engine.UpdateAccessor(p, "unicode", true, interp.nativeMethod("unicode", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.Unicode), nil
	}))
	engine.UpdateAccessor(p, "sticky", true, interp.nativeMethod("sticky", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(r.compiled.Flags.Sticky), nil
	}))

	// --- 原型方法 ---
	_ = p.Set("exec", interp.nativeMethod("exec", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		return r.execString(str)
	}))
	_ = p.Set("test", interp.nativeMethod("test", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		return engine.Boolean(r.testString(str)), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str("/" + escapeRegExpSource(r.source) + "/" + r.flags), nil
	}))

	// [Symbol.species] getter：返回构造器。
	engine.UpdateAccessor(p, engine.SymbolSpecies.SymbolKey(), true, interp.nativeMethod("get [Symbol.species]", func(this engine.Value, _ []engine.Value) (engine.Value, error) {
		return this, nil
	}))

	// [Symbol.match]
	_ = p.Set(engine.SymbolMatch.SymbolKey(), interp.nativeMethod("match", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		if !r.compiled.Flags.Global {
			return r.execString(str)
		}
		matches := r.compiled.MatchAllIndex(str)
		if len(matches) == 0 {
			return engine.Null(), nil
		}
		elems := make([]engine.Value, 0, len(matches))
		for _, m := range matches {
			elems = append(elems, engine.Str(str[m[0]:m[1]]))
		}
		out := engine.NewArray(elems)
		engine.SetProto(out, r.interp.arrayProto)
		return out, nil
	}))
	// [Symbol.replace]
	_ = p.Set(engine.SymbolReplace.SymbolKey(), interp.nativeMethod("replace", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		if len(args) < 2 {
			return engine.Str(str), nil
		}
		return regexpReplace(r.interp, r, str, args[1], r.compiled.Flags.Global)
	}))
	// [Symbol.search]
	_ = p.Set(engine.SymbolSearch.SymbolKey(), interp.nativeMethod("search", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		m := r.compiled.MatchIndex(str)
		if m == nil {
			return engine.IntValue(-1), nil
		}
		return engine.IntValue(m[0]), nil
	}))
	// [Symbol.split]
	_ = p.Set(engine.SymbolSplit.SymbolKey(), interp.nativeMethod("split", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		r, err := getRegexp(this)
		if err != nil {
			return engine.Undefined(), err
		}
		str := ""
		if len(args) > 0 {
			str = args[0].String()
		}
		limit := -1
		if len(args) > 1 {
			if n, ok := args[1].Float(); ok {
				limit = int(n)
			}
		}
		return regexpSplit(r.interp, r, str, limit)
	}))

	// --- 构造器 ---
	ctor := interp.makeFunc("RegExp", func(args []engine.Value) (engine.Value, error) {
		var pattern, flagsStr string
		if len(args) > 0 && !args[0].IsUndefined() {
			if rv, ok := args[0].(*RegexpValue); ok {
				pattern = rv.source
				if len(args) > 1 && !args[1].IsUndefined() {
					flagsStr = args[1].String()
				} else {
					flagsStr = rv.flags
				}
				return interp.makeRegexp(pattern, flagsStr)
			}
			pattern = args[0].String()
		}
		if len(args) > 1 && !args[1].IsUndefined() {
			flagsStr = args[1].String()
		}
		return interp.makeRegexp(pattern, flagsStr)
	})
	_ = ctor.Set("prototype", p)
	_ = p.Set("constructor", ctor)
	_ = interp.globalObj.Set("RegExp", ctor)
	interp.constructors["RegExp"] = ctor
}

// regexpReplace 用正则 r 对 str 执行替换。replacement 可为替换串（含 $ 替换）
// 或函数。global 为 true 时替换全部匹配。
func regexpReplace(interp *Interpreter, r *RegexpValue, str string, replacement engine.Value, global bool) (engine.Value, error) {
	var matches [][]int
	if global {
		matches = r.compiled.MatchAllIndex(str)
	} else {
		if m := r.compiled.MatchIndex(str); m != nil {
			matches = [][]int{m}
		}
	}
	if len(matches) == 0 {
		return engine.Str(str), nil
	}

	// 函数替换：f(match, p1..pn, offset, string[, groups])。
	if fn, err := asCallable(replacement); err == nil {
		var b strings.Builder
		last := 0
		for _, m := range matches {
			b.WriteString(str[last:m[0]])
			args := []engine.Value{engine.Str(str[m[0]:m[1]])}
			// 捕获组从索引 2 起（m[0:2] 为整体匹配）。
			for i := 2; i+1 < len(m); i += 2 {
				if m[i] < 0 {
					args = append(args, engine.Undefined())
				} else {
					args = append(args, engine.Str(str[m[i]:m[i+1]]))
				}
			}
			args = append(args, engine.IntValue(m[0]), engine.Str(str))
			if groups := namedGroups(r, str, m); groups != nil {
				args = append(args, groups)
			}
			v, err := fn.callWith(engine.Undefined(), args)
			if err != nil {
				return nil, err
			}
			b.WriteString(v.String())
			last = m[1]
		}
		b.WriteString(str[last:])
		return engine.Str(b.String()), nil
	}

	// 字符串替换：支持 $$ $& $` $' $n $nn $<name>。
	template := replacement.String()
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(str[last:m[0]])
		b.WriteString(expandReplacement(template, str, m, r))
		last = m[1]
	}
	b.WriteString(str[last:])
	return engine.Str(b.String()), nil
}

// expandReplacement 展开替换串中的 $ 序列。
func expandReplacement(template, str string, m []int, r *RegexpValue) string {
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		c := template[i]
		if c != '$' || i+1 >= len(template) {
			b.WriteByte(c)
			continue
		}
		n := template[i+1]
		switch {
		case n == '$':
			b.WriteByte('$')
			i++
		case n == '&':
			b.WriteString(str[m[0]:m[1]])
			i++
		case n == '`':
			b.WriteString(str[:m[0]])
			i++
		case n == '\'':
			b.WriteString(str[m[1]:])
			i++
		case n == '<':
			end := strings.IndexByte(template[i+2:], '>')
			if end < 0 {
				b.WriteString("$<")
				i++
				continue
			}
			name := template[i+2 : i+2+end]
			gi := -1
			for g := 1; g <= r.compiled.NumGroups(); g++ {
				if r.compiled.GroupName(g) == name {
					gi = 2 * g
					break
				}
			}
			if gi >= 0 && m[gi] >= 0 {
				b.WriteString(str[m[gi]:m[gi+1]])
			}
			i += 2 + end
		case n >= '0' && n <= '9':
			j := i + 1
			num := 0
			for j < len(template) && j-i <= 2 && template[j] >= '0' && template[j] <= '9' {
				num = num*10 + int(template[j]-'0')
				j++
			}
			if num >= 1 && num <= r.compiled.NumGroups() {
				gi := 2 * num
				if m[gi] >= 0 {
					b.WriteString(str[m[gi]:m[gi+1]])
				}
				i = j - 1
			} else {
				b.WriteString(template[i:j])
				i = j - 1
			}
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

// namedGroups 提取命名捕获组对象；无命名组时返回 nil。
func namedGroups(r *RegexpValue, str string, m []int) engine.Value {
	var groups map[string]engine.Value
	for g := 1; g <= r.compiled.NumGroups(); g++ {
		if name := r.compiled.GroupName(g); name != "" {
			if groups == nil {
				groups = make(map[string]engine.Value)
			}
			gi := 2 * g
			if m[gi] >= 0 {
				groups[name] = engine.Str(str[m[gi]:m[gi+1]])
			} else {
				groups[name] = engine.Undefined()
			}
		}
	}
	if groups == nil {
		return nil
	}
	return engine.NewObjectFrom(groups)
}

// regexpSplit 用正则 r 分割 str，捕获组并入结果。limit<0 表示无限制。
func regexpSplit(interp *Interpreter, r *RegexpValue, str string, limit int) (engine.Value, error) {
	if limit == 0 {
		return engine.NewArray(nil), nil
	}
	var elems []engine.Value
	push := func(v engine.Value) {
		if limit > 0 && len(elems) >= limit {
			return
		}
		elems = append(elems, v)
	}
	matches := r.compiled.MatchAllIndex(str)
	last := 0
	for _, m := range matches {
		push(engine.Str(str[last:m[0]]))
		// 捕获组从索引 2 起（m[0:2] 为整体匹配）。
		for i := 2; i+1 < len(m); i += 2 {
			if m[i] >= 0 {
				push(engine.Str(str[m[i]:m[i+1]]))
			}
		}
		last = m[1]
	}
	push(engine.Str(str[last:]))
	out := engine.NewArray(elems)
	engine.SetProto(out, interp.arrayProto)
	return out, nil
}
