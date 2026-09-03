package interpreter

import "testing"

// 本文件覆盖 Array.prototype 与 Array 静态方法的新增实现（ES5+/ES2019/ES2022/ES2023）。
// 风格对齐 vm_test.go：使用 vmEvalStr / vmEvalStrErr 辅助函数 + 表格驱动。

func TestVMFrozenArrayMutatorsThrow(t *testing.T) {
	cases := []struct{ method, call string }{
		{"push", `a.push(4)`},
		{"pop", `a.pop()`},
		{"shift", `a.shift()`},
		{"unshift", `a.unshift(0)`},
		{"splice", `a.splice(1,1)`},
		{"sort", `a.sort()`},
		{"reverse", `a.reverse()`},
		{"fill", `a.fill(0)`},
		{"copyWithin", `a.copyWithin(0,1)`},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			code := `var a=[3,2,1]; Object.freeze(a); var threw=false; try { ` + tc.call + `; } catch(e) { threw=true; } JSON.stringify([threw,a.join(",")])`
			if got := vmEvalStr(t, code); got != `[true,"3,2,1"]` {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestVMArrayPush(t *testing.T) {
	cases := []struct{ code, want string }{
		{`var a=[]; a.push(1,2,3); a.join(",")`, "1,2,3"},
		{`var a=[1]; a.push(2); JSON.stringify([a.join(","), a.push()])`, `["1,2",2]`},
		{`var a=[]; for (var i=0;i<5;i++) a.push(i); a.join(",")`, "0,1,2,3,4"},
		// preventExtensions：不能扩 length（与 freeze/seal 一样 TypeError）
		{`var a=[1,2]; Object.preventExtensions(a); var threw=false; try{a.push(3)}catch(e){threw=true} JSON.stringify([threw,a.join(",")])`, `[true,"1,2"]`},
		// defineProperty 抬高 length 后，push 仍在末尾追加
		{`var a=[1]; Object.defineProperty(a,"1",{value:9,writable:false,enumerable:true,configurable:true}); a.push(8); a.join(",")`, "1,9,8"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestVMArrayPushMany 防止 push 再次对每个元素做 IsFullyWritable 全表扫描
//（O(n²)）。2 万次追加在快路径下应瞬间完成；旧实现会在秒级。
func TestVMArrayPushMany(t *testing.T) {
	got := vmEvalStr(t, `var a=[]; for (var i=0;i<20000;i++) a.push(i); a.length+","+a[0]+","+a[19999]`)
	if got != "20000,0,19999" {
		t.Fatalf("got %q", got)
	}
}

// === ES5 基础方法 ========================================================

func TestVMArraySplice(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 删除
		{`var a=[1,2,3,4]; a.splice(1,2); a.join(",")`, "1,4"},
		{`var a=[1,2,3,4]; a.splice(1,2).join(",")`, "2,3"},
		// 插入（deleteCount=0）
		{`var a=[1,2,3]; a.splice(1,0,9,9); a.join(",")`, "1,9,9,2,3"},
		// 替换
		{`var a=[1,2,3]; a.splice(1,1,8); a.join(",")`, "1,8,3"},
		// 负索引
		{`var a=[1,2,3]; a.splice(-1,1); a.join(",")`, "1,2"},
		// 默认删到末尾
		{`var a=[1,2,3]; a.splice(1); a.join(",")`, "1"},
		// 超出长度的 start 当作追加
		{`var a=[1,2]; a.splice(5,0,3); a.join(",")`, "1,2,3"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMArraySort(t *testing.T) {
	// 无比较函数：按字符串码点序
	cases := []struct {
		code string
		want string
	}{
		{`[3,1,2].sort().join(",")`, "1,2,3"},
		{`["banana","apple","cherry"].sort().join(",")`, "apple,banana,cherry"},
		// 数字比较函数
		{`[10,2,1,30].sort((a,b)=>a-b).join(",")`, "1,2,10,30"},
		{`[3,1,2].sort((a,b)=>b-a).join(",")`, "3,2,1"},
		// 空数组
		{`[].sort().length`, "0"},
		// sort 返回排序后的原数组引用（可链式取 length）
		{`[1,2,3].sort((a,b)=>a-b).length`, "3"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMArrayFind(t *testing.T) {
	got := vmEvalStr(t, `[1,2,3,4].find(x => x > 2)+""`)
	if got != "3" {
		t.Errorf("find = %q, want 3", got)
	}
	got = vmEvalStr(t, `[1,2,3].findIndex(x => x === 2)+""`)
	if got != "1" {
		t.Errorf("findIndex = %q, want 1", got)
	}
	got = vmEvalStr(t, `[1,2,3].find(x => x > 5)+""`)
	if got != "undefined" {
		t.Errorf("find(not found) = %q, want undefined", got)
	}
	got = vmEvalStr(t, `[1,2,3].findIndex(x => x > 5)+""`)
	if got != "-1" {
		t.Errorf("findIndex(not found) = %q, want -1", got)
	}
	// thisArg 透传：以箭头函数闭包验证（普通函数 this 绑定属引擎既有缺陷，
	// 见 development-plan 已知问题，此处不覆盖）
	got = vmEvalStr(t, `var o={min:2}; [1,2,3].find((x)=>x>o.min)+""`)
	if got != "3" {
		t.Errorf("find with closure = %q, want 3", got)
	}
}

func TestVMArraySomeEvery(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`[1,2,3].some(x => x > 2)+""`, "true"},
		{`[1,2,3].some(x => x > 5)+""`, "false"},
		{`[].some(x => true)+""`, "false"},
		{`[1,2,3].every(x => x > 0)+""`, "true"},
		{`[1,2,3].every(x => x > 1)+""`, "false"},
		{`[].every(x => false)+""`, "true"}, // 空数组 every 为 true（规范）
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMArrayReduceRight(t *testing.T) {
	// 从右向左拼接
	got := vmEvalStr(t, `["a","b","c"].reduceRight((acc, x) => acc + x)`)
	if got != "cba" {
		t.Errorf("reduceRight = %q, want cba", got)
	}
	// 带初值
	got = vmEvalStr(t, `[1,2,3].reduceRight((acc, x) => acc + x, 100)+""`)
	if got != "106" {
		t.Errorf("reduceRight with init = %q, want 106", got)
	}
	// 数值差分（右结合）
	got = vmEvalStr(t, `[1,2,3,4].reduceRight((acc, x) => acc - x)+""`)
	if got != "-2" { // 1-(2-(3-4)) = 1-(2-(-1)) = 1-3 = -2
		t.Errorf("reduceRight nested sub = %q, want -2", got)
	}
}

func TestVMArrayFillCopyWithin(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`[1,2,3,4].fill(0).join(",")`, "0,0,0,0"},
		{`[1,2,3,4].fill(0, 2).join(",")`, "1,2,0,0"},
		{`[1,2,3,4].fill(0, 1, 3).join(",")`, "1,0,0,4"},
		{`[1,2,3,4].fill(0, -2).join(",")`, "1,2,0,0"},
		{`[1,2,3,4,5].copyWithin(0, 3).join(",")`, "4,5,3,4,5"},
		{`[1,2,3,4,5].copyWithin(0, 3, 4).join(",")`, "4,2,3,4,5"},
		{`[1,2,3,4,5].copyWithin(1, 3).join(",")`, "1,4,5,4,5"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 迭代器方法 ==========================================================

func TestVMArrayKeysValuesEntries(t *testing.T) {
	got := vmEvalStr(t, `var sum=0; for (var i of [10,20,30].keys()) sum += i; sum+""`)
	if got != "3" { // 0+1+2
		t.Errorf("keys sum = %q, want 3", got)
	}
	got = vmEvalStr(t, `var sum=0; for (var v of [10,20,30].values()) sum += v; sum+""`)
	if got != "60" {
		t.Errorf("values sum = %q, want 60", got)
	}
	got = vmEvalStr(t, `var s=""; for (var p of [10,20,30].entries()) s += p[0]+":"+p[1]+" "; s`)
	if got != "0:10 1:20 2:30 " {
		t.Errorf("entries = %q", got)
	}
}

// === ES2019 flat / flatMap ===============================================

func TestVMArrayFlat(t *testing.T) {
	// 用 JSON.stringify 比较嵌套结构，避免 String() 格式差异（[ a, b ] vs [a,b]）。
	cases := []struct {
		code string
		want string
	}{
		{`JSON.stringify([1,[2,3]].flat())`, "[1,2,3]"},
		{`JSON.stringify([1,[2,[3,[4]]]].flat())`, "[1,2,[3,[4]]]"}, // 默认深度 1
		{`JSON.stringify([1,[2,[3,[4]]]].flat(2))`, "[1,2,3,[4]]"},  // 深度 2
		{`JSON.stringify([1,[2,[3,[4]]]].flat(3))`, "[1,2,3,4]"},    // 深度 3
		{`JSON.stringify([1,[2,[3,[4]]]].flat(Infinity))`, "[1,2,3,4]"},
		{`JSON.stringify([1,[2,3],[4]].flat())`, "[1,2,3,4]"},
		// flatMap
		{`[1,2,3].flatMap(x => [x, x*2]).join(",")`, "1,2,2,4,3,6"},
		{`[1,2,3].flatMap(x => x*10).join(",")`, "10,20,30"}, // 回调返回非数组时原样保留
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === ES2023 findLast / findLastIndex =====================================

func TestVMArrayFindLast(t *testing.T) {
	got := vmEvalStr(t, `[1,2,3,4].findLast(x => x > 2)+""`)
	if got != "4" {
		t.Errorf("findLast = %q, want 4", got)
	}
	got = vmEvalStr(t, `[1,2,3,4].findLastIndex(x => x > 2)+""`)
	if got != "3" {
		t.Errorf("findLastIndex = %q, want 3", got)
	}
	got = vmEvalStr(t, `[1,2,3].findLast(x => x > 5)+""`)
	if got != "undefined" {
		t.Errorf("findLast(not found) = %q, want undefined", got)
	}
	got = vmEvalStr(t, `[1,2,3].findLastIndex(x => x > 5)+""`)
	if got != "-1" {
		t.Errorf("findLastIndex(not found) = %q, want -1", got)
	}
}

func TestVMArrayAt(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`[1,2,3].at(0)+""`, "1"},
		{`[1,2,3].at(2)+""`, "3"},
		{`[1,2,3].at(-1)+""`, "3"},
		{`[1,2,3].at(-2)+""`, "2"},
		{`[1,2,3].at(5)+""`, "undefined"},
		{`[1,2,3].at(-5)+""`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Array 静态方法 ======================================================

func TestVMArrayFrom(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Array.from([1,2,3]).join(",")`, "1,2,3"},
		{`Array.from("abc").join(",")`, "a,b,c"},
		{`Array.from([1,2,3], x => x*2).join(",")`, "2,4,6"},
		{`Array.from({length: 3}).join(",")`, ",,"},
		{`Array.from({0:'a', 1:'b', length:2}).join(",")`, "a,b"},
		{`Array.from([1,2,3]).length`, "3"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMArrayOf(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Array.of(1,2,3).join(",")`, "1,2,3"},
		{`Array.of().length`, "0"},
		{`Array.of(5).length`, "1"},
		{`Array.of("a","b","c").join(",")`, "a,b,c"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}
