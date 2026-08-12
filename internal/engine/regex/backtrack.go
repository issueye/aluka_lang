package regex

import (
	"errors"
	"unicode"
)

// Backtracking regex engine (fallback).
//
// Go's RE2 (used by the primary path) does not support lookarounds or
// backreferences, which several npm packages that Express depends on use
// (e.g. bytes uses /\B(?=(\d{3})+(?!\d))/g). This file provides a compact
// recursive-backtracking matcher that supports those features. It is used
// only when the JS pattern cannot be translated to RE2 (errLookaround /
// errBackref). Semantics aim to match V8 for the common subset:
// literals, character classes, quantifiers, alternation, groups (incl.
// lookarounds), anchors, and backreferences.

// --- AST node kinds ---
type btKind int

const (
	btLit btKind = iota
	btClass
	btDot
	btGroup
	btRepeat
	btAlt
	btAnchor
	btBackref
)

// btGroupKind distinguishes group node flavours.
type btGroupKind int

const (
	grpCapture btGroupKind = iota
	grpNoncap
	grpLookahead
	grpNegLookahead
	grpLookbehind
	grpNegLookbehind
)

type btAnchorKind int

const (
	ancStart   btAnchorKind = iota // ^
	ancEnd                         // $
	ancWord                        // \b
	ancNonWord                     // \B
)

type btRange struct {
	lo, hi rune
}

// btClassPart is one term of a character class: a set of ranges, optionally
// negated (e.g. \D inside a class).
type btClassPart struct {
	neg    bool
	ranges []btRange
}

type btNode struct {
	kind btKind

	lit     rune
	parts   []btClassPart // for btClass
	negated bool          // overall class negation [^...]
	dotAll  bool

	grpKind  btGroupKind
	sub      []btNode
	name     string
	groupIdx int // capture index (1-based) for grpCapture

	child  *btNode
	min    int
	max    int // -1 = unbounded
	greedy bool

	alts [][]btNode

	anchor btAnchorKind

	refIdx  int
	refName string
}

// btRegexp is a compiled backtracking pattern.
type btRegexp struct {
	root       []btNode
	ignoreCase bool
	multiline  bool
	dotAll     bool
	numGroups  int
	groupNames map[string]int
}

// btState carries mutable match state.
type btState struct {
	captures []int
	pos      int
	frames   []btFrame // 本序列及内层序列的重复回退帧（LIFO）
	// 当前"打开中"的捕获组（其子序列正在匹配）。帧压入时若处于组内，
	// 记录组索引与起始位置，回退恢复后按新位置重算该组的捕获终点——
	// 否则组捕获（在组完成时才写入）会停留在旧值。
	openGroupIdx   int
	openGroupStart int
}

// btFrame 是序列内重复量词的回退帧。贪心重复在"少吃一次"时压帧；
// 懒重复在"多吃一次"时压帧。帧保存压入时刻的位置与捕获组。
type btFrame struct {
	nodes []btNode // 所属序列（判断 pc 是否可解释为当前序列的索引）
	pc    int      // 重复节点后继节点在所属序列中的索引
	pos   int
	caps  []int
	more  bool     // lazy：弹帧后先多吃一个子节点再继续
	child *btNode
	// 帧压入时处于打开状态的捕获组（-1 = 无）。恢复时重算其捕获终点。
	grpIdx   int
	grpStart int
}

// exec finds the first match at or after a given start byte offset.
// Returns the match indices [wholeStart, wholeEnd, g1s, g1e, ...] or nil.
func (r *btRegexp) exec(s string, start int) []int {
	n := len(s)
	_ = n
	for p := start; p <= n; p++ {
		caps := make([]int, (r.numGroups+1)*2)
		for i := range caps {
			caps[i] = -1
		}
		st := &btState{captures: caps, pos: p, openGroupIdx: -1}
		ok := r.matchSeq(s, st, r.root) && matchEnd(r, s, st)
		if ok {
			out := make([]int, len(caps))
			copy(out, caps)
			out[0] = p      // whole match start（根节点非捕获组时 caps[0] 仍为 -1）
			out[1] = st.pos // whole match end
			return out
		}
	}
	return nil
}

// matchEnd verifies the pattern consumed the whole root (the root is the
// concatenation of all top-level nodes; exec handles ^ anchoring).
func matchEnd(r *btRegexp, s string, st *btState) bool { return true }

