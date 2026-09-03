package project

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// BuildWeb 构建单个 web 入口（.html / .css / .js / .ts / .tsx）。
// JS 图的 shake/emit 交给 bundler/webemit；本函数管插件生命周期与 HTML 编排。
func BuildWeb(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (bundled Bundle, err error) {
	if resolver == nil {
		resolver = module.NewResolver()
	}
	host := opts.Host()
	// BuildStart 中途失败时仍跑 closeBundle，释放已启动插件的资源。
	defer func() {
		if cerr := host.CloseBundle(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if err = host.BuildStart(); err != nil {
		return Bundle{}, err
	}

	bundled, err = bundleEntry(rt, resolver, entry, opts)
	if err != nil {
		return Bundle{}, err
	}
	extra, err := host.GenerateBundle(assetNames(bundled.Assets))
	if err != nil {
		return Bundle{}, err
	}
	outDir := OutputDir(opts)
	for name, content := range extra {
		if strings.TrimSpace(name) == "" {
			return Bundle{}, fmt.Errorf("generateBundle: asset name is empty")
		}
		if _, err = assetTarget(outDir, name); err != nil {
			return Bundle{}, fmt.Errorf("generateBundle: %w", err)
		}
		bundled.Assets[name] = []byte(content)
	}
	return bundled, nil
}

func useNativeESM(opts Options) bool {
	return opts.Format == "" || opts.Format == "esm"
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
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

func assetNames(assets map[string][]byte) []string {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
