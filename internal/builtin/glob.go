package builtin

// Node fs.glob/globSync 兼容实现。
// 移植 node v22.x lib/internal/fs/glob.js 的遍历算法 + minimatch 段匹配语义
// （Windows nocase、dot 规则、brace 展开）。
// 注意：Node 22.23 的 Glob 构造器只读取 cwd/exclude/withFileTypes 三个选项，
// nodir/nofile/dot/absolute/ignore 在 globSync 中实际被忽略（实测），故此处
// 同样忽略这些选项以对齐行为。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// --- 段编译 ---

type globTokKind int

const (
	tokLit globTokKind = iota
	tokStar
	tokQmark
	tokClass
)

type globClassItem struct{ lo, hi byte }

type globTok struct {
	kind  globTokKind
	ch    byte // tokLit
	neg   bool // tokClass 取反（[!...]）
	items []globClassItem
}

// globSeg 一个路径段 pattern。isStar 表示 '**'（GLOBSTAR）。
type globSeg struct {
	literal   string
	isLiteral bool // 纯字面段（=== 匹配，大小写敏感）
	isStar    bool // GLOBSTAR
	tokens    []globTok
}

// globPattern 编译后的单个 glob pattern（对应 minimatch set 的一行）。
type globPattern struct {
	parts   []globSeg // 段列表
	strs    []string  // 原始段字符串（cacheKey 用）
	indexes map[int]bool
	nocase  bool
}

func (p *globPattern) at(i int) *globSeg {
	if i < 0 || i >= len(p.parts) {
		return nil
	}
	return &p.parts[i]
}

func (p *globPattern) isFirst() bool { return p.indexes[0] }

func (p *globPattern) isLast(isDir bool) bool {
	last := len(p.parts) - 1
	if p.indexes[last] {
		return true
	}
	// 尾段 ''（pattern 尾部斜杠）且前一段是 GLOBSTAR。
	return last >= 2 && p.parts[last].isLiteral && p.parts[last].literal == "" &&
		isDir && p.indexes[last-1] && p.parts[last-1].isStar
}

// test 对应 Pattern.test：GLOBSTAR 恒 true，字面段 ===，通配段走匹配器。
func (p *globPattern) test(idx int, name string) bool {
	if idx >= len(p.parts) {
		return false
	}
	s := p.parts[idx]
	if s.isStar {
		return true
	}
	if s.isLiteral {
		return s.literal == name
	}
	return globSegMatch(&s, name, p.nocase)
}

// child 复制 pattern，替换 indexes。
func (p *globPattern) child(indexes map[int]bool) *globPattern {
	return &globPattern{parts: p.parts, strs: p.strs, indexes: indexes, nocase: p.nocase}
}

// cacheKey 对应 Pattern.cacheKey：strs[index] 起以 '/' 连接。
func (p *globPattern) cacheKey(index int) string {
	return strings.Join(p.strs[index:], "/")
}

// globSegMatch 通配段匹配（手动回溯；dot:false 时段首 magic 不匹配段首 '.'，
// 单元素类已转字面无限制；nocase 忽略 ASCII 大小写）。
func globSegMatch(seg *globSeg, name string, nocase bool) bool {
	// dot 规则：段首是 magic（star/qmark/多元素 class）时不匹配段首 '.'。
	// 仅"单元素非取反类"已转字面无限制（[.] 可匹配段首点）。
	if len(name) > 0 && name[0] == '.' && len(seg.tokens) > 0 {
		t := seg.tokens[0]
		if t.kind == tokStar || t.kind == tokQmark || t.kind == tokClass {
			if !(t.kind == tokClass && !t.neg && len(t.items) == 1 && t.items[0].lo == t.items[0].hi) {
				return false
			}
		}
	}
	var match func(ti, ni int) bool
	match = func(ti, ni int) bool {
		if ti == len(seg.tokens) {
			return ni == len(name)
		}
		t := seg.tokens[ti]
		switch t.kind {
		case tokStar:
			for k := ni; k <= len(name); k++ {
				if match(ti+1, k) {
					return true
				}
			}
			return false
		case tokQmark:
			if ni >= len(name) {
				return false
			}
			return match(ti+1, ni+1)
		case tokClass:
			if ni >= len(name) {
				return false
			}
			if !globClassMatch(t, name[ni], nocase) {
				return false
			}
			return match(ti+1, ni+1)
		default: // tokLit
			if ni >= len(name) {
				return false
			}
			if !globCharEq(t.ch, name[ni], nocase) {
				return false
			}
			return match(ti+1, ni+1)
		}
	}
	return match(0, 0)
}

