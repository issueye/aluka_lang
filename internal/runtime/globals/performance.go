package globals

import (
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
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
func EnsurePerformance(ctx engine.Context) (engine.Value, error) {
	if existing, err := ctx.Global().Get("performance"); err == nil && existing != nil && existing.IsObject() {
		return existing, nil
	}

	start := time.Now()
	performance := engine.NewObject()
	_ = performance.Set("timeOrigin", engine.Number(float64(start.UnixNano())/1e6))
	_ = performance.Set("now", engine.NewFunction("now", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(time.Since(start).Nanoseconds()) / 1e6), nil
	}))
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
					_, _ = f.Call([]engine.Value{list})
				}
			}
		}
	}
	_ = performance.Set("mark", engine.NewFunction("mark", func(args []engine.Value) (engine.Value, error) {
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
	_ = performance.Set("measure", engine.NewFunction("measure", func(args []engine.Value) (engine.Value, error) {
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
	_ = performance.Set("getEntries", engine.NewFunction("getEntries", func(args []engine.Value) (engine.Value, error) {
		return engine.NewArray(perfEntries), nil
	}))
	_ = performance.Set("getEntriesByType", engine.NewFunction("getEntriesByType", func(args []engine.Value) (engine.Value, error) {
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
	_ = performance.Set("getEntriesByName", engine.NewFunction("getEntriesByName", func(args []engine.Value) (engine.Value, error) {
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
	_ = performance.Set("clearMarks", engine.NewFunction("clearMarks", func(args []engine.Value) (engine.Value, error) {
		perfMarks = map[string]float64{}
		return engine.Undefined(), nil
	}))
	_ = performance.Set("markResourceTiming", engine.NewFunction("markResourceTiming", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

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
