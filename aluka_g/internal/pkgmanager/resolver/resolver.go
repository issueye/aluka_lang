// Package resolver 实现依赖解析（Phase 5 WBS 5.3）。
//
// 简化 hoisting 策略（P0）：
//   - 自根依赖开始 BFS，对每个包用 semver 选范围内最高版本。
//   - 同一名字只解析一次（先到先得，近似 npm 顶层 hoisting）；
//     冲突版本不做嵌套（文档记录简化）。
//   - peerDependencies 并入常规依赖安装；optionalDependencies 解析失败时跳过。
package resolver

import (
	"fmt"
	"sort"

	"github.com/aluka-lang/aluka/internal/pkgmanager/registry"
	"github.com/aluka-lang/aluka/internal/pkgmanager/semver"
)

// Dep 是单条依赖边。
type Dep struct {
	Name     string
	Range    string
	Optional bool // optionalDependencies 成员：解析失败时跳过
}

// Resolved 是解析后的单个包。
type Resolved struct {
	Name         string
	Version      string
	Tarball      string
	Dependencies []Dep
}

// Resolution 是完整解析结果。
type Resolution struct {
	Roots []Dep
	Pkgs  map[string]*Resolved // key = name@version
	// order 保留安装顺序（解析顺序，父依赖先于子依赖）。
	order []string
}

// PkgOrder 返回安装顺序（解析顺序）。
func (r *Resolution) PkgOrder() []*Resolved {
	out := make([]*Resolved, 0, len(r.order))
	for _, k := range r.order {
		if p, ok := r.Pkgs[k]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Resolver 解析依赖树。
type Resolver struct {
	client  *registry.Client
	hoisted map[string]string // name → version（已解析版本）
	pkgs    map[string]*Resolved
	order   []string
}

// New 创建解析器。
func New(client *registry.Client) *Resolver {
	return &Resolver{
		client:  client,
		hoisted: map[string]string{},
		pkgs:    map[string]*Resolved{},
	}
}

// Resolve 解析根依赖集合。
func (res *Resolver) Resolve(rootDeps []Dep) (*Resolution, error) {
	queue := append([]Dep(nil), rootDeps...)
	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]
		if err := res.resolveOne(dep, &queue); err != nil {
			if dep.Optional {
				continue // 可选依赖失败不阻塞安装。
			}
			return nil, err
		}
	}
	return &Resolution{Roots: rootDeps, Pkgs: res.pkgs, order: res.order}, nil
}

// resolveOne 解析单个依赖并把其子依赖入队。
func (res *Resolver) resolveOne(dep Dep, queue *[]Dep) error {
	// 已解析（hoisting 先到先得）。
	if _, ok := res.hoisted[dep.Name]; ok {
		return nil
	}
	md, err := res.client.GetMetadata(dep.Name)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dep.Name, err)
	}
	// 候选版本。
	var cands []semver.Version
	for vs := range md.Versions {
		v, perr := semver.Parse(vs)
		if perr != nil {
			continue
		}
		cands = append(cands, v)
	}
	sort.Slice(cands, func(i, j int) bool { return semver.Compare(cands[i], cands[j]) > 0 })
	rng, err := semver.ParseRange(dep.Range)
	if err != nil {
		return fmt.Errorf("resolve %s: bad range %q: %w", dep.Name, dep.Range, err)
	}
	best, ok := semver.MaxSatisfying(cands, rng)
	if !ok {
		return fmt.Errorf("resolve %s: no version satisfies %q", dep.Name, dep.Range)
	}
	ver := md.Versions[best.String()]
	resolved := &Resolved{
		Name:    dep.Name,
		Version: best.String(),
		Tarball: ver.Dist.Tarball,
	}
	if resolved.Tarball == "" {
		return fmt.Errorf("resolve %s@%s: missing tarball in metadata", dep.Name, best.String())
	}
	// 合并 dependencies + peerDependencies。
	all := map[string]string{}
	for k, v := range ver.Dependencies {
		all[k] = v
	}
	for k, v := range ver.PeerDependencies {
		all[k] = v
	}
	var sub []Dep
	for k, v := range all {
		resolved.Dependencies = append(resolved.Dependencies, Dep{Name: k, Range: v})
		sub = append(sub, Dep{Name: k, Range: v})
	}
	// 可选依赖（不覆盖常规/peer 已含的同名项）。
	for k, v := range ver.OptionalDependencies {
		if _, dup := all[k]; dup {
			continue
		}
		resolved.Dependencies = append(resolved.Dependencies, Dep{Name: k, Range: v})
		sub = append(sub, Dep{Name: k, Range: v, Optional: true})
	}
	// 记录 + 子依赖入队。
	key := dep.Name + "@" + best.String()
	res.hoisted[dep.Name] = best.String()
	res.pkgs[key] = resolved
	res.order = append(res.order, key)
	*queue = append(*queue, sub...)
	return nil
}