func globCharEq(a, b byte, nocase bool) bool {
	if a == b {
		return true
	}
	if nocase {
		// ASCII 大小写折叠（JS toLowerCase 的近似；文件名字节场景足够）。
		if a >= 'A' && a <= 'Z' {
			return a+32 == b
		}
		if b >= 'A' && b <= 'Z' {
			return b+32 == a
		}
	}
	return false
}

func globClassMatch(t globTok, c byte, nocase bool) bool {
	hit := false
	for _, it := range t.items {
		if c >= it.lo && c <= it.hi {
			hit = true
			break
		}
		if nocase {
			// 范围两端也做大小写折叠（[A-Z] 匹配小写）。
			lo, hi := it.lo, it.hi
			fold := func(b byte) byte {
				if b >= 'A' && b <= 'Z' {
					return b + 32
				}
				if b >= 'a' && b <= 'z' {
					return b - 32
				}
				return b
			}
			lo2, hi2 := fold(lo), fold(hi)
			if lo2 > hi2 {
				lo2, hi2 = hi2, lo2
			}
			if c >= lo2 && c <= hi2 {
				hit = true
				break
			}
		}
	}
	if t.neg {
		return !hit
	}
	return hit
}

// globSegHasMagic 判断段是否含通配符（* ? [）。
func globSegHasMagic(seg string) bool {
	return strings.ContainsAny(seg, "*?[")
}

// globCompileSeg 编译一个路径段。
func globCompileSeg(seg string) globSeg {
	if seg == "**" {
		return globSeg{isStar: true}
	}
	if !globSegHasMagic(seg) {
		return globSeg{literal: seg, isLiteral: true}
	}
	gs := globSeg{}
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '*':
			// 连续星号合并（minimatch 语义）。
			if len(gs.tokens) == 0 || gs.tokens[len(gs.tokens)-1].kind != tokStar {
				gs.tokens = append(gs.tokens, globTok{kind: tokStar})
			}
		case '?':
			gs.tokens = append(gs.tokens, globTok{kind: tokQmark})
		case '[':
			cls, next := globParseClass(seg, i)
			i = next
			// 单元素类 → 字面（[.] 匹配段首点，无 dot 限制）。
			if cls.kind == tokLit {
				gs.tokens = append(gs.tokens, cls)
			} else {
				gs.tokens = append(gs.tokens, cls)
			}
		default:
			gs.tokens = append(gs.tokens, globTok{kind: tokLit, ch: seg[i]})
		}
	}
	return gs
}

// globParseClass 解析 [..] 字符类（返回 token 与消费位置；']' 为首元素时字面）。
func globParseClass(seg string, i int) (globTok, int) {
	t := globTok{kind: tokClass}
	j := i + 1
	if j < len(seg) && (seg[j] == '!' || seg[j] == '^') {
		t.neg = true
		j++
	}
	first := true
	for j < len(seg) {
		if seg[j] == ']' && !first {
			break
		}
		first = false
		if j+2 < len(seg) && seg[j+1] == '-' && seg[j+2] != ']' {
			t.items = append(t.items, globClassItem{seg[j], seg[j+2]})
			j += 3
		} else {
			t.items = append(t.items, globClassItem{seg[j], seg[j]})
			j++
		}
	}
	// 单元素类 → 字面 token（minimatch 优化，无 dot 限制）。
	if !t.neg && len(t.items) == 1 && t.items[0].lo == t.items[0].hi {
		return globTok{kind: tokLit, ch: t.items[0].lo}, j
	}
	return t, j
}

