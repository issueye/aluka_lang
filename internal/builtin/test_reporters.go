package builtin

// node:test/reporters 内置模块——测试报告器（M1 入口面；完整 report 行为 M7）。
// Node 22 CJS 视角：导出 dot/junit/lcov/spec/tap 五个报告器（无 default 键）；
// 其中 lcov 是预构造的 Transform 实例（object），其余为可构造类。
// 报告输出契约在 M7 实现。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTestReporters 构造 node:test/reporters 模块导出对象。
func NewTestReporters(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 类报告器：dot / junit / spec / tap（可 new）。
	for _, name := range []string{"dot", "junit", "spec", "tap"} {
		nameCopy := name
		ctor := engine.NewFunction(nameCopy, func(args []engine.Value) (engine.Value, error) {
			return newReporterStream(ctx), nil
		})
		proto := engine.NewObject()
		_ = proto.Set("toString", engine.NewFunction("toString", func(args []engine.Value) (engine.Value, error) {
			return engine.Str(nameCopy), nil
		}))
		if co, ok := ctor.AsObject(); ok {
			_ = co.Set("prototype", proto)
		}
		_ = m.Set(nameCopy, ctor)
	}

	// lcov：Node 22 导出为预构造实例（object），不可 new。
	_ = m.Set("lcov", newReporterStream(ctx))

	return m, nil
}

// newReporterStream 构造报告器底层的可写流（M1 仅吞数据）。
func newReporterStream(ctx engine.Context) engine.Value {
	// 复用 node:stream 的 PassThrough/Transform：通过 loader 不可行（无引用），
	// 用事件发射器 + write 方法模拟可写流即可。
	w := newEmitterInstance().(engine.Object)
	_ = w.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(true), nil
	}))
	_ = w.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		emitEvent(w, "finish")
		emitEvent(w, "close")
		return engine.Undefined(), nil
	}))
	_ = w.Set("pipe", engine.NewFunction("pipe", func(args []engine.Value) (engine.Value, error) {
		return w, nil
	}))
	return w
}