// matchNode matches a node starting at st.pos, advancing st.pos on success.
func (r *btRegexp) matchNode(s string, st *btState, n *btNode) bool {
	switch n.kind {
	case btLit:
		if r.matchAtomAt(s, st.pos, n.lit) {
			st.pos++
			return true
		}
		return false
	case btClass:
		if r.matchClassAt(s, st.pos, n) {
			st.pos++
			return true
		}
		return false
	case btDot:
		if r.matchDotAt(s, st.pos, n.dotAll) {
			st.pos++
			return true
		}
		return false
	case btAnchor:
		return r.matchAnchor(s, st.pos, n.anchor)
	case btBackref:
		return r.matchBackref(s, st, n)
		case btGroup:
			switch n.grpKind {
			case grpCapture, grpNoncap:
				start := st.pos
				// 记录打开中的捕获组（仅 grpCapture），供回退帧恢复时
				// 重算捕获终点；嵌套组保存并恢复外层上下文。
				savedGrpIdx, savedGrpStart := st.openGroupIdx, st.openGroupStart
				if n.grpKind == grpCapture {
					st.openGroupIdx, st.openGroupStart = n.groupIdx, start
				}
				if !r.matchSeq(s, st, n.sub) {
					st.openGroupIdx, st.openGroupStart = savedGrpIdx, savedGrpStart
					return false
				}
				st.openGroupIdx, st.openGroupStart = savedGrpIdx, savedGrpStart
				if n.grpKind == grpCapture {
					r.setCapture(st, n.groupIdx, start, st.pos)
				}
				return true
		case grpLookahead:
			// V8 语义：前瞻内的捕获组会写入整体匹配结果（如 /(?=(a))b/ 组1 = "a"）。
			sub := &btState{captures: cloneCaps(st.captures), pos: st.pos, openGroupIdx: -1}
			if r.matchSeq(s, sub, n.sub) {
				copy(st.captures, sub.captures)
				return true
			}
			return false
		case grpNegLookahead:
			sub := &btState{captures: cloneCaps(st.captures), pos: st.pos, openGroupIdx: -1}
			return !r.matchSeq(s, sub, n.sub)
		case grpLookbehind, grpNegLookbehind:
			return r.matchLookbehind(s, st, n)
		}
	case btRepeat:
		return r.matchRepeat(s, st, n)
	case btAlt:
		for _, alt := range n.alts {
			savePos := st.pos
			saveCaps := cloneCaps(st.captures)
			if r.matchSeq(s, st, alt) {
				return true
			}
			st.pos = savePos
			copy(st.captures, saveCaps)
		}
		return false
	}
	return false
}

// matchSeq matches a sequence of nodes. 重复量词在此展开为迭代消费 +
// 显式回退帧：贪心重复先吃满（每吃一个压一个"停在此处"帧），当后续
// 节点失败时按 LIFO 弹帧恢复更少的迭代次数重试；懒重复默认停住（压一个
// "多吃一个"帧），后续失败时多吃一个再重试。
func (r *btRegexp) matchSeq(s string, st *btState, nodes []btNode) bool {
	base := len(st.frames)
	startPos := st.pos
	startCaps := cloneCaps(st.captures)
	pc := 0
	for pc < len(nodes) {
		n := &nodes[pc]
		if n.kind == btRepeat {
			if !r.repeatInSeq(s, st, n, nodes, pc) {
				st.pos = startPos
				copy(st.captures, startCaps)
				st.frames = st.frames[:base]
				return false
			}
			pc++
			continue
		}
		if r.matchNode(s, st, n) {
			pc++
			continue
		}
		// 当前节点失败：弹帧回退（恢复更少/更多迭代后重试）。
		if !r.backtrackSeq(s, st, nodes, &pc, base) {
			st.pos = startPos
			copy(st.captures, startCaps)
			st.frames = st.frames[:base]
			return false
		}
	}
	return true
}

