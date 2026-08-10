package engine

// WeakMap 是一个以 JS 对象为 key 的弱引用关联存储。
//
// 背景：builtin 包历史上用 map[Object]V 关联 JS 对象与 Go 资源
// （如 x509CertMap、httpAgentTransports）。强引用 key 会阻止 JS 对象被
// Go GC 回收（与 jsHeap 的 weak.Pointer 机制冲突），对象死亡后条目也永不
// 清除，造成双重泄漏。WeakMap 用 weak.Pointer 作 key，对象被 GC 回收后
// 条目在下次 sweep 时自动失效，不再阻止回收。
//
// 使用：
//
//	wm := engine.NewWeakMap[*x509.Certificate]()
//	wm.Set(obj, cert)
//	cert, ok := wm.Get(obj)
//
// 清理时机：所有 WeakMap 注册到 weakMaps 全局列表，在 jsHeap 的周期 sweep
// （每 gcSweepEvery 次分配）与显式 GC() 时统一清理失效条目，无需额外 goroutine。

import "sync"

import "weak"

// weakMapEntry 内部条目：weak key + 值。
type weakMapEntry[V any] struct {
	key weak.Pointer[objectValue]
	val V
}

// WeakMap 以 JS Object 为 key 的弱引用 map。
type WeakMap[V any] struct {
	entries map[weak.Pointer[objectValue]]weakMapEntry[V]
}

// NewWeakMap 创建空 WeakMap 并注册到全局清理列表。
func NewWeakMap[V any]() *WeakMap[V] {
	wm := &WeakMap[V]{entries: make(map[weak.Pointer[objectValue]]weakMapEntry[V])}
	weakMapsMu.Lock()
	weakMaps = append(weakMaps, wm)
	weakMapsMu.Unlock()
	return wm
}

// Set 关联 obj→v。obj 必须是引擎创建的对象（*objectValue）。
func (m *WeakMap[V]) Set(obj Object, v V) {
	ov := objectFromObject(obj)
	if ov == nil {
		return
	}
	wp := weak.Make(ov)
	m.entries[wp] = weakMapEntry[V]{key: wp, val: v}
}

// Get 读取 obj 关联的值。对象已被回收或未设置时返回 false。
func (m *WeakMap[V]) Get(obj Object) (V, bool) {
	var zero V
	ov := objectFromObject(obj)
	if ov == nil {
		return zero, false
	}
	wp := weak.Make(ov)
	if e, ok := m.entries[wp]; ok {
		return e.val, true
	}
	return zero, false
}

// Delete 移除 obj 的条目。
func (m *WeakMap[V]) Delete(obj Object) {
	ov := objectFromObject(obj)
	if ov == nil {
		return
	}
	delete(m.entries, weak.Make(ov))
}

// sweepWeakMap 清理已被 Go GC 回收（weak.Value()==nil）的条目。
// 由 sweepAllWeakMaps 在 sweep 周期统一调用。
func (m *WeakMap[V]) sweepWeakMap() {
	for wp, e := range m.entries {
		if e.key.Value() == nil {
			delete(m.entries, wp)
		}
	}
}

// Len 返回当前条目数（含可能尚未清理的失效条目；主要用于观测）。
func (m *WeakMap[V]) Len() int { return len(m.entries) }

// weakMapper 是所有 WeakMap 的擦除接口，便于全局列表统一清理。
type weakMapper interface {
	sweepWeakMap()
}

var (
	weakMaps   []weakMapper
	weakMapsMu sync.Mutex
)

// sweepAllWeakMaps 清理所有已注册 WeakMap 的失效条目。
// 在 jsHeap.sweepLocked 与 GC() 中调用。
func sweepAllWeakMaps() {
	weakMapsMu.Lock()
	list := weakMaps
	weakMapsMu.Unlock()
	for _, wm := range list {
		wm.sweepWeakMap()
	}
}

// objectFromObject 从 Object 接口取底层 *objectValue。
// 普通对象（NewObject/NewObjectFromPairs 等）返回 *objectValue；
// 其它对象类型（DateValue/BufferValue 等）返回 nil（不作为 WeakMap key）。
func objectFromObject(obj Object) *objectValue {
	if ov, ok := obj.(*objectValue); ok {
		return ov
	}
	return nil
}
