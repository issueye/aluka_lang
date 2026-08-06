package globals

// Web API 补全（N22-C4）：navigator 全局 + BroadcastChannel。
//
// navigator：Node 21+ 全局（userAgent/platform/hardwareConcurrency 等）。
// BroadcastChannel：同"频道名"广播（postMessage 投递给所有同频道监听者，
// 不含发送者自身；close 后移出注册表）。与 MessageChannel 同一投递机制
// （ctx.PostTask 到 JS 线程触发 'message' 事件）。

import (
	"runtime"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// bcRegistry 是进程级 BroadcastChannel 注册表（跨实例共享同频道）。
var bcRegistry = struct {
	sync.Mutex
	channels map[string][]*bcState
}{channels: map[string][]*bcState{}}

// bcState 是 BroadcastChannel 实例状态。
type bcState struct {
	ctx       engine.Context
	name      string
	self      engine.Value // channel 对象（读 onmessage 属性）
	listeners []engine.Value
}

// NewNavigator 注册全局 navigator 对象（N22-C4）。
func NewNavigator(ctx engine.Context, cfg NavigatorConfig) error {
	nav := engine.NewObject()
	_ = nav.Set("userAgent", engine.Str("aluka/0.1.0"))
	_ = nav.Set("platform", engine.Str(nodePlatform()))
	_ = nav.Set("hardwareConcurrency", engine.IntValue(runtime.NumCPU()))
	_ = nav.Set("language", engine.Str("en-US"))
	_ = nav.Set("languages", engine.NewArray([]engine.Value{engine.Str("en-US")}))
	_ = nav.Set("onLine", engine.Boolean(true))
	return ctx.Global().Set("navigator", nav)
}

// NavigatorConfig 配置 navigator 全局。
type NavigatorConfig struct{}

// nodePlatform 映射 GOOS 到 Node 的 navigator.platform 风格。
func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "Win32"
	case "darwin":
		return "Darwin"
	default:
		return "Linux"
	}
}

// NewBroadcastChannel 注册全局 BroadcastChannel（N22-C4）。
func NewBroadcastChannel(ctx engine.Context, cfg MessageConfig) error {
	_ = ctx.Global().Set("BroadcastChannel", engine.NewFunction("BroadcastChannel", func(args []engine.Value) (engine.Value, error) {
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		state := &bcState{ctx: ctx, name: name}
		bcRegistry.Lock()
		bcRegistry.channels[name] = append(bcRegistry.channels[name], state)
		bcRegistry.Unlock()
		return newBroadcastChannelInstance(state), nil
	}))
	return nil
}

// newBroadcastChannelInstance 构造 BroadcastChannel 对象。
func newBroadcastChannelInstance(state *bcState) engine.Value {
	ch := engine.NewObject()
	state.self = ch

	_ = ch.Set("name", engine.Str(state.name))
	_ = ch.Set("postMessage", engine.NewFunction("postMessage", func(args []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(args) > 0 {
			msg = args[0]
		}
		// 投递给所有同频道实例（不含自己）。
		bcRegistry.Lock()
		var targets []*bcState
		for _, s := range bcRegistry.channels[state.name] {
			if s != state {
				targets = append(targets, s)
			}
		}
		bcRegistry.Unlock()
		for _, t := range targets {
			t.ctx.PostTask(func() {
				deliverBroadcast(t, msg)
			})
		}
		return engine.Undefined(), nil
	}))

	_ = ch.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		bcRegistry.Lock()
		list := bcRegistry.channels[state.name]
		for i, s := range list {
			if s == state {
				bcRegistry.channels[state.name] = append(list[:i], list[i+1:]...)
				break
			}
		}
		bcRegistry.Unlock()
		return engine.Undefined(), nil
	}))

	// onmessage 属性与 addEventListener/removeEventListener。
	_ = ch.Set("addEventListener", engine.NewFunction("addEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && args[0].String() == "message" {
			state.listeners = append(state.listeners, args[1])
		}
		return engine.Undefined(), nil
	}))
	_ = ch.Set("removeEventListener", engine.NewFunction("removeEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		for i, l := range state.listeners {
			if l == args[1] {
				state.listeners = append(state.listeners[:i], state.listeners[i+1:]...)
				break
			}
		}
		return engine.Undefined(), nil
	}))
	return ch
}

// deliverBroadcast 在接收者上触发 'message' 事件（onmessage 或监听器）。
func deliverBroadcast(state *bcState, msg engine.Value) {
	ev := newMessageEventInstance([]engine.Value{engine.Str("message")})
	if evObj, ok := ev.AsObject(); ok {
		_ = evObj.Set("data", msg)
		_ = evObj.Set("target", state.self)
	}
	// 监听器（addEventListener）。
	for _, l := range state.listeners {
		if f, ok := l.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{ev})
		}
	}
	// onmessage 属性。
	if o, ok := state.self.AsObject(); ok {
		if v, err := o.Get("onmessage"); err == nil && v.IsFunction() {
			if f, ok := v.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{ev})
			}
		}
	}
}
