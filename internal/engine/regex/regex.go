// Package regex 实现 JS RegExp 的匹配内核。
//
// 设计说明：
//   - 采用 JS 语法 → Go RE2 语法翻译层（translate.go），复用 Go 标准库 regexp 匹配；
//     需求分析明确允许复用 Go regexp/syntax（requirements-analysis.md 行 95）。
//   - 自研 NFA+回溯匹配器（compiler.go 正则→NFA / matcher.go 回溯匹配器）为后续迭代，
//     本包目录与开发计划 internal/engine/regex/ 保持一致。
//   - 已知限制：反向引用、前瞻/后行断言不支持（编译时报错）；个别语义近似（见 translate.go）。
package regex

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Flags 是解析后的 RegExp 标志位集合。
type Flags struct {
	HasIndices  bool // d
	Global      bool // g
	IgnoreCase  bool // i
	Multiline   bool // m
	DotAll      bool // s
	Unicode     bool // u
	UnicodeSets bool // v（暂按 Unicode 处理）
	Sticky      bool // y
}

// flagOrder 是规范输出顺序（RegExp.prototype.flags）。
const flagOrder = "dgimsuvy"

// ParseFlags 校验并解析 flags 字符串。非法标志或重复标志返回错误。
func ParseFlags(s string) (Flags, error) {
	var f Flags
	seen := [256]bool{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if seen[c] {
			return Flags{}, fmt.Errorf("invalid regular expression flag: duplicate %q", string(c))
		}
		seen[c] = true
		switch c {
		case 'd':
			f.HasIndices = true
		case 'g':
			f.Global = true
		case 'i':
			f.IgnoreCase = true
		case 'm':
			f.Multiline = true
		case 's':
			f.DotAll = true
		case 'u':
			f.Unicode = true
		case 'v':
			// v（unicodeSets）本身即 Unicode 模式，但独立标志，避免与 u 冲突
			// （此前置 Unicode=true 后与自身重复检查冲突，导致 /v 一律报错）。
			f.UnicodeSets = true
		case 'y':
			f.Sticky = true
		default:
			return Flags{}, fmt.Errorf("invalid regular expression flag %q", string(c))
		}
	}
	if f.Unicode && f.UnicodeSets {
		return Flags{}, fmt.Errorf("invalid regular expression flag: 'u' and 'v' cannot both be set")
	}
	return f, nil
}

// String 返回按规范顺序（dgimsuvy）排序的标志字符串。
func (f Flags) String() string {
	var b strings.Builder
	for i := 0; i < len(flagOrder); i++ {
		c := flagOrder[i]
		switch c {
		case 'd':
			if f.HasIndices {
				b.WriteByte('d')
			}
		case 'g':
			if f.Global {
				b.WriteByte('g')
			}
		case 'i':
			if f.IgnoreCase {
				b.WriteByte('i')
			}
		case 'm':
			if f.Multiline {
				b.WriteByte('m')
			}
		case 's':
			if f.DotAll {
				b.WriteByte('s')
			}
		case 'u':
			if f.Unicode {
				b.WriteByte('u')
			}
		case 'v':
			if f.UnicodeSets {
				b.WriteByte('v')
			}
		case 'y':
			if f.Sticky {
				b.WriteByte('y')
			}
		}
	}
	return b.String()
}

// Compiled 是一个已编译的 JS 正则。
type Compiled struct {
	Source     string // 原始 JS pattern（未经规范化，保留 \/ 等原文）
	Flags      Flags  // 解析后的标志
	re         *regexp.Regexp
	GroupNames []string // 捕获组名（索引 0 为整体匹配，无名组为空字符串）
	// bt 在包含前瞻/后行/反向引用（RE2 不支持）时作为回退匹配器。
	bt *btRegexp
}

// Compile 校验 flags、翻译 JS 语法并编译为 Go 正则。
// 翻译或编译失败返回错误（引擎层将其转为 SyntaxError）。
func Compile(source, flagsStr string) (*Compiled, error) {
	f, err := ParseFlags(flagsStr)
	if err != nil {
		return nil, err
	}
	goSrc, err := translate(source, f)
	if err != nil {
		// RE2 不支持前瞻/后行断言、反向引用与类内补集成员（[\S]）：
		// 回退到自研回溯匹配器。
		if errors.Is(err, errLookaround) || errors.Is(err, errBackref) || errors.Is(err, errClassSubset) {
			bt, berr := compileBacktrack(source, f)
			if berr != nil {
				return nil, err
			}
			return &Compiled{Source: source, Flags: f, bt: bt}, nil
		}
		return nil, err
	}
	// 全局修饰符以 (?i)(?m) 前缀注入；s（dotAll）由 translate 将 "." 翻译为
	// [\s\S]（Go regexp 的 "." 默认不匹配换行，无 (?s) 全局开关依赖）。
	prefix := ""
	if f.IgnoreCase {
		prefix += "(?i)"
	}
	if f.Multiline {
		prefix += "(?m)"
	}
	re, err := regexp.Compile(prefix + goSrc)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	return &Compiled{Source: source, Flags: f, re: re, GroupNames: re.SubexpNames()}, nil
}

// NumGroups 返回捕获组数量（不含整体匹配）。
func (c *Compiled) NumGroups() int {
	if c.bt != nil {
		return c.bt.numGroups
	}
	return len(c.GroupNames) - 1
}

// GroupName 返回第 i 个捕获组的名称（无名组为空字符串）。
func (c *Compiled) GroupName(i int) string {
	if c.bt != nil {
		for name, idx := range c.bt.groupNames {
			if idx == i {
				return name
			}
		}
		return ""
	}
	if i < 0 || i >= len(c.GroupNames) {
		return ""
	}
	return c.GroupNames[i]
}

// MatchIndex 返回 s 中第一个匹配的索引区间。
// 返回值为 [整体 start, 整体 end, 组1 start, 组1 end, ...]，未参与的组为 -1；
// 无匹配时返回 nil。
func (c *Compiled) MatchIndex(s string) []int {
	if c.bt != nil {
		return c.bt.exec(s, 0)
	}
	return c.re.FindStringSubmatchIndex(s)
}

// MatchAllIndex 返回 s 中所有非重叠匹配的索引（整体匹配 + 捕获组）。
func (c *Compiled) MatchAllIndex(s string) [][]int {
	if c.bt != nil {
		var out [][]int
		search := 0
		for {
			m := c.bt.exec(s, search)
			if m == nil {
				break
			}
			out = append(out, m)
			// 零宽匹配需前进，避免死循环。
			if m[1] == m[0] {
				if m[1] >= len(s) {
					break
				}
				search = m[1] + 1
			} else {
				search = m[1]
			}
		}
		return out
	}
	return c.re.FindAllStringSubmatchIndex(s, -1)
}