// repeatInSeq 在序列上下文匹配一个重复节点：满足 min 次后按贪心/懒
// 压回退帧。pc 是该重复节点在 nodes 中的索引（后继为 pc+1）。
func (r *btRegexp) repeatInSeq(s string, st *btState, n *btNode, nodes []btNode, pc int) bool {
	count := 0
	// 先满足 min 次（不可回退）。
	for count < n.min {
		before := st.pos
		if !r.matchNode(s, st, n.child) {
			return false
		}
		if st.pos == before {
			break // 空迭代（如 (?=a)+）：无法推进，视为已满足
		}
		count++
	}
	if n.greedy {
		// 贪心：每多吃一个就压一个"停在此处"帧（帧状态 = 少吃一次）。
		for n.max < 0 || count < n.max {
			before := st.pos
			st.frames = append(st.frames, btFrame{
				nodes: nodes, pc: pc + 1, pos: st.pos, caps: cloneCaps(st.captures),
				grpIdx: st.openGroupIdx, grpStart: st.openGroupStart,
			})
			if !r.matchNode(s, st, n.child) {
				break
			}
			if st.pos == before {
				break // 空迭代：不能再推进
			}
			count++
		}
		return true
	}
	// 懒：默认停在此处，压一个"多吃一个"帧；后续失败时补吃。
	if n.max < 0 || count < n.max {
		st.frames = append(st.frames, btFrame{
			nodes: nodes, pc: pc + 1, pos: st.pos, caps: cloneCaps(st.captures),
			more: true, child: n.child,
			grpIdx: st.openGroupIdx, grpStart: st.openGroupStart,
		})
	}
	return true
}

// backtrackSeq 弹出最近的回退帧恢复状态并让序列重试。返回 false 表示
// 无更多回退点（序列失败）。
func (r *btRegexp) backtrackSeq(s string, st *btState, nodes []btNode, pc *int, base int) bool {
	for len(st.frames) > base {
		f := st.frames[len(st.frames)-1]
		st.frames = st.frames[:len(st.frames)-1]
		st.pos = f.pos
		copy(st.captures, f.caps)
		// 帧压入时组捕获尚未写入（组在其子序列完成后才 setCapture）：
		// 按恢复后的位置重算打开中捕获组的终点。
		if f.grpIdx >= 0 {
			r.setCapture(st, f.grpIdx, f.grpStart, st.pos)
		}
		if f.more {
			// 懒重复：先多吃一个子节点，并压新的"再吃一个"帧。
			if !r.matchNode(s, st, f.child) {
				continue
			}
			st.frames = append(st.frames, btFrame{
				nodes: f.nodes, pc: f.pc, pos: st.pos, caps: cloneCaps(st.captures),
				more: true, child: f.child,
				grpIdx: st.openGroupIdx, grpStart: st.openGroupStart,
			})
		}
		if sameSeq(f.nodes, nodes) {
			*pc = f.pc
			return true
		}
		// 帧属于内层已完成的序列：仅恢复状态，重试当前失败节点。
		return true
	}
	return false
}

// sameSeq 判断两个序列切片是否指向同一底层数组（同一序列实例）。
func sameSeq(a, b []btNode) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

// matchRepeat handles greedy/lazy quantifiers by backtracking.
// 仅用于重复作为其他节点的直接子节点（如 (a*)*）的独立路径；
// 序列内的重复由 matchSeq/repeatInSeq 的帧机制处理。失败时恢复进入时
// 的 pos/captures（原子性）。
func (r *btRegexp) matchRepeat(s string, st *btState, n *btNode) bool {
	startPos := st.pos
	startCaps := cloneCaps(st.captures)
	ok := r.repeatHelper(s, st, n, 0)
	if !ok {
		st.pos = startPos
		copy(st.captures, startCaps)
	}
	return ok
}

func (r *btRegexp) repeatHelper(s string, st *btState, n *btNode, count int) bool {
	if n.greedy {
		// Try consuming as much as possible first.
		if count < n.min {
			return r.consumeOne(s, st, n.child, n, count)
		}
		// Try one more (if under max), else accept current.
		if n.max < 0 || count < n.max {
			savePos := st.pos
			saveCaps := cloneCaps(st.captures)
			if r.consumeOne(s, st, n.child, n, count) {
				return true
			}
			st.pos = savePos
			copy(st.captures, saveCaps)
		}
		return true
	}
	// Lazy: try accepting first, then consume more.
	if count < n.min {
		return r.consumeOne(s, st, n.child, n, count)
	}
	// Accept current.
	savePos := st.pos
	saveCaps := cloneCaps(st.captures)
	if n.max < 0 || count < n.max {
		if r.consumeOne(s, st, n.child, n, count) {
			return true
		}
		st.pos = savePos
		copy(st.captures, saveCaps)
	}
	return true
}

