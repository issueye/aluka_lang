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

func (r *RegexpValue) SetProto(proto engine.Object) { engine.SetProto(r.obj, proto) }
func (r *RegexpValue) Proto() engine.Object         { return engine.GetProto(r.obj) }

func (r *RegexpValue) Type() engine.ValueType              { return engine.TypeObject }
func (r *RegexpValue) String() string                      { return "/" + escapeRegExpSource(r.source) + "/" + r.flags }
func (r *RegexpValue) Int() (int, bool)                    { return 0, false }
func (r *RegexpValue) Float() (float64, bool)              { return 0, false }
func (r *RegexpValue) Bool() (bool, bool)                  { return true, true }
func (r *RegexpValue) IsUndefined() bool                   { return false }
func (r *RegexpValue) IsNull() bool                        { return false }
func (r *RegexpValue) IsObject() bool                      { return true }
func (r *RegexpValue) IsFunction() bool                    { return false }
func (r *RegexpValue) AsObject() (engine.Object, bool)     { return r, true }
func (r *RegexpValue) AsFunction() (engine.Function, bool) { return nil, false }

func (r *RegexpValue) Get(key string) (engine.Value, error) { return r.obj.Get(key) }
func (r *RegexpValue) Set(key string, v engine.Value) error { return r.obj.Set(key, v) }
func (r *RegexpValue) Keys() []string                       { return r.obj.Keys() }
func (r *RegexpValue) Delete(key string) bool               { return r.obj.Delete(key) }
func (r *RegexpValue) UnwrapObject() engine.Object          { return r.obj }

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

// matchIndex 在 str 中执行匹配并返回 UTF-16 索引数组（整体 + 捕获组）。
// 遵循 g/y 的 lastIndex 语义：命中后更新，失败时重置为 0。直接 exec/test
// 命中零宽匹配时不主动推进；需要循环的上层协议负责 AdvanceStringIndex。
func (r *RegexpValue) matchIndex(str string) ([]int, bool, error) {
	f := r.compiled.Flags
	if !f.Global && !f.Sticky {
		m, err := r.compiled.Exec(str)
		return m, m != nil, regexpExecError(err)
	}
	li := r.lastIndex()
	if li < 0 {
		li = 0
	}
	if li > regex.UTF16Index(str, len(str)) {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false, nil
	}
	m, err := r.compiled.ExecAt(str, li)
	if err != nil {
		return nil, false, regexpExecError(err)
	}
	if m == nil {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false, nil
	}
	if f.Sticky && m[0] != li {
		_ = r.obj.Set("lastIndex", engine.IntValue(0))
		return nil, false, nil
	}
	_ = r.obj.Set("lastIndex", engine.IntValue(m[1]))
	return m, true, nil
}

func regexpExecError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", engine.ErrRangeError, err)
}

