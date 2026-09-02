// VM 编译与模块执行入口：Eval/Compile/CompileAST、字节码优化开关与 RunModule。

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/compiler"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// Eval parses, compiles, and executes JS source.
func (v *VM) Eval(src, filename string) (engine.Value, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	return v.EvalProgram(prog, filename)
}

// EvalProgram compiles and executes a pre-parsed AST. Used by the module
// loader to run ESM modules after AST transformation.
func (v *VM) EvalProgram(prog *ast.Program, filename string) (engine.Value, error) {
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("aluka: compile error: %w", err)
	}
	return v.runModule(mod)
}

// Compile 解析源码并编译为字节码 Module（不执行）。供字节码缓存使用：
// 加载器可先检查磁盘缓存，未命中时调用此方法编译并写盘，再调用 RunModule 执行。
func (v *VM) Compile(src, filename string) (*bytecode.Module, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return nil, fmt.Errorf("aluka: compile error: %w", err)
	}
	if err := v.optimizeModule(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// CompileAST 编译预解析的 AST 为字节码 Module（不执行）。
func (v *VM) CompileAST(prog *ast.Program, filename string) (*bytecode.Module, error) {
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return nil, fmt.Errorf("aluka: compile error: %w", err)
	}
	if err := v.optimizeModule(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// optimizeModule 按 VM 开关对编译产物运行字节码优化器。
// 优化出错视为编译错误（validateFunc 先行校验，出错概率极低且表示编译器 bug）。
func (v *VM) optimizeModule(mod *bytecode.Module) error {
	if !v.optimizeBytecode {
		return nil
	}
	if _, err := bytecode.OptimizeModule(mod); err != nil {
		return fmt.Errorf("aluka: bytecode optimize error: %w", err)
	}
	return nil
}

// SetOptimizeBytecode 控制后续 Compile/CompileAST 是否执行字节码优化
// （默认开启）。build 路径用它对齐 --bytecode-opt/--no-bytecode-opt 语义。
func (v *VM) SetOptimizeBytecode(enabled bool) {
	v.optimizeBytecode = enabled
}

// RunModule 执行已编译的字节码 Module（公开版，供缓存恢复后执行）。
func (v *VM) RunModule(mod *bytecode.Module) (engine.Value, error) {
	return v.runModule(mod)
}

// runModule executes the top-level function of a module.
//
// This function is re-entrant: when require() is called inside a module, the
// native require function calls Eval → EvalProgram → runModule for the
// dependency. To avoid clobbering the caller's execution state (stack, frames,
// module), we save and restore them around the nested execution.
func (v *VM) runModule(mod *bytecode.Module) (engine.Value, error) {
	// Save caller state for re-entrant calls.
	savedStack := v.stack
	savedFrames := v.frames
	savedModule := v.module
	isTopLevel := len(savedFrames) == 0

	v.module = mod
	v.interp.currentVM = v
	main := mod.Functions[0]
	// Reserve locals for the top-level frame (fresh stack for this module),
	// 并按 main.MaxStack 预留操作数栈（帧内 push 永不扩容）。
	v.stack = make([]engine.Value, 0, main.NumLocals+16)
	for i := 0; i < main.NumLocals; i++ {
		v.stack = append(v.stack, engine.Undefined())
	}
	v.ensureFrameStack(main)
	// Fresh frames slice — never mutate savedFrames. Preallocate capacity so
	// hot-path call frames (fastCallClosure/callClosure append) rarely grow.
	v.frames = make([]vmFrame, 1, 128)
	v.frames[0] = vmFrame{tmpl: main, base: 0}
	result, err := v.run()

	if isTopLevel {
		// Clean up leftover stack values before draining scheduled jobs
		// (callbacks reuse v.stack). Keep v.module alive —
		// nextTick/microtask callbacks (Promise reactions, async continuations)
		// AND event-loop tasks (timers, http handlers, user closures)
		// may call back into the VM and need the module's templates.
		// module 在事件循环（RunLoop）结束后才允许被 GC 回收。
		v.stack = v.stack[:0]
		v.frames = v.frames[:0]
		// Drain the Node job queues (nextTick before Promise reactions and
		// queueMicrotask callbacks). Errors from microtasks are handled internally by
		// Promise reactions; any uncaught error is silently ignored here.
		v.interp.drainJobQueues()
	} else {
		// Restore the caller's execution state so its run() loop continues.
		v.stack = savedStack
		v.frames = savedFrames
		v.module = savedModule
	}
	return result, err
}