func (r *btRegexp) consumeOne(s string, st *btState, child *btNode, n *btNode, count int) bool {
	before := st.pos
	if !r.matchNode(s, st, child) {
		return false
	}
	if st.pos == before {
		// 空迭代（如 (?=a)*）：继续递归会死循环，直接接受当前次数。
		return true
	}
	return r.repeatHelper(s, st, n, count+1)
}

func (r *btRegexp) matchAnchor(s string, pos int, kind btAnchorKind) bool {
	switch kind {
	case ancStart:
		if r.multiline {
			return pos == 0 || s[pos-1] == '\n'
		}
		return pos == 0
	case ancEnd:
		if r.multiline {
			return pos == len(s) || s[pos] == '\n'
		}
		return pos == len(s)
	case ancWord:
		return r.isWordAt(s, pos, true)
	case ancNonWord:
		return !r.isWordAt(s, pos, true)
	}
	return false
}

func (r *btRegexp) isWordAt(s string, pos int, boundary bool) bool {
	before := pos > 0 && isWordByte(s[pos-1])
	after := pos < len(s) && isWordByte(s[pos])
	if boundary {
		return before != after
	}
	return before == after
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// matchAtomAt matches a single literal rune at pos.
func (r *btRegexp) matchAtomAt(s string, pos int, lit rune) bool {
	if pos >= len(s) {
		return false
	}
	ch := rune(s[pos])
	if r.ignoreCase {
		return lower(ch) == lower(lit)
	}
	return ch == lit
}

// matchClassAt matches a character class at pos (byte-oriented for ASCII).
func (r *btRegexp) matchClassAt(s string, pos int, n *btNode) bool {
	if pos >= len(s) {
		return false
	}
	ch := rune(s[pos])
	if r.ignoreCase {
		ch = lower(ch)
	}
	m := false
	for _, part := range n.parts {
		in := false
		for _, rn := range part.ranges {
			lo, hi := rn.lo, rn.hi
			if r.ignoreCase {
				lo, hi = lower(lo), lower(hi)
			}
			if ch >= lo && ch <= hi {
				in = true
				break
			}
		}
		// 取补成员（如 [\S]）：字符不在该 part 范围即匹配。
		if part.neg {
			if !in {
				m = true
			}
		} else if in {
			m = true
		}
	}
	if n.negated {
		return !m
	}
	return m
}

func (r *btRegexp) matchDotAt(s string, pos int, dotAll bool) bool {
	if pos >= len(s) {
		return false
	}
	if dotAll {
		return true
	}
	return s[pos] != '\n' && s[pos] != '\r'
}

func (r *btRegexp) setCapture(st *btState, idx, start, end int) {
	st.captures[idx*2] = start
	st.captures[idx*2+1] = end
}

func (r *btRegexp) matchBackref(s string, st *btState, n *btNode) bool {
	idx := n.refIdx
	if idx < 0 || idx > r.numGroups {
		// named backref
		if n.refName != "" {
			if gi, ok := r.groupNames[n.refName]; ok {
				idx = gi
			} else {
				return false
			}
		}
	}
	start := st.captures[idx*2]
	end := st.captures[idx*2+1]
	if start < 0 {
		return true // unmatched group matches empty
	}
	length := end - start
	if st.pos+length > len(s) {
		return false
	}
	if s[st.pos:st.pos+length] != s[start:end] {
		return false
	}
	st.pos += length
	return true
}

func (r *btRegexp) matchLookbehind(s string, st *btState, n *btNode) bool {
	// Lookbehind: the sub-pattern must match ending at st.pos.
	neg := n.grpKind == grpNegLookbehind
	start := st.pos
	ok := false
	// Try each possible start position up to st.pos.
	for p := 0; p <= start; p++ {
		sub := &btState{captures: cloneCaps(st.captures), pos: p, openGroupIdx: -1}
		if r.matchSeq(s, sub, n.sub) && sub.pos == start {
			ok = true
			// 与前瞻一致：后行断言内的捕获组写入整体结果。
			copy(st.captures, sub.captures)
			break
		}
	}
	if neg {
		return !ok
	}
	return ok
}

func cloneCaps(c []int) []int {
	out := make([]int, len(c))
	copy(out, c)
	return out
}

func lower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// --- Parser -----------------------------------------------------------------

// btParser walks a JS regex pattern and builds the btNode AST.
type btParser struct {
	src        string
	i          int
	numGroups  int
	groupNames map[string]int
}

// compileBacktrack parses pattern into a backtracking matcher.
func compileBacktrack(pattern string, f Flags) (*btRegexp, error) {
	p := &btParser{src: pattern, groupNames: map[string]int{}}
	nodes, err := p.parseSeq(')')
	if err != nil {
		return nil, err
	}
	if p.i < len(p.src) {
		return nil, errUnterminated
	}
	return &btRegexp{
		root:       nodes,
		ignoreCase: f.IgnoreCase,
		multiline:  f.Multiline,
		dotAll:     f.DotAll,
		numGroups:  p.numGroups,
		groupNames: p.groupNames,
	}, nil
}

// parseSeq parses a sequence of atoms until (but not consuming) stop.
// Alternation is handled by returning an ALT node when branches are present.
func (p *btParser) parseSeq(stop byte) ([]btNode, error) {
	var branches [][]btNode
	cur := []btNode{}
	for p.i < len(p.src) {
		c := p.src[p.i]
		if c == stop || c == ')' {
			break
		}
		if c == '|' {
			branches = append(branches, cur)
			cur = nil
			p.i++
			continue
		}
		atom, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		node, err := p.parseQuantifier(atom)
		if err != nil {
			return nil, err
		}
		cur = append(cur, node)
	}
	if len(branches) == 0 {
		return cur, nil
	}
	branches = append(branches, cur)
	if len(branches) == 1 {
		return cur, nil
	}
	return []btNode{{kind: btAlt, alts: branches}}, nil
}

// parseQuantifier applies * + ? {n,m} to the preceding atom.
func (p *btParser) parseQuantifier(atom btNode) (btNode, error) {
	if p.i >= len(p.src) {
		return atom, nil
	}
	c := p.src[p.i]
	min, max := 0, -1
	switch c {
	case '*':
		p.i++
	case '+':
		min, max = 1, -1
		p.i++
	case '?':
		min, max = 0, 1
		p.i++
	case '{':
		lo, hi, ok, ni := p.parseBrace()
		if !ok {
			return atom, nil // literal '{'
		}
		min, max = lo, hi
		p.i = ni
	default:
		return atom, nil
	}
	greedy := true
	if p.i < len(p.src) && p.src[p.i] == '?' {
		greedy = false
		p.i++
	}
	return btNode{kind: btRepeat, child: &atom, min: min, max: max, greedy: greedy}, nil
}

// parseBrace parses {n,m} / {n,} / {n}. Returns (min, max, ok, nextIndex).
func (p *btParser) parseBrace() (int, int, bool, int) {
	i := p.i + 1
	readNum := func() (int, bool) {
		start := i
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			i++
		}
		if start == i {
			return 0, false
		}
		n := 0
		for j := start; j < i; j++ {
			n = n*10 + int(p.src[j]-'0')
		}
		return n, true
	}
	lo, ok1 := readNum()
	if !ok1 {
		return 0, 0, false, p.i
	}
	if i < len(p.src) && p.src[i] == ',' {
		i++
		if i < len(p.src) && p.src[i] == '}' {
			// {n,}
			i++
			return lo, -1, true, i
		}
		hi, ok2 := readNum()
		if !ok2 {
			return 0, 0, false, p.i
		}
		if i < len(p.src) && p.src[i] == '}' {
			i++
			return lo, hi, true, i
		}
		return 0, 0, false, p.i
	}
	if i < len(p.src) && p.src[i] == '}' {
		i++
		return lo, lo, true, i
	}
	return 0, 0, false, p.i
}

