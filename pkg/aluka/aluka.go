// Package aluka 提供面向 Go 应用程序嵌入的公共 API。
package aluka

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// 导出核心接口与类型别名供宿主直接使用。
type (
	Engine    = engine.Engine
	Context   = engine.Context
	Value     = engine.Value
	Object    = engine.Object
	Function  = engine.Function
	Func      = engine.Func
	ValueType = engine.ValueType
)

// 常用值与对象构造辅助函数。
var (
	Undefined   = engine.Undefined
	Null        = engine.Null
	Boolean     = engine.Boolean
	Number      = engine.Number
	IntValue    = engine.IntValue
	Str         = engine.Str
	NewObject   = engine.NewObject
	NewArray    = engine.NewArray
	NewFunction = engine.NewFunction
)

// Runtime 代表一个嵌入式 Aluka 执行运行时。
type Runtime struct {
	mu     sync.Mutex
	eng    engine.Engine
	ctx    engine.Context
	loader *modmodule.Loader
}

// NewRuntime 创建并初始化带有完整标准全局对象和内置模块的运行时。
func NewRuntime() (*Runtime, error) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		_ = eng.Shutdown()
		return nil, fmt.Errorf("创建引擎上下文失败: %w", err)
	}

	// 注册全局标准对象
	_ = globals.NewConsole(ctx, globals.ConsoleConfig{})
	_ = globals.NewProcess(ctx, globals.ProcessConfig{})
	_ = globals.NewTimers(ctx, globals.TimerConfig{})
	_ = globals.NewBuffer(ctx, globals.BufferConfig{})
	_ = globals.NewEncoding(ctx, globals.EncodingConfig{})
	_ = globals.NewURL(ctx, globals.URLConfig{})
	_ = globals.NewFetch(ctx, globals.FetchConfig{})
	_ = globals.NewWebCrypto(ctx, globals.WebCryptoConfig{})
	_ = globals.NewPerformance(ctx, globals.PerformanceConfig{})

	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())

	// 注册模块加载器
	loader := modmodule.NewLoader(ctx)
	builtin.RegisterAll(loader)
	_ = builtin.InstallGetBuiltinModule(ctx, loader)

	cwd, _ := os.Getwd()
	evalParent := filepath.Join(cwd, "[eval]")
	_ = ctx.Global().Set("require", loader.MakeRequireFunc(evalParent))
	_ = ctx.Global().Set("__import", loader.MakeImportFunc(evalParent))

	return &Runtime{
		eng:    eng,
		ctx:    ctx,
		loader: loader,
	}, nil
}

// Context 返回当前运行时的上下文对象。
func (r *Runtime) Context() Context {
	return r.ctx
}

// Global 返回全局 globalThis 对象。
func (r *Runtime) Global() Object {
	return r.ctx.Global()
}

// Eval 执行一段脚本代码并排空事件循环微任务。
func (r *Runtime) Eval(code, filename string) (Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if filename == "" {
		filename = "[eval]"
	}

	val, err := r.ctx.Eval(code, filename)
	if err != nil {
		return Undefined(), err
	}

	if vmCtx, ok := r.ctx.(interface{ RunLoop() }); ok {
		vmCtx.RunLoop()
	}

	return val, nil
}

// RunFile 执行指定的脚本文件。
func (r *Runtime) RunFile(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.loader.Run(path); err != nil {
		return err
	}

	if vmCtx, ok := r.ctx.(interface{ RunLoop() }); ok {
		vmCtx.RunLoop()
	}
	return nil
}

// Close 关闭并释放运行时。
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ctx != nil {
		_ = r.ctx.Close()
	}
	if r.eng != nil {
		_ = r.eng.Shutdown()
	}
	return nil
}
