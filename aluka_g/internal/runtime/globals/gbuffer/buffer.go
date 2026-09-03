package gbuffer

// 全局 Buffer（Node.js Buffer API）与 node:buffer 模块。
//
// 架构（对应开发计划 2.4/2.18）：
//   - Buffer 值是 engine 层的 BufferValue（字节数据 + 数字索引 + 只读 length）。
//   - 本包构造 Buffer 构造器（含静态方法 from/alloc/allocUnsafe/byteLength/
//     isBuffer/concat/compare/isEncoding），实例方法以闭包捕获底层 []byte
//     的方式安装（绕过 engine.Func 无 this 绑定的限制）。
//   - 编码转换统一走 encodeBuffer/decodeBuffer（utf8/latin1/ascii/base64/
//     hex/utf16le）。
//   - NewBuffer 注册全局 Buffer；NewBufferModule 构造 node:buffer 模块导出
//     （复用同一实现，供 builtin/registry.go 注册）。

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
)

// BufferConfig 配置 Buffer 全局（当前无可用选项，保留类型以备扩展）。
type BufferConfig struct{}

// bufferProto 是 Buffer.prototype（Uint8Array 兼容）。实例以它作原型，
// 使 buf instanceof Buffer / Uint8Array 成立。
var bufferProto engine.Object

// bufferU8Ctor 是引擎 TypedArray 的 Uint8Array 构造器（存在时非 nil）。
// Buffer 构造器对 ArrayBuffer/TypedArray 入参委托给它的视图语义。
var bufferU8Ctor engine.Value

// NewBuffer 注册全局 Buffer 构造器。若引擎已注册 TypedArray 的 Uint8Array
// （interpreter setupTypedArrays），则保留它并把 Buffer 作为其子类
// （Buffer.prototype → Uint8Array.prototype，buf instanceof Uint8Array 成立）；
// 否则回退旧行为：Uint8Array 作为 Buffer 的别名。
func NewBuffer(ctx engine.Context, cfg BufferConfig) error {
	// 检测已注册的 TypedArray Uint8Array 构造器（有 BYTES_PER_ELEMENT 静态属性）。
	taU8 := engine.Undefined()
	if v, err := ctx.Global().Get("Uint8Array"); err == nil && v.IsFunction() {
		if o, ok := v.AsObject(); ok {
			if bpe, err := o.Get("BYTES_PER_ELEMENT"); err == nil && !bpe.IsUndefined() {
				taU8 = v
			}
		}
	}
	bufferU8Ctor = taU8

	ctor := newBufferExports()
	if err := ctx.Global().Set("Buffer", ctor); err != nil {
		return err
	}
	if !taU8.IsUndefined() {
		// TypedArray Uint8Array 保留为全局；Buffer.prototype 链接到其 prototype。
		if o, ok := taU8.AsObject(); ok {
			if up, err := o.Get("prototype"); err == nil && !up.IsUndefined() {
				if upObj, ok := up.AsObject(); ok {
					engine.SetProto(bufferProto, upObj)
				}
			}
		}
		return nil
	}
	// 旧路径：Buffer 与 Uint8Array 为同一构造器（Bun/Web API 语义）。
	return ctx.Global().Set("Uint8Array", ctor)
}

// NewBufferModule 构造 node:buffer 模块导出对象。
// Node 语义：node:buffer 的 Buffer 与全局 Buffer 是同一对象——若全局已注册
// （CLI 默认注册）则复用，否则新建。
func NewBufferModule(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	var buf engine.Value
	if v, err := ctx.Global().Get("Buffer"); err == nil && v.IsFunction() {
		buf = v
	} else {
		buf = newBufferExports()
	}
	_ = m.Set("Buffer", buf)
	_ = m.Set("SlowBuffer", buf) // 简化：同 Buffer（无慢速分配语义）
	_ = m.Set("kMaxLength", engine.IntValue(1<<30))
	_ = m.Set("constants", engine.NewObject())

	// buffer.isUtf8/isAscii（Node 22：仅在 node:buffer 模块导出，
	// Buffer 类上无此方法——实测）。
	_ = m.Set("isUtf8", engine.NewFunction("isUtf8", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		data, ok := bufferBytes(args[0])
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(utf8.Valid(data)), nil
	}))
	_ = m.Set("isAscii", engine.NewFunction("isAscii", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		data, ok := bufferBytes(args[0])
		if !ok {
			return engine.Boolean(false), nil
		}
		for _, b := range data {
			if b >= 0x80 {
				return engine.Boolean(false), nil
			}
		}
		return engine.Boolean(true), nil
	}))

	// transcode(source, fromEnc, toEnc)：在字符编码之间重编码字节序列。
	// 支持 utf8/utf16le/ucs2/latin1/binary/ascii/base64/hex。
	_ = m.Set("transcode", engine.NewFunction("transcode", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("transcode: source, fromEnc, toEnc required")
		}
		data, ok := bufferBytes(args[0])
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: transcode: source must be a Buffer or Uint8Array", engine.ErrTypeError)
		}
		fromEnc, ok1 := transcodeEncoding(args[1].String())
		toEnc, ok2 := transcodeEncoding(args[2].String())
		if !ok1 || !ok2 {
			return engine.Undefined(), fmt.Errorf("%w: transcode: unsupported encoding", engine.ErrTypeError)
		}
		units := transcodeUnits(data, fromEnc)
		return NewInstance(transcodeBytes(units, toEnc)), nil
	}))
	return m, nil
}

