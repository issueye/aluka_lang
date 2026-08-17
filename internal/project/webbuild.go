package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/minify"
	"github.com/aluka-lang/aluka/internal/bundler/shake"
	"github.com/aluka-lang/aluka/internal/bundler/vue"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// BuildWeb 构建单个 web 入口（.html / .css / .js / .ts / .tsx）。
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

func bundleEntry(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (Bundle, error) {
	resolver.SetWebConditions()
	applyResolverAliases(resolver, opts.Aliases)
	ext := strings.ToLower(filepath.Ext(entry))
	empty := Bundle{Assets: map[string][]byte{}}

	if ext == ".html" {
		htmlData, err := os.ReadFile(entry)
		if err != nil {
			return empty, fmt.Errorf("read HTML entry %q: %w", entry, err)
		}
		htmlStr := string(htmlData)
		parsed := emit.ParseHTMLEntry(htmlStr)
		replacements := make(map[string]string)
		entryDir := filepath.Dir(entry)
		assets := make(map[string][]byte)
		watch := []string{mustAbs(entry)}
		var extraCSS, preload []string

		for _, script := range parsed.Scripts {
			scriptPath := filepath.Join(entryDir, filepath.FromSlash(script.Original))
			sub, err := bundleEntry(rt, resolver, scriptPath, opts)
			if err != nil {
				return empty, err
			}
			watch = append(watch, sub.Watch...)
			for _, p := range sub.Preload {
				preload = append(preload, withPublicBase(opts.PublicBase, p))
			}
			barrel := PrimaryName(scriptPath)
			for k, v := range sub.Assets {
				if k == barrel {
					continue
				}
				assets[k] = v
				if strings.HasSuffix(strings.ToLower(k), ".css") {
					extraCSS = append(extraCSS, k)
				}
			}
			scriptOut := sub.EntryJS
			if scriptOut == "" {
				scriptOut = barrel
			}
			replacements[script.Original] = withPublicBase(opts.PublicBase, scriptOut)
			if scriptOut != "" {
				preload = append(preload, withPublicBase(opts.PublicBase, scriptOut))
			}
		}

		for _, sheet := range parsed.Stylesheets {
			sheetPath := filepath.Join(entryDir, filepath.FromSlash(sheet.Original))
			cssData, err := os.ReadFile(sheetPath)
			if err != nil {
				return empty, fmt.Errorf("read stylesheet %q: %w", sheetPath, err)
			}
			watch = append(watch, mustAbs(sheetPath))
			content := string(cssData)
			if opts.Minify {
				content = emit.MinifyCSS(content)
			}
			outName := stylesheetOutName(sheet.Original, content, opts)
			if prev, ok := assets[outName]; ok && string(prev) != content {
				return empty, fmt.Errorf("stylesheet output collision %q (from %q)", outName, sheet.Original)
			}
			assets[outName] = []byte(content)
			replacements[sheet.Original] = withPublicBase(opts.PublicBase, outName)
		}

		for i, href := range extraCSS {
			extraCSS[i] = withPublicBase(opts.PublicBase, href)
		}
		rewritten := emit.RewriteHTML(htmlStr, replacements)
		rewritten = injectHTMLStylesheets(rewritten, extraCSS)
		rewritten, err = opts.Host().TransformIndexHTML(rewritten)
		if err != nil {
			return empty, err
		}
		if useNativeESM(opts) {
			rewritten = emit.EnhanceHTML(rewritten, uniqueStrings(preload))
		}
		assets[filepath.Base(entry)] = []byte(rewritten)
		return Bundle{Assets: assets, Watch: uniqueStrings(watch), Preload: uniqueStrings(preload)}, nil
	}

	if ext == ".css" {
		cssData, err := os.ReadFile(entry)
		if err != nil {
			return empty, fmt.Errorf("read CSS entry %q: %w", entry, err)
		}
		content := string(cssData)
		if opts.Minify {
			content = emit.MinifyCSS(content)
		}
		base := filepath.Base(entry)
		return Bundle{
			Assets: map[string][]byte{base: []byte(content)},
			Watch:  []string{mustAbs(entry)},
		}, nil
	}

	return bundleJS(rt, resolver, entry, opts)
}

func bundleJS(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (Bundle, error) {
	empty := Bundle{Assets: map[string][]byte{}}
	vm := runtimeVM(rt)
	var graphOpts []graph.Option
	if opts.VueCompiler == "official" {
		graphOpts = append(graphOpts, graph.WithVueCompiler(vue.NewOfficialCompiler(vm, entry)))
	}
	graphOpts = append(graphOpts, graph.WithPlugins(opts.Host()))
	graphResult, err := graph.Build(vm, resolver, entry, graphOpts...)
	if err != nil {
		return empty, err
	}
	if err := ValidateWebBuiltins(graphResult.Builtins); err != nil {
		return empty, err
	}
	if len(graphResult.UnresolvedDynamic) > 0 {
		return empty, fmt.Errorf("web target requires a string literal for dynamic import() (source %s)", graphResult.UnresolvedDynamic[0])
	}

	kept := make(map[string]bool, len(graphResult.SourceUnits))
	for key := range graphResult.SourceUnits {
		kept[key] = true
	}
	if opts.TreeShake {
		shaken, err := shake.ShakeOpts(graphResult, graphResult.Entry, shake.Options{KeepEntryExports: true})
		if err != nil {
			return empty, err
		}
		kept = shaken.Kept
	}

	baseName := filepath.Base(entry)
	if e := filepath.Ext(baseName); e != "" {
		baseName = baseName[:len(baseName)-len(e)]
	}
	jsFileName := baseName + ".js"
	defines := mergeDefines(opts)

	if useNativeESM(opts) {
		return emitNativeJS(graphResult, kept, jsFileName, baseName, defines, opts)
	}

	assets := make(map[string][]byte)
	mainModules := staticModuleClosure(graphResult, graphResult.Entry, kept)
	modules := make([]emit.Module, 0, len(mainModules))
	moduleSources := make(map[string]string)
	mainKeys := make([]string, 0, len(mainModules))
	for key := range mainModules {
		mainKeys = append(mainKeys, key)
	}
	sort.Strings(mainKeys)
	for _, key := range mainKeys {
		unit := graphResult.SourceUnits[key]
		if unit == nil || !kept[key] {
			continue
		}
		if opts.Sourcemap {
			moduleSources[key] = string(unit.Source)
		}
		if opts.Minify {
			minify.Program(unit.Program)
		}
		modules = append(modules, emit.Module{
			ID:             key,
			Prog:           unit.Program,
			IsTLA:          unit.HasTLA,
			IsCJS:          unit.ModuleKind == module.ModuleCommonJS,
			Resolved:       graphResult.Resolutions[key],
			DynamicImports: dynamicImportsFor(key, graphResult.DynamicDeps),
		})
	}

	outJS, err := emit.Bundle{
		EntryID: graphResult.Entry,
		Modules: modules,
		Assets:  graphResult.Assets,
		Format:  opts.Format,
		Global:  opts.GlobalName,
		Defines: defines,
	}.Build()
	if err != nil {
		return empty, err
	}

	for _, dep := range graphResult.DynamicDeps {
		if graphResult.SourceUnits[dep.Target] == nil {
			continue
		}
		chunkKeys := staticModuleClosure(graphResult, dep.Target, kept)
		chunkIDs := make([]string, 0, len(chunkKeys))
		for key := range chunkKeys {
			chunkIDs = append(chunkIDs, key)
		}
		sort.Strings(chunkIDs)
		chunkModules := make([]emit.Module, 0, len(chunkIDs))
		for _, key := range chunkIDs {
			chunkUnit := graphResult.SourceUnits[key]
			if chunkUnit == nil {
				continue
			}
			if opts.Minify {
				minify.Program(chunkUnit.Program)
			}
			chunkModules = append(chunkModules, emit.Module{
				ID:             key,
				Prog:           chunkUnit.Program,
				IsTLA:          chunkUnit.HasTLA,
				IsCJS:          chunkUnit.ModuleKind == module.ModuleCommonJS,
				Resolved:       graphResult.Resolutions[key],
				DynamicImports: dynamicImportsFor(key, graphResult.DynamicDeps),
			})
		}
		chunkText, err := (emit.Bundle{EntryID: dep.Target, Modules: chunkModules, Defines: defines}).BuildChunk()
		if err != nil {
			return empty, err
		}
		assets[dynamicChunkName(dep.Target)] = []byte(chunkText)
	}

	if opts.Sourcemap {
		mapFileName := jsFileName + ".map"
		smJSON, err := emit.GenerateSimpleSourceMap(jsFileName, moduleSources)
		if err != nil {
			return empty, fmt.Errorf("generate sourcemap: %w", err)
		}
		assets[mapFileName] = []byte(smJSON)
		outJS += "\n//# sourceMappingURL=" + mapFileName + "\n"
	}

	if cssName, cssBundle, err := bundleCSS(graphResult, baseName, false, opts.Minify, opts.AssetsDir); err != nil {
		return empty, err
	} else if cssName != "" {
		assets[cssName] = []byte(cssBundle)
	}

	assets[jsFileName] = []byte(outJS)
	return Bundle{Assets: assets, Watch: graphResult.WatchFiles, EntryJS: jsFileName}, nil
}

func emitNativeJS(graphResult *graph.Result, kept map[string]bool, jsFileName, baseName string, defines map[string]string, opts Options) (Bundle, error) {
	empty := Bundle{Assets: map[string][]byte{}}
	keys := make([]string, 0, len(graphResult.SourceUnits))
	for key := range graphResult.SourceUnits {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	modules := make([]emit.Module, 0, len(keys))
	moduleSources := make(map[string]string)
	for _, key := range keys {
		unit := graphResult.SourceUnits[key]
		if unit == nil || !kept[key] {
			continue
		}
		if opts.Sourcemap {
			moduleSources[key] = string(unit.Source)
		}
		if opts.Minify {
			minify.Program(unit.Program)
		}
		modules = append(modules, emit.Module{
			ID:             key,
			Prog:           unit.Program,
			IsTLA:          unit.HasTLA,
			IsCJS:          unit.ModuleKind == module.ModuleCommonJS,
			Resolved:       graphResult.Resolutions[key],
			DynamicImports: dynamicImportsFor(key, graphResult.DynamicDeps),
		})
	}

	native, err := emit.BuildNativeESM(emit.Bundle{
		EntryID:   graphResult.Entry,
		Modules:   modules,
		Assets:    graphResult.Assets,
		Defines:   defines,
		AssetsDir: opts.AssetsDir,
	})
	if err != nil {
		return empty, err
	}

	assets := native.Files
	if opts.Sourcemap && native.EntryFile != "" {
		mapFileName := native.EntryFile + ".map"
		smJSON, err := emit.GenerateSimpleSourceMap(filepath.Base(native.EntryFile), moduleSources)
		if err != nil {
			return empty, fmt.Errorf("generate sourcemap: %w", err)
		}
		assets[mapFileName] = []byte(smJSON)
		assets[native.EntryFile] = append(append([]byte{}, assets[native.EntryFile]...), []byte("\n//# sourceMappingURL="+filepath.Base(mapFileName)+"\n")...)
	}

	cssOut := ""
	if cssName, cssBundle, err := bundleCSS(graphResult, baseName, true, opts.Minify, opts.AssetsDir); err != nil {
		return empty, err
	} else if cssName != "" {
		assets[cssName] = []byte(cssBundle)
		cssOut = cssName
	}

	var exportNames []string
	for _, m := range modules {
		if m.ID == graphResult.Entry {
			exportNames, err = emit.CollectExports(m.Prog)
			if err != nil {
				return empty, err
			}
			break
		}
	}
	barrel := emit.BuildESMBarrel(jsFileName, native.EntryFile, exportNames)
	// 裸 JS 入口加载稳定 barrel 时必须副作用导入 CSS（HTML 入口另有 link 注入）。
	if cssOut != "" {
		barrel = emit.CSSSideEffectImport(jsFileName, cssOut) + barrel
	}
	assets[jsFileName] = []byte(barrel)

	preload := append([]string{}, native.Preload...)
	preload = append(preload, native.Async...)
	return Bundle{
		Assets:  assets,
		Watch:   graphResult.WatchFiles,
		EntryJS: native.EntryFile,
		Preload: uniqueStrings(preload),
	}, nil
}

func bundleCSS(graphResult *graph.Result, baseName string, hashed bool, minifyCSS bool, assetsDir string) (string, string, error) {
	var cssFiles []emit.CSSFile
	for assetKey, data := range graphResult.Assets {
		if strings.HasSuffix(assetKey, ".css") {
			cssFiles = append(cssFiles, emit.CSSFile{ID: assetKey, Content: string(data)})
		}
	}
	if len(cssFiles) == 0 {
		return "", "", nil
	}
	cssBundle, err := emit.BundleCSS(cssFiles, minifyCSS)
	if err != nil {
		return "", "", fmt.Errorf("bundle CSS: %w", err)
	}
	if cssBundle == "" {
		return "", "", nil
	}
	if hashed {
		return emit.HashedAssetPathIn(assetsDir, baseName, emit.ContentHash(cssBundle), ".css"), cssBundle, nil
	}
	return baseName + ".css", cssBundle, nil
}

func runtimeVM(rt ScriptRuntime) *interpreter.VM {
	type hasVM interface {
		VM() *interpreter.VM
	}
	if h, ok := rt.(hasVM); ok {
		return h.VM()
	}
	return nil
}

// ValidateWebBuiltins 返回浏览器目标下 Node 内置模块的可操作诊断。
func ValidateWebBuiltins(builtins []graph.BuiltinDep) error {
	if len(builtins) == 0 {
		return nil
	}
	b := builtins[0]
	return fmt.Errorf(
		"web target 不支持 Node 内置模块 %q（来源 %s）——浏览器环境请改用 Web API（如 node:fs → fetch/File System Access API），或经 --polyfill 注入（M2）",
		b.Spec, b.Source)
}

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

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func injectHTMLStylesheets(html string, hrefs []string) string {
	var b strings.Builder
	lower := strings.ToLower(html)
	seen := map[string]bool{}
	for _, h := range hrefs {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if strings.Contains(html, h) {
			continue
		}
		b.WriteString(`<link rel="stylesheet" crossorigin href="`)
		b.WriteString(h)
		b.WriteString(`">`)
	}
	extra := b.String()
	if extra == "" {
		return html
	}
	if i := strings.LastIndex(lower, "</head>"); i >= 0 {
		return html[:i] + extra + html[i:]
	}
	return extra + html
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

// ProductionDefines 是 web production 构建注入的最小 define 集。
func ProductionDefines() map[string]string {
	return map[string]string{
		"process.env.NODE_ENV":                    `"production"`,
		"__VUE_OPTIONS_API__":                     "true",
		"__VUE_PROD_DEVTOOLS__":                   "false",
		"__VUE_PROD_HYDRATION_MISMATCH_DETAILS__": "false",
	}
}

// DevelopmentDefines 是 web development / aluka dev 注入的最小 define 集。
func DevelopmentDefines() map[string]string {
	return map[string]string{
		"process.env.NODE_ENV":                    `"development"`,
		"__VUE_OPTIONS_API__":                     "true",
		"__VUE_PROD_DEVTOOLS__":                   "true",
		"__VUE_PROD_HYDRATION_MISMATCH_DETAILS__": "true",
	}
}

// DefaultDefines 按 mode 返回内置 define；未知 mode 按 production。
func DefaultDefines(mode string) map[string]string {
	if mode == "development" {
		return DevelopmentDefines()
	}
	return ProductionDefines()
}

func mergeDefines(opts Options) map[string]string {
	_, mode := opts.BuildEnv()
	out := make(map[string]string, len(opts.Defines)+4)
	for k, v := range DefaultDefines(mode) {
		out[k] = v
	}
	// 用户 / 配置 define 覆盖内置（与 Vite/esbuild 一致）。
	for k, v := range opts.Defines {
		out[k] = v
	}
	return out
}

func stylesheetOutName(original, content string, opts Options) string {
	baseSheet := filepath.Base(original)
	baseName := strings.TrimSuffix(baseSheet, filepath.Ext(baseSheet))
	// 路径参与 hash，避免同 basename 的多 stylesheet 互相覆盖。
	hash := emit.ContentHash(filepath.ToSlash(original), content)
	if useNativeESM(opts) {
		return emit.HashedAssetPathIn(opts.AssetsDir, baseName, hash, ".css")
	}
	return baseName + "-" + hash + ".css"
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

func assetNames(assets map[string][]byte) []string {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