// globCompilePattern 编译完整 pattern（含 brace 展开）。
func globCompilePattern(pattern string, nocase bool) []*globPattern {
	expanded := globBraceExpand(pattern)
	var out []*globPattern
	for _, e := range expanded {
		// 反斜杠全部替换为 '/'（minimatch windowsPathsNoEscape 语义，
		// Node 22 glob 在所有平台都设置该选项）。
		e = strings.ReplaceAll(e, "\\", "/")
		strs := strings.Split(e, "/")
		// 压缩中间空段与 '.' 段（minimatch preserveMultipleSlashes=false，
		// Node glob 未设置该选项 → 默认压缩；首尾段保留——首段 '' 标记绝对路径）。
		var filtered []string
		for i, s := range strs {
			if i > 0 && i < len(strs)-1 && (s == "" || s == ".") {
				continue
			}
			filtered = append(filtered, s)
		}
		strs = filtered
		parts := make([]globSeg, len(strs))
		for i, s := range strs {
			parts[i] = globCompileSeg(s)
		}
		indexes := map[int]bool{0: true}
		out = append(out, &globPattern{parts: parts, strs: strs, indexes: indexes, nocase: nocase})
	}
	return out
}

// --- brace 展开（brace-expansion 语义）---

func globBraceExpand(pattern string) []string {
	if !strings.Contains(pattern, "{") {
		return []string{pattern}
	}
	// Bash 怪癖：顶层 {} 前缀转义。
	if strings.HasPrefix(pattern, "{}") {
		pattern = "\\{\\}" + pattern[2:]
	}
	return globBraceExpandInner(pattern, true)
}

// globBraceExpandInner 递归展开：从左到右处理顶层 {..} 组。
func globBraceExpandInner(str string, isTop bool) []string {
	acc := []string{""}
	firstGroup := true
	for {
		start := -1
		depth := 0
		for i := 0; i < len(str); i++ {
			switch str[i] {
			case '\\':
				i++ // 跳过转义字符
			case '{':
				if start == -1 {
					start = i
				}
				depth++
			case '}':
				depth--
				if depth == 0 && start != -1 {
					// 找到顶层组 str[start:i+1]
					pre := str[:start]
					body := str[start+1 : i]
					values := globBraceValues(body)
					acc = globBraceCombine(acc, pre, values, isTop && firstGroup)
					str = str[i+1:]
					firstGroup = false
					goto nextGroup
				}
			}
		}
		// 无更多组：剩余作为字面。
		acc = globBraceAppend(acc, str)
		break
	nextGroup:
	}
	return acc
}

// globBraceAppend 把字面串追加到每个累积前缀。
func globBraceAppend(acc []string, s string) []string {
	out := make([]string, 0, len(acc))
	for _, a := range acc {
		out = append(out, a+s)
	}
	return out
}

