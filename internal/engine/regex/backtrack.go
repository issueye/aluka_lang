package regex

import (
	"errors"
	"unicode/utf8"
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
	// 当前打开中的捕获组上下文。量词帧保存完整栈，使嵌套捕获内的懒重复
	// 回退后能更新每一层捕获终点。
	openGroups []btOpenGroup
	// steps 是本轮匹配的步数预算（灾难性回溯护栏）：超限后 aborted 置位，
	// 所有匹配路径立即短路失败，exec 不再尝试后续起始位置（本轮整体
	// 返回不匹配）。lookaround 子状态与父状态共享预算。
	steps   int
	limit   int
	aborted bool
}

// btMaxSteps 是一次 exec（含所有候选起始位置）的总步数上限。正常模式
// （含依赖里的全部形态）远低于此值；超限只发生在灾难性回溯（如
// (a+)+b 对长串）。语义取向：不抛错，按不匹配处理（保守；正确答案
// 本就不存在或极难命中）。
const btMaxSteps = 1 << 22

// spendStep 消耗一步预算；超限或已中止时返回 false（调用方应立即失败返回）。
func (st *btState) spendStep() bool {
	if st.aborted {
		return false
	}
	st.steps++
	limit := st.limit
	if limit <= 0 {
		limit = btMaxSteps
	}
	if st.steps > limit {
		st.aborted = true
		return false
	}
	return true
}

// syncBudget 把 lookaround 子状态消耗的预算同步回父状态。
func (st *btState) syncBudget(sub *btState) {
	if sub.steps > st.steps {
		st.steps = sub.steps
	}
	if sub.aborted {
		st.aborted = true
	}
}

type btOpenGroup struct {
	idx   int
	start int
}

// btFrame 是序列内重复量词的回退帧。贪心重复在"少吃一次"时压帧；
// 懒重复在"多吃一次"时压帧。帧保存压入时刻的位置与捕获组。
type btFrame struct {
	nodes []btNode // 所属序列（判断 pc 是否可解释为当前序列的索引）
	pc    int      // 重复节点后继节点在所属序列中的索引
	pos   int
	caps  []int
	more  bool // lazy：弹帧后先多吃一个子节点再继续
	child *btNode
	// 帧压入时的完整打开捕获组栈。
	openGroups []btOpenGroup
}

// exec finds the first match at or after a given start byte offset.
// Returns the match indices [wholeStart, wholeEnd, g1s, g1e, ...] or nil.
func (r *btRegexp) exec(s string, start int) []int {
	m, _, _ := r.execWithLimit(s, start, btMaxSteps)
	return m
}

// execWithLimit 是带可配置步数上限的内部执行器。生产 exec 用 btMaxSteps；
// 测试用低上限明确验证护栏实际触发（而非仅凭"执行很快"推断）。
func (r *btRegexp) execWithLimit(s string, start, limit int) (match []int, aborted bool, steps int) {
	n := len(s)
	for p := start; p <= n; p = nextRuneBoundary(s, p) {
		caps := make([]int, (r.numGroups+1)*2)
		for i := range caps {
			caps[i] = -1
		}
		remaining := limit - steps
		if remaining <= 0 {
			return nil, true, steps
		}
		st := &btState{captures: caps, pos: p, limit: remaining}
		ok := r.matchSeq(s, st, r.root) && matchEnd(r, s, st)
		steps += st.steps
		if st.aborted {
			return nil, true, steps
		}
		if ok {
			out := make([]int, len(caps))
			copy(out, caps)
			out[0] = p      // whole match start（根节点非捕获组时 caps[0] 仍为 -1）
			out[1] = st.pos // whole match end
			return out, false, steps
		}
	}
	return nil, false, steps
}

// matchEnd verifies the pattern consumed the whole root (the root is the
// concatenation of all top-level nodes; exec handles ^ anchoring).
func matchEnd(r *btRegexp, s string, st *btState) bool { return true }

