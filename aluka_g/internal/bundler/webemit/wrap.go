package webemit

import (
	"fmt"
	"sort"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
)

func emitWrapped(gr *graph.Result, kept map[string]bool, jsFileName, baseName string, opts Options) (Result, error) {
	empty := Result{Assets: map[string][]byte{}}
	assets := make(map[string][]byte)
	mainModules := staticModuleClosure(gr, gr.Entry, kept)
	modules, moduleSources := collectModules(gr, keysOf(mainModules), kept, opts)

	outJS, err := emit.Bundle{
		EntryID: gr.Entry,
		Modules: modules,
		Assets:  gr.Assets,
		Format:  opts.Format,
		Global:  opts.GlobalName,
		Defines: opts.Defines,
	}.Build()
	if err != nil {
		return empty, err
	}

	for _, dep := range gr.DynamicDeps {
		if gr.SourceUnits[dep.Target] == nil {
			continue
		}
		chunkKeys := staticModuleClosure(gr, dep.Target, kept)
		ids := keysOf(chunkKeys)
		sort.Strings(ids)
		chunkModules, _ := collectModules(gr, ids, kept, Options{
			Minify:  opts.Minify,
			Defines: opts.Defines,
		})
		chunkText, err := (emit.Bundle{EntryID: dep.Target, Modules: chunkModules, Defines: opts.Defines}).BuildChunk()
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

	if cssName, cssBundle, err := bundleGraphCSS(gr, baseName, false, opts.Minify, opts.AssetsDir); err != nil {
		return empty, err
	} else if cssName != "" {
		assets[cssName] = []byte(cssBundle)
	}

	assets[jsFileName] = []byte(outJS)
	return Result{Assets: assets, Watch: gr.WatchFiles, EntryJS: jsFileName}, nil
}