// transcodeEncoding 归一化 transcode 支持的编码名。
// Node 实测仅支持 utf8/utf16le/ucs2/latin1/binary/ascii（hex/base64 抛错）。
func transcodeEncoding(enc string) (string, bool) {
	switch strings.ToLower(enc) {
	case "utf8", "utf-8":
		return "utf8", true
	case "utf16le", "utf-16le", "ucs2", "ucs-2":
		return "utf16le", true
	case "latin1", "binary", "iso-8859-1":
		return "latin1", true
	case "ascii":
		return "ascii", true
	}
	return "", false
}

// transcodeUnits 把源字节按 fromEnc 解码为 UTF-16 码元序列。
func transcodeUnits(data []byte, fromEnc string) []uint16 {
	switch fromEnc {
	case "utf16le":
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:]))
		}
		return units
	case "latin1", "ascii":
		units := make([]uint16, len(data))
		for i, by := range data {
			units[i] = uint16(by)
		}
		return units
	default: // utf8
		s := string(data)
		var units []uint16
		for _, r := range s {
			if r <= 0xFFFF {
				units = append(units, uint16(r))
			} else {
				r -= 0x10000
				units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			}
		}
		return units
	}
}

// transcodeBytes 把 UTF-16 码元序列按 toEnc 编码为字节。
func transcodeBytes(units []uint16, toEnc string) []byte {
	switch toEnc {
	case "utf16le":
		out := make([]byte, len(units)*2)
		for i, u := range units {
			binary.LittleEndian.PutUint16(out[i*2:], u)
		}
		return out
	case "latin1", "ascii":
		out := make([]byte, len(units))
		for i, u := range units {
			out[i] = byte(u)
		}
		return out
	default: // utf8
		var sb strings.Builder
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				r := 0x10000 + (int(u-0xD800) << 10) + int(units[i+1]-0xDC00)
				sb.WriteRune(rune(r))
				i++
			} else {
				sb.WriteRune(rune(u))
			}
		}
		return []byte(sb.String())
	}
}

// bufferBytes 提取 Buffer/TypedArray/ArrayBuffer 的字节。
func bufferBytes(v engine.Value) ([]byte, bool) {
	if bv, ok := v.(*engine.BufferValue); ok {
		return bv.Bytes(), true
	}
	if data, ok := engine.AsArrayBuffer(v); ok {
		return data, true
	}
	if ta, ok := engine.AsTypedArray(v); ok {
		return ta.Bytes(), true
	}
	return nil, false
}

