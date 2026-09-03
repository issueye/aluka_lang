package gtimers

// 全局 gc() 函数（开发计划 1B.6 自研 GC）。
// 触发标记-清除并返回堆统计 {allocCount, liveCount, markedCount}。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// GCConfig 配置 gc 全局。
type GCConfig struct{}

// NewGC 注册全局 gc()。
func NewGC(ctx engine.Context, cfg GCConfig) error {
	_ = ctx.Global().Set("gc", engine.NewFunction("gc", func(args []engine.Value) (engine.Value, error) {
		stats := engine.GC([]engine.Value{ctx.Global()})
		obj := engine.NewObject()
		_ = obj.Set("allocCount", engine.IntValue(int(stats.AllocCount)))
		_ = obj.Set("liveCount", engine.IntValue(int(stats.LiveCount)))
		_ = obj.Set("markedCount", engine.IntValue(int(stats.MarkedCount)))
		return obj, nil
	}))
	return nil
}
