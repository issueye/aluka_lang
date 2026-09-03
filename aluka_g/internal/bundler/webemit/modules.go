package webemit

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/minify"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func staticModuleClosure(gr *graph.Result, entry string, kept map[string]bool) map[string]bool {
	out := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if out[key] || !kept[key] {
			continue
		}
		out[key] = true
		for spec, target := range gr.Resolutions[key] {
			dynamic := false
			for _, dep := range gr.DynamicDeps {
				if dep.Source == key && dep.Spec == spec {
					dynamic = true
					break
				}
			}
			if !dynamic && kept[target] {
				queue = append(queue, target)
			}
		}
	}
	return out
}

func collectModules(gr *graph.Result, keys []string, kept map[string]bool, opts Options) ([]emit.Module, map[string]string) {
	sort.Strings(keys)
	modules := make([]emit.Module, 0, len(keys))
	sources := make(map[string]string)
	for _, key := range keys {
		unit := gr.SourceUnits[key]
		if unit == nil || !kept[key] {
			continue
		}
		if opts.Sourcemap {
			sources[key] = string(unit.Source)
		}
		if opts.Minify {
			minify.Program(unit.Program)
		}
		modules = append(modules, emit.Module{
			ID:             key,
			Prog:           unit.Program,
			IsTLA:          unit.HasTLA,
			IsCJS:          unit.ModuleKind == module.ModuleCommonJS,
			Resolved:       gr.Resolutions[key],
			DynamicImports: dynamicImportsFor(key, gr.DynamicDeps),
		})
	}
	return modules, sources
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func sourceUnitKeys(gr *graph.Result) []string {
	keys := make([]string, 0, len(gr.SourceUnits))
	for key := range gr.SourceUnits {
		keys = append(keys, key)
	}
	return keys
}

func dynamicImportsFor(source string, deps []graph.DynamicDep) map[string]emit.DynamicImport {
	out := make(map[string]emit.DynamicImport)
	for _, dep := range deps {
		if dep.Source != source {
			continue
		}
		out[dep.Spec] = emit.DynamicImport{Chunk: dynamicChunkName(dep.Target), Target: dep.Target}
	}
	return out
}

func dynamicChunkName(target string) string {
	clean := filepath.ToSlash(target)
	var h uint32 = 2166136261
	for i := 0; i < len(clean); i++ {
		h ^= uint32(clean[i])
		h *= 16777619
	}
	return fmt.Sprintf("chunk-%08x.js", h)
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