// parseAtom parses a single atom (literal, class, dot, group, anchor, backref).
func (p *btParser) parseAtom() (btNode, error) {
	if p.i >= len(p.src) {
		return btNode{}, errUnterminated
	}
	c := p.src[p.i]
	switch {
	case c == '.':
		p.i++
		return btNode{kind: btDot, dotAll: false}, nil
	case c == '[':
		return p.parseClass()
	case c == '(':
		return p.parseGroup()
	case c == '^':
		p.i++
		return btNode{kind: btAnchor, anchor: ancStart}, nil
	case c == '$':
		p.i++
		return btNode{kind: btAnchor, anchor: ancEnd}, nil
	case c == '\\':
		return p.parseEscape()
	case c == '*' || c == '+' || c == '?':
		// A quantifier with no preceding atom is a literal in non-u mode.
		p.i++
		return btNode{kind: btLit, lit: rune(c)}, nil
	default:
		p.i++
		return btNode{kind: btLit, lit: rune(c)}, nil
	}
}

// parseGroup parses ( ... ) with its prefix.
func (p *btParser) parseGroup() (btNode, error) {
	start := p.i
	p.i++ // consume '('
	gk := grpCapture
	name := ""
	variety := ""
	if p.i < len(p.src) && p.src[p.i] == '?' {
		if p.i+1 < len(p.src) {
			variety = string(p.src[p.i+1])
		}
		switch variety {
		case ":":
			gk = grpNoncap
			p.i += 2
		case "=":
			gk = grpLookahead
			p.i += 2
		case "!":
			gk = grpNegLookahead
			p.i += 2
		case "<":
			if p.i+2 < len(p.src) && (p.src[p.i+2] == '=' || p.src[p.i+2] == '!') {
				if p.src[p.i+2] == '=' {
					gk = grpLookbehind
				} else {
					gk = grpNegLookbehind
				}
				p.i += 3
			} else {
				// named capture (?<name>...)
				j := p.i + 2
				for j < len(p.src) && p.src[j] != '>' {
					j++
				}
				if j >= len(p.src) {
					return btNode{}, errors.New("invalid regular expression: unterminated group name")
				}
				name = p.src[p.i+2 : j]
				p.i = j + 1
				gk = grpCapture
			}
		default:
			if variety == "P" {
				// (?P<name>...) — Go-style, treat as capture.
				j := p.i + 2
				for j < len(p.src) && p.src[j] != '>' {
					j++
				}
				if j >= len(p.src) {
					return btNode{}, errors.New("invalid regular expression: unterminated group name")
				}
				name = p.src[p.i+2 : j]
				p.i = j + 1
				gk = grpCapture
			} else {
				// Unsupported group construct.
				return btNode{}, errors.New("invalid regular expression: unsupported group")
			}
		}
	}
	// Assign capture index.
	idx := 0
	if gk == grpCapture {
		p.numGroups++
		idx = p.numGroups
		if name != "" {
			p.groupNames[name] = idx
		}
	}
	sub, err := p.parseSeq(')')
	if err != nil {
		return btNode{}, err
	}
	if p.i >= len(p.src) || p.src[p.i] != ')' {
		_ = start
		return btNode{}, errUnterminated
	}
	p.i++ // consume ')'
	return btNode{kind: btGroup, grpKind: gk, sub: sub, name: name, groupIdx: idx}, nil
}

