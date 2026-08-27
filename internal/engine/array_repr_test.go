package engine

// ArrayValue 表示优化（v7 排查落地）的语义回归测试：
//   - 新建数组稠密（present==nil），length 标志走 lengthWritable 字段，
//     attrs map 不再 eager 分配；
//   - 首个洞出现才物化位图；收缩/追加/批量写路径在稠密↔稀疏间转换正确。

import (
	"reflect"
	"testing"
)

func newDense(vals ...Value) *ArrayValue { return NewArray(vals) }

func TestArrayDenseRepresentation(t *testing.T) {
	a := newDense(IntValue(1), IntValue(2), IntValue(3))
	if a.present != nil {
		t.Fatalf("新建数组应稠密（present==nil），got %v", a.present)
	}
	if a.attrs != nil {
		t.Fatalf("新建数组不应持有 attrs map，got %v", a.attrs)
	}
	if got := a.attrOf("length"); got != (PropAttrs{Writable: true}) {
		t.Fatalf("attrOf(length) = %+v, want {W:true}", got)
	}
	if got := AttrsOf(a, "length"); got != (PropAttrs{Writable: true}) {
		t.Fatalf("AttrsOf(length) = %+v, want {W:true}", got)
	}
	if d, ok := OwnPropertyDescriptor(a, "length"); !ok || !d.Writable || d.Enumerable || d.Configurable {
		t.Fatalf("length 描述符 = %+v ok=%v, want W=true E=false C=false", d, ok)
	}
	if keys := a.Keys(); !reflect.DeepEqual(keys, []string{"0", "1", "2"}) {
		t.Fatalf("Keys() = %v, want [0 1 2]", keys)
	}
}

func TestArrayHoleMaterialization(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(a *ArrayValue)
		wantKeys  []string
		wantHole0 bool
	}{
		{
			name:      "delete 制造洞",
			setup:     func(a *ArrayValue) { a.Delete("1") },
			wantKeys:  []string{"0", "2"},
			wantHole0: false,
		},
		{
			name:      "越界写制造前导洞",
			setup:     func(a *ArrayValue) { _ = a.Set("5", IntValue(9)) },
			wantKeys:  []string{"0", "1", "2", "5"},
			wantHole0: false,
		},
		{
			name:      "length 扩张产生洞",
			setup:     func(a *ArrayValue) { _ = a.Set("length", IntValue(4)) },
			wantKeys:  []string{"0", "1", "2"},
			wantHole0: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newDense(IntValue(10), IntValue(20), IntValue(30))
			tc.setup(a)
			if a.present == nil {
				t.Fatal("制造洞后位图应物化")
			}
			if got := a.Keys(); !reflect.DeepEqual(got, tc.wantKeys) {
				t.Fatalf("Keys() = %v, want %v", got, tc.wantKeys)
			}
			if v, _ := a.Get("2"); tc.wantHole0 && !v.IsUndefined() {
				t.Fatalf("Get(2) = %v, want undefined", v)
			}
		})
	}
}

func TestArrayDenseShrinkStaysDense(t *testing.T) {
	a := newDense(IntValue(1), IntValue(2), IntValue(3), IntValue(4))
	if err := a.Set("length", IntValue(2)); err != nil {
		t.Fatal(err)
	}
	if a.present != nil {
		t.Fatalf("稠密数组收缩后应保持稠密，got %v", a.present)
	}
	a.Append(IntValue(5))
	if got, _ := a.Get("length"); got.String() != "3" {
		t.Fatalf("length = %s, want 3", got)
	}
	if keys := a.Keys(); !reflect.DeepEqual(keys, []string{"0", "1", "2"}) {
		t.Fatalf("Keys() = %v", keys)
	}
	if v, _ := a.Get("2"); v.String() != "5" {
		t.Fatalf("Get(2) = %v, want 5", v)
	}
}

func TestArrayLengthWritableFlip(t *testing.T) {
	a := newDense(IntValue(1))
	// freeze 语义：length 置非可写后 push/扩张/Set(length) 均失效。
	if err := DefineOwnProperty(a, "length", Descriptor{HasWritable: true, Writable: false}); err != nil {
		t.Fatal(err)
	}
	if a.attrOf("length").Writable {
		t.Fatal("length 应为非可写")
	}
	if a.CanAppend(1) {
		t.Fatal("CanAppend 应为 false")
	}
	if err := a.Set("length", IntValue(5)); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Get("length"); got.String() != "1" {
		t.Fatalf("非可写 length 被改为 %s", got)
	}
	// 非可写后不可翻回可写（ES 规范）。
	if err := DefineOwnProperty(a, "length", Descriptor{HasWritable: true, Writable: true}); err == nil {
		t.Fatal("writable false→true 重定义应被拒绝")
	}
	// enumerable/configurable 置 true 必须拒绝。
	if err := DefineOwnProperty(a, "length", Descriptor{HasEnumerable: true, Enumerable: true}); err == nil {
		t.Fatal("length enumerable:true 应被拒绝")
	}
	if a.attrs != nil {
		t.Fatalf("length 标志翻转不应物化 attrs map，got %v", a.attrs)
	}
}

