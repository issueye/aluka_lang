package nodediag

// node:perf_hooks 内置模块（开发计划 3.10）。
//
// 在全局 performance 对象上补齐 User Timing API 面：
//   mark/measure/getEntries*/clear*/timerify/eventLoopUtilization/nodeTiming，
//   以及 PerformanceObserver / Performance 类、constants、
//   monitorEventLoopDelay / createHistogram（确定性可测的最小实现）。
//
// 时间基准复用全局 performance.now()（globals 初始化时确立的 timeOrigin），
// 保证 mark.startTime 与 now() 同一时间轴。差分用例只断言相对量与类型，
// 不比较绝对值。

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// perfObserver 是 PerformanceObserver 实例。
type perfObserver struct {
	mu         sync.Mutex
	cb         engine.Value
	entryTypes []string
	active     bool
}

// perfState 保存 performance 的 entry 缓冲与观察者。
type perfState struct {
	mu           sync.Mutex
	entries      []engine.Value
	marks        map[string]float64 // mark name → startTime
	obs          []*perfObserver
	markProto    engine.Object // PerformanceMark.prototype
	measureProto engine.Object // PerformanceMeasure.prototype
}

func newPerfState() *perfState {
	return &perfState{marks: make(map[string]float64)}
}

func (s *perfState) addEntry(e engine.Value) {
	s.mu.Lock()
	s.entries = append(s.entries, e)
	s.mu.Unlock()
}

func (s *perfState) markTime(name string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.marks[name]; ok {
		return t
	}
	return 0
}

func (s *perfState) notifyObservers(ctx engine.Context, entryType string, entry engine.Value) {
	s.mu.Lock()
	var targets []*perfObserver
	for _, o := range s.obs {
		o.mu.Lock()
		hasType := false
		for _, t := range o.entryTypes {
			if t == entryType {
				hasType = true
				break
			}
		}
		queued := o.active && hasType
		o.mu.Unlock()
		if queued {
			targets = append(targets, o)
		}
	}
	s.mu.Unlock()
	for _, o := range targets {
		o := o
		// Node 语义：observer 回调异步派发；disconnect() 后已排队但未派发的
		// 记录被丢弃。这里用 PostTask 异步执行并在派发时检查 active。
		ctx.PostTask(func() {
			o.mu.Lock()
			active := o.active
			o.mu.Unlock()
			if !active {
				return
			}
			if f, ok := o.cb.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{newEntryList([]engine.Value{entry})}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		})
	}
}