// newBufferExports 创建 Buffer 构造器（含静态方法与 prototype 占位）。
func newBufferExports() engine.Value {
	ctor := engine.NewFunction("Buffer", func(args []engine.Value) (engine.Value, error) {
		// Buffer() / Buffer(size) / Buffer(str[, encoding])
		if len(args) == 0 {
			return NewInstance(nil), nil
		}
		arg := args[0]
		// Buffer(arrayBuffer | typedArray) → 委托 TypedArray Uint8Array 视图。
		if f, ok := bufferU8Ctor.AsFunction(); ok {
			if _, ok := engine.AsArrayBuffer(arg); ok {
				return f.Call(args)
			}
			if _, ok := engine.AsTypedArray(arg); ok {
				return f.Call(args)
			}
		}
		if arg.Type() != engine.TypeBoolean && !arg.IsObject() && !arg.IsFunction() {
			if n, ok := arg.Int(); ok {
				if n < 0 {
					return engine.Undefined(), fmt.Errorf("%w: negative buffer size %d", engine.ErrRangeError, n)
				}
				return NewInstance(make([]byte, n)), nil
			}
		}
		return bufferFromArgs(args)
	})

	co, _ := ctor.AsObject()
	// prototype 占位（实例方法以 own property 安装，prototype 仅作标识/instanceof）。
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	_ = co.Set("prototype", proto)
	bufferProto = proto

	// --- 静态方法 ----------------------------------------------------------
	_ = co.Set("from", engine.NewFunction("from", bufferFromArgs))

	_ = co.Set("alloc", engine.NewFunction("alloc", func(args []engine.Value) (engine.Value, error) {
		size := gbase.ArgInt(args, 0, 0)
		if size < 0 {
			return engine.Undefined(), fmt.Errorf("%w: negative buffer size %d", engine.ErrRangeError, size)
		}
		d := make([]byte, size)
		if len(args) > 1 && !args[1].IsUndefined() && !args[1].IsNull() {
			fillBuf, err := fillBytes(args[1])
			if err != nil {
				return engine.Undefined(), err
			}
			// Node 语义：fill 字节循环重复填充。
			if len(fillBuf) > 0 {
				for i := range d {
					d[i] = fillBuf[i%len(fillBuf)]
				}
			}
		}
		return NewInstance(d), nil
	}))

	_ = co.Set("allocUnsafe", engine.NewFunction("allocUnsafe", func(args []engine.Value) (engine.Value, error) {
		size := gbase.ArgInt(args, 0, 0)
		if size < 0 {
			return engine.Undefined(), fmt.Errorf("%w: negative buffer size %d", engine.ErrRangeError, size)
		}
		return NewInstance(make([]byte, size)), nil
	}))

	if au, err := co.Get("allocUnsafe"); err == nil {
		_ = co.Set("allocUnsafeSlow", au) // Node 中为独立慢速分配，此处复用
	}
	_ = co.Set("byteLength", engine.NewFunction("byteLength", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		encoding := ""
		if len(args) > 1 {
			encoding = args[1].String()
		}
		return engine.IntValue(len(encodeBuffer(args[0].String(), encoding))), nil
	}))

	_ = co.Set("isBuffer", engine.NewFunction("isBuffer", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			_, ok := engine.AsBuffer(args[0])
			return engine.Boolean(ok), nil
		}
		return engine.Boolean(false), nil
	}))

	_ = co.Set("concat", engine.NewFunction("concat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return NewInstance(nil), nil
		}
		a, ok := args[0].(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Buffer.concat expects an array", engine.ErrTypeError)
		}
		elems := a.Elems()
		// 提取字节：Buffer 或 Uint8Array；其他类型（如 string）按 Node 语义
		// 抛 TypeError——静默跳过会掩盖"chunk 类型错误"类缺陷（如 raw-body
		// 收到 string chunk 时 body 静默为空）。
		chunks := make([][]byte, 0, len(elems))
		for _, e := range elems {
			var data []byte
			if b, ok := engine.AsBuffer(e); ok {
				data = b
			} else if t, ok := e.(*engine.TypedArrayValue); ok && t.Kind() == engine.KindUint8 {
				data = t.Bytes()
			} else {
				return engine.Undefined(), fmt.Errorf("%w: Buffer.concat argument must be an instance of Buffer or Uint8Array", engine.ErrTypeError)
			}
			chunks = append(chunks, data)
		}
		total := 0
		if len(args) > 1 && !args[1].IsUndefined() {
			total = gbase.ArgInt(args, 1, 0)
		} else {
			for _, c := range chunks {
				total += len(c)
			}
		}
		out := make([]byte, 0, total)
		for _, c := range chunks {
			out = append(out, c...)
		}
		if len(out) > total {
			out = out[:total]
		}
		return NewInstance(out), nil
	}))

	_ = co.Set("compare", engine.NewFunction("compare", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.IntValue(0), nil
		}
		a, aok := engine.AsBuffer(args[0])
		b, bok := engine.AsBuffer(args[1])
		if !aok || !bok {
			return engine.Undefined(), fmt.Errorf("%w: Buffer.compare expects buffers", engine.ErrTypeError)
		}
		return engine.IntValue(bytes.Compare(a, b)), nil
	}))

	_ = co.Set("isEncoding", engine.NewFunction("isEncoding", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		_, ok := normalizeEncoding(args[0].String())
		return engine.Boolean(ok), nil
	}))

	return ctor
}

// NewBufferInstance 创建带完整方法的 Buffer 实例。
// 供 builtin 包模块（zlib 等）构造返回给 JS 的 Buffer 值。
func NewBufferInstance(data []byte) engine.Value {
	return NewInstance(data)
}

