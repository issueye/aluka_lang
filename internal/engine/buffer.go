package engine

// BufferValue 是 JS Buffer 实例：字节数据 + 对象属性（Node.js Buffer/Uint8Array 语义）。
//
// 嵌入 *objectValue 获得通用对象能力（属性表、原型链），重写 Get/Set/Keys
// 以支持：
//   - 数字索引访问：buf[0] / buf[1] = 65（直接读写底层字节）
//   - 只读 length：buf.length 返回字节数，赋值静默忽略（Node 语义）
//
// 实例方法（toString/write/readUInt* 等）由调用方（globals 包）安装，
// 通过闭包捕获底层 []byte，不依赖 JS this 绑定。

import (
	"fmt"
	"strconv"
)

// BufferValue 是 Buffer 值的对象实现。
type BufferValue struct {
	*objectValue
	data []byte
}

// NewBuffer 创建包装给定字节数据的 Buffer 值。
// 返回 Object 接口，调用方可继续 Set 实例方法。
func NewBuffer(data []byte) Object {
	b := &BufferValue{
		objectValue: &objectValue{shape: rootShape},
		data:        data,
	}
	register(b.objectValue)
	return b
}

// Bytes 返回底层字节切片（只读视图，调用方不应修改）。
func (b *BufferValue) Bytes() []byte { return b.data }

// AsBuffer 从 Value 中提取 Buffer 的底层字节，非 Buffer 时返回 false。
func AsBuffer(v Value) ([]byte, bool) {
	b, ok := v.(*BufferValue)
	if !ok {
		return nil, false
	}
	return b.data, true
}

func (b *BufferValue) Type() ValueType { return TypeObject }

// String 返回 utf8 解码后的内容（与 buf.toString() 默认行为一致，
// 也支持 '' + buf 拼接）。
func (b *BufferValue) String() string { return string(b.data) }

// AsObject 返回 Buffer 自身——数字索引/只读 length 需走重写的 Get/Set，
// 不能回退到嵌入的 *objectValue。
func (b *BufferValue) AsObject() (Object, bool) { return b, true }

// Get 重写：length 与数字索引直接读底层字节。
func (b *BufferValue) Get(key string) (Value, error) {
	if key == "length" {
		return IntValue(len(b.data)), nil
	}
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 && idx < len(b.data) {
		return IntValue(int(b.data[idx])), nil
	}
	return b.objectValue.Get(key)
}

// Set 重写：数字索引写底层字节，length 只读（静默忽略）。
func (b *BufferValue) Set(key string, value Value) error {
	if key == "length" {
		return nil // 只读属性，赋值忽略
	}
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 && idx < len(b.data) {
		n, ok := value.Int()
		if !ok {
			return fmt.Errorf("%w: buffer index value must be a number", ErrTypeError)
		}
		b.data[idx] = byte(n)
		return nil
	}
	return b.objectValue.Set(key, value)
}

// Keys 重写：数字索引 + length（与 Buffer 可枚举属性一致）。
func (b *BufferValue) Keys() []string {
	out := make([]string, 0, len(b.data)+1)
	for i := range b.data {
		out = append(out, strconv.Itoa(i))
	}
	out = append(out, "length")
	return out
}