// NewPerfHooks 构造 node:perf_hooks 模块导出对象。
func NewPerfHooks(ctx engine.Context) (engine.Value, error) {
	perfVal, err := globals.EnsurePerformance(ctx)
	if err != nil {
		return engine.Undefined(), err
	}
	perfObj, ok := perfVal.AsObject()
	if !ok {
		return engine.Undefined(), fmt.Errorf("perf_hooks: global performance not an object")
	}
	// 复用 globals 注册的 Performance.prototype（含 WebIDL 原型方法，工作流
	// B3）：模块加载只增强原型，不重挂实例原型、不在实例上写自有方法——
	// 保持 performance 自有键为空的原型链语义。
	perfProto := engine.GetProto(perfVal)
	if perfProto == nil {
		perfProto = engine.NewObject()
		engine.SetProto(perfVal, perfProto)
	}
	state := newPerfState()

	nowFn := func() float64 {
		if f, e := perfObj.Get("now"); e == nil && f.IsFunction() {
			if ff, ok := f.AsFunction(); ok {
				if v, ce := ff.Call(nil); ce == nil {
					if n, ok := v.Float(); ok {
						return n
					}
				}
			}
		}
		return float64(time.Now().UnixNano()) / 1e6
	}

	makeEntry := func(name, entryType string, startTime, duration float64) engine.Object {
		e := engine.NewObject()
		_ = e.Set("name", engine.Str(name))
		_ = e.Set("entryType", engine.Str(entryType))
		_ = e.Set("startTime", engine.Number(startTime))
		_ = e.Set("duration", engine.Number(duration))
		return e
	}

	// --- mark(name) -------------------------------------------------------
	_ = perfProto.Set("mark", engine.NewFunction("mark", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		st := nowFn()
		entry := makeEntry(name, "mark", st, 0)
		if state.markProto != nil {
			engine.SetProto(entry, state.markProto)
		}
		state.mu.Lock()
		state.marks[name] = st
		state.entries = append(state.entries, entry)
		state.mu.Unlock()
		state.notifyObservers(ctx, "mark", entry)
		return entry, nil
	}))

	// --- measure(name[, startMarkOrOptions][, endMark]) -------------------
	_ = perfProto.Set("measure", engine.NewFunction("measure", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		start := 0.0
		end := nowFn()
		if len(args) > 1 && !args[1].IsUndefined() && !args[1].IsNull() {
			if args[1].IsObject() {
				if o, ok := args[1].AsObject(); ok {
					if v, e := o.Get("start"); e == nil && !v.IsUndefined() {
						if n, ok := v.Float(); ok {
							start = n
						} else {
							start = state.markTime(v.String())
						}
					}
					if v, e := o.Get("end"); e == nil && !v.IsUndefined() {
						if n, ok := v.Float(); ok {
							end = n
						} else {
							end = state.markTime(v.String())
						}
					}
				}
			} else {
				start = state.markTime(args[1].String())
				if len(args) > 2 {
					end = state.markTime(args[2].String())
				}
			}
		}
		duration := end - start
		if duration < 0 {
			duration = 0
		}
		entry := makeEntry(name, "measure", start, duration)
		if state.measureProto != nil {
			engine.SetProto(entry, state.measureProto)
		}
		state.addEntry(entry)
		state.notifyObservers(ctx, "measure", entry)
		return entry, nil
	}))

	// --- getEntries 系列 ---------------------------------------------------
	_ = perfProto.Set("getEntries", engine.NewFunction("getEntries", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		all := append([]engine.Value(nil), state.entries...)
		state.mu.Unlock()
		return engine.NewArray(all), nil
	}))
	_ = perfProto.Set("getEntriesByType", engine.NewFunction("getEntriesByType", func(args []engine.Value) (engine.Value, error) {
		t := nodebase.StrArg(args, 0)
		state.mu.Lock()
		var out []engine.Value
		for _, e := range state.entries {
			if o, ok := e.AsObject(); ok {
				if v, err := o.Get("entryType"); err == nil && v.String() == t {
					out = append(out, e)
				}
			}
		}
		state.mu.Unlock()
		return engine.NewArray(out), nil
	}))
	_ = perfProto.Set("getEntriesByName", engine.NewFunction("getEntriesByName", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		typ := ""
		if len(args) > 1 {
			typ = args[1].String()
		}
		state.mu.Lock()
		var out []engine.Value
		for _, e := range state.entries {
			if o, ok := e.AsObject(); ok {
				if v, err := o.Get("name"); err == nil && v.String() == name {
					if typ == "" || func() bool {
						if v2, err := o.Get("entryType"); err == nil {
							return v2.String() == typ
						}
						return false
					}() {
						out = append(out, e)
					}
				}
			}
		}
		state.mu.Unlock()
		return engine.NewArray(out), nil
	}))
	_ = perfProto.Set("clearMarks", engine.NewFunction("clearMarks", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		state.mu.Lock()
		if name == "" {
			state.marks = make(map[string]float64)
		} else {
			delete(state.marks, name)
		}
		state.entries = filterEntries(state.entries, "mark", name)
		state.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = perfProto.Set("clearMeasures", engine.NewFunction("clearMeasures", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		state.mu.Lock()
		state.entries = filterEntries(state.entries, "measure", name)
		state.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = perfProto.Set("clearResourceTimings", engine.NewFunction("clearResourceTimings", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// --- timerify(fn) ------------------------------------------------------
	_ = perfProto.Set("timerify", engine.NewFunction("timerify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("perf_hooks: timerify requires a function")
		}
		fn := args[0]
		return engine.NewFunction("timerified", func(callArgs []engine.Value) (engine.Value, error) {
			start := nowFn()
			f, _ := fn.AsFunction()
			result, err := f.Call(callArgs)
			state.addEntry(makeEntry("", "function", start, nowFn()-start))
			return result, err
		}), nil
	}))

	// --- eventLoopUtilization / nodeTiming ---------------------------------
	_ = perfProto.Set("eventLoopUtilization", engine.NewFunction("eventLoopUtilization", func(args []engine.Value) (engine.Value, error) {
		o := engine.NewObject()
		_ = o.Set("idle", engine.Number(0))
		_ = o.Set("active", engine.Number(0))
		_ = o.Set("utilization", engine.Number(0))
		return o, nil
	}))
	_ = perfProto.Set("nodeTiming", makeNodeTiming(nowFn()))

	// --- PerformanceObserver -----------------------------------------------
	obsProto := engine.NewObject()
	_ = obsProto.Set("observe", interpreter.NewNativeMethod("observe", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		o := lookupPerfObserver(this)
		if o == nil {
			return engine.Undefined(), fmt.Errorf("perf_hooks: not a PerformanceObserver")
		}
		var entryTypes []string
		if len(args) > 0 && args[0].IsObject() {
			if opts, ok := args[0].AsObject(); ok {
				if v, e := opts.Get("entryTypes"); e == nil && v.IsObject() {
					if arr, ok := v.(*engine.ArrayValue); ok {
						for _, t := range arr.Elems() {
							entryTypes = append(entryTypes, t.String())
						}
					}
				}
				if v, e := opts.Get("type"); e == nil && !v.IsUndefined() {
					entryTypes = append(entryTypes, v.String())
				}
			}
		}
		o.mu.Lock()
		o.entryTypes = entryTypes
		o.active = true
		o.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = obsProto.Set("disconnect", interpreter.NewNativeMethod("disconnect", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		o := lookupPerfObserver(this)
		if o != nil {
			o.mu.Lock()
			o.active = false
			o.mu.Unlock()
		}
		return engine.Undefined(), nil
	}))
	_ = obsProto.Set("takeRecords", interpreter.NewNativeMethod("takeRecords", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.NewArray(nil), nil
	}))

	obsCtor := engine.NewFunction("PerformanceObserver", func(args []engine.Value) (engine.Value, error) {
		cb := engine.Undefined()
		if len(args) > 0 {
			cb = args[0]
		}
		o := &perfObserver{cb: cb}
		inst := engine.NewObject()
		for _, k := range obsProto.Keys() {
			if v, err := obsProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		perfObsRegistry.Lock()
		perfObsRegistry.m[inst] = o
		perfObsRegistry.Unlock()
		state.mu.Lock()
		state.obs = append(state.obs, o)
		state.mu.Unlock()
		return inst, nil
	})
	if co, ok := obsCtor.AsObject(); ok {
		_ = co.Set("prototype", obsProto)
		_ = co.Set("supportedEntryTypes", engine.NewArray([]engine.Value{
			engine.Str("dns"), engine.Str("function"), engine.Str("gc"), engine.Str("http"),
			engine.Str("http2"), engine.Str("mark"), engine.Str("measure"), engine.Str("net"),
			engine.Str("resource"),
		}))
	}

	// --- 模块导出 -----------------------------------------------------------
	moduleObj := engine.NewObject()
	_ = moduleObj.Set("performance", perfVal)

	// Performance 类（performance 实例所属类）：prototype 复用实例现有原型。
	perfCtor := engine.NewFunction("Performance", func(args []engine.Value) (engine.Value, error) {
		return perfVal, nil
	})
	_ = perfProto.Set("constructor", perfCtor)
	_ = engine.DefineOwnProperty(perfProto, "constructor", engine.Descriptor{HasEnumerable: true, Enumerable: false})
	if co, ok := perfCtor.AsObject(); ok {
		_ = co.Set("prototype", perfProto)
	}

	// PerformanceMark / PerformanceMeasure 类（mark/measure 返回实例的原型）。
	markCtor := engine.NewFunction("PerformanceMark", func(args []engine.Value) (engine.Value, error) {
		return makeEntry(nodebase.StrArg(args, 0), "mark", nowFn(), 0), nil
	})
	state.markProto = engine.NewObject()
	_ = state.markProto.Set("constructor", markCtor)
	if co, ok := markCtor.AsObject(); ok {
		_ = co.Set("prototype", state.markProto)
	}
	measureCtor := engine.NewFunction("PerformanceMeasure", func(args []engine.Value) (engine.Value, error) {
		return makeEntry(nodebase.StrArg(args, 0), "measure", 0, 0), nil
	})
	state.measureProto = engine.NewObject()
	_ = state.measureProto.Set("constructor", measureCtor)
	if co, ok := measureCtor.AsObject(); ok {
		_ = co.Set("prototype", state.measureProto)
	}

	// PerformanceEntry / PerformanceResourceTiming：最小类面。
	entryCtor := engine.NewFunction("PerformanceEntry", func(args []engine.Value) (engine.Value, error) {
		return makeEntry(nodebase.StrArg(args, 0), "performance", 0, 0), nil
	})
	resCtor := engine.NewFunction("PerformanceResourceTiming", func(args []engine.Value) (engine.Value, error) {
		return makeEntry(nodebase.StrArg(args, 0), "resource", 0, 0), nil
	})
	entryListCtor := engine.NewFunction("PerformanceObserverEntryList", func(args []engine.Value) (engine.Value, error) {
		return newEntryList(nil), nil
	})

	_ = moduleObj.Set("Performance", perfCtor)
	_ = moduleObj.Set("PerformanceEntry", entryCtor)
	_ = moduleObj.Set("PerformanceMark", markCtor)
	_ = moduleObj.Set("PerformanceMeasure", measureCtor)
	_ = moduleObj.Set("PerformanceObserver", obsCtor)
	_ = moduleObj.Set("PerformanceObserverEntryList", entryListCtor)
	_ = moduleObj.Set("PerformanceResourceTiming", resCtor)

	// 全局注册（Node 语义：globalThis.PerformanceObserver 等无需 require 即可用）。
	for _, name := range []string{
		"Performance", "PerformanceEntry", "PerformanceMark", "PerformanceMeasure",
		"PerformanceObserver", "PerformanceObserverEntryList", "PerformanceResourceTiming",
	} {
		if v, err := moduleObj.Get(name); err == nil {
			_ = ctx.Global().Set(name, v)
		}
	}

	// --- constants -----------------------------------------------------------
	constants := engine.NewObject()
	_ = constants.Set("NODE_PERFORMANCE_GC_MAJOR", engine.IntValue(4))
	_ = constants.Set("NODE_PERFORMANCE_GC_MINOR", engine.IntValue(1))
	_ = constants.Set("NODE_PERFORMANCE_GC_INCREMENTAL", engine.IntValue(8))
	_ = constants.Set("NODE_PERFORMANCE_GC_WEAKCB", engine.IntValue(16))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_NO", engine.IntValue(0))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_CONSTRUCT_RETAINED", engine.IntValue(2))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_FORCED", engine.IntValue(4))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_SYNCHRONOUS_PHANTOM_PROCESSING", engine.IntValue(8))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_ALL_AVAILABLE_GARBAGE", engine.IntValue(16))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_ALL_EXTERNAL_MEMORY", engine.IntValue(32))
	_ = constants.Set("NODE_PERFORMANCE_GC_FLAGS_SCHEDULE_IDLE", engine.IntValue(64))
	_ = moduleObj.Set("constants", constants)

	// --- createHistogram / monitorEventLoopDelay -----------------------------
	_ = moduleObj.Set("createHistogram", engine.NewFunction("createHistogram", func(args []engine.Value) (engine.Value, error) {
		return newHistogram(), nil
	}))
	_ = moduleObj.Set("monitorEventLoopDelay", engine.NewFunction("monitorEventLoopDelay", func(args []engine.Value) (engine.Value, error) {
		return newEventLoopDelayHistogram(), nil
	}))

	return moduleObj, nil
}

// --- PerformanceObserver 注册表 -------------------------------------------------

var perfObsRegistry = struct {
	sync.Mutex
	m map[engine.Object]*perfObserver
}{m: make(map[engine.Object]*perfObserver)}

func lookupPerfObserver(inst engine.Value) *perfObserver {
	if inst == nil || !inst.IsObject() {
		return nil
	}
	o, _ := inst.AsObject()
	perfObsRegistry.Lock()
	defer perfObsRegistry.Unlock()
	return perfObsRegistry.m[o]
}

// --- entry list ---------------------------------------------------------------

var entryListData = struct {
	sync.Mutex
	m map[engine.Object][]engine.Value
}{m: make(map[engine.Object][]engine.Value)}

var entryListProtoOnce sync.Once
var entryListProtoVal engine.Object

func entryListGlobalProto() engine.Object {
	entryListProtoOnce.Do(func() {
		p := engine.NewObject()
		_ = p.Set("getEntries", interpreter.NewNativeMethod("getEntries", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return getEntryListEntries(this, ""), nil
		}))
		_ = p.Set("getEntriesByType", interpreter.NewNativeMethod("getEntriesByType", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return getEntryListEntries(this, "type:"+nodebase.StrArg(args, 0)), nil
		}))
		_ = p.Set("getEntriesByName", interpreter.NewNativeMethod("getEntriesByName", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return getEntryListEntries(this, "name:"+nodebase.StrArg(args, 0)), nil
		}))
		entryListProtoVal = p
	})
	return entryListProtoVal
}