// newBufferInstance 创建 Buffer 实例并安装实例方法（闭包捕获底层数据）。
func NewInstance(data []byte) engine.Object {
	buf := engine.NewBuffer(data)
	// 实例原型指向 Buffer.prototype（instanceof Buffer/Uint8Array 成立）。
	if bufferProto != nil {
		engine.SetProto(buf, bufferProto)
	}
	d := data

	// TypedArray 兼容：.buffer 返回底层 ArrayBuffer（以自身近似，
	// worker transferList 的 detach 经 AsBuffer 定位）。
	_ = buf.Set("buffer", buf)

	// toString([encoding[, start[, end]]])
	_ = buf.Set("toString", engine.NewFunction("toString", func(args []engine.Value) (engine.Value, error) {
		encoding := "utf8"
		if len(args) > 0 {
			encoding = args[0].String()
		}
		start, end := 0, len(d)
		if len(args) > 1 {
			start = gbase.ArgInt(args, 1, 0)
		}
		if len(args) > 2 {
			end = gbase.ArgInt(args, 2, len(d))
		}
		start = gbase.ClampIdx(start, 0, len(d))
		end = gbase.ClampIdx(end, start, len(d))
		return engine.Str(Decode(d[start:end], encoding)), nil
	}))

	// write(str[, offset[, length]][, encoding])
	// 参数可省略；第 3 参数若为字符串则是 encoding（Node 语义）。
	_ = buf.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		encoding := "utf8"
		offset := 0
		maxLen := len(d)
		for i := 1; i < len(args); i++ {
			switch args[i].Type() {
			case engine.TypeString:
				encoding = args[i].String()
			default:
				if i == 1 {
					offset = gbase.ArgInt(args, i, 0)
				} else {
					if requested := gbase.ArgInt(args, i, -1); requested >= 0 && requested < maxLen {
						maxLen = requested
					}
				}
			}
		}
		offset = gbase.ClampIdx(offset, 0, len(d))
		src := encodeBuffer(args[0].String(), encoding)
		avail := len(d) - offset
		if maxLen > avail {
			maxLen = avail
		}
		if maxLen > len(src) {
			maxLen = len(src)
		}
		copy(d[offset:offset+maxLen], src[:maxLen])
		return engine.IntValue(maxLen), nil
	}))

	// toJSON()
	_ = buf.Set("toJSON", engine.NewFunction("toJSON", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		_ = obj.Set("type", engine.Str("Buffer"))
		elems := make([]engine.Value, len(d))
		for i, b := range d {
			elems[i] = engine.IntValue(int(b))
		}
		_ = obj.Set("data", engine.NewArray(elems))
		return obj, nil
	}))

	// slice([start[, end]]) / subarray([start[, end]])：共享底层数据（Node 语义）。
	sliceFn := engine.NewFunction("slice", func(args []engine.Value) (engine.Value, error) {
		start, end := 0, len(d)
		if len(args) > 0 {
			start = gbase.ArgInt(args, 0, 0)
		}
		if len(args) > 1 {
			end = gbase.ArgInt(args, 1, len(d))
		}
		start = gbase.ClampIdx(start, 0, len(d))
		end = gbase.ClampIdx(end, start, len(d))
		return NewInstance(d[start:end]), nil
	})
	_ = buf.Set("slice", sliceFn)
	_ = buf.Set("subarray", sliceFn)

	// equals(other)
	_ = buf.Set("equals", engine.NewFunction("equals", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		other, ok := engine.AsBuffer(args[0])
		return engine.Boolean(ok && bytes.Equal(d, other)), nil
	}))

	// copy(target[, targetStart[, sourceStart[, sourceEnd]]])
	_ = buf.Set("copy", engine.NewFunction("copy", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		target, ok := engine.AsBuffer(args[0])
		if !ok {
			return engine.IntValue(0), nil
		}
		targetStart := gbase.ArgInt(args, 1, 0)
		sourceStart := gbase.ArgInt(args, 2, 0)
		sourceEnd := gbase.ArgInt(args, 3, len(d))
		sourceStart = gbase.ClampIdx(sourceStart, 0, len(d))
		sourceEnd = gbase.ClampIdx(sourceEnd, sourceStart, len(d))
		src := d[sourceStart:sourceEnd]
		n := copy(target[targetStart:], src)
		return engine.IntValue(n), nil
	}))

	// fill(value[, offset[, end]])
	_ = buf.Set("fill", engine.NewFunction("fill", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return buf, nil
		}
		fill, err := fillBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		start, end := 0, len(d)
		if len(args) > 1 {
			start = gbase.ArgInt(args, 1, 0)
		}
		if len(args) > 2 {
			end = gbase.ArgInt(args, 2, len(d))
		}
		start = gbase.ClampIdx(start, 0, len(d))
		end = gbase.ClampIdx(end, start, len(d))
		if len(fill) == 0 {
			return buf, nil
		}
		for i := start; i < end; i++ {
			d[i] = fill[(i-start)%len(fill)]
		}
		return buf, nil
	}))

	// indexOf / includes（字节子序列搜索，value 可为数字/字符串/Buffer）
	_ = buf.Set("indexOf", engine.NewFunction("indexOf", func(args []engine.Value) (engine.Value, error) {
		idx, _ := bufferSearch(d, args)
		return engine.IntValue(idx), nil
	}))
	_ = buf.Set("includes", engine.NewFunction("includes", func(args []engine.Value) (engine.Value, error) {
		idx, _ := bufferSearch(d, args)
		return engine.Boolean(idx >= 0), nil
	}))

	// --- 读方法（readUInt8/readUInt16LE/BE/readUInt32LE/BE/readInt*/Float/Double）
	installBufferReaders(buf, d)
	// --- 写方法（writeUInt8/writeUInt16LE/BE/writeUInt32LE/BE/writeInt*/Float/Double）
	installBufferWriters(buf, d)

	return buf
}

