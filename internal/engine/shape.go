package engine

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
	id    uint64             // 全局唯一标识（IC hash 用）
	names []string           // 属性名（插入顺序，即槽位顺序）
	index map[string]int     // 属性名 → 槽位索引
	next  map[string]*Shape  // transition：加属性 name → 新 Shape
}

// shapeCounter 分配 Shape 唯一 id。
var shapeCounter uint64

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
	if s.next == nil {
		s.next = make(map[string]*Shape)
	}
	if ns, ok := s.next[name]; ok {
		return ns
	}
	shapeCounter++
	ns := &Shape{
		id:    shapeCounter,
		names: make([]string, 0, len(s.names)+1),
		index: make(map[string]int, len(s.index)+1),
		next:  make(map[string]*Shape),
	}
	ns.names = append(ns.names, s.names...)
	for k, v := range s.index {
		ns.index[k] = v
	}
	ns.names = append(ns.names, name)
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

// ICache 是 VM 级属性访问内联缓存。
type ICache struct {
	entries [icSize]icEntry
}

// GetCached 尝试命中 obj 的 (key) 属性（仅对隐藏类对象有效）。
// 返回 own 属性当前值；未命中或非隐藏类对象返回 (Undefined, false)。
func (c *ICache) GetCached(obj Value, key string) (Value, bool) {
	ov, ok := obj.(*objectValue)
	if !ok {
		return Undefined(), false
	}
	h := icHash(ov.shape.id, key)
	e := &c.entries[h]
	if !e.valid || e.shape != ov.shape || e.key != key {
		return Undefined(), false
	}
	if ov.deleted != nil && ov.deleted[key] {
		return Undefined(), false // 已删除，缓存失效
	}
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

// icHash 计算 (shapeId, key) 的槽位。
func icHash(shapeID uint64, key string) uint32 {
	h := uint32(shapeID)
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return h & (icSize - 1)
}
