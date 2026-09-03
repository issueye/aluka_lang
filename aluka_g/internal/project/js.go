package project

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/vue"
	"github.com/aluka-lang/aluka/internal/bundler/webemit"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func bundleJS(rt ScriptRuntime, resolver *module.Resolver, entry string, opts Options) (Bundle, error) {
	empty := Bundle{Assets: map[string][]byte{}}
	vm := runtimeVM(rt)
	var graphOpts []graph.Option
	if opts.VueCompiler == "official" {
		oc := vue.NewOfficialCompiler(vm, entry)
		oc.Register = builtin.RegisterAll
		graphOpts = append(graphOpts, graph.WithVueCompiler(oc))
	}
	graphOpts = append(graphOpts, graph.WithPlugins(opts.Host()))
	graphResult, err := graph.Build(vm, resolver, entry, graphOpts...)
	if err != nil {
		return empty, err
	}
	if err := ValidateWebBuiltins(graphResult.Builtins); err != nil {
		return empty, err
	}
	out, err := webemit.Emit(graphResult, webemit.Options{
		Format:     opts.Format,
		GlobalName: opts.GlobalName,
		AssetsDir:  opts.AssetsDir,
		Minify:     opts.Minify,
		TreeShake:  opts.TreeShake,
		Sourcemap:  opts.Sourcemap,
		Defines:    mergeDefines(opts),
		EntryFile:  entry,
	})
	if err != nil {
		return empty, err
	}
	return Bundle{
		Assets:  out.Assets,
		Watch:   out.Watch,
		EntryJS: out.EntryJS,
		Preload: out.Preload,
	}, nil
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