// matchNode matches a node starting at st.pos, advancing st.pos on success.
func (r *btRegexp) matchNode(s string, st *btState, n *btNode) bool {
	if !st.spendStep() {
		return false
	}
	switch n.kind {
	case btLit:
		if size := r.matchAtomAt(s, st.pos, n.lit); size > 0 {
			st.pos += size
			return true
		}
		return false
	case btClass:
		if size := r.matchClassAt(s, st.pos, n); size > 0 {
			st.pos += size
			return true
		}
		return false
	case btDot:
		if size := r.matchDotAt(s, st.pos, n.dotAll || r.dotAll); size > 0 {
			st.pos += size
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
			openBase := len(st.openGroups)
			if n.grpKind == grpCapture {
				st.openGroups = append(st.openGroups, btOpenGroup{idx: n.groupIdx, start: start})
			}
			if !r.matchSeq(s, st, n.sub) {
				st.openGroups = st.openGroups[:openBase]
				return false
			}
			st.openGroups = st.openGroups[:openBase]
			if n.grpKind == grpCapture {
				r.setCapture(st, n.groupIdx, start, st.pos)
			}
			return true
		case grpLookahead:
			// V8 语义：前瞻内的捕获组会写入整体匹配结果（如 /(?=(a))b/ 组1 = "a"）。
			sub := &btState{captures: cloneCaps(st.captures), pos: st.pos, steps: st.steps, limit: st.limit}
			if r.matchSeq(s, sub, n.sub) {
				copy(st.captures, sub.captures)
				st.syncBudget(sub)
				return true
			}
			st.syncBudget(sub)
			return false
		case grpNegLookahead:
			sub := &btState{captures: cloneCaps(st.captures), pos: st.pos, steps: st.steps, limit: st.limit}
			matched := r.matchSeq(s, sub, n.sub)
			st.syncBudget(sub)
			return !matched
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
	if !st.spendStep() {
		return false // 步数预算耗尽：立即短路失败
	}
	base := len(st.frames)
	startPos := st.pos
	startCaps := cloneCaps(st.captures)
	pc := 0
	for pc < len(nodes) {
		if st.aborted {
			st.pos = startPos
			copy(st.captures, startCaps)
			st.frames = st.frames[:base]
			return false
		}
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
	clearFrom := firstCaptureIndex(n.child)
	// 先满足 min 次（不可回退）。
	for count < n.min {
		before := st.pos
		if count > 0 {
			clearCaptures(st.captures, clearFrom)
		}
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
				openGroups: cloneOpenGroups(st.openGroups),
			})
			clearCaptures(st.captures, clearFrom)
			if !r.matchNode(s, st, n.child) {
				last := st.frames[len(st.frames)-1]
				copy(st.captures, last.caps)
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
			openGroups: cloneOpenGroups(st.openGroups),
		})
	}
	return true
}