// bufferFromArgs 实现 Buffer.from(value[, encoding])。
func bufferFromArgs(args []engine.Value) (engine.Value, error) {
	if len(args) == 0 {
		return NewInstance(nil), nil
	}
	val := args[0]
	encoding := ""
	if len(args) > 1 {
		encoding = args[1].String()
	}
	switch {
	case val.Type() == engine.TypeString:
		return NewInstance(encodeBuffer(val.String(), encoding)), nil
	case val.IsObject() && !val.IsFunction():
		if b, ok := engine.AsBuffer(val); ok {
			d := make([]byte, len(b))
			copy(d, b)
			return NewInstance(d), nil
		}
		if a, ok := val.(*engine.ArrayValue); ok {
			elems := a.Elems()
			d := make([]byte, len(elems))
			for i, e := range elems {
				if n, ok := e.Int(); ok {
					d[i] = byte(n)
				}
			}
			return NewInstance(d), nil
		}
		// TypedArray：逐元素拷贝（Node 语义：Buffer.from(typedArray) 拷贝元素值）。
		if ta, ok := val.(*engine.TypedArrayValue); ok {
			n := ta.Length()
			d := make([]byte, n)
			for i := 0; i < n; i++ {
				if v, ok := ta.ElementAt(i).Int(); ok {
					d[i] = byte(v)
				}
			}
			return NewInstance(d), nil
		}
	}
	return NewInstance(nil), nil
}

// fillBytes 把 fill 参数（数字/字符串/Buffer）转成填充字节。
func fillBytes(v engine.Value) ([]byte, error) {
	switch {
	case v.Type() == engine.TypeString:
		return encodeBuffer(v.String(), "utf8"), nil
	case v.IsObject():
		if b, ok := engine.AsBuffer(v); ok {
			return b, nil
		}
	}
	if n, ok := v.Int(); ok {
		return []byte{byte(n)}, nil
	}
	return nil, nil
}

// bufferSearch 在 data 中查找 value 的字节位置（返回索引；notFound=-1）。
// value 可为数字（单字节）/字符串（按编码解码）/Buffer。
func bufferSearch(data []byte, args []engine.Value) (int, error) {
	if len(args) == 0 {
		return -1, nil
	}
	var needle []byte
	switch {
	case args[0].Type() == engine.TypeString:
		enc := "utf8"
		if len(args) > 2 {
			enc = args[2].String()
		}
		needle = encodeBuffer(args[0].String(), enc)
	case args[0].IsObject():
		if b, ok := engine.AsBuffer(args[0]); ok {
			needle = b
		} else {
			return -1, nil
		}
	default:
		if n, ok := args[0].Int(); ok {
			needle = []byte{byte(n)}
		} else {
			return -1, nil
		}
	}
	start := gbase.ArgInt(args, 1, 0)
	start = gbase.ClampIdx(start, 0, len(data))
	if len(needle) == 0 {
		return start, nil
	}
	return bytesIndex(data, needle, start), nil
}