// newEntryList 构造 PerformanceObserverEntryList 实例。
func newEntryList(entries []engine.Value) engine.Value {
	inst := engine.NewObject()
	entryListData.Lock()
	entryListData.m[inst] = append([]engine.Value(nil), entries...)
	entryListData.Unlock()
	proto := entryListGlobalProto()
	for _, k := range proto.Keys() {
		if v, err := proto.Get(k); err == nil {
			_ = inst.Set(k, v)
		}
	}
	return inst
}

func getEntryListEntries(inst engine.Value, filter string) engine.Value {
	if inst == nil || !inst.IsObject() {
		return engine.NewArray(nil)
	}
	o, _ := inst.AsObject()
	entryListData.Lock()
	entries := append([]engine.Value(nil), entryListData.m[o]...)
	entryListData.Unlock()
	if filter == "" {
		return engine.NewArray(entries)
	}
	kind, val := filter[:4], filter[5:]
	var out []engine.Value
	for _, e := range entries {
		eo, ok := e.AsObject()
		if !ok {
			continue
		}
		if kind == "type" {
			if v, err := eo.Get("entryType"); err == nil && v.String() == val {
				out = append(out, e)
			}
		} else {
			if v, err := eo.Get("name"); err == nil && v.String() == val {
				out = append(out, e)
			}
		}
	}
	return engine.NewArray(out)
}