// parseClass parses a character class [...] .
func (p *btParser) parseClass() (btNode, error) {
	p.i++ // consume '['
	negated := false
	if p.i < len(p.src) && p.src[p.i] == '^' {
		negated = true
		p.i++
	}
	node := btNode{kind: btClass, negated: negated}
	first := true
	for p.i < len(p.src) {
		c := p.src[p.i]
		if c == ']' {
			if first && hasClosingBracket(p.src, p.i+1) {
				// 类首 ']' 且其后还有闭合括号：字面 ]（如 []a] 匹配 ] 或 a，
				// 与 translate 路径的既有语义一致）。
				p.i++
				node.parts = append(node.parts, btClassPart{ranges: []btRange{{']', ']'}}})
				first = false
				continue
			}
			p.i++
			if !first {
				return node, nil
			}
			// 类立即闭合为空类（JS 语义）：[^] 匹配任意字符，[] 永不匹配。
			// （此前无条件把类首 ] 当字面，导致 [^] 扫描不到闭合而报
			// unterminated——js-tokens 的 StringLiteral 正则依赖此语义。）
			if negated {
				node.parts = append(node.parts, btClassPart{ranges: []btRange{{0, unicode.MaxRune}}})
			}
			return node, nil
		}
		// Parse a character or escape, then optional range '-' high.
		ch, err := p.parseClassChar()
		if err != nil {
			return btNode{}, err
		}
		first = false
		if p.i+1 < len(p.src) && p.src[p.i] == '-' && p.src[p.i+1] != ']' {
			p.i++ // consume '-'
			hi, err := p.parseClassChar()
			if err != nil {
				return btNode{}, err
			}
			if ch.kind == 0 && hi.kind == 0 {
				node.parts = append(node.parts, btClassPart{ranges: []btRange{{ch.lo, hi.lo}}})
				continue
			}
			// Escape as range endpoint: approximate by appending both.
			node.parts = append(node.parts, ch.parts...)
			node.parts = append(node.parts, hi.parts...)
			continue
		}
		node.parts = append(node.parts, ch.parts...)
	}
	return btNode{}, errors.New("invalid regular expression: unterminated character class")
}