// bytesIndex 从 start 起查找 needle（bytes.Index 的带起点版本）。
func bytesIndex(data, needle []byte, start int) int {
	if start >= len(data) {
		return -1
	}
	idx := bytes.Index(data[start:], needle)
	if idx < 0 {
		return -1
	}
	return start + idx
}

// installBufferReaders 安装所有 read 方法。
func installBufferReaders(buf engine.Object, d []byte) {
	// 无符号整型读。
	_ = buf.Set("readUInt8", engine.NewFunction("readUInt8", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 1)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(d[off])), nil
	}))
	_ = buf.Set("readUInt16LE", engine.NewFunction("readUInt16LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 2)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(binary.LittleEndian.Uint16(d[off:]))), nil
	}))
	_ = buf.Set("readUInt16BE", engine.NewFunction("readUInt16BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 2)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(binary.BigEndian.Uint16(d[off:]))), nil
	}))
	_ = buf.Set("readUInt32LE", engine.NewFunction("readUInt32LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(binary.LittleEndian.Uint32(d[off:]))), nil
	}))
	_ = buf.Set("readUInt32BE", engine.NewFunction("readUInt32BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(binary.BigEndian.Uint32(d[off:]))), nil
	}))
	// 有符号整型读。
	_ = buf.Set("readInt8", engine.NewFunction("readInt8", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 1)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(int8(d[off]))), nil
	}))
	_ = buf.Set("readInt16LE", engine.NewFunction("readInt16LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 2)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(int16(binary.LittleEndian.Uint16(d[off:])))), nil
	}))
	_ = buf.Set("readInt16BE", engine.NewFunction("readInt16BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 2)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(int16(binary.BigEndian.Uint16(d[off:])))), nil
	}))
	_ = buf.Set("readInt32LE", engine.NewFunction("readInt32LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(int32(binary.LittleEndian.Uint32(d[off:])))), nil
	}))
	_ = buf.Set("readInt32BE", engine.NewFunction("readInt32BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(int(int32(binary.BigEndian.Uint32(d[off:])))), nil
	}))
	// 浮点读。
	_ = buf.Set("readFloatLE", engine.NewFunction("readFloatLE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(float64(math.Float32frombits(binary.LittleEndian.Uint32(d[off:])))), nil
	}))
	_ = buf.Set("readFloatBE", engine.NewFunction("readFloatBE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 4)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(float64(math.Float32frombits(binary.BigEndian.Uint32(d[off:])))), nil
	}))
	_ = buf.Set("readDoubleLE", engine.NewFunction("readDoubleLE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(math.Float64frombits(binary.LittleEndian.Uint64(d[off:]))), nil
	}))
	_ = buf.Set("readDoubleBE", engine.NewFunction("readDoubleBE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(math.Float64frombits(binary.BigEndian.Uint64(d[off:]))), nil
	}))
	// BigInt 读（readBigUInt64LE/BE、readBigInt64LE/BE，Node 12+）。
	_ = buf.Set("readBigUInt64LE", engine.NewFunction("readBigUInt64LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.BigInt(new(big.Int).SetUint64(binary.LittleEndian.Uint64(d[off:]))), nil
	}))
	_ = buf.Set("readBigUInt64BE", engine.NewFunction("readBigUInt64BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.BigInt(new(big.Int).SetUint64(binary.BigEndian.Uint64(d[off:]))), nil
	}))
	_ = buf.Set("readBigInt64LE", engine.NewFunction("readBigInt64LE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		// 有符号：按 int64 位型解释（高位置位时为负）。
		return engine.BigInt(big.NewInt(int64(binary.LittleEndian.Uint64(d[off:])))), nil
	}))
	_ = buf.Set("readBigInt64BE", engine.NewFunction("readBigInt64BE", func(args []engine.Value) (engine.Value, error) {
		off, err := readOffset(d, args, 8)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.BigInt(big.NewInt(int64(binary.BigEndian.Uint64(d[off:])))), nil
	}))
	// swap16/swap32/swap64：原地字节序交换，返回 Buffer 自身（链式）。
	_ = buf.Set("swap16", engine.NewFunction("swap16", func(args []engine.Value) (engine.Value, error) {
		for i := 0; i+1 < len(d); i += 2 {
			d[i], d[i+1] = d[i+1], d[i]
		}
		return buf, nil
	}))
	_ = buf.Set("swap32", engine.NewFunction("swap32", func(args []engine.Value) (engine.Value, error) {
		for i := 0; i+3 < len(d); i += 4 {
			d[i], d[i+3] = d[i+3], d[i]
			d[i+1], d[i+2] = d[i+2], d[i+1]
		}
		return buf, nil
	}))
	_ = buf.Set("swap64", engine.NewFunction("swap64", func(args []engine.Value) (engine.Value, error) {
		for i := 0; i+7 < len(d); i += 8 {
			for j := 0; j < 4; j++ {
				d[i+j], d[i+7-j] = d[i+7-j], d[i+j]
			}
		}
		return buf, nil
	}))
}

