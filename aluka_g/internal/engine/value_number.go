// Number 值：slab 分配的 numberValue 表示、构造入口与 JS 数字格式化。

package engine

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
)

// numberBox 是数字的不可变存储单元（slab 分配，见 newNumber）。
type numberBox struct{ V float64 }

// numberValue 是 JS Number 的表示：单指针字结构体（pointer-shaped），
// 装入 interface 直接存进数据字、零分配——值类型 float64 每次转换都会
// convT64 堆分配，Tier 0 算术/比较/计数热路径的分配大头由此消除。
// 代价：interface 相等比较变为指针比较（同值双 box 不等）。JS 语义的
// 相等（==/===）经 equality.go 走 Float() 数值比较，不受影响；仓库内
// 已审计无 map 键与直接 == 比较依赖数值相等（structuredClone 的 seen
// 仅对象键、domain forwarders 仅事件对象键）。
type numberValue struct{ b *numberBox }

// 数字 slab：64KB 块内原子 bump 分配（~几 ns），耗尽后加锁换新块。
// 旧块由存活的 box 指针保活（含少数存活数字时最多浪费一个块）。
const numSlabBoxes = 8192 // 8192 × 8B = 64KB

var (
	numSlabMu  sync.Mutex
	numSlabPtr atomic.Pointer[[]numberBox]
	numSlabIdx atomic.Int64
)

// newNumber 从当前 slab 原子 bump 分配一个数字单元。
func newNumber(f float64) numberValue {
	for {
		sp := numSlabPtr.Load()
		if sp != nil {
			i := numSlabIdx.Add(1)
			if int(i) <= len(*sp) {
				box := &(*sp)[i-1]
				box.V = f
				return numberValue{b: box}
			}
		}
		numSlabMu.Lock()
		if sp := numSlabPtr.Load(); sp == nil || numSlabIdx.Load() >= int64(len(*sp)) {
			fresh := make([]numberBox, numSlabBoxes)
			numSlabPtr.Store(&fresh)
			numSlabIdx.Store(0)
		}
		numSlabMu.Unlock()
	}
}

// NumberBox 是数字存储单元的导出别名：VM 私有数字 slab（单线程免原子）
// 直接填充单元后经 NumberFromBox 构造 Value。
type NumberBox = numberBox

// NumberFromBox 以调用方已填充的单元构造 Number Value
// （VM 私有 slab 快路径；单元一经发布不可变）。
func NumberFromBox(b *NumberBox) Value { return numberValue{b: b} }

// Number 包装 Go float64 为 JS Value。
// JS 中所有数字都是 float64（除 BigInt），故统一用 float64 表示。
func Number(n float64) Value { return newNumber(n) }

// IntValue 包装 Go int 为 JS Value。
func IntValue(n int) Value { return newNumber(float64(n)) }

func (n numberValue) Type() ValueType { return TypeNumber }

func (n numberValue) String() string { return formatNumber(n.b.V) }

func (n numberValue) Int() (int, bool) { return int(n.b.V), true }

func (n numberValue) Float() (float64, bool) { return n.b.V, true }

func (n numberValue) Bool() (bool, bool) { return n.b.V != 0 && !math.IsNaN(n.b.V), true }

func (n numberValue) IsUndefined() bool { return false }

func (n numberValue) IsNull() bool { return false }

func (n numberValue) IsObject() bool { return false }

func (n numberValue) IsFunction() bool { return false }

func (n numberValue) AsObject() (Object, bool) { return nil, false }

func (n numberValue) AsFunction() (Function, bool) { return nil, false }

// formatNumber 按 JS Number.prototype.toString 规则格式化。
func formatNumber(n float64) string {
	// JS 特殊值（Infinity / -Infinity / NaN）。
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	if math.IsNaN(n) {
		return "NaN"
	}
	// 整数：去掉小数点
	if n == float64(int64(n)) && n >= -9007199254740991 && n <= 9007199254740991 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}
