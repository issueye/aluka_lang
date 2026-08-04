package engine

// 自研 GC 测试（1B.6）：对象注册 + 根集标记遍历 + 统计。

import "testing"

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