// globBraceCombine 组合 acc 前缀与组值。
func globBraceCombine(acc []string, pre string, values []string, dropEmpties bool) []string {
	out := make([]string, 0, len(acc)*len(values))
	for _, a := range acc {
		for _, v := range values {
			e := a + pre + v
			if dropEmpties && e == "" {
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

// globBraceValues 计算 {body} 的展开值：逗号组或数字/字母序列。
func globBraceValues(body string) []string {
	// 无顶层逗号 → 序列或字面。
	if !globBraceHasComma(body) {
		if v, ok := globBraceSequence(body); ok {
			return v
		}
		// 无逗号无序列：递归展开（嵌套 {a}），否则字面保留。
		if strings.Contains(body, "{") {
			return globBraceExpandInner(body, false)
		}
		return []string{body}
	}
	// 逗号组：按顶层逗号分割，递归展开每个备选。
	var parts []string
	depth := 0
	cur := ""
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			cur += string(body[i])
			if i+1 < len(body) {
				cur += string(body[i+1])
				i++
			}
		case '{':
			depth++
			cur += "{"
		case '}':
			depth--
			cur += "}"
		case ',':
			if depth == 0 {
				parts = append(parts, cur)
				cur = ""
			} else {
				cur += ","
			}
		default:
			cur += string(body[i])
		}
	}
	parts = append(parts, cur)
	var values []string
	for _, p := range parts {
		var sub []string
		if strings.Contains(p, "{") {
			sub = globBraceExpandInner(p, false)
		} else {
			sub = []string{p}
		}
		values = append(values, sub...)
	}
	return values
}

func globBraceHasComma(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// globBraceSequence 数字/字母序列 {1..5}、{a..e}、{5..1}、{1..9..2}。
func globBraceSequence(body string) ([]string, bool) {
	parts := strings.Split(body, "..")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, false
	}
	var isAlpha bool
	var x, y, incr int64
	if isNum(parts[0]) && isNum(parts[1]) {
		n1, _ := strconv.ParseInt(parts[0], 10, 64)
		n2, _ := strconv.ParseInt(parts[1], 10, 64)
		x, y = n1, n2
	} else if isAlpha1(parts[0]) && isAlpha1(parts[1]) {
		isAlpha = true
		x = int64(parts[0][0])
		y = int64(parts[1][0])
	} else {
		return nil, false
	}
	incr = 1
	if len(parts) == 3 && parts[2] != "" {
		n, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, false
		}
		incr = n
		if incr < 0 {
			incr = -incr
		}
		if incr == 0 {
			incr = 1
		}
	}
	width := len(parts[0])
	if len(parts[1]) > width {
		width = len(parts[1])
	}
	pad := strings.HasPrefix(parts[0], "0") && len(parts[0]) > 1
	if !pad {
		pad = strings.HasPrefix(parts[1], "0") && len(parts[1]) > 1
	}
	var out []string
	if y >= x {
		for i := x; i <= y; i += incr {
			out = append(out, globSeqFmt(i, width, pad, isAlpha))
		}
	} else {
		for i := x; i >= y; i -= incr {
			out = append(out, globSeqFmt(i, width, pad, isAlpha))
		}
	}
	return out, true
}

func globSeqFmt(i int64, width int, pad, isAlpha bool) string {
	if isAlpha {
		c := byte(i)
		if c == '\\' {
			return ""
		}
		return string(c)
	}
	s := strconv.FormatInt(i, 10)
	if pad {
		for len(s) < width {
			s = "0" + s
		}
	}
	return s
}

func isNum(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAlpha1(s string) bool {
	return len(s) == 1 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
}

// --- 遍历引擎（移植 lib/internal/fs/glob.js 的 Glob 类）---

type globDirent struct {
	name     string
	isDir    bool
	isSymlnk bool
}

type globStat struct {
	isDir    bool
	isSymlnk bool
}

type globQueueItem struct {
	path     string
	patterns []*globPattern
}

type globSub struct {
	path     string
	patterns []*globPattern
}

type globEngine struct {
	root       string
	nocase     bool
	isWindows  bool
	seen       map[string]map[string]bool // path → set(cacheKey)
	results    []string
	resultSet  map[string]bool
	excludeFn  func(string) bool
	subs       []globSub
	subIdx     map[string]int
	statCache  map[string]*globStat
	readdirMap map[string][]globDirent
}

func newGlobEngine(root string, nocase bool, excludeFn func(string) bool) *globEngine {
	return &globEngine{
		root:       root,
		nocase:     nocase,
		isWindows:  runtime.GOOS == "windows",
		seen:       map[string]map[string]bool{},
		resultSet:  map[string]bool{},
		excludeFn:  excludeFn,
		subIdx:     map[string]int{},
		statCache:  map[string]*globStat{},
		readdirMap: map[string][]globDirent{},
	}
}

func (g *globEngine) cacheAdd(path string, p *globPattern) bool {
	set, ok := g.seen[path]
	if !ok {
		set = map[string]bool{}
		g.seen[path] = set
	}
	found := false
	for idx := range p.indexes {
		key := p.cacheKey(idx)
		if set[key] {
			found = true
		} else {
			set[key] = true
		}
	}
	return found
}

func (g *globEngine) seenAt(path string, p *globPattern, index int) bool {
	set, ok := g.seen[path]
	if !ok {
		return false
	}
	return set[p.cacheKey(index)]
}

func (g *globEngine) addResult(value string) {
	if g.resultSet[value] {
		return
	}
	if g.excludeFn != nil {
		abs := filepath.Join(g.root, value)
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
		if g.excludeFn(abs) {
			return
		}
	}
	g.resultSet[value] = true
	g.results = append(g.results, value)
}

func (g *globEngine) addSubpattern(path string, p *globPattern) {
	if i, ok := g.subIdx[path]; ok {
		g.subs[i].patterns = append(g.subs[i].patterns, p)
	} else {
		g.subIdx[path] = len(g.subs)
		g.subs = append(g.subs, globSub{path: path, patterns: []*globPattern{p}})
	}
}

func (g *globEngine) statSync(path string) *globStat {
	if s, ok := g.statCache[path]; ok {
		return s
	}
	info, err := os.Lstat(path)
	if err != nil {
		g.statCache[path] = nil
		return nil
	}
	s := &globStat{isDir: info.IsDir(), isSymlnk: info.Mode()&os.ModeSymlink != 0}
	g.statCache[path] = s
	return s
}

func (g *globEngine) readdirSync(path string) []globDirent {
	if d, ok := g.readdirMap[path]; ok {
		return d
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		g.readdirMap[path] = nil
		return nil
	}
	var out []globDirent
	for _, e := range entries {
		out = append(out, globDirent{name: e.Name(), isDir: e.IsDir(), isSymlnk: e.Type()&os.ModeSymlink != 0})
	}
	g.readdirMap[path] = out
	return out
}

// addSubpatterns 对应 Glob.#addSubpatterns。
func (g *globEngine) addSubpatterns(path string, p *globPattern) {
	if g.cacheAdd(path, p) {
		return
	}
	fullpath := filepath.Join(g.root, path)
	stat := g.statSync(fullpath)
	last := len(p.parts) - 1
	isDir := stat != nil && (stat.isDir || (stat.isSymlnk && p.hasSeenSymlinks()))
	isLast := p.isLast(isDir)
	isFirst := p.isFirst()

	if g.excludeFn != nil {
		if a, err := filepath.Abs(fullpath); err == nil {
			if g.excludeFn(a) {
				return
			}
		}
	}
	// 绝对路径分支。
	if isFirst && g.isWindows && p.parts[0].isLiteral && strings.HasSuffix(p.parts[0].literal, ":") {
		g.addSubpattern(p.parts[0].literal+"\\", p.child(map[int]bool{1: true}))
		return
	}
	if isFirst && p.parts[0].isLiteral && p.parts[0].literal == "" {
		g.addSubpattern("/", p.child(map[int]bool{1: true}))
		return
	}
	if isFirst && p.parts[0].isLiteral && p.parts[0].literal == ".." {
		g.addSubpattern("../", p.child(map[int]bool{1: true}))
		return
	}
	if isFirst && p.parts[0].isLiteral && p.parts[0].literal == "." {
		g.addSubpattern(".", p.child(map[int]bool{1: true}))
		return
	}
	// 末尾字面段：stat 优化（FS 层面大小写不敏感）。
	lastPart := &p.parts[last]
	if isLast && lastPart.isLiteral {
		pName := lastPart.literal
		st := g.statSync(filepath.Join(fullpath, pName))
		if st != nil && (pName != "" || isDir) {
			g.addResult(filepath.Join(path, pName))
		}
		if len(p.indexes) == 1 && p.indexes[last] {
			return
		}
	} else if isLast && lastPart.isStar &&
		(path != "." || (p.parts[0].isLiteral && p.parts[0].literal == ".") || (last == 0 && stat != nil)) {
		g.addResult(path)
	}
	if !isDir {
		return
	}
	// children。
	var children []globDirent
	firstIdx := -1
	if len(p.indexes) == 1 {
		for i := range p.parts {
			if p.indexes[i] {
				firstIdx = i
				break
			}
		}
	}
	if firstIdx >= 0 && p.parts[firstIdx].isLiteral {
		st := g.statSync(filepath.Join(fullpath, p.parts[firstIdx].literal))
		if st == nil {
			return
		}
		children = []globDirent{{name: p.parts[firstIdx].literal, isDir: st.isDir, isSymlnk: st.isSymlnk}}
	} else {
		children = g.readdirSync(fullpath)
	}
	for _, entry := range children {
		entryPath := filepath.Join(path, entry.name)
		subIdx := map[int]bool{}
		fromSymlink := false
		for idx := range p.indexes {
			if g.seenAt(entryPath, p, idx) || g.seenAt(entryPath, p, idx+1) {
				return
			}
			cur := p.parts[idx]
			nextIdx := idx + 1
			if cur.isStar {
				// GLOBSTAR 分支。
				isDot := len(entry.name) > 0 && entry.name[0] == '.'
				nextMatches := p.test(nextIdx, entry.name)
				nextNonGlobIdx := nextIdx
				for nextNonGlobIdx < len(p.parts) && p.parts[nextNonGlobIdx].isStar {
					nextNonGlobIdx++
				}
				matchesDot := isDot && p.test(nextNonGlobIdx, entry.name)
				if (isDot && !matchesDot) ||
					(g.excludeFn != nil && g.excludeFn(filepath.Join(g.root, entryPath))) {
					continue
				}
				if !fromSymlink && entry.isDir {
					subIdx[idx] = true
				} else if !fromSymlink && idx == last {
					g.addResult(entryPath)
				}
				if nextMatches && nextIdx == last && !isLast {
					g.addResult(entryPath)
				} else if nextMatches && entry.isDir {
					subIdx[idx+2] = true
				}
				if (nextMatches || (p.parts[0].isLiteral && p.parts[0].literal == ".")) &&
					(entry.isDir || entry.isSymlnk) && !fromSymlink {
					subIdx[nextIdx] = true
				}
				// '**/..' 场景（父目录回溯）。
				if nextIdx < len(p.parts) && p.parts[nextIdx].isLiteral && p.parts[nextIdx].literal == ".." && entry.isDir {
					parent := filepath.Join(path, "..")
					if nextIdx < last {
						if _, ok := g.subIdx[parent]; !ok && !g.seenAt(parent, p, nextIdx+1) {
							g.addSubpattern(parent, p.child(map[int]bool{nextIdx + 1: true}))
						}
					} else {
						if !g.seenAt(path, p, nextIdx) {
							g.cacheAdd(path, p.child(map[int]bool{nextIdx: true}))
							g.addResult(path)
						}
						if !g.seenAt(path, p, nextIdx) || !g.seenAt(parent, p, nextIdx) {
							g.cacheAdd(parent, p.child(map[int]bool{nextIdx: true}))
							g.addResult(parent)
						}
					}
				}
			}
			if cur.isLiteral {
				// 字面段：test（=== 匹配）。
				if p.test(idx, entry.name) && idx != last {
					subIdx[nextIdx] = true
				} else if cur.literal == "." && p.test(nextIdx, entry.name) {
					if nextIdx == last {
						g.addResult(entryPath)
					} else {
						subIdx[nextIdx+1] = true
					}
				}
			}
			if !cur.isLiteral && !cur.isStar && p.test(idx, entry.name) {
				// 通配段（RegExp 语义）。
				if idx == last {
					g.addResult(entryPath)
				} else if entry.isDir {
					subIdx[nextIdx] = true
				}
			}
		}
		if len(subIdx) > 0 {
			g.addSubpattern(entryPath, p.child(subIdx))
		}
	}
}

// hasSeenSymlinks 对应 Pattern.hasSeenSymlinks（简化：indexes 有非 symlink 标记）。
func (p *globPattern) hasSeenSymlinks() bool {
	return true
}

// globSyncRun 主循环（对应 Glob.globSync）。
func (g *globEngine) globSyncRun(patterns []*globPattern) []string {
	queue := []globQueueItem{{path: ".", patterns: patterns}}
	for len(queue) > 0 {
		item := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		g.subs = nil
		g.subIdx = map[string]int{}
		for _, p := range item.patterns {
			g.addSubpatterns(item.path, p)
		}
		for _, s := range g.subs {
			queue = append(queue, globQueueItem{path: s.path, patterns: s.patterns})
		}
	}
	return g.results
}

// globToResults 把结果路径转 engine 数组（withFileTypes 时返回 dirent 对象）。
func globToResults(ge *globEngine, paths []string, withFileTypes bool, root string) []engine.Value {
	out := make([]engine.Value, 0, len(paths))
	for _, p := range paths {
		if withFileTypes {
			full := filepath.Join(root, p)
			info, err := os.Lstat(full)
			if err != nil {
				continue
			}
			d := engine.NewObject()
			_ = d.Set("name", engine.Str(filepath.Base(p)))
			_ = d.Set("parentPath", engine.Str(filepath.Dir(p)))
			_ = d.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(info.IsDir()), nil
			}))
			_ = d.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(!info.IsDir()), nil
			}))
			_ = d.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(info.Mode()&os.ModeSymlink != 0), nil
			}))
			_ = d.Set("isBlockDevice", engine.NewFunction("isBlockDevice", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(false), nil
			}))
			_ = d.Set("isCharacterDevice", engine.NewFunction("isCharacterDevice", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(false), nil
			}))
			_ = d.Set("isFIFO", engine.NewFunction("isFIFO", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(false), nil
			}))
			_ = d.Set("isSocket", engine.NewFunction("isSocket", func(args []engine.Value) (engine.Value, error) {
				return engine.Boolean(false), nil
			}))
			out = append(out, d)
		} else {
			out = append(out, engine.Str(p))
		}
	}
	return out
}

