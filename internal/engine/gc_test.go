package engine

// 自研 GC 测试（1B.6）：对象注册 + 根集标记遍历 + 统计。

import (
	"testing"
	"weak"
)

// TestGCRegisterAndMark 验证对象注册与标记遍历。
func TestGCRegisterAndMark(t *testing.T) {
	// 创建对象图：root → child（属性引用）+ arr（数组元素）。
	root := NewObject()
	child := NewObject()
	_ = child.Set("v", IntValue(42))
	_ = root.Set("child", child)
	arr := NewArray([]Value{IntValue(1), child})
	_ = root.Set("arr", arr)

	stats := GC([]Value{root})
	if stats.AllocCount < 3 {
		t.Errorf("allocCount = %d, want >= 3 (root/child/arr)", stats.AllocCount)
	}
	if stats.MarkedCount < 3 {
		t.Errorf("markedCount = %d, want >= 3 (root/child/arr)", stats.MarkedCount)
	}
	if stats.LiveCount < 1 {
		t.Errorf("liveCount = %d, want >= 1", stats.LiveCount)
	}
}

// TestGCMarkTraversal 验证标记遍历沿对象图正确传播。
func TestGCMarkTraversal(t *testing.T) {
	// root → a → b（深层引用）。
	root := NewObject()
	a := NewObject()
	b := NewObject()
	_ = a.Set("b", b)
	_ = root.Set("a", a)

	marked := markFromRoots([]Value{root})
	if len(marked) != 3 {
		t.Errorf("marked = %d, want 3 (root/a/b)", len(marked))
	}
	if !marked[a.(*objectValue)] || !marked[b.(*objectValue)] {
		t.Error("deep objects a/b should be marked reachable from root")
	}
}

// TestJSHeapSweepThresholdAmortized 验证注册表清扫阈值按存活规模摊还：
// 固定步长（每 gcSweepEvery 次分配扫一遍全表）在存活对象远多于步长时
// 退化为 O(N²)——每几次分配就遍历整张表。清扫后阈值必须涨到存活数之上。
func TestJSHeapSweepThresholdAmortized(t *testing.T) {
	h := &jsHeap{objects: make(map[weak.Pointer[objectValue]]struct{}), sweepAt: gcSweepEvery}
	live := make([]*objectValue, 0, 4*gcSweepEvery)
	for i := 0; i < 4*gcSweepEvery; i++ {
		o := &objectValue{shape: rootShape}
		live = append(live, o) // 强引用保活，清扫不得删除条目
		h.objects[weak.Make(o)] = struct{}{}
		if len(h.objects) >= h.sweepAt {
			h.sweepLocked()
		}
	}
	if len(h.objects) != len(live) {
		t.Fatalf("存活对象条目被误删: %d, want %d", len(h.objects), len(live))
	}
	if h.sweepAt <= len(h.objects) {
		t.Fatalf("清扫阈值 %d 未超过存活数 %d——下次分配又会全表扫描", h.sweepAt, len(h.objects))
	}
}