// execString 执行匹配并构造 RegExp.exec 的结果数组。
func (r *RegexpValue) execString(str string) (engine.Value, error) {
	m, ok, err := r.matchIndex(str)
	if err != nil {
		return engine.Undefined(), err
	}
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
			v = engine.Str(regex.UTF16Slice(str, m[i], m[i+1]))
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

// execStringAtMatch 由 Matches 的第 i 个匹配构造 exec 结果数组。
func (r *RegexpValue) execStringAtMatch(mt *regex.Matches, i int) (engine.Value, error) {
	u := mt.U16[i]
	elems := make([]engine.Value, 0, len(u)/2)
	var groups map[string]engine.Value
	for j := 0; j+1 < len(u); j += 2 {
		var v engine.Value
		if u[j] < 0 {
			v = engine.Undefined()
		} else {
			v = engine.Str(mt.Slice(i, j))
		}
		elems = append(elems, v)
		if j > 0 {
			if name := r.compiled.GroupName(j / 2); name != "" {
				if groups == nil {
					groups = make(map[string]engine.Value)
				}
				groups[name] = v
			}
		}
	}
	result := engine.NewArray(elems)
	engine.SetProto(result, r.interp.arrayProto)
	_ = result.Set("index", engine.IntValue(u[0]))
	_ = result.Set("input", engine.Str(mt.Source()))
	if groups != nil {
		_ = result.Set("groups", engine.NewObjectFrom(groups))
	} else {
		_ = result.Set("groups", engine.Undefined())
	}
	return result, nil
}

// testString 返回是否有匹配（与 exec 相同的 lastIndex 语义）。
func (r *RegexpValue) testString(str string) (bool, error) {
	_, ok, err := r.matchIndex(str)
	return ok, err
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
		matched, err := r.testString(str)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(matched), nil
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
		mt, execErr := r.compiled.AllMatches(str)
		if execErr != nil {
			return engine.Undefined(), regexpExecError(execErr)
		}
		if mt.Len() == 0 {
			return engine.Null(), nil
		}
		elems := make([]engine.Value, 0, mt.Len())
		for i := 0; i < mt.Len(); i++ {
			elems = append(elems, engine.Str(mt.Slice(i, 0)))
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
		m, execErr := r.compiled.Exec(str)
		if execErr != nil {
			return engine.Undefined(), regexpExecError(execErr)
		}
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
// 切片经 Matches 完成（O(len(str))）；offset 参数取 UTF-16 索引（JS 规范值）。
func regexpReplace(interp *Interpreter, r *RegexpValue, str string, replacement engine.Value, global bool) (engine.Value, error) {
	var mt *regex.Matches
	if global {
		var err error
		mt, err = r.compiled.AllMatches(str)
		if err != nil {
			return engine.Undefined(), regexpExecError(err)
		}
	} else {
		var ok bool
		var err error
		mt, ok, err = r.compiled.ExecSingle(str)
		if err != nil {
			return engine.Undefined(), regexpExecError(err)
		}
		if !ok {
			return engine.Str(str), nil
		}
	}
	if mt.Len() == 0 {
		return engine.Str(str), nil
	}

	// 函数替换：f(match, p1..pn, offset, string[, groups])。
	if fn, err := asCallable(replacement); err == nil {
		var b strings.Builder
		prev := -1
		for i := 0; i < mt.Len(); i++ {
			if i > 0 {
				b.WriteString(mt.Between(prev, 1, i, 0))
			} else {
				b.WriteString(mt.Head(0, 0))
			}
			u := mt.U16[i]
			args := []engine.Value{engine.Str(mt.Slice(i, 0))}
			// 捕获组从索引 2 起（[0:2] 为整体匹配）。
			for j := 2; j+1 < len(u); j += 2 {
				if u[j] < 0 {
					args = append(args, engine.Undefined())
				} else {
					args = append(args, engine.Str(mt.Slice(i, j)))
				}
			}
			args = append(args, engine.IntValue(u[0]), engine.Str(str))
			if groups := namedGroupsMatch(r, mt, i); groups != nil {
				args = append(args, groups)
			}
			v, err := fn.callWith(engine.Undefined(), args)
			if err != nil {
				return nil, err
			}
			b.WriteString(v.String())
			prev = i
		}
		b.WriteString(mt.Tail(prev, 1))
		return engine.Str(b.String()), nil
	}

	// 字符串替换：支持 $$ $& $` $' $n $nn $<name>。
	template := replacement.String()
	var b strings.Builder
	prev := -1
	for i := 0; i < mt.Len(); i++ {
		if i > 0 {
			b.WriteString(mt.Between(prev, 1, i, 0))
		} else {
			b.WriteString(mt.Head(0, 0))
		}
		b.WriteString(expandReplacementMatch(template, r, mt, i))
		prev = i
	}
	b.WriteString(mt.Tail(prev, 1))
	return engine.Str(b.String()), nil
}

// expandReplacementMatch 展开替换串中的 $ 序列（切片经 Matches，孤立代理
// 单元以 U+FFFD 呈现）。
func expandReplacementMatch(template string, r *RegexpValue, mt *regex.Matches, i int) string {
	var b strings.Builder
	for k := 0; k < len(template); k++ {
		c := template[k]
		if c != '$' || k+1 >= len(template) {
			b.WriteByte(c)
			continue
		}
		n := template[k+1]
		switch {
		case n == '$':
			b.WriteByte('$')
			k++
		case n == '&':
			b.WriteString(mt.Slice(i, 0))
			k++
		case n == '`':
			b.WriteString(mt.Head(i, 0))
			k++
		case n == '\'':
			b.WriteString(mt.Tail(i, 1))
			k++
		case n == '<':
			end := strings.IndexByte(template[k+2:], '>')
			if end < 0 {
				b.WriteString("$<")
				k++
				continue
			}
			name := template[k+2 : k+2+end]
			gi := -1
			for g := 1; g <= r.compiled.NumGroups(); g++ {
				if r.compiled.GroupName(g) == name {
					gi = 2 * g
					break
				}
			}
			if gi >= 0 && mt.U16[i][gi] >= 0 {
				b.WriteString(mt.Slice(i, gi))
			}
			k += 2 + end
		case n >= '0' && n <= '9':
			j := k + 1
			num := 0
			for j < len(template) && j-k <= 2 && template[j] >= '0' && template[j] <= '9' {
				num = num*10 + int(template[j]-'0')
				j++
			}
			if num >= 1 && num <= r.compiled.NumGroups() {
				gi := 2 * num
				if mt.U16[i][gi] >= 0 {
					b.WriteString(mt.Slice(i, gi))
				}
				k = j - 1
			} else {
				b.WriteString(template[k:j])
				k = j - 1
			}
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

// namedGroupsMatch 提取命名捕获组对象（切片经 Matches）；无命名组时返回 nil。
func namedGroupsMatch(r *RegexpValue, mt *regex.Matches, i int) engine.Value {
	var groups map[string]engine.Value
	for g := 1; g <= r.compiled.NumGroups(); g++ {
		if name := r.compiled.GroupName(g); name != "" {
			if groups == nil {
				groups = make(map[string]engine.Value)
			}
			gi := 2 * g
			if mt.U16[i][gi] >= 0 {
				groups[name] = engine.Str(mt.Slice(i, gi))
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
// 切片经 Matches 完成，全部 O(len(str))。
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
	mt, err := r.compiled.AllMatches(str)
	if err != nil {
		return engine.Undefined(), regexpExecError(err)
	}
	if mt.Len() == 0 {
		push(engine.Str(str))
		out := engine.NewArray(elems)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}
	push(engine.Str(mt.Head(0, 0)))
	prev := 0
	for i := 0; i < mt.Len(); i++ {
		if i > 0 {
			push(engine.Str(mt.Between(prev, 1, i, 0)))
		}
		// 捕获组从索引 2 起（[0:2] 为整体匹配）。
		u := mt.U16[i]
		for j := 2; j+1 < len(u); j += 2 {
			if u[j] >= 0 {
				push(engine.Str(mt.Slice(i, j)))
			}
		}
		prev = i
	}
	push(engine.Str(mt.Tail(prev, 1)))
	out := engine.NewArray(elems)
	engine.SetProto(out, interp.arrayProto)
	return out, nil
}
