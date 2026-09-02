package engine

import "sync"

// 隐藏类（Hidden Class / Shape）——开发计划 1B.5。
//
// 设计（V8 风格 shape 树）：
//   - Shape 描述一组属性名及其槽位索引，多个结构相同的对象共享同一 Shape。
//   - 添加新属性时经 transition 派生新 Shape（父 Shape + 属性名），
//     结构相似的对象共享前缀 Shape。
//   - 对象只存 slots（[]Value），属性访问经 shape.index 映射到槽位 O(1)。
//   - Shape 不可变（只增 transition）；删除属性在对象级标记（deleted map），
//     避免污染共享 Shape。

// Shape 是一组属性的布局描述。
type Shape struct {
	id    uint64            // 全局唯一标识（IC hash 用）
	names []string          // 属性名（插入顺序，即槽位顺序）
	index map[string]int    // 属性名 → 槽位索引
	next  map[string]*Shape // transition：加属性 name → 新 Shape
}

// shapeCounter 分配 Shape 唯一 id。
var shapeCounter uint64

// maxShapeProps 限制 Shape transition 树深度。rootShape 是进程级全局树且
// 永不回收：若对同一对象持续添加不同属性名（如动态对象 o["k"+i]=i），每
// 个新属性派生一个新 Shape 并挂到树上，第 N 个 Shape 累积 N 个属性名，总
// 内存 O(N²)，最终 OOM。超过阈值后 transition 返回"不参与树缓存"的独立
// Shape（仍带完整 names/index，对象释放后可随 Go GC 回收），把最坏内存
// 降到 O(N)。正常对象（固定结构，属性数通常远小于该值）仍完整享受 Shape
// 共享。
const maxShapeProps = 128

// shapeTransitionMu serializes writes to the process-wide Shape tree. Native
// async operations may prepare JS values on worker goroutines while the VM is
// active, so rootShape transitions are not confined to the event-loop thread.
var shapeTransitionMu sync.Mutex

// rootShape 是空对象 Shape（所有 Shape 转移树的根）。
var rootShape = &Shape{
	id:    1,
	index: make(map[string]int),
	next:  make(map[string]*Shape),
}

// NumProps 返回属性数量。
func (s *Shape) NumProps() int { return len(s.names) }

// PropName 返回第 i 个属性名。
func (s *Shape) PropName(i int) string { return s.names[i] }

// lookup 返回属性名的槽位索引（仅当属性存在于本 Shape）。
func (s *Shape) lookup(name string) (int, bool) {
	idx, ok := s.index[name]
	return idx, ok
}

// transition 派生加 name 属性后的新 Shape（共享父 Shape）。
func (s *Shape) transition(name string) *Shape {
	shapeTransitionMu.Lock()
	defer shapeTransitionMu.Unlock()

	// 深度超限：创建独立 Shape（不挂全局树，对象释放后可回收），
	// 防止动态属性名把 O(N²) 的 shape 树撑爆。
	if len(s.names) >= maxShapeProps {
		shapeCounter++
		ns := &Shape{
			id:    shapeCounter,
			names: append(append([]string(nil), s.names...), name),
			index: make(map[string]int, len(s.index)+1),
			next:  make(map[string]*Shape),
		}
		for k, v := range s.index {
			ns.index[k] = v
		}
		ns.index[name] = len(s.names)
		return ns
	}

	if s.next == nil {
		s.next = make(map[string]*Shape)
	}
	if ns, ok := s.next[name]; ok {
		return ns
	}
	shapeCounter++
	ns := &Shape{
		id:    shapeCounter,
		names: append(append([]string(nil), s.names...), name),
		index: make(map[string]int, len(s.index)+1),
		next:  make(map[string]*Shape),
	}
	for k, v := range s.index {
		ns.index[k] = v
	}
	ns.index[name] = len(s.names)
	s.next[name] = ns
	return ns
}

// --- 内联缓存（Inline Cache） --------------------------------------------
//
// 基于隐藏类的属性访问缓存：记录 (shape, key) → 槽位索引，命中时直接读
// 槽位，跳过 shape.index 的 map 查找与 deleted 检查。

// icSize 是 IC 表大小（2 的幂，取模 hash）。
const icSize = 2048

// icEntry 是单条 IC 记录。
type icEntry struct {
	shape *Shape
	key   string
	idx   int
	valid bool
}

// callICSize 是方法调用 IC 表大小（per-PC 槽）。
const callICSize = 4096

// callICEntry 是单条方法调用 IC 记录（O1-C4）：
// 缓存 (pc, shape, key) → 槽位 + 方法值。命中时验证 slots[idx] == fn
// （方法被替换则自动失效），直接 invoke，跳过属性解析链。
// key 必须参与匹配：不同函数模板可能在同一 pc 调用不同方法名。
type callICEntry struct {
	pc    int32
	shape *Shape
	key   string
	idx   int32
	fn    Value
	valid bool
}

// ICache 是 VM 级属性访问内联缓存（读/写共用 entries 表——
// (shape, key) → 槽位索引对读写一致）。
type ICache struct {
	entries [icSize]icEntry
	calls   [callICSize]callICEntry // 方法调用缓存（O1-C4）

	// 命中统计（--ic-stats 报告，O1 验收）。
	getHit, getMiss   uint64
	setHit, setMiss   uint64
	callHit, callMiss uint64
}

// ICStats 是内联缓存命中统计。
type ICStats struct {
	GetHit, GetMiss   uint64
	SetHit, SetMiss   uint64
	CallHit, CallMiss uint64
}

// Stats 返回命中统计快照（O1：--ic-stats 报告）。
func (c *ICache) Stats() ICStats {
	return ICStats{
		GetHit: c.getHit, GetMiss: c.getMiss,
		SetHit: c.setHit, SetMiss: c.setMiss,
		CallHit: c.callHit, CallMiss: c.callMiss,
	}
}

