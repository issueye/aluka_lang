package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// Value 是 flag 值的抽象（对齐 stdlib flag.Value 的最小接口）。
//
// Set 解析并写入值；返回的错误消息不含前缀，由 FlagSet 统一添加前缀。
type Value interface {
	Set(string) error
	// String 返回当前值的字符串表示（帮助生成用）。
	String() string
}

// booler 由布尔类 flag 值实现：裸 --flag 形式不消费下一 token。
type booler interface {
	IsBool() bool
}

// builtinValue 标记框架内置值类型：解析错误由框架包装为
// "invalid value %q for --<name>: <err>"；自定义值的错误原样透传。
type builtinValue interface {
	builtinValue()
}

// FuncValue 以函数实现 Value，便于内联自定义解析逻辑。
type FuncValue struct {
	Fn func(string) error
}

// Set 调用注册的解析函数。
func (f FuncValue) Set(s string) error { return f.Fn(s) }

// String 实现 Value。
func (f FuncValue) String() string { return "" }

// ActionValue 是布尔类自定义值：裸 --flag 即触发动作（不消费值），
// 用于"仅开关 + 副作用"的 flag（如 --tree-shake 需联动设置两个字段）。
type ActionValue struct {
	Fn func() error
}

// Set 调用注册的动作函数（值本身被忽略）。
func (a ActionValue) Set(string) error { return a.Fn() }

// IsBool 声明为布尔类 flag。
func (a ActionValue) IsBool() bool { return true }

// String 实现 Value。
func (a ActionValue) String() string { return "" }

// Flag 描述单个 flag 的声明与解析行为。
type Flag struct {
	fs             *FlagSet
	name           string
	aliases        []string
	usage          string
	value          Value
	isBool         bool
	optional       bool   // 可选值：裸 --flag 不消费下一 token，写入 implicit
	implicit       string // optional 时裸形式写入的值
	lenientMissing bool   // 缺值时静默忽略（不报错、不消费）
	lenientValue   bool   // 值解析失败时静默忽略（保持默认）
	missingMsg     string // 缺值错误消息（整体，不含前缀）；空 → 默认消息
}

// Alias 追加 flag 别名（同一 flag 的多个名字）。
func (f *Flag) Alias(names ...string) *Flag {
	f.aliases = append(f.aliases, names...)
	for _, n := range names {
		f.fs.flags[n] = f
	}
	return f
}

// OptionalValue 声明为可选值 flag：裸 --flag 不消费下一 token，
// 写入 Implicit 指定的值（默认空字符串）。
func (f *Flag) OptionalValue() *Flag {
	f.optional = true
	return f
}

// Implicit 设置可选值 flag 裸形式写入的值。
func (f *Flag) Implicit(v string) *Flag {
	f.implicit = v
	return f
}

// LenientMissing 缺值时静默忽略（不报错、不消费）。
func (f *Flag) LenientMissing() *Flag {
	f.lenientMissing = true
	return f
}

// LenientValue 值解析失败时静默忽略（保持默认值）。
func (f *Flag) LenientValue() *Flag {
	f.lenientValue = true
	return f
}

// MissingMsg 自定义缺值错误消息（整体消息，不含前缀）。
func (f *Flag) MissingMsg(msg string) *Flag {
	f.missingMsg = msg
	return f
}

// FlagSet 是命令（或全局）的 flag 集合。
//
// Parse 采用扫描式解析：flag 与位置参数可任意交错，位置参数按出现顺序
// 收集返回。未注册的 flag 默认也放入位置参数（宽松语义，对齐 Aluka 现状
// CLI）；StrictUnknown 置位后改为报错。
type FlagSet struct {
	prefix string // 错误消息前缀（如 "aluka: " / "aluka build: "）
	// StrictUnknown 置位后，未注册的 flag 报 "unknown option" 错误；
	// 默认 false：未注册 flag 落入位置参数（宽松语义）。
	StrictUnknown bool
	flags         map[string]*Flag
	order         []*Flag
}

// NewFlagSet 创建空 FlagSet；prefix 为错误消息前缀。
func NewFlagSet(prefix string) *FlagSet {
	return &FlagSet{prefix: prefix, flags: map[string]*Flag{}}
}