// installBufferWriters 安装所有 write 方法（返回写入后 offset）。
func installBufferWriters(buf engine.Object, d []byte) {
	writeUInt := func(offArg int, size int, be bool, signed bool) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), fmt.Errorf("%w: missing value", engine.ErrTypeError)
			}
			off := gbase.ArgInt(args, 1, 0)
			if off < 0 || off+size > len(d) {
				return engine.Undefined(), fmt.Errorf("%w: write beyond buffer length", engine.ErrRangeError)
			}
			var u uint64
			if signed {
				iv, _ := args[0].Int()
				u = uint64(iv)
			} else {
				if n, ok := args[0].Int(); ok {
					u = uint64(n)
				} else {
					return engine.Undefined(), fmt.Errorf("%w: expected integer", engine.ErrTypeError)
				}
			}
			switch size {
			case 1:
				d[off] = byte(u)
			case 2:
				if be {
					binary.BigEndian.PutUint16(d[off:], uint16(u))
				} else {
					binary.LittleEndian.PutUint16(d[off:], uint16(u))
				}
			case 4:
				if be {
					binary.BigEndian.PutUint32(d[off:], uint32(u))
				} else {
					binary.LittleEndian.PutUint32(d[off:], uint32(u))
				}
			}
			return engine.IntValue(off + size), nil
		}
	}
	_ = buf.Set("writeUInt8", engine.NewFunction("writeUInt8", writeUInt(1, 1, false, false)))
	_ = buf.Set("writeUInt16LE", engine.NewFunction("writeUInt16LE", writeUInt(2, 2, false, false)))
	_ = buf.Set("writeUInt16BE", engine.NewFunction("writeUInt16BE", writeUInt(2, 2, true, false)))
	_ = buf.Set("writeUInt32LE", engine.NewFunction("writeUInt32LE", writeUInt(4, 4, false, false)))
	_ = buf.Set("writeUInt32BE", engine.NewFunction("writeUInt32BE", writeUInt(4, 4, true, false)))
	_ = buf.Set("writeInt8", engine.NewFunction("writeInt8", writeUInt(1, 1, false, true)))
	_ = buf.Set("writeInt16LE", engine.NewFunction("writeInt16LE", writeUInt(2, 2, false, true)))
	_ = buf.Set("writeInt16BE", engine.NewFunction("writeInt16BE", writeUInt(2, 2, true, true)))
	_ = buf.Set("writeInt32LE", engine.NewFunction("writeInt32LE", writeUInt(4, 4, false, true)))
	_ = buf.Set("writeInt32BE", engine.NewFunction("writeInt32BE", writeUInt(4, 4, true, true)))

	writeFloat := func(size int, be bool) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), fmt.Errorf("%w: missing value", engine.ErrTypeError)
			}
			off := gbase.ArgInt(args, 1, 0)
			if off < 0 || off+size > len(d) {
				return engine.Undefined(), fmt.Errorf("%w: write beyond buffer length", engine.ErrRangeError)
			}
			f, _ := args[0].Float()
			if size == 4 {
				if be {
					binary.BigEndian.PutUint32(d[off:], math.Float32bits(float32(f)))
				} else {
					binary.LittleEndian.PutUint32(d[off:], math.Float32bits(float32(f)))
				}
			} else {
				if be {
					binary.BigEndian.PutUint64(d[off:], math.Float64bits(f))
				} else {
					binary.LittleEndian.PutUint64(d[off:], math.Float64bits(f))
				}
			}
			return engine.IntValue(off + size), nil
		}
	}
	_ = buf.Set("writeFloatLE", engine.NewFunction("writeFloatLE", writeFloat(4, false)))
	_ = buf.Set("writeFloatBE", engine.NewFunction("writeFloatBE", writeFloat(4, true)))
	_ = buf.Set("writeDoubleLE", engine.NewFunction("writeDoubleLE", writeFloat(8, false)))
	_ = buf.Set("writeDoubleBE", engine.NewFunction("writeDoubleBE", writeFloat(8, true)))

	// BigInt 写（writeBigUInt64LE/BE、writeBigInt64LE/BE，Node 12+）。
	writeBig := func(be bool, signed bool) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), fmt.Errorf("%w: missing value", engine.ErrTypeError)
			}
			off := gbase.ArgInt(args, 1, 0)
			if off < 0 || off+8 > len(d) {
				return engine.Undefined(), fmt.Errorf("%w: write beyond buffer length", engine.ErrRangeError)
			}
			bi, ok := engine.BigIntValue(args[0])
			if !ok {
				// Node 对 Number 也接受（校验整数范围），简化：转 int64。
				if n, ok2 := args[0].Int(); ok2 {
					bi = big.NewInt(int64(n))
					ok = true
				}
			}
			if !ok {
				return engine.Undefined(), fmt.Errorf("%w: expected BigInt", engine.ErrTypeError)
			}
			u := bi.Uint64()
			if signed {
				u = uint64(bi.Int64())
			}
			if be {
				binary.BigEndian.PutUint64(d[off:], u)
			} else {
				binary.LittleEndian.PutUint64(d[off:], u)
			}
			return engine.IntValue(off + 8), nil
		}
	}
	_ = buf.Set("writeBigUInt64LE", engine.NewFunction("writeBigUInt64LE", writeBig(false, false)))
	_ = buf.Set("writeBigUInt64BE", engine.NewFunction("writeBigUInt64BE", writeBig(true, false)))
	_ = buf.Set("writeBigInt64LE", engine.NewFunction("writeBigInt64LE", writeBig(false, true)))
	_ = buf.Set("writeBigInt64BE", engine.NewFunction("writeBigInt64BE", writeBig(true, true)))
}