// globParseOptions 解析 glob 选项（cwd/exclude/withFileTypes；其余忽略——
// Node 22.23 实测不生效）。
func globParseOptions(opts engine.Value) (root string, excludeFn func(string) bool, withFileTypes bool, err error) {
	root = "."
	if opts.IsUndefined() {
		return
	}
	o, ok := opts.AsObject()
	if !ok {
		return
	}
	if v, e := o.Get("cwd"); e == nil && v.Type() == engine.TypeString && v.String() != "" {
		root = v.String()
	}
	if v, e := o.Get("withFileTypes"); e == nil {
		if b, ok2 := v.Bool(); ok2 {
			withFileTypes = b
		}
	}
	if v, e := o.Get("exclude"); e == nil && !v.IsUndefined() {
		if f, ok2 := v.AsFunction(); ok2 {
			excludeFn = func(path string) bool {
				r, err := f.Call([]engine.Value{engine.Str(path)})
				if err != nil {
					return false
				}
				b, _ := r.Bool()
				return b
			}
		} else if arr, ok2 := v.(*engine.ArrayValue); ok2 {
			// exclude 数组：resolve(root, pattern) 后全路径匹配。
			var patterns []*globPattern
			nocase := runtime.GOOS == "windows"
			for _, k := range arr.Keys() {
				if k == "length" {
					continue
				}
				iv, _ := arr.Get(k)
				absPat := filepath.Join(root, iv.String())
				if a, e2 := filepath.Abs(absPat); e2 == nil {
					absPat = a
				}
				patterns = append(patterns, globCompilePattern(strings.ReplaceAll(absPat, "\\", "/"), nocase)...)
			}
			excludeFn = func(path string) bool {
				for _, p := range patterns {
					if globFullMatch(p, path) {
						return true
					}
				}
				return false
			}
		} else {
			err = fmt.Errorf("exclude must be a function or string array")
			return
		}
	}
	return
}

