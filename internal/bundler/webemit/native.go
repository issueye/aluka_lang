package webemit

import (
	"fmt"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
)

func emitNative(gr *graph.Result, kept map[string]bool, jsFileName, baseName string, opts Options) (Result, error) {
	empty := Result{Assets: map[string][]byte{}}
	modules, moduleSources := collectModules(gr, sourceUnitKeys(gr), kept, opts)

	native, err := emit.BuildNativeESM(emit.Bundle{
		EntryID:   gr.Entry,
		Modules:   modules,
		Assets:    gr.Assets,
		Defines:   opts.Defines,
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
	if cssName, cssBundle, err := bundleGraphCSS(gr, baseName, true, opts.Minify, opts.AssetsDir); err != nil {
		return empty, err
	} else if cssName != "" {
		assets[cssName] = []byte(cssBundle)
		cssOut = cssName
	}

	var exportNames []string
	for _, m := range modules {
		if m.ID == gr.Entry {
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
	return Result{
		Assets:  assets,
		Watch:   gr.WatchFiles,
		EntryJS: native.EntryFile,
		Preload: uniqueStrings(preload),
	}, nil
}