// readOffset 校验并返回读操作偏移（保证 off+size 在界内）。
func readOffset(d []byte, args []engine.Value, size int) (int, error) {
	off := gbase.ArgInt(args, 0, 0)
	if off < 0 || off+size > len(d) {
		return 0, fmt.Errorf("%w: read beyond buffer length", engine.ErrRangeError)
	}
	return off, nil
}

// --- 编码转换 --------------------------------------------------------------

// normalizeEncoding 归一化编码名并返回规范形式。
func normalizeEncoding(enc string) (string, bool) {
	switch strings.ToLower(enc) {
	case "", "utf8", "utf-8":
		return "utf8", true
	case "latin1", "binary", "iso-8859-1":
		return "latin1", true
	case "ascii":
		return "ascii", true
	case "base64", "base64url":
		return "base64", true
	case "hex":
		return "hex", true
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		return "utf16le", true
	}
	return "", false
}

// encodeBuffer 将字符串按编码转为字节。
func encodeBuffer(s, encoding string) []byte {
	enc, ok := normalizeEncoding(encoding)
	if !ok {
		enc = "utf8"
	}
	switch enc {
	case "latin1":
		out := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			out[i] = s[i]
		}
		return out
	case "ascii":
		out := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			out[i] = s[i] & 0x7F
		}
		return out
	case "hex":
		b, _ := hex.DecodeString(s)
		return b
	case "base64":
		b, _ := DecodeBase64(s)
		return b
	case "utf16le":
		units := utf16.Encode([]rune(s))
		out := make([]byte, len(units)*2)
		for i, u := range units {
			binary.LittleEndian.PutUint16(out[i*2:], u)
		}
		return out
	default: // utf8
		return []byte(s)
	}
}

// decodeBuffer 将字节按编码转为字符串。
func Decode(data []byte, encoding string) string {
	enc, ok := normalizeEncoding(encoding)
	if !ok {
		enc = "utf8"
	}
	switch enc {
	case "latin1":
		// latin1 每个字节对应一个 Unicode 码点（0-255）。
		runes := make([]rune, len(data))
		for i, by := range data {
			runes[i] = rune(by)
		}
		return string(runes)
	case "ascii":
		var b strings.Builder
		for _, by := range data {
			b.WriteByte(by & 0x7F)
		}
		return b.String()
	case "hex":
		return hex.EncodeToString(data)
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	case "utf16le":
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:]))
		}
		return string(utf16.Decode(units))
	default: // utf8
		if utf8.Valid(data) {
			return string(data)
		}
		// 非法序列：替换为 U+FFFD（TextDecoder 默认行为）。
		return strings.ToValidUTF8(string(data), "\uFFFD")
	}
}

// decodeBase64 解码 base64（容错无 padding 输入）。
func DecodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	padded := s
	for len(padded)%4 != 0 {
		padded += "="
	}
	return base64.StdEncoding.DecodeString(padded)
}

// --- 辅助 ------------------------------------------------------------------