// GetCached 尝试命中 obj 的 (key) 属性（仅对隐藏类对象有效）。
// 返回 own 属性当前值；未命中或非隐藏类对象返回 (Undefined, false)。
func (c *ICache) GetCached(obj Value, key string) (Value, bool) {
	ov, ok := obj.(*objectValue)
	if !ok {
		c.getMiss++
		return Undefined(), false
	}
	h := icHash(ov.shape.id, key)
	e := &c.entries[h]
	if !e.valid || e.shape != ov.shape || e.key != key {
		c.getMiss++
		return Undefined(), false
	}
	if ov.isDeleted(key) {
		c.getMiss++
		return Undefined(), false // 已删除，缓存失效
	}
	c.getHit++
	return ov.slots[e.idx], true
}

// CachePut 在 shape.lookup 成功后记录 (shape, key) → 槽位。
// 非 own 属性（在原型链上）不记录。
func (c *ICache) CachePut(obj Value, key string) {
	ov, ok := obj.(*objectValue)
	if !ok {
		return
	}
	if idx, ok := ov.shape.lookup(key); ok {
		h := icHash(ov.shape.id, key)
		c.entries[h] = icEntry{shape: ov.shape, key: key, idx: idx, valid: true}
	}
}

// SetCached 尝试命中写入缓存：直接写槽位（O1-C3）。
// 返回 true 表示已写入（隐藏类 own 属性、无 deleted 标记、缓存命中）。
// 注：transition 写（属性首次添加，如对象字面量构建）在结构上不可
// 命中——查询基于写前 shape 而缓存记录写后 shape——不计入 miss。
func (c *ICache) SetCached(obj Value, key string, val Value) bool {
	ov, ok := obj.(*objectValue)
	if !ok {
		c.setMiss++
		return false
	}
	h := icHash(ov.shape.id, key)
	e := &c.entries[h]
	if !e.valid || e.shape != ov.shape || e.key != key {
		if _, has := ov.shape.lookup(key); !has {
			return false // transition 写（结构上不可缓存）
		}
		c.setMiss++
		return false
	}
	if ov.isDeleted(key) {
		c.setMiss++
		return false // 已删除，缓存失效（走完整路径恢复）
	}
	// writable:false 属性不可直写：回退完整路径（objectValue.Set 静默
	// 拒绝）。attrs 惰性分配，普通对象此处仅一次 nil 判断。
	if a, ok := ov.getAttr(key); ok && !a.Writable {
		c.setMiss++
		return false
	}
	// accessor 槽位不可直写：必须回退到 setProperty 的 FindAccessor 拦截
	// 调 setter（IC 前置后 accessor 槽位会到达这里）。
	if _, isAcc := ov.slots[e.idx].(*AccessorValue); isAcc {
		c.setMiss++
		return false
	}
	if ov.isNonExtensible() {
		// Existing properties remain writable; this guard documents that IC only
		// ever writes an existing slot and cannot add a property.
		if _, exists := ov.getSlot(key); !exists {
			c.setMiss++
			return false
		}
	}
	c.setHit++
	ov.slots[e.idx] = val
	return true
}

// SetPut 在属性写入成功后记录写入缓存（O1-C3）。
// 仅记录"已存在槽位"的属性（transition 新属性不记录）。
func (c *ICache) SetPut(obj Value, key string) {
	ov, ok := obj.(*objectValue)
	if !ok {
		return
	}
	if ov.isDeleted(key) {
		return
	}
	if idx, ok := ov.shape.lookup(key); ok {
		h := icHash(ov.shape.id, key)
		c.entries[h] = icEntry{shape: ov.shape, key: key, idx: idx, valid: true}
	}
}

// CallCached 尝试命中方法调用缓存（O1-C4）：返回缓存的方法函数。
// 验证槽位当前值仍等于缓存值（方法被替换/删除自动失效）。
func (c *ICache) CallCached(pc int, obj Value, key string) (Value, bool) {
	ov, ok := obj.(*objectValue)
	if !ok {
		c.callMiss++
		return Undefined(), false
	}
	if ov.isDeleted(key) {
		c.callMiss++
		return Undefined(), false // 已删除：槽位未置空，必须失效走完整解析
	}
	h := uint32(pc) & (callICSize - 1)
	e := &c.calls[h]
	if !e.valid || int(e.pc) != pc || e.shape != ov.shape || e.key != key {
		c.callMiss++
		return Undefined(), false
	}
	if int(e.idx) >= len(ov.slots) {
		c.callMiss++
		return Undefined(), false
	}
	cur := ov.slots[e.idx]
	if cur != e.fn {
		c.callMiss++
		return Undefined(), false // 方法已被替换/删除
	}
	c.callHit++
	return e.fn, true
}

// CallPut 记录方法调用缓存（仅隐藏类 own 属性、非 accessor 值）。
func (c *ICache) CallPut(pc int, obj Value, key string, fn Value) {
	ov, ok := obj.(*objectValue)
	if !ok || fn == nil {
		return
	}
	if _, isAcc := fn.(*AccessorValue); isAcc {
		return // accessor 走拦截路径
	}
	if ov.isDeleted(key) {
		return
	}
	idx, ok := ov.shape.lookup(key)
	if !ok {
		return // 原型链属性不缓存
	}
	h := uint32(pc) & (callICSize - 1)
	c.calls[h] = callICEntry{pc: int32(pc), shape: ov.shape, key: key, idx: int32(idx), fn: fn, valid: true}
}

// icHash 计算 (shapeId, key) 的槽位。
func icHash(shapeID uint64, key string) uint32 {
	h := uint32(shapeID)
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return h & (icSize - 1)
}