// globFullMatch 全路径匹配：pattern 的所有段（含 GLOBSTAR）匹配绝对路径。
func globFullMatch(p *globPattern, path string) bool {
	// 路径转 '/' 分段（Windows 反斜杠）。
	path = strings.ReplaceAll(path, "\\", "/")
	return globFullMatchExact(p, path)
}

// globFullMatchExact 全路径匹配（不做反斜杠归一化——posix 语义下
// '\' 是字面字符，用于 path.matchesGlob）。绝对路径 pattern 的首段 ''
// 与路径首段 '' 直接对齐匹配。
func globFullMatchExact(p *globPattern, path string) bool {
	segs := strings.Split(path, "/")
	return globPartsMatch(p.parts, 0, segs, 0, p.nocase)
}

func globPartsMatch(parts []globSeg, pi int, segs []string, si int, nocase bool) bool {
	if pi == len(parts) {
		return si == len(segs)
	}
	cur := parts[pi]
	if cur.isStar {
		for k := si; k <= len(segs); k++ {
			if globPartsMatch(parts, pi+1, segs, k, nocase) {
				return true
			}
		}
		return false
	}
	if si >= len(segs) {
		return false
	}
	if cur.isLiteral {
		if cur.literal != segs[si] {
			return false
		}
	} else if !globSegMatch(&cur, segs[si], nocase) {
		return false
	}
	return globPartsMatch(parts, pi+1, segs, si+1, nocase)
}
