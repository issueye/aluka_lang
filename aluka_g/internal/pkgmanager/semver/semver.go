// Package semver 实现 npm 兼容的语义化版本解析、比较与范围匹配（Phase 5 WBS 5.2）。
//
// 自研实现（不引入外部 semver 库），支持：
//   - 版本解析：1.2.3 / 1.2.3-beta.1+meta / v1.2.3 / 1.2 / 1（缺失补 0）
//   - 范围：^、~、>=、<=、>、<、=、裸版本、x/* 通配、空格 AND、|| OR
//   - prerelease：按 npm 规则参与排序；范围匹配时，预发布版本仅被
//     显式包含其 prerelease 的比较器匹配（npm 排除规则）
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 是解析后的语义化版本。
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // prerelease 标识符（空 = 稳定版）
	Build               []string // build metadata（不参与比较）
}

// Parse 解析版本字符串（容错 v 前缀、缺失部分、通配 x）。
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("semver: empty version")
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	var v Version
	// build metadata。
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if s[i+1:] != "" {
			v.Build = strings.Split(s[i+1:], ".")
		}
		s = s[:i]
	}
	// prerelease。
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if s[i+1:] != "" {
			v.Pre = strings.Split(s[i+1:], ".")
		}
		s = s[:i]
	}
	if s == "" {
		return Version{}, fmt.Errorf("semver: empty version")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return Version{}, fmt.Errorf("semver: too many components %q", s)
	}
	nums := make([]int, 0, 3)
	for _, p := range parts {
		if p == "" {
			return Version{}, fmt.Errorf("semver: empty component in %q", s)
		}
		// 通配组件（允许 "1.x.2" 形式的宽容解析，虽然规范不允许）。
		if p == "x" || p == "X" || p == "*" {
			nums = append(nums, 0)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("semver: invalid component %q", p)
		}
		nums = append(nums, n)
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// String 输出规范版本串（含 prerelease，忽略 build）。
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// IsPre 判断是否为预发布版本。
func (v Version) IsPre() bool { return len(v.Pre) > 0 }

// comparePre 比较 prerelease 标识符序列（npm 规则）。
func comparePre(a, b []string) int {
	// 有 prerelease < 无 prerelease。
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePreID(a[i], b[i]); c != 0 {
			return c
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// comparePreID 比较单个 prerelease 标识符（数字按数值、字母按字典序、数字 < 字母）。
func comparePreID(a, b string) int {
	an, aIsNum := isNumeric(a)
	bn, bIsNum := isNumeric(b)
	switch {
	case aIsNum && bIsNum:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	case aIsNum && !bIsNum:
		return -1
	case !aIsNum && bIsNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isNumeric(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Compare 比较两个版本（忽略 build metadata）。
// 返回 -1 / 0 / 1（a < b / a == b / a > b）。
func Compare(a, b Version) int {
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}
	return comparePre(a.Pre, b.Pre)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Range 是版本范围（多组 AND 比较器，组间 OR）。
type Range struct {
	sets [][]Comparator
	any  bool // 匹配任意版本（* / latest / 空）
}

// Comparator 是单个版本比较器。
type Comparator struct {
	Op  string // "", ">=", "<=", ">", "<", "~", "^"
	Ver Version
	// 通配级别：0=精确，1=major，2=minor，3=patch（如 1.x → 2）。
	wild int
}

// ParseRange 解析版本范围字符串。
func ParseRange(s string) (Range, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" || s == "latest" || s == "x" || s == "X" {
		return Range{any: true}, nil
	}
	var r Range
	for _, group := range strings.Split(s, "||") {
		comps, err := parseSet(group)
		if err != nil {
			return Range{}, err
		}
		r.sets = append(r.sets, comps)
	}
	return r, nil
}

// parseSet 解析一个空格分隔的比较器组（AND）。
func parseSet(s string) ([]Comparator, error) {
	var comps []Comparator
	toks := strings.Fields(s)
	i := 0
	for i < len(toks) {
		tok := toks[i]
		// npm 允许操作符与版本间有空格（如 ">= 2.1.2 < 3.0.0"）：
		// 裸操作符 token 与下一个版本 token 合并。
		if isBareOp(tok) && i+1 < len(toks) {
			tok += toks[i+1]
			i += 2
		} else {
			i++
		}
		c, err := parseComparator(tok)
		if err != nil {
			return nil, err
		}
		comps = append(comps, c)
	}
	if len(comps) == 0 {
		return []Comparator{{Op: "", Ver: Version{}, wild: 0}}, nil
	}
	return comps, nil
}

// isBareOp 判断 token 是否为不带动版本的操作符（需与下一个 token 合并）。
func isBareOp(tok string) bool {
	switch tok {
	case ">=", "<=", "==", ">", "<", "~", "^", "=":
		return true
	}
	return false
}

// parseComparator 解析单个比较器 token。
func parseComparator(tok string) (Comparator, error) {
	op := ""
	rest := tok
	for _, p := range []string{">=", "<=", "==", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(tok, p) {
			op = p
			rest = tok[len(p):]
			break
		}
	}
	if op == "==" {
		op = "="
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return Comparator{}, fmt.Errorf("semver: comparator %q missing version", tok)
	}
	// 通配级别检测。
	wild := 0
	parts := strings.Split(rest, ".")
	for i, p := range parts {
		if p == "x" || p == "X" || p == "*" {
			wild = i + 1
			// 之后的组件都视为缺失。
			parts = parts[:i]
			rest = strings.Join(parts, ".")
			if rest == "" {
				// 全部通配：如 "*"、"1.x" 的 rest 只剩 "1" 或 ""。
			}
			break
		}
	}
	v, err := Parse(rest)
	if err != nil {
		return Comparator{}, fmt.Errorf("semver: bad comparator %q: %w", tok, err)
	}
	// 通配版本：通配组件补 0 已在 Parse 处理（例如 "1.2.x" → "1.2" → 1.2.0）。
	return Comparator{Op: op, Ver: v, wild: wild}, nil
}

// Test 判断版本是否落在范围内。
func (r Range) Test(v Version) bool {
	if r.any {
		// npm 语义：* 不匹配 prerelease。
		return !v.IsPre()
	}
	for _, group := range r.sets {
		ok := true
		for _, c := range group {
			if !c.test(v) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// test 判断单个比较器是否匹配。
func (c Comparator) test(v Version) bool {
	base := c.Ver
	// prerelease 排除规则：v 有 prerelease 但比较器版本没有 → 不匹配。
	if v.IsPre() && !base.IsPre() {
		return false
	}
	switch {
	case c.wild > 0:
		// x 通配 → 半开区间。
		lo, hi := wildBounds(c)
		return Compare(v, lo) >= 0 && Compare(v, hi) < 0
	case c.Op == "" || c.Op == "=":
		return Compare(v, base) == 0
	case c.Op == ">":
		return Compare(v, base) > 0
	case c.Op == "<":
		return Compare(v, base) < 0
	case c.Op == ">=":
		return Compare(v, base) >= 0
	case c.Op == "<=":
		return Compare(v, base) <= 0
	case c.Op == "^":
		lo, hi := caretBounds(base)
		return Compare(v, lo) >= 0 && Compare(v, hi) < 0
	case c.Op == "~":
		lo, hi := tildeBounds(base)
		return Compare(v, lo) >= 0 && Compare(v, hi) < 0
	}
	return false
}

// wildBounds 计算 x 通配区间 [lo, hi)。
// wild 是通配组件序号：2=minor 通配（1.x → [1.0.0, 2.0.0)）、
// 3=patch 通配（1.2.x → [1.2.0, 1.3.0)）、4+=多级（1.2.3.x → [1.2.3, 1.2.4)）。
func wildBounds(c Comparator) (Version, Version) {
	lo := c.Ver
	hi := c.Ver
	switch c.wild {
	case 2: // 1.x
		lo.Minor, lo.Patch = 0, 0
		hi.Major, hi.Minor, hi.Patch = lo.Major+1, 0, 0
	case 3: // 1.2.x
		lo.Patch = 0
		hi.Minor, hi.Patch = lo.Minor+1, 0
	default: // 1.2.3.x 等
		hi.Patch = lo.Patch + 1
	}
	lo.Pre, hi.Pre = nil, []string{"0"}
	return lo, hi
}

// caretBounds 计算 ^ 范围 [lo, hi)。
// 下界保留 prerelease（^1.0.0-beta.2 应包含 1.0.0-beta.2 本身）；
// 上界取下一大版本的 -0（最低预发布），排除 2.0.0-rc.1 等（npm 语义：
// ^1.0.0 不匹配 2.0.0-0 及以上的任何版本）。
func caretBounds(v Version) (Version, Version) {
	lo, hi := v, v
	switch {
	case v.Major > 0:
		hi.Major, hi.Minor, hi.Patch = v.Major+1, 0, 0
	case v.Minor > 0:
		hi.Minor, hi.Patch = v.Minor+1, 0
	default:
		hi.Patch = v.Patch + 1
	}
	hi.Pre = []string{"0"}
	return lo, hi
}

// tildeBounds 计算 ~ 范围 [lo, hi)。语义同 caretBounds（保留下界 prerelease）。
func tildeBounds(v Version) (Version, Version) {
	lo, hi := v, v
	if v.Minor > 0 || v.Patch > 0 {
		hi.Minor, hi.Patch = v.Minor+1, 0
	} else {
		hi.Major, hi.Minor, hi.Patch = v.Major+1, 0, 0
	}
	hi.Pre = []string{"0"}
	return lo, hi
}

// MaxSatisfying 从候选版本列表中选出范围内最高版本。
// 无匹配返回 (zero, false)。
func MaxSatisfying(candidates []Version, r Range) (Version, bool) {
	var best Version
	found := false
	for _, v := range candidates {
		if !r.Test(v) {
			continue
		}
		if !found || Compare(v, best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}