func TestArrayIndexAttrsLazyMap(t *testing.T) {
	a := newDense(IntValue(1), IntValue(2))
	if err := DefineOwnProperty(a, "0", Descriptor{HasWritable: true, Writable: false, HasValue: true, Value: IntValue(7), HasEnumerable: true, Enumerable: true, HasConfigurable: true, Configurable: true}); err != nil {
		t.Fatal(err)
	}
	if a.attrs == nil {
		t.Fatal("索引约束应物化 attrs map")
	}
	if err := a.Set("0", IntValue(9)); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.Get("0"); v.String() != "7" {
		t.Fatalf("非可写索引被改为 %v", v)
	}
	// 恢复默认标志后条目应从 map 收敛移除。
	if err := DefineOwnProperty(a, "0", Descriptor{HasWritable: true, Writable: true, HasEnumerable: true, Enumerable: true, HasConfigurable: true, Configurable: true}); err != nil {
		t.Fatal(err)
	}
	if len(a.attrs) != 0 {
		t.Fatalf("默认标志应收敛移除条目，got %v", a.attrs)
	}
	if err := a.Set("0", IntValue(9)); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.Get("0"); v.String() != "9" {
		t.Fatalf("Get(0) = %v, want 9", v)
	}
}

func TestArrayHolesConstructor(t *testing.T) {
	a := NewArrayHoles(3)
	if a.present == nil || len(a.present) != 3 {
		t.Fatalf("NewArrayHoles 应持有全 false 位图，got %v", a.present)
	}
	if keys := a.Keys(); len(keys) != 0 {
		t.Fatalf("全洞数组 Keys() = %v, want 空", keys)
	}
	if v, _ := a.Get("1"); !v.IsUndefined() {
		t.Fatalf("Get(1) = %v, want undefined", v)
	}
	a.SetIndex(1, IntValue(5))
	if keys := a.Keys(); !reflect.DeepEqual(keys, []string{"1"}) {
		t.Fatalf("Keys() = %v, want [1]", keys)
	}
}

func TestArrayBulkPathsDenseInvariant(t *testing.T) {
	t.Run("AppendValues", func(t *testing.T) {
		a := newDense(IntValue(1))
		a.AppendValues([]Value{IntValue(2), IntValue(3)})
		if a.present != nil {
			t.Fatalf("稠密+批量追加应保持稠密，got %v", a.present)
		}
		if got, _ := a.Get("length"); got.String() != "3" {
			t.Fatalf("length = %s", got)
		}
	})
	t.Run("AppendNumberRange", func(t *testing.T) {
		a := NewArrayHoles(0)
		a2 := newDense()
		_ = a
		a2.AppendNumberRange(0, 4)
		if a2.present != nil {
			t.Fatalf("稠密数组批量追加应保持稠密")
		}
		if got, _ := a2.Get("3"); got.String() != "3" {
			t.Fatalf("Get(3) = %v, want 3", got)
		}
	})
	t.Run("WriteNumberRange 跨洞扩张", func(t *testing.T) {
		a := newDense(IntValue(0), IntValue(1))
		a.WriteNumberRange(3, 10, 2) // 扩张到 len=5，[2] 保持洞
		if !a.isPresent(0) || a.isPresent(2) || !a.isPresent(3) || !a.isPresent(4) {
			t.Fatalf("位图状态错误: %v", a.present)
		}
		if keys := a.Keys(); !reflect.DeepEqual(keys, []string{"0", "1", "3", "4"}) {
			t.Fatalf("Keys() = %v", keys)
		}
	})
}

func TestArrayTrailingIndexAttrsGuard(t *testing.T) {
	a := newDense(IntValue(1))
	if a.HasTrailingIndexAttrs(2) {
		t.Fatal("无约束数组不应报 trailing attrs")
	}
	// 索引 2 定义非默认标志（writable:false），随后收缩 length 使描述符
	// 残留在尾随窗口：push 快路径必须让位给 Set 以遵守 writable。
	if err := DefineOwnProperty(a, "2", Descriptor{HasValue: true, Value: IntValue(9), HasWritable: true, Writable: false, HasConfigurable: true, Configurable: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("length", IntValue(2)); err != nil {
		t.Fatal(err)
	}
	if !a.HasTrailingIndexAttrs(1) {
		t.Fatalf("尾随索引残留自定义描述符时应回退 Set 路径, attrs=%v", a.attrs)
	}
}