// classChar is the result of parsing one class character.
type classChar struct {
	kind  int // 0 = rune, 1 = escaped shorthand (\d etc.)
	lo    rune
	parts []btClassPart
}

func (p *btParser) parseClassChar() (classChar, error) {
	if p.i >= len(p.src) {
		return classChar{}, errors.New("invalid regular expression: unterminated character class")
	}
	c := p.src[p.i]
	if c == '\\' {
		return p.parseClassEscape()
	}
	p.i++
	return classChar{kind: 0, lo: rune(c), parts: []btClassPart{{ranges: []btRange{{rune(c), rune(c)}}}}}, nil
}

func (p *btParser) parseClassEscape() (classChar, error) {
	p.i++ // consume '\'
	if p.i >= len(p.src) {
		return classChar{}, errors.New("invalid regular expression: unterminated escape")
	}
	e := p.src[p.i]
	switch e {
	case 'd':
		p.i++
		return classChar{parts: []btClassPart{{ranges: []btRange{{'0', '9'}}}}}, nil
	case 'D':
		p.i++
		return classChar{parts: []btClassPart{{neg: true, ranges: []btRange{{'0', '9'}}}}}, nil
	case 'w':
		p.i++
		return classChar{parts: []btClassPart{{ranges: wordRanges}}}, nil
	case 'W':
		p.i++
		return classChar{parts: []btClassPart{{neg: true, ranges: wordRanges}}}, nil
	case 's':
		p.i++
		return classChar{parts: []btClassPart{{ranges: spaceRanges}}}, nil
	case 'S':
		p.i++
		return classChar{parts: []btClassPart{{neg: true, ranges: spaceRanges}}}, nil
	case 'n', 't', 'r', 'f', 'v':
		p.i++
		return classChar{parts: []btClassPart{{ranges: []btRange{{ctrl(e), ctrl(e)}}}}}, nil
	case 'x':
		v, ok := p.parseHex(2)
		if !ok {
			return classChar{}, errors.New("invalid regular expression: incomplete \\x escape")
		}
		return classChar{parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
	case 'u':
		if p.i+1 < len(p.src) && p.src[p.i+1] == '{' {
			j := p.i + 2
			for j < len(p.src) && p.src[j] != '}' {
				j++
			}
			if j >= len(p.src) {
				return classChar{}, errors.New("invalid regular expression: unterminated \\u escape")
			}
			v := hexVal(p.src[p.i+2 : j])
			p.i = j + 1
			return classChar{parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
		}
		v, ok := p.parseHex(4)
		if !ok {
			return classChar{}, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return classChar{parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
	case '0':
		p.i++
		return classChar{parts: []btClassPart{{ranges: []btRange{{0, 0}}}}}, nil
	default:
		// Literal metachar or ident char.
		p.i++
		ch := rune(e)
		return classChar{parts: []btClassPart{{ranges: []btRange{{ch, ch}}}}}, nil
	}
}

// parseEscape parses an escape that appears outside a character class.
func (p *btParser) parseEscape() (btNode, error) {
	p.i++ // consume '\'
	if p.i >= len(p.src) {
		return btNode{}, errors.New("invalid regular expression: trailing backslash")
	}
	e := p.src[p.i]
	switch {
	case e >= '1' && e <= '9':
		idx := int(e - '0')
		p.i++
		return btNode{kind: btBackref, refIdx: idx}, nil
	case e == 'k':
		// \k<name>
		if p.i+1 < len(p.src) && p.src[p.i+1] == '<' {
			j := p.i + 2
			for j < len(p.src) && p.src[j] != '>' {
				j++
			}
			if j >= len(p.src) {
				return btNode{}, errors.New("invalid regular expression: unterminated \\k escape")
			}
			name := p.src[p.i+2 : j]
			p.i = j + 1
			// refIdx 置 -1，使 matchBackref 走命名组分支（默认 0 会被当成组 0 整体匹配）。
			return btNode{kind: btBackref, refIdx: -1, refName: name}, nil
		}
		p.i++
		return btNode{kind: btLit, lit: 'k'}, nil
	case e == 'b':
		p.i++
		return btNode{kind: btAnchor, anchor: ancWord}, nil
	case e == 'B':
		p.i++
		return btNode{kind: btAnchor, anchor: ancNonWord}, nil
	case e == 'd':
		p.i++
		return btNode{kind: btClass, parts: []btClassPart{{ranges: []btRange{{'0', '9'}}}}}, nil
	case e == 'D':
		p.i++
		return btNode{kind: btClass, negated: true, parts: []btClassPart{{ranges: []btRange{{'0', '9'}}}}}, nil
	case e == 'w':
		p.i++
		return btNode{kind: btClass, parts: []btClassPart{{ranges: wordRanges}}}, nil
	case e == 'W':
		p.i++
		return btNode{kind: btClass, negated: true, parts: []btClassPart{{ranges: wordRanges}}}, nil
	case e == 's':
		p.i++
		return btNode{kind: btClass, parts: []btClassPart{{ranges: spaceRanges}}}, nil
	case e == 'S':
		p.i++
		return btNode{kind: btClass, negated: true, parts: []btClassPart{{ranges: spaceRanges}}}, nil
	case e == 'n', e == 't', e == 'r', e == 'f', e == 'v':
		p.i++
		return btNode{kind: btLit, lit: ctrl(e)}, nil
	case e == 'x':
		v, ok := p.parseHex(2)
		if !ok {
			return btNode{}, errors.New("invalid regular expression: incomplete \\x escape")
		}
		return btNode{kind: btLit, lit: v}, nil
	case e == 'u':
		if p.i+1 < len(p.src) && p.src[p.i+1] == '{' {
			j := p.i + 2
			for j < len(p.src) && p.src[j] != '}' {
				j++
			}
			if j >= len(p.src) {
				return btNode{}, errors.New("invalid regular expression: unterminated \\u escape")
			}
			v := hexVal(p.src[p.i+2 : j])
			p.i = j + 1
			return btNode{kind: btLit, lit: v}, nil
		}
		v, ok := p.parseHex(4)
		if !ok {
			return btNode{}, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return btNode{kind: btLit, lit: v}, nil
	case e == '0':
		p.i++
		return btNode{kind: btLit, lit: 0}, nil
	case e == 'c':
		p.i++
		if p.i < len(p.src) {
			ch := p.src[p.i]
			if ch >= 'A' && ch <= 'Z' {
				p.i++
				return btNode{kind: btLit, lit: rune(ch & 0x1f)}, nil
			}
		}
		return btNode{kind: btLit, lit: 'c'}, nil
	default:
		// Literal metachar escape.
		p.i++
		return btNode{kind: btLit, lit: rune(e)}, nil
	}
}

func (p *btParser) parseHex(n int) (rune, bool) {
	if p.i+1+n > len(p.src)+1 {
		return 0, false
	}
	if p.i+1+n > len(p.src) {
		return 0, false
	}
	hex := p.src[p.i+1 : p.i+1+n]
	for _, c := range hex {
		if !isHexDigit(byte(c)) {
			return 0, false
		}
	}
	p.i += 1 + n
	return hexVal(hex), true
}

func hexVal(s string) rune {
	var v rune
	for _, c := range s {
		v = v*16 + hexDigit(byte(c))
	}
	return v
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexDigit(b byte) rune {
	switch {
	case b >= '0' && b <= '9':
		return rune(b - '0')
	case b >= 'a' && b <= 'f':
		return rune(b-'a') + 10
	default:
		return rune(b-'A') + 10
	}
}

func ctrl(e byte) rune {
	switch e {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case 'f':
		return '\f'
	case 'v':
		return '\v'
	}
	return rune(e)
}

var wordRanges = []btRange{{'a', 'z'}, {'A', 'Z'}, {'0', '9'}, {'_', '_'}}

// spaceRanges 是 JS 的 \s 全集（WhiteSpace + LineTerminator），与
// translate.go 的 jsWhiteSpaceClass 一致——Go 的 \s 只是 ASCII 子集。
var spaceRanges = []btRange{
	{'\t', '\r'},             // TAB LF VT FF CR（0x09-0x0D）
	{' ', ' '},               // SP
	{0x00A0, 0x00A0},         // NBSP
	{0x1680, 0x1680},         // OGHAM SPACE MARK
	{0x2000, 0x200A},         // EN QUAD .. HAIR SPACE
	{0x2028, 0x2029},         // LS PS
	{0x202F, 0x202F},         // NNBSP
	{0x205F, 0x205F},         // MMSP
	{0x3000, 0x3000},         // IDEOGRAPHIC SPACE
	{0xFEFF, 0xFEFF},         // ZWNBSP
}
