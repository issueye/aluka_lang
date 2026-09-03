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
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrBacktrackLimit 表示回溯匹配超过执行步数预算。
var ErrBacktrackLimit = errors.New("regular expression backtracking budget exhausted")

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

const surrogateTokenBase rune = 0xF0000

// compileCache 缓存已编译正则（进程级）。JS 正则字面量每次求值都会创建新
// RegExp 对象并调用 Compile；若每次重新翻译 + Go regexp.Compile，循环内使用
// 字面量（如 /.../.test(s)）会比 V8（复用字面量编译结果）慢数十倍。Compiled
// 只读（re/bt/GroupNames 只用于匹配），可安全跨对象共享。
var (
	compileCache   = map[string]*Compiled{}
	compileCacheMu sync.Mutex
)

// maxCompiledCacheSize 限制缓存条数，防止用户代码动态生成无限模式时缓存
// 无限膨胀。超限时简单重建（丢弃全部）——真实应用常用模式数远低于此。
const maxCompiledCacheSize = 512

// Compile 校验 flags、翻译 JS 语法并编译为 Go 正则。
// 翻译或编译失败返回错误（引擎层将其转为 SyntaxError）。
// 相同 (source, flags) 的编译结果被缓存复用。
func Compile(source, flagsStr string) (*Compiled, error) {
	key := source + "\x00" + flagsStr
	compileCacheMu.Lock()
	if c, ok := compileCache[key]; ok {
		compileCacheMu.Unlock()
		return c, nil
	}
	compileCacheMu.Unlock()

	f, err := ParseFlags(flagsStr)
	if err != nil {
		return nil, err
	}
	matchSource := source
	if !f.Unicode && !f.UnicodeSets {
		matchSource = encodePatternCodeUnits(source)
	}
	goSrc, err := translate(matchSource, f)
	if err != nil {
		// RE2 不支持前瞻/后行断言、反向引用与类内补集成员（[\S]）：
		// 回退到自研回溯匹配器。
		if errors.Is(err, errLookaround) || errors.Is(err, errBackref) || errors.Is(err, errClassSubset) {
			bt, berr := compileBacktrack(matchSource, f)
			if berr != nil {
				return nil, berr
			}
			compiled := &Compiled{Source: source, Flags: f, bt: bt}
			cacheCompiled(key, compiled)
			return compiled, nil
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
	compiled := &Compiled{Source: source, Flags: f, re: re, GroupNames: re.SubexpNames()}
	cacheCompiled(key, compiled)
	return compiled, nil
}

// cacheCompiled 把编译结果写入缓存（带条数上限）。
func cacheCompiled(key string, c *Compiled) {
	compileCacheMu.Lock()
	defer compileCacheMu.Unlock()
	if len(compileCache) >= maxCompiledCacheSize {
		compileCache = map[string]*Compiled{}
	}
	compileCache[key] = c
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

// Exec 返回 s 中第一个匹配的 UTF-16 code unit 索引区间。
// 返回值为 [整体 start, 整体 end, 组1 start, 组1 end, ...]，未参与的组为 -1；
// 无匹配时返回 nil。
func (c *Compiled) Exec(s string) ([]int, error) {
	return c.ExecAt(s, 0)
}

// ExecAt 返回起始 UTF-16 索引及其后的第一个匹配。
func (c *Compiled) ExecAt(s string, start int) ([]int, error) {
	matchInput := c.matchInput(s)
	byteStart := matchByteIndex(matchInput, s, start, c.unicodeMode())
	m, err := c.exec(matchInput[byteStart:], btMaxSteps)
	if err != nil || m == nil {
		return m, err
	}
	for i := range m {
		if m[i] >= 0 {
			m[i] += byteStart
		}
	}
	c.indicesToUTF16(matchInput, m)
	return m, nil
}

func (c *Compiled) exec(s string, limit int) ([]int, error) {
	if c.bt != nil {
		m, aborted, _ := c.bt.execWithLimit(s, 0, limit)
		if aborted {
			return nil, ErrBacktrackLimit
		}
		return m, nil
	}
	return c.re.FindStringSubmatchIndex(s), nil
}

// Matches 是一次全部匹配的索引集合，附带语义正确的切片方法。
// Raw 为原始串字节偏移；Mid 标记该边界是否落在代理对中间——此时被切开的
// 单元按 UTF16Slice 语义以 U+FFFD 呈现。
type Matches struct {
	s   string
	U16 [][]int  // UTF-16 code unit 索引（JS 可见值）
	Raw [][]int  // 原始串字节偏移（O(1) 切片用）
	Mid [][]bool // 各索引是否切在代理对中间
}

func (m *Matches) Len() int { return len(m.U16) }

// Source 返回匹配的目标串。
func (m *Matches) Source() string { return m.s }

// Slice 返回匹配 i 的第 j 个捕获（j 为偶数的组起始下标，覆盖 [j, j+1)）。
func (m *Matches) Slice(i, j int) string {
	if m.U16[i][j] >= m.U16[i][j+1] {
		return "" // 零宽捕获
	}
	var pre, post string
	b0, b1 := m.Raw[i][j], m.Raw[i][j+1]
	if m.Mid[i][j] {
		pre = "\uFFFD"
	}
	if m.Mid[i][j+1] {
		b1 -= 4 // 回退到星体码点起点，其首单元以 U+FFFD 呈现
		post = "\uFFFD"
	}
	return pre + m.s[b0:b1] + post
}

// Between 返回 (i1,j1) 到 (i2,j2) 之间的子串（匹配间隙）。
func (m *Matches) Between(i1, j1, i2, j2 int) string {
	if m.U16[i1][j1] >= m.U16[i2][j2] {
		return "" // 零宽间隙
	}
	var pre, post string
	b0, b1 := m.Raw[i1][j1], m.Raw[i2][j2]
	if m.Mid[i1][j1] {
		pre = "\uFFFD"
	}
	if m.Mid[i2][j2] {
		b1 -= 4
		post = "\uFFFD"
	}
	return pre + m.s[b0:b1] + post
}

// Head 返回串首到 (i,j) 的子串。
func (m *Matches) Head(i, j int) string {
	if m.U16[i][j] == 0 {
		return ""
	}
	b := m.Raw[i][j]
	if m.Mid[i][j] {
		b -= 4
		return "\uFFFD" + m.s[:b]
	}
	return m.s[:b]
}

// Tail 返回 (i,j) 到串尾的子串。串尾恒为码点边界，无尾部孤元。
func (m *Matches) Tail(i, j int) string {
	if m.Raw[i][j] == len(m.s) {
		return ""
	}
	if m.Mid[i][j] {
		return "\uFFFD" + m.s[m.Raw[i][j]:]
	}
	return m.s[m.Raw[i][j]:]
}

// ExecAll 返回 s 中所有非重叠匹配的 UTF-16 索引（整体匹配 + 捕获组）。
func (c *Compiled) ExecAll(s string) ([][]int, error) {
	m, err := c.AllMatches(s)
	if err != nil || m == nil {
		return nil, err
	}
	return m.U16, nil
}

// AllMatches 返回 s 中全部非重叠匹配。零宽匹配按 AdvanceStringIndex 推进：
// u/v 模式前进一个码点，传统模式前进一个 UTF-16 code unit。
//
// 性能关键：整串只在开头做一次 code-unit 编码，之后所有匹配在编码串上以
// 字节偏移推进，索引按匹配顺序增量换算。不要退回「每个匹配都调 ExecAt」
// 的写法——那会对整串反复编码并从 0 重数索引，复杂度 O(匹配数 × 串长)，
// 大字符串 split/matchAll 会从毫秒级劣化到十秒级。
func (c *Compiled) AllMatches(s string) (*Matches, error) {
	matchInput := c.matchInput(s)
	out := &Matches{s: s}
	w := &utf16Walker{input: matchInput, raw: s, unicode: c.unicodeMode()}
	search := 0
	for search <= len(matchInput) {
		m, err := c.execOneFrom(matchInput, search)
		if err != nil {
			return nil, err
		}
		if m == nil {
			break
		}
		// execOneFrom 返回编码串字节索引；convert 之后即失效，推进须用字节值。
		mStart, mEnd := m[0], m[1]
		u16, raw, mid := w.convert(m)
		out.U16 = append(out.U16, u16)
		out.Raw = append(out.Raw, raw)
		out.Mid = append(out.Mid, mid)
		if mEnd == mStart {
			// 零宽：按 AdvanceStringIndex 前进（编码串上一 rune = 一 code unit
			// 或一码点，与传统/u 模式语义一致）。
			search = nextRuneBoundary(matchInput, mEnd)
		} else {
			search = mEnd
		}
	}
	return out, nil
}

// ExecSingle 返回首个匹配（含字节偏移与切分标记）。
func (c *Compiled) ExecSingle(s string) (*Matches, bool, error) {
	matchInput := c.matchInput(s)
	w := &utf16Walker{input: matchInput, raw: s, unicode: c.unicodeMode()}
	m, err := c.execOneFrom(matchInput, 0)
	if err != nil || m == nil {
		return nil, false, err
	}
	u16, raw, mid := w.convert(m)
	return &Matches{s: s, U16: [][]int{u16}, Raw: [][]int{raw}, Mid: [][]bool{mid}}, true, nil
}

// execOneFrom 返回编码串上自字节偏移 search 起的第一个匹配。
// 返回索引相对 matchInput 整串（字节）；无匹配返回 nil。
func (c *Compiled) execOneFrom(matchInput string, search int) ([]int, error) {
	if c.bt != nil {
		m, aborted, _ := c.bt.execWithLimit(matchInput, search, btMaxSteps)
		if aborted {
			return nil, ErrBacktrackLimit
		}
		return m, nil
	}
	m := c.re.FindStringSubmatchIndex(matchInput[search:])
	if m == nil {
		return nil, nil
	}
	for i := range m {
		if m[i] >= 0 {
			m[i] += search
		}
	}
	return m, nil
}

// utf16Walker 同步行扫描编码串与原始串，把编码串上的字节索引增量换算为
// UTF-16 code unit 索引与原始串字节偏移。匹配非重叠且升序，游标只前进，
// 全部匹配合计 O(len(matchInput))。
type utf16Walker struct {
	input      string // 编码串（u/v 模式下与 raw 相同）
	raw        string
	bytePos    int  // 已走过的编码串字节位置
	rawBytePos int  // 已走过的原始串字节位置
	units      int  // 已累计的 UTF-16 code unit 数
	lastStepHi bool // 最后一步消费的是高代理令牌（边界切在代理对中间）
	unicode    bool
}

// convert 换算一次匹配的索引：返回 UTF-16 索引、原始串字节偏移与代理对
// 切分标记三份新切片（未参与组均为 -1 / false）。组索引可能因前瞻等结构
// 非严格升序，先对参与索引升序定位，再按原位置回填。
func (w *utf16Walker) convert(m []int) ([]int, []int, []bool) {
	idxs := make([]int, 0, len(m))
	for _, v := range m {
		if v >= 0 {
			idxs = append(idxs, v)
		}
	}
	sort.Ints(idxs)
	units := make(map[int]int, len(idxs))
	bytes := make(map[int]int, len(idxs))
	mids := make(map[int]bool, len(idxs))
	for _, idx := range idxs {
		w.advanceTo(idx)
		units[idx] = w.units
		bytes[idx] = w.rawBytePos
		mids[idx] = w.lastStepHi
	}
	u16 := make([]int, len(m))
	raw := make([]int, len(m))
	mid := make([]bool, len(m))
	for i, v := range m {
		if v < 0 {
			u16[i], raw[i], mid[i] = -1, -1, false
		} else {
			u16[i], raw[i], mid[i] = units[v], bytes[v], mids[v]
		}
	}
	return u16, raw, mid
}

// advanceTo 把游标推进到编码串字节位置 byteIdx：
//   - u/v 模式：编码串即原串，逐码点前进，星体面值码点占 2 个 code unit；
//   - 传统模式：编码串每个 rune 恰对应 1 个 code unit；代理对令牌化后
//     「高+低」两个令牌 rune 共同对应原始串的一个星体面值码点。
//
// 若最后一步消费的是高代理令牌（lastStepHi），说明该边界切在代理对中间：
// 原始串字节位置已越过整个码点，需由调用方以 U+FFFD 补偿被切开的单元。
func (w *utf16Walker) advanceTo(byteIdx int) {
	for w.bytePos < byteIdx {
		r, size := utf8.DecodeRuneInString(w.input[w.bytePos:])
		w.lastStepHi = false
		if w.unicode {
			w.rawBytePos += size
			if r > 0xffff {
				w.units += 2
			} else {
				w.units++
			}
		} else {
			if r >= surrogateTokenBase {
				if r < surrogateTokenBase+0x400 {
					// 高代理令牌：消费原始串中对应的星体面值码点
					_, rsize := utf8.DecodeRuneInString(w.raw[w.rawBytePos:])
					w.rawBytePos += rsize
					w.lastStepHi = true
				}
				// 低代理令牌：原始码点已随高代理令牌消费，不重复前进
			} else {
				w.rawBytePos += size
			}
			w.units++
		}
		w.bytePos += size
	}
}

func (c *Compiled) unicodeMode() bool { return c.Flags.Unicode || c.Flags.UnicodeSets }

func (c *Compiled) matchInput(s string) string {
	if c.unicodeMode() {
		return s
	}
	return encodeStringCodeUnits(s)
}

func (c *Compiled) indicesToUTF16(matchInput string, indices []int) {
	for i, index := range indices {
		if index < 0 {
			continue
		}
		if c.unicodeMode() {
			indices[i] = UTF16Index(matchInput, index)
		} else {
			indices[i] = utf8.RuneCountInString(matchInput[:index])
		}
	}
}

func matchByteIndex(matchInput, original string, index int, unicodeMode bool) int {
	if index <= 0 {
		return 0
	}
	if unicodeMode {
		return ByteIndex(original, index)
	}
	byteIndex := 0
	for units := 0; byteIndex < len(matchInput) && units < index; units++ {
		_, size := utf8.DecodeRuneInString(matchInput[byteIndex:])
		byteIndex += size
	}
	return byteIndex
}

func encodePatternCodeUnits(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		if r <= 0xffff {
			b.WriteRune(r)
			continue
		}
		hi, lo := utf16.EncodeRune(r)
		b.WriteRune(surrogateToken(hi))
		b.WriteRune(surrogateToken(lo))
	}
	return b.String()
}

func encodeStringCodeUnits(s string) string {
	// 快路径：不含星体面值字符（绝大多数真实输入）时无需重编码，
	// 避免大字符串每次匹配都整串分配复制。
	if !strings.ContainsFunc(s, func(r rune) bool { return r > 0xffff }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r <= 0xffff {
			b.WriteRune(r)
			continue
		}
		hi, lo := utf16.EncodeRune(r)
		b.WriteRune(surrogateToken(hi))
		b.WriteRune(surrogateToken(lo))
	}
	return b.String()
}

func surrogateToken(r rune) rune { return surrogateTokenBase + r - 0xd800 }

func patternCodeUnit(r rune, f Flags) rune {
	if !f.Unicode && !f.UnicodeSets && r >= 0xd800 && r <= 0xdfff {
		return surrogateToken(r)
	}
	return r
}

// UTF16Index 把 Go 字符串字节边界转换为 ECMAScript UTF-16 code unit 索引。
func UTF16Index(s string, byteIndex int) int {
	if byteIndex <= 0 {
		return 0
	}
	if byteIndex > len(s) {
		byteIndex = len(s)
	}
	units := 0
	for _, r := range s[:byteIndex] {
		if r <= 0xffff {
			units++
		} else {
			units += 2
		}
	}
	return units
}

// ByteIndex 把 ECMAScript UTF-16 code unit 索引转换为不拆分 UTF-8 字符的字节边界。
// 位于代理对中间的索引按 Unicode RegExp 的 AdvanceStringIndex 语义前移到码点末尾。
func ByteIndex(s string, utf16Index int) int {
	if utf16Index <= 0 {
		return 0
	}
	units := 0
	for byteIndex, r := range s {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units >= utf16Index {
			return byteIndex
		}
		if units+width > utf16Index {
			return byteIndex + len(string(r))
		}
		units += width
	}
	return len(s)
}

// AdvanceStringIndex 实现 ECMAScript AdvanceStringIndex。
func AdvanceStringIndex(s string, index int, unicodeMode bool) int {
	if !unicodeMode || index < 0 {
		return index + 1
	}
	units := utf16.Encode([]rune(s))
	if index+1 >= len(units) || units[index] < 0xd800 || units[index] > 0xdbff ||
		units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
		return index + 1
	}
	return index + 2
}

// UTF16Slice 按 ECMAScript UTF-16 索引截取字符串。孤立 surrogate 无法由当前
// Go string 值表示，utf16.Decode 会将其替换为 U+FFFD。
func UTF16Slice(s string, start, end int) string {
	units := utf16.Encode([]rune(s))
	if start < 0 {
		start = 0
	}
	if end > len(units) {
		end = len(units)
	}
	if start > end {
		start = end
	}
	return string(utf16.Decode(units[start:end]))
}