// --- 工具 ---------------------------------------------------------------------

// filterEntries 移除 entryType 匹配且（name 为空或匹配）的条目。
func filterEntries(entries []engine.Value, entryType, name string) []engine.Value {
	var out []engine.Value
	for _, e := range entries {
		o, ok := e.AsObject()
		if !ok {
			out = append(out, e)
			continue
		}
		if v, err := o.Get("entryType"); err == nil && v.String() == entryType {
			if name == "" {
				continue
			}
			if n, err := o.Get("name"); err == nil && n.String() == name {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

func makeNodeTiming(duration float64) engine.Value {
	o := engine.NewObject()
	_ = o.Set("name", engine.Str("node"))
	_ = o.Set("entryType", engine.Str("node"))
	_ = o.Set("startTime", engine.Number(0))
	_ = o.Set("duration", engine.Number(duration))
	_ = o.Set("nodeStart", engine.Number(0))
	_ = o.Set("v8Start", engine.Number(0))
	_ = o.Set("bootstrapComplete", engine.Number(0))
	_ = o.Set("environment", engine.Number(0))
	_ = o.Set("loopStart", engine.Number(-1))
	_ = o.Set("loopExit", engine.Number(-1))
	_ = o.Set("idleTime", engine.Number(0))
	return o
}

// --- Histogram -----------------------------------------------------------------

// histogram 实现 RecordableHistogram 的确定性最小面。
type histogram struct {
	mu     sync.Mutex
	values []float64
}

func newHistogram() engine.Value {
	h := &histogram{}
	inst := engine.NewObject()
	_ = inst.Set("record", interpreter.NewNativeMethod("record", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if n, ok := args[0].Float(); ok {
				h.mu.Lock()
				h.values = append(h.values, n)
				h.mu.Unlock()
			}
		}
		return engine.Undefined(), nil
	}))
	_ = inst.Set("recordDelta", interpreter.NewNativeMethod("recordDelta", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = inst.Set("add", interpreter.NewNativeMethod("add", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = inst.Set("reset", interpreter.NewNativeMethod("reset", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		h.mu.Lock()
		h.values = nil
		h.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = inst.Set("percentile", interpreter.NewNativeMethod("percentile", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		p := 0.0
		if len(args) > 0 {
			p, _ = args[0].Float()
		}
		return engine.Number(h.percentile(p)), nil
	}))
	_ = inst.Set("percentiles", interpreter.NewNativeMethod("percentiles", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		o := engine.NewObject()
		_ = o.Set("0", engine.Number(h.percentile(0)))
		_ = o.Set("50", engine.Number(h.percentile(50)))
		_ = o.Set("100", engine.Number(h.percentile(100)))
		return o, nil
	}))
	for _, prop := range []string{"count", "min", "max", "mean", "stddev", "exceeds"} {
		engine.SetAccessor(inst, prop, engine.NewFunction(prop, func(args []engine.Value) (engine.Value, error) {
			return engine.Number(h.accessor(prop)), nil
		}), nil)
	}
	return inst
}

func (h *histogram) accessor(prop string) float64 {
	switch prop {
	case "count":
		return float64(h.count())
	case "min":
		return h.min()
	case "max":
		return h.max()
	case "mean":
		return h.mean()
	case "stddev":
		return h.stddev()
	case "exceeds":
		return 0
	}
	return 0
}

func (h *histogram) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.values)
}

func (h *histogram) min() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.values) == 0 {
		return math.Inf(1)
	}
	m := h.values[0]
	for _, v := range h.values {
		if v < m {
			m = v
		}
	}
	return m
}

func (h *histogram) max() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.values) == 0 {
		return 0
	}
	m := h.values[0]
	for _, v := range h.values {
		if v > m {
			m = v
		}
	}
	return m
}

