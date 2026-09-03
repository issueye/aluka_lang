package engine

// 自研 GC 测试（1B.6）：对象注册 + 根集标记遍历 + 统计。

import (
	"runtime"
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

// TestJSHeapSweepAtResetAfterShrink 验证把表清小后阈值重新按存活规模收缩：
// 周期清扫与显式 GC 都会清小表，阈值必须跟着回落——否则周期清扫要等表重新
// 长到旧阈值才触发，期间死条目持续堆积。
func TestJSHeapSweepAtResetAfterShrink(t *testing.T) {
	h := &jsHeap{objects: make(map[weak.Pointer[objectValue]]struct{}), sweepAt: gcSweepEvery}
	live := make([]*objectValue, 0, 3*gcSweepEvery)
	for i := 0; i < 3*gcSweepEvery; i++ {
		o := &objectValue{shape: rootShape}
		live = append(live, o)
		h.objects[weak.Make(o)] = struct{}{}
		if len(h.objects) >= h.sweepAt {
			h.sweepLocked()
		}
	}
	// 模拟显式 GC 把存活集收缩到 50：弱引用失效后清除全部条目。
	h.objects = make(map[weak.Pointer[objectValue]]struct{})
	for i := 0; i < 50; i++ {
		h.objects[weak.Make(live[i])] = struct{}{}
	}
	h.resetSweepAtLocked()
	if h.sweepAt != gcSweepEvery {
		t.Fatalf("收缩后阈值 = %d, want 起始值 %d", h.sweepAt, gcSweepEvery)
	}
	// 再模拟高存活集：阈值必须涨到存活数的两倍以上。
	h.objects = make(map[weak.Pointer[objectValue]]struct{})
	for i := 0; i < len(live); i++ {
		h.objects[weak.Make(live[i])] = struct{}{}
	}
	h.resetSweepAtLocked()
	if h.sweepAt <= len(h.objects) {
		t.Fatalf("高存活集阈值 %d 未超过存活数 %d", h.sweepAt, len(h.objects))
	}
}

// TestGCExplicitResetsSweepAt 验证显式 GC 在清除弱引用后重置全局自动清扫
// 阈值（发现 1 回归用例）：清小表后 sweepAt 必须回落，周期清扫才能继续生效。
func TestGCExplicitResetsSweepAt(t *testing.T) {
	EnableMetrics()
	defer DisableMetricsForTest()

	// 先制造一个大表：注册对象并强制 Go GC 回收它们，使弱引用失效。
	// 显式 GC 会清除失效条目并把表清小。
	root := NewObject()
	ephemeral := make([]*objectValue, 0, gcSweepEvery*2)
	for i := 0; i < gcSweepEvery*2; i++ {
		o := NewObject().(*objectValue)
		ephemeral = append(ephemeral, o)
	}
	if jsHeapGlobal.sweepAt <= gcSweepEvery {
		t.Fatalf("前置条件不成立: sweepAt = %d", jsHeapGlobal.sweepAt)
	}
	// 让临时对象全部失去强引用，然后显式 GC 清除它们。
	ephemeral = nil
	runtime.GC()
	GC([]Value{root})
	if jsHeapGlobal.sweepAt != gcSweepEvery {
		t.Fatalf("显式 GC 后 sweepAt = %d, want 起始值 %d", jsHeapGlobal.sweepAt, gcSweepEvery)
	}
}