// Bool 注册布尔 flag（裸 --flag 置 true；支持 --flag=true|false 形态）。
func (fs *FlagSet) Bool(name, usage string, p *bool) *Flag {
	return fs.Var((*boolValue)(p), name, usage)
}

// String 注册字符串 flag（支持 --flag=value 与 --flag value 两种形态）。
func (fs *FlagSet) String(name, usage string, p *string) *Flag {
	return fs.Var((*stringValue)(p), name, usage)
}

// Int 注册 int flag。
func (fs *FlagSet) Int(name, usage string, p *int) *Flag {
	return fs.Var((*intValue)(p), name, usage)
}

// Var 注册自定义值 flag；值类型实现 Value 接口，实现 IsBool() 时按布尔类处理。
func (fs *FlagSet) Var(v Value, name, usage string) *Flag {
	f := &Flag{fs: fs, name: name, usage: usage, value: v}
	if b, ok := v.(booler); ok && b.IsBool() {
		f.isBool = true
	}
	fs.add(f)
	return f
}

func (fs *FlagSet) add(f *Flag) {
	fs.flags[f.name] = f
	fs.order = append(fs.order, f)
}

// Parse 扫描式解析 args，返回剩余位置参数。
//
// 支持 --flag、--flag=value、--flag value（值 flag）与单横线形态；
// "--" 不做特殊处理（按普通未知 token 处理，宽松集合落入位置参数，
// 与 Aluka 现状 CLI 一致）。
func (fs *FlagSet) Parse(args []string) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := splitFlagToken(a)
		f := fs.flags[name]
		if f == nil {
			if fs.StrictUnknown {
				return nil, fmt.Errorf("%sunknown option %s", fs.prefix, a)
			}
			pos = append(pos, a)
			continue
		}
		if hasVal {
			if err := fs.setValue(f, val); err != nil {
				if f.lenientValue {
					continue
				}
				return nil, err
			}
			continue
		}
		if f.isBool || f.optional {
			v := "true"
			if f.optional {
				v = f.implicit
			}
			if err := fs.setValue(f, v); err != nil {
				if f.lenientValue {
					continue
				}
				return nil, err
			}
			continue
		}
		if i+1 < len(args) {
			i++
			if err := fs.setValue(f, args[i]); err != nil {
				if f.lenientValue {
					continue
				}
				return nil, err
			}
			continue
		}
		if f.lenientMissing {
			continue
		}
		msg := f.missingMsg
		if msg == "" {
			msg = "missing value after --" + f.name
		}
		return nil, fmt.Errorf("%s%s", fs.prefix, msg)
	}
	return pos, nil
}

// setValue 写入 flag 值并包装错误（前缀 + 内置值类型专用消息）。
func (fs *FlagSet) setValue(f *Flag, val string) error {
	err := f.value.Set(val)
	if err == nil {
		return nil
	}
	switch f.value.(type) {
	case *boolValue:
		return fmt.Errorf("%sinvalid boolean value %q for --%s", fs.prefix, val, f.name)
	case builtinValue:
		return fmt.Errorf("%sinvalid value %q for --%s: %v", fs.prefix, val, f.name, err)
	default:
		return fmt.Errorf("%s%v", fs.prefix, err)
	}
}

// splitFlagToken 拆分 "--name=value" / "-name=value" 为名字与值。
func splitFlagToken(a string) (name, val string, hasVal bool) {
	s := strings.TrimPrefix(a, "-")
	s = strings.TrimPrefix(s, "-")
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

// 内置值类型（错误由框架包装，见 setValue 的 builtinValue 分支）。

// boolValue 布尔值（接受 strconv.ParseBool 的 true/false/1/0 等）。
type boolValue bool

func (b *boolValue) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*b = boolValue(v)
	return nil
}

func (b *boolValue) String() string { return strconv.FormatBool(bool(*b)) }
func (b *boolValue) IsBool() bool   { return true }
func (b *boolValue) builtinValue()  {}

// stringValue 字符串值。
type stringValue string

func (s *stringValue) Set(v string) error {
	*s = stringValue(v)
	return nil
}

func (s *stringValue) String() string { return string(*s) }
func (s *stringValue) builtinValue()  {}

// intValue int 值。
type intValue int

func (i *intValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*i = intValue(n)
	return nil
}

func (i *intValue) String() string { return strconv.Itoa(int(*i)) }
func (i *intValue) builtinValue()  {}
