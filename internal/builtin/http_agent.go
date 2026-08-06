package builtin

// node:http Agent 兼容实现——keepAlive 连接复用。
// Agent 实例持有 Go *http.Transport（自带连接池）；http.request 的
// options.agent 决定使用哪个 Transport：Agent 实例 → 其池；false → 无池
// （每次新建连接）；缺省 → 全局共享 Transport（Node 19+ globalAgent
// keepAlive:true 语义）。

import (
	"net/http"
	"math"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// httpAgentTransports Agent 实例 → Transport（连接池）。
var httpAgentTransports = map[engine.Object]*http.Transport{}

// httpGlobalTransport 全局共享 Transport（Node 默认 globalAgent 的
// keepAlive 语义：连接跨请求复用）。
var httpGlobalTransport *http.Transport

// getHttpGlobalTransport 惰性构造全局 Transport。
func getHttpGlobalTransport() *http.Transport {
	if httpGlobalTransport == nil {
		httpGlobalTransport = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     60 * time.Second,
		}
	}
	return httpGlobalTransport
}

// registerHttpAgent 注册 http.Agent 构造器与 globalAgent。
func registerHttpAgent(ctx engine.Context, m engine.Object) {
	_ = m.Set("Agent", engine.NewFunction("Agent", func(args []engine.Value) (engine.Value, error) {
		keepAlive := false
		keepAliveMsecs := 1000.0
		maxSockets := 0.0 // Infinity
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("keepAlive"); err == nil && !v.IsUndefined() {
					if b, ok2 := v.Bool(); ok2 {
						keepAlive = b
					}
				}
				if v, err := o.Get("keepAliveMsecs"); err == nil && !v.IsUndefined() {
					if f, ok2 := v.Float(); ok2 {
						keepAliveMsecs = f
					}
				}
				if v, err := o.Get("maxSockets"); err == nil && !v.IsUndefined() {
					if f, ok2 := v.Float(); ok2 {
						maxSockets = f
					}
				}
			}
		}
		tr := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     time.Duration(keepAliveMsecs) * time.Millisecond,
		}
		if !keepAlive {
			tr.DisableKeepAlives = true
		}
		agent := engine.NewObject()
		httpAgentTransports[agent] = tr
		_ = agent.Set("keepAlive", engine.Boolean(keepAlive))
		_ = agent.Set("keepAliveMsecs", engine.Number(keepAliveMsecs))
		// maxSockets 默认 Infinity（Node 语义）。
		if maxSockets > 0 {
			_ = agent.Set("maxSockets", engine.Number(maxSockets))
		} else {
			_ = agent.Set("maxSockets", engine.Number(math.Inf(1)))
		}
		_ = agent.Set("maxFreeSockets", engine.Number(256))
		_ = agent.Set("sockets", engine.NewObject())
		_ = agent.Set("freeSockets", engine.NewObject())
		_ = agent.Set("requests", engine.NewObject())
		_ = agent.Set("getName", engine.NewFunction("getName", func(args []engine.Value) (engine.Value, error) {
			return engine.Str("http"), nil
		}))
		_ = agent.Set("createConnection", engine.NewFunction("createConnection", func(args []engine.Value) (engine.Value, error) {
			// 简化：返回一个未连接的 net.Socket（Node 的 createConnection 由
			// Agent 连接池专用；aluka 的请求走 Go Transport，此处仅为 API 面）。
			socket, _ := newNetSocket(ctx, nil)
			return socket, nil
		}))
		_ = agent.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
			tr.CloseIdleConnections()
			return engine.Undefined(), nil
		}))
		return agent, nil
	}))

	// 全局默认 agent（Node 19+ 默认 keepAlive:true）。
	ga := engine.NewObject()
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     60 * time.Second,
	}
	httpAgentTransports[ga] = tr
	_ = ga.Set("keepAlive", engine.Boolean(true))
	_ = ga.Set("keepAliveMsecs", engine.Number(1000))
	_ = ga.Set("maxFreeSockets", engine.Number(256))
	_ = ga.Set("sockets", engine.NewObject())
	_ = ga.Set("freeSockets", engine.NewObject())
	_ = ga.Set("requests", engine.NewObject())
	_ = ga.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		tr.CloseIdleConnections()
		return engine.Undefined(), nil
	}))
	_ = m.Set("globalAgent", ga)
}

// resolveAgentTransport 解析 options.agent 对应的 Transport：
// 返回 (transport, 是否每次新建连接)。
func resolveAgentTransport(agentVal engine.Value) (tr *http.Transport, noReuse bool) {
	if agentVal.IsUndefined() || agentVal.IsNull() {
		return getHttpGlobalTransport(), false
	}
	if b, ok := agentVal.Bool(); ok && !b {
		return nil, true // agent:false → 每次新建
	}
	if ao, ok := agentVal.AsObject(); ok {
		if t, ok := httpAgentTransports[ao]; ok {
			return t, false
		}
	}
	return getHttpGlobalTransport(), false
}
