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
	_ = performance.Set("mark", engine.NewFunction("mark", func(args []engine.Value) (engine.Value, error) {
		entry := engine.NewObject()
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		_ = entry.Set("name", engine.Str(name))
		_ = entry.Set("entryType", engine.Str("mark"))
		_ = entry.Set("startTime", engine.Number(float64(time.Since(start).Nanoseconds())/1e6))
		_ = entry.Set("duration", engine.Number(0))
		return entry, nil
	}))
	_ = performance.Set("measure", engine.NewFunction("measure", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = performance.Set("markResourceTiming", engine.NewFunction("markResourceTiming", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	if err := ctx.Global().Set("performance", performance); err != nil {
		return engine.Undefined(), err
	}
	return performance, nil
}
