// Package aluka 把 *interpreter.VM 适配为 project.ScriptRuntime。
// 只做薄包装：Loader + 配置 shims；不实现构建逻辑。
package aluka

import (
	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// Runtime 绑定一次 VM/Loader，供 webconfig 与插件保活同一模块缓存。
type Runtime struct {
	vm     *interpreter.VM
	loader *module.Loader
}

// New 包装已有 VM。调用方负责创建/关闭 VM。
func New(vm *interpreter.VM) *Runtime {
	loader := module.NewLoader(vm)
	loader.SetNoCache(true)
	builtin.RegisterAll(loader)
	installConfigShims(loader)
	return &Runtime{vm: vm, loader: loader}
}

// Require 加载模块并返回 exports。
func (r *Runtime) Require(id, parent string) (engine.Value, error) {
	return r.loader.RequireModule(id, parent)
}

// VM 供 graph / official Vue 编译器取出具体引擎（本阶段不改它们的签名）。
func (r *Runtime) VM() *interpreter.VM {
	return r.vm
}

func installConfigShims(loader *module.Loader) {
	defineConfig := engine.NewFunction("defineConfig", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return args[0], nil
	})
	vite := engine.NewObject()
	_ = vite.Set("defineConfig", defineConfig)
	_ = vite.Set("loadEnv", engine.NewFunction("loadEnv", func([]engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	loader.RegisterVirtualModule("vite", vite)

	vuePlugin := engine.NewFunction("pluginVue", func(args []engine.Value) (engine.Value, error) {
		api := engine.Undefined()
		if len(args) > 0 {
			api = args[0]
		}
		p := engine.NewObject()
		_ = p.Set("name", engine.Str("vue"))
		_ = p.Set("api", api)
		return p, nil
	})
	loader.RegisterVirtualModule("@vitejs/plugin-vue", vuePlugin)
}
