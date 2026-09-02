// 引擎入口：Engine（AST 解释器）与 VMEngine（字节码 VM）的 engine.Engine 实现。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// Engine implements engine.Engine using an AST-walking interpreter.
// 说明（P0-4）：AST-walking 解释器长期未与 parser/compiler 同步，无法处理
// ES2015+ 语法（class/解构等会 panic）。`--ast` 现已复用字节码 VM 引擎，
// 保证功能完整；Engine 类型保留以维持引擎抽象接口不变。
type Engine struct{}

// NewEngine creates a new interpreter engine.
func NewEngine() *Engine { return &Engine{} }

// NewContext 复用字节码 VM 引擎（P0-4：AST 解释器已废弃为 CLI 引擎路径）。
func (e *Engine) NewContext() (engine.Context, error) {
	return NewVM()
}

func (e *Engine) Shutdown() error { return nil }

func (e *Engine) Version() string { return "aluka-interpreter-1A" }

// VMEngine implements engine.Engine using the bytecode VM (Phase 1B).
type VMEngine struct{}

// NewVMEngine creates a new VM-based engine.
func NewVMEngine() *VMEngine { return &VMEngine{} }

func (e *VMEngine) NewContext() (engine.Context, error) {
	return NewVM()
}

func (e *VMEngine) Shutdown() error { return nil }

func (e *VMEngine) Version() string { return "aluka-vm-1B" }
