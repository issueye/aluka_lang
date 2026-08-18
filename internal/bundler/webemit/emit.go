// Package webemit 把已构建的模块图变成 web 产物文件。
//
// 工作台（internal/project）负责配置、插件会话、HTML 入口和写盘；
// shake / minify / Native ESM 或 CJS·UMD wrap / CSS 拼接在本包完成。
package webemit

import (
	"fmt"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/shake"
)

// Options 控制「图 → 文件」的产出形态（不含项目配置 / 插件生命周期）。
type Options struct {
	Format     string
	GlobalName string
	AssetsDir  string
	Minify     bool
	TreeShake  bool
	Sourcemap  bool
	Defines    map[string]string
	// EntryFile 是原始入口路径，用来生成 barrel 文件名（main.ts → main.js）。
	EntryFile string
}

// Result 是单入口 JS 图的产物文件。
type Result struct {
	Assets  map[string][]byte
	Watch   []string
	EntryJS string
	Preload []string
}

// Emit 对 graph.Build 的结果做 tree-shake、minify 并写出 JS/CSS 资产。
func Emit(gr *graph.Result, opts Options) (Result, error) {
	empty := Result{Assets: map[string][]byte{}}
	if gr == nil {
		return empty, fmt.Errorf("webemit: nil graph")
	}
	if len(gr.UnresolvedDynamic) > 0 {
		return empty, fmt.Errorf("web target requires a string literal for dynamic import() (source %s)", gr.UnresolvedDynamic[0])
	}

	kept := make(map[string]bool, len(gr.SourceUnits))
	for key := range gr.SourceUnits {
		kept[key] = true
	}
	if opts.TreeShake {
		shaken, err := shake.ShakeOpts(gr, gr.Entry, shake.Options{KeepEntryExports: true})
		if err != nil {
			return empty, err
		}
		kept = shaken.Kept
	}

	baseName := entryBaseName(opts.EntryFile, gr.Entry)
	jsFileName := baseName + ".js"
	if useNativeESM(opts.Format) {
		return emitNative(gr, kept, jsFileName, baseName, opts)
	}
	return emitWrapped(gr, kept, jsFileName, baseName, opts)
}

func useNativeESM(format string) bool {
	return format == "" || format == "esm"
}

func entryBaseName(entryFile, graphEntry string) string {
	path := entryFile
	if path == "" {
		path = graphEntry
	}
	baseName := filepath.Base(path)
	if e := filepath.Ext(baseName); e != "" {
		baseName = baseName[:len(baseName)-len(e)]
	}
	if baseName == "" || baseName == "." {
		return "index"
	}
	return baseName
}
