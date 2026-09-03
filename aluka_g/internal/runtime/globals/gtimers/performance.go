package gtimers

import (
	"fmt"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gevent"
)

// PerformanceConfig configures the global Performance API.
type PerformanceConfig struct{}

// NewPerformance registers the global performance object.
func NewPerformance(ctx engine.Context, cfg PerformanceConfig) error {
	_, err := EnsurePerformance(ctx)
	return err
}

// EnsurePerformance returns the shared global performance object, creating it
// when node:perf_hooks is used before normal global setup.
// WebIDL 原型链语义（工作流 B3）：performance 自有键为空，方法挂
// Performance.prototype（含 timeOrigin getter），链到 EventTarget.prototype →
// %Object.prototype%，Symbol.toStringTag = "Performance"。
func EnsurePerformance(ctx engine.Context) (engine.Value, error) {
	if existing, err := ctx.Global().Get("performance"); err == nil && existing != nil && existing.IsObject() {
		return existing, nil
	}

	start := time.Now()

	// --- Performance 接口（Base = EventTarget.prototype）---
	gevent.EnsureTargetProto()
	_, perfProto, err := gbase.RegisterInterface(ctx, gbase.WebInterface{
		Name: "Performance",
		Tag:  "Performance",
		Base: gevent.TargetProto,
	})
	if err != nil {
		return engine.Undefined(), err
	}

	// 条目存储（mark/measure/getEntries*/clear*，供全局 PerformanceObserver）。
	perfEntries := make([]engine.Value, 0)
	perfMarks := map[string]float64{}
	perfObservers := make([]engine.Value, 0)
	makeEntry := func(name, entryType string, startTime, duration float64) engine.Object {
		e := engine.NewObject()
		_ = e.Set("name", engine.Str(name))
		_ = e.Set("entryType", engine.Str(entryType))
		_ = e.Set("startTime", engine.Number(startTime))
		_ = e.Set("duration", engine.Number(duration))
		return e
	}
	notifyObservers := func(entryType string, entry engine.Value) {
		for _, o := range perfObservers {
			oo, ok := o.AsObject()
			if !ok {
				continue
			}
			if cb, err := oo.Get("_callback"); err == nil && cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					list := engine.NewObject()
					_ = list.Set("getEntries", engine.NewFunction("getEntries", func(a []engine.Value) (engine.Value, error) {
						return engine.NewArray([]engine.Value{entry}), nil
					}))
					_ = list.Set("getEntriesByType", engine.NewFunction("getEntriesByType", func(a []engine.Value) (engine.Value, error) {
						return engine.NewArray([]engine.Value{entry}), nil
					}))
					_ = list.Set("getEntriesByName", engine.NewFunction("getEntriesByName", func(a []engine.Value) (engine.Value, error) {
						return engine.NewArray([]engine.Value{entry}), nil
					}))
					if _, err := f.Call([]engine.Value{list}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
	}

	// timeOrigin：原型 getter（Node 22 中非自有属性）。
	engine.SetAccessor(perfProto, "timeOrigin",
		interpreter.NewNativeMethod("get timeOrigin", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return engine.Number(float64(start.UnixNano()) / 1e6), nil
		}),
		engine.Undefined())

	_ = perfProto.Set("now", engine.NewFunction("now", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(time.Since(start).Nanoseconds()) / 1e6), nil
	}))
	_ = perfProto.Set("mark", engine.NewFunction("mark", func(args []engine.Value) (engine.Value, error) {
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		st := float64(time.Since(start).Nanoseconds()) / 1e6
		entry := makeEntry(name, "mark", st, 0)
		perfMarks[name] = st
		perfEntries = append(perfEntries, entry)
		notifyObservers("mark", entry)
		return entry, nil
	}))
	_ = perfProto.Set("measure", engine.NewFunction("measure", func(args []engine.Value) (engine.Value, error) {
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		st := float64(time.Since(start).Nanoseconds()) / 1e6
		entry := makeEntry(name, "measure", st, 0)
		perfEntries = append(perfEntries, entry)
		notifyObservers("measure", entry)
		return entry, nil
	}))
	_ = perfProto.Set("getEntries", engine.NewFunction("getEntries", func(args []engine.Value) (engine.Value, error) {
		return engine.NewArray(perfEntries), nil
	}))
	_ = perfProto.Set("getEntriesByType", engine.NewFunction("getEntriesByType", func(args []engine.Value) (engine.Value, error) {
		typ := ""
		if len(args) > 0 {
			typ = args[0].String()
		}
		var out []engine.Value
		for _, e := range perfEntries {
			if o, ok := e.AsObject(); ok {
				if v, err := o.Get("entryType"); err == nil && v.String() == typ {
					out = append(out, e)
				}
			}
		}
		return engine.NewArray(out), nil
	}))
	_ = perfProto.Set("getEntriesByName", engine.NewFunction("getEntriesByName", func(args []engine.Value) (engine.Value, error) {
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		var out []engine.Value
		for _, e := range perfEntries {
			if o, ok := e.AsObject(); ok {
				if v, err := o.Get("name"); err == nil && v.String() == name {
					out = append(out, e)
				}
			}
		}
		return engine.NewArray(out), nil
	}))
	_ = perfProto.Set("clearMarks", engine.NewFunction("clearMarks", func(args []engine.Value) (engine.Value, error) {
		perfMarks = map[string]float64{}
		var kept []engine.Value
		for _, e := range perfEntries {
			if o, ok := e.AsObject(); ok {
				if v, err := o.Get("entryType"); err == nil && v.String() == "mark" {
					continue
				}
			}
			kept = append(kept, e)
		}
		perfEntries = kept
		return engine.Undefined(), nil
	}))
	// clearMeasures / clearResourceTimings：过滤对应 entryType。
	clearByType := func(entryType string) engine.Value {
		return engine.NewFunction("clear"+entryType, func(args []engine.Value) (engine.Value, error) {
			var kept []engine.Value
			for _, e := range perfEntries {
				if o, ok := e.AsObject(); ok {
					if v, err := o.Get("entryType"); err == nil && v.String() == entryType {
						continue
					}
				}
				kept = append(kept, e)
			}
			perfEntries = kept
			return engine.Undefined(), nil
		})
	}
	_ = perfProto.Set("clearMeasures", clearByType("measure"))
	_ = perfProto.Set("clearResourceTimings", clearByType("resource"))
	// markResourceTiming：外部资源计时打点（http/fetch 侧调用）。
	_ = perfProto.Set("markResourceTiming", engine.NewFunction("markResourceTiming", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// eventLoopUtilization：返回 {idle, active, utilization}（启动期零值）。
	_ = perfProto.Set("eventLoopUtilization", engine.NewFunction("eventLoopUtilization", func(args []engine.Value) (engine.Value, error) {
		u := engine.NewObject()
		_ = u.Set("idle", engine.Number(0))
		_ = u.Set("active", engine.Number(0))
		_ = u.Set("utilization", engine.Number(0))
		return u, nil
	}))
	// nodeTiming：原型 getter，返回 node 启动计时条目。
	engine.SetAccessor(perfProto, "nodeTiming",
		interpreter.NewNativeMethod("get nodeTiming", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			nt := engine.NewObject()
			_ = nt.Set("name", engine.Str("node"))
			_ = nt.Set("entryType", engine.Str("node"))
			_ = nt.Set("startTime", engine.Number(0))
			_ = nt.Set("duration", engine.Number(float64(time.Since(start).Nanoseconds())/1e6))
			return nt, nil
		}),
		engine.Undefined())
	// onresourcetimingbufferfull：事件处理器属性（默认 null）。
	_ = perfProto.Set("onresourcetimingbufferfull", engine.Null())
	// setResourceTimingBufferSize：缓冲区上限（启动期 no-op）。
	_ = perfProto.Set("setResourceTimingBufferSize", engine.NewFunction("setResourceTimingBufferSize", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// timerify(fn)：包装并记录执行耗时的函数（启动期直通包装）。
	_ = perfProto.Set("timerify", engine.NewFunction("timerify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: timerify expects a function", engine.ErrTypeError)
		}
		fn := args[0]
		return engine.NewFunction("timerified", func(fa []engine.Value) (engine.Value, error) {
			f, ok := fn.AsFunction()
			if !ok {
				return engine.Undefined(), nil
			}
			t0 := time.Since(start).Nanoseconds()
			rv, err := f.Call(fa)
			perfEntries = append(perfEntries, makeEntry("timerify", "function", float64(t0)/1e6, float64(time.Since(start).Nanoseconds()-t0)/1e6))
			return rv, err
		}), nil
	}))
	// toJSON：序列化快照。
	_ = perfProto.Set("toJSON", engine.NewFunction("toJSON", func(args []engine.Value) (engine.Value, error) {
		o := engine.NewObject()
		_ = o.Set("name", engine.Str("node"))
		_ = o.Set("entryType", engine.Str("node"))
		_ = o.Set("startTime", engine.Number(0))
		_ = o.Set("duration", engine.Number(float64(time.Since(start).Nanoseconds())/1e6))
		_ = o.Set("timeOrigin", engine.Number(float64(start.UnixNano())/1e6))
		return o, nil
	}))

	performance := engine.NewObject()
	engine.SetProto(performance, perfProto)

	// 全局 PerformanceObserver（启动期基础实现；node:perf_hooks 加载后由
	// builtin 的完整实现覆盖同名全局）。
	obsCtor := engine.NewFunction("PerformanceObserver", func(args []engine.Value) (engine.Value, error) {
		o := engine.NewObject()
		if len(args) > 0 && args[0].IsFunction() {
			_ = o.Set("_callback", args[0])
		}
		_ = o.Set("observe", engine.NewFunction("observe", func(ca []engine.Value) (engine.Value, error) {
			if !containsValue(perfObservers, o) {
				perfObservers = append(perfObservers, o)
			}
			return engine.Undefined(), nil
		}))
		_ = o.Set("disconnect", engine.NewFunction("disconnect", func(ca []engine.Value) (engine.Value, error) {
			for i, po := range perfObservers {
				if po == o {
					perfObservers = append(perfObservers[:i], perfObservers[i+1:]...)
					break
				}
			}
			return engine.Undefined(), nil
		}))
		_ = o.Set("takeRecords", engine.NewFunction("takeRecords", func(ca []engine.Value) (engine.Value, error) {
			return engine.NewArray(nil), nil
		}))
		return o, nil
	})
	for _, name := range []string{"PerformanceObserver"} {
		_ = ctx.Global().Set(name, obsCtor)
	}

	if err := ctx.Global().Set("performance", performance); err != nil {
		return engine.Undefined(), err
	}
	return performance, nil
}

// containsValue 判断切片是否包含目标值。
func containsValue(vals []engine.Value, target engine.Value) bool {
	for _, v := range vals {
		if v == target {
			return true
		}
	}
	return false
}