// backtrackSeq 弹出最近的回退帧恢复状态并让序列重试。返回 false 表示
// 无更多回退点（序列失败）。
func (r *btRegexp) backtrackSeq(s string, st *btState, nodes []btNode, pc *int, base int) bool {
	for len(st.frames) > base {
		if !st.spendStep() {
			return false
		}
		f := st.frames[len(st.frames)-1]
		st.frames = st.frames[:len(st.frames)-1]
		st.pos = f.pos
		copy(st.captures, f.caps)
		st.openGroups = cloneOpenGroups(f.openGroups)
		r.updateOpenCaptures(st)
		if f.more {
			// 懒重复：先多吃一个子节点，并压新的"再吃一个"帧。
			if !r.matchNode(s, st, f.child) {
				continue
			}
			r.updateOpenCaptures(st)
			st.frames = append(st.frames, btFrame{
				nodes: f.nodes, pc: f.pc, pos: st.pos, caps: cloneCaps(st.captures),
				more: true, child: f.child,
				openGroups: cloneOpenGroups(f.openGroups),
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
			return pos == 0 || isLineTerminatorBefore(s, pos)
		}
		return pos == 0
	case ancEnd:
		if r.multiline {
			return pos == len(s) || isLineTerminatorAt(s, pos)
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
	before := false
	if pos > 0 {
		ch, _ := utf8.DecodeLastRuneInString(s[:pos])
		before = isWordRune(ch)
	}
	after := false
	if pos < len(s) {
		ch, _ := utf8.DecodeRuneInString(s[pos:])
		after = isWordRune(ch)
	}
	if boundary {
		return before != after
	}
	return before == after
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// matchAtomAt 匹配 pos 处的单个字面量，返回消费的字节数。
func (r *btRegexp) matchAtomAt(s string, pos int, lit rune) int {
	if pos >= len(s) {
		return 0
	}
	ch, size := utf8.DecodeRuneInString(s[pos:])
	if r.ignoreCase {
		if lower(ch) == lower(lit) {
			return size
		}
		return 0
	}
	if ch == lit {
		return size
	}
	return 0
}

// matchClassAt 匹配 pos 处的单个字符，返回消费的字节数。
func (r *btRegexp) matchClassAt(s string, pos int, n *btNode) int {
	if pos >= len(s) {
		return 0
	}
	ch, size := utf8.DecodeRuneInString(s[pos:])
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
		if !m {
			return size
		}
		return 0
	}
	if m {
		return size
	}
	return 0
}

func (r *btRegexp) matchDotAt(s string, pos int, dotAll bool) int {
	if pos >= len(s) {
		return 0
	}
	ch, size := utf8.DecodeRuneInString(s[pos:])
	if dotAll || !isLineTerminator(ch) {
		return size
	}
	return 0
}

func (r *btRegexp) setCapture(st *btState, idx, start, end int) {
	st.captures[idx*2] = start
	st.captures[idx*2+1] = end
}

func (r *btRegexp) updateOpenCaptures(st *btState) {
	for _, group := range st.openGroups {
		r.setCapture(st, group.idx, group.start, st.pos)
	}
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
	// Try each possible rune boundary up to st.pos.
	for p := 0; p <= start; p = nextRuneBoundary(s, p) {
		sub := &btState{captures: cloneCaps(st.captures), pos: p, steps: st.steps, limit: st.limit}
		if r.matchSeq(s, sub, n.sub) && sub.pos == start {
			ok = true
			// 与前瞻一致：后行断言内的捕获组写入整体结果。
			copy(st.captures, sub.captures)
			st.syncBudget(sub)
			break
		}
		st.syncBudget(sub)
		if st.aborted {
			return false
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

func cloneOpenGroups(groups []btOpenGroup) []btOpenGroup {
	return append([]btOpenGroup(nil), groups...)
}

func firstCaptureIndex(n *btNode) int {
	first := 0
	var visit func(*btNode)
	visit = func(node *btNode) {
		if node == nil {
			return
		}
		if node.kind == btGroup && node.grpKind == grpCapture && (first == 0 || node.groupIdx < first) {
			first = node.groupIdx
		}
		for i := range node.sub {
			visit(&node.sub[i])
		}
		visit(node.child)
		for _, alt := range node.alts {
			for i := range alt {
				visit(&alt[i])
			}
		}
	}
	visit(n)
	return first
}

func clearCaptures(captures []int, from int) {
	if from <= 0 {
		return
	}
	for i := from * 2; i < len(captures); i++ {
		captures[i] = -1
	}
}

func nextRuneBoundary(s string, pos int) int {
	if pos >= len(s) {
		return len(s) + 1
	}
	_, size := utf8.DecodeRuneInString(s[pos:])
	return pos + size
}

func isLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}

func isLineTerminatorAt(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return isLineTerminator(r)
}

func isLineTerminatorBefore(s string, pos int) bool {
	if pos <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	return isLineTerminator(r)
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
	unicode    bool
	numGroups  int
	groupNames map[string]int
}

// compileBacktrack parses pattern into a backtracking matcher.
func compileBacktrack(pattern string, f Flags) (*btRegexp, error) {
	p := &btParser{src: pattern, unicode: f.Unicode || f.UnicodeSets, groupNames: map[string]int{}}
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
		lit, size := utf8.DecodeRuneInString(p.src[p.i:])
		p.i += size
		return btNode{kind: btLit, lit: lit}, nil
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
			// 表示法本身已足够：parts 为空时 classMatched=false，negated=true
			// 整体取反为 true；不要再塞全范围 part，否则二次取反会变成永不匹配。
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
				if ch.lo > hi.lo {
					return btNode{}, errors.New("invalid regular expression: range out of order in character class")
				}
				node.parts = append(node.parts, btClassPart{ranges: []btRange{{ch.lo, hi.lo}}})
				continue
			}
			if p.unicode {
				return btNode{}, errors.New("invalid regular expression: invalid character class range")
			}
			// Annex B legacy 模式：shorthand 不能作为范围端点，'-' 按字面量。
			node.parts = append(node.parts, ch.parts...)
			node.parts = append(node.parts, btClassPart{ranges: []btRange{{'-', '-'}}})
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
	lit, size := utf8.DecodeRuneInString(p.src[p.i:])
	p.i += size
	return classChar{kind: 0, lo: lit, parts: []btClassPart{{ranges: []btRange{{lit, lit}}}}}, nil
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
		return classChar{kind: 1, parts: []btClassPart{{ranges: []btRange{{'0', '9'}}}}}, nil
	case 'D':
		p.i++
		return classChar{kind: 1, parts: []btClassPart{{neg: true, ranges: []btRange{{'0', '9'}}}}}, nil
	case 'w':
		p.i++
		return classChar{kind: 1, parts: []btClassPart{{ranges: wordRanges}}}, nil
	case 'W':
		p.i++
		return classChar{kind: 1, parts: []btClassPart{{neg: true, ranges: wordRanges}}}, nil
	case 's':
		p.i++
		return classChar{kind: 1, parts: []btClassPart{{ranges: spaceRanges}}}, nil
	case 'S':
		p.i++
		return classChar{kind: 1, parts: []btClassPart{{neg: true, ranges: spaceRanges}}}, nil
	case 'n', 't', 'r', 'f', 'v':
		p.i++
		ch := ctrl(e)
		return classChar{kind: 0, lo: ch, parts: []btClassPart{{ranges: []btRange{{ch, ch}}}}}, nil
	case 'x':
		v, ok := p.parseHex(2)
		if !ok {
			return classChar{}, errors.New("invalid regular expression: incomplete \\x escape")
		}
		return classChar{kind: 0, lo: v, parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
	case 'u':
		if p.i+1 < len(p.src) && p.src[p.i+1] == '{' {
			j := p.i + 2
			for j < len(p.src) && p.src[j] != '}' {
				j++
			}
			if j >= len(p.src) {
				return classChar{}, errors.New("invalid regular expression: unterminated \\u escape")
			}
			v := patternCodeUnit(hexVal(p.src[p.i+2:j]), Flags{Unicode: p.unicode})
			p.i = j + 1
			return classChar{kind: 0, lo: v, parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
		}
		v, ok := p.parseHex(4)
		if !ok {
			return classChar{}, errors.New("invalid regular expression: incomplete \\u escape")
		}
		v = patternCodeUnit(v, Flags{Unicode: p.unicode})
		return classChar{kind: 0, lo: v, parts: []btClassPart{{ranges: []btRange{{v, v}}}}}, nil
	case '0':
		p.i++
		return classChar{kind: 0, lo: 0, parts: []btClassPart{{ranges: []btRange{{0, 0}}}}}, nil
	default:
		if p.unicode {
			return classChar{}, errors.New("invalid regular expression: invalid escape")
		}
		literal, size := utf8.DecodeRuneInString(p.src[p.i:])
		p.i += size
		return classChar{kind: 0, lo: literal, parts: []btClassPart{{ranges: []btRange{{literal, literal}}}}}, nil
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
			v := patternCodeUnit(hexVal(p.src[p.i+2:j]), Flags{Unicode: p.unicode})
			p.i = j + 1
			return btNode{kind: btLit, lit: v}, nil
		}
		v, ok := p.parseHex(4)
		if !ok {
			return btNode{}, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return btNode{kind: btLit, lit: patternCodeUnit(v, Flags{Unicode: p.unicode})}, nil
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
		if p.unicode {
			return btNode{}, errors.New("invalid regular expression: invalid escape")
		}
		literal, size := utf8.DecodeRuneInString(p.src[p.i:])
		p.i += size
		return btNode{kind: btLit, lit: literal}, nil
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
	{'\t', '\r'},     // TAB LF VT FF CR（0x09-0x0D）
	{' ', ' '},       // SP
	{0x00A0, 0x00A0}, // NBSP
	{0x1680, 0x1680}, // OGHAM SPACE MARK
	{0x2000, 0x200A}, // EN QUAD .. HAIR SPACE
	{0x2028, 0x2029}, // LS PS
	{0x202F, 0x202F}, // NNBSP
	{0x205F, 0x205F}, // MMSP
	{0x3000, 0x3000}, // IDEOGRAPHIC SPACE
	{0xFEFF, 0xFEFF}, // ZWNBSP
}