func (h *histogram) mean() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range h.values {
		sum += v
	}
	return sum / float64(len(h.values))
}

func (h *histogram) stddev() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.values)
	if n == 0 {
		return 0
	}
	mean := 0.0
	for _, v := range h.values {
		mean += v
	}
	mean /= float64(n)
	var sum float64
	for _, v := range h.values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(n))
}

// percentile 按最近秩法计算百分位（p=0 → min，p=100 → max）。
func (h *histogram) percentile(p float64) float64 {
	h.mu.Lock()
	vals := append([]float64(nil), h.values...)
	h.mu.Unlock()
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	if p <= 0 {
		return vals[0]
	}
	if p >= 100 {
		return vals[len(vals)-1]
	}
	rank := (p / 100) * float64(len(vals)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return vals[lo]
	}
	return vals[lo] + (vals[hi]-vals[lo])*(rank-float64(lo))
}

// newEventLoopDelayHistogram 实现 monitorEventLoopDelay 的最小面。
func newEventLoopDelayHistogram() engine.Value {
	inst := engine.NewObject()
	_ = inst.Set("enable", engine.NewFunction("enable", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = inst.Set("disable", engine.NewFunction("disable", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = inst.Set("reset", engine.NewFunction("reset", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = inst.Set("percentile", engine.NewFunction("percentile", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(0), nil
	}))
	_ = inst.Set("percentiles", engine.NewFunction("percentiles", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	for _, prop := range []string{"count", "min", "max", "mean", "stddev", "exceeds"} {
		engine.SetAccessor(inst, prop, engine.NewFunction(prop, func(args []engine.Value) (engine.Value, error) {
			return engine.Number(0), nil
		}), nil)
	}
	return inst
}
