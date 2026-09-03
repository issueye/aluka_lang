package engine

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/big"
)

// 本文件实现字节码常量池（FuncTemplate.Constants []engine.Value）的序列化/
// 反序列化，供字节码磁盘缓存（1C.14）使用。
//
// 常量池只可能包含三种类型（由 compiler 的 AddConst/AddStringCond 注入）：
//   - numberValue (float64)
//   - bigIntValue (*big.Int)
//   - stringValue (string)
// 编码格式：1 字节类型标签 + 类型特定载荷。

const (
	constTagNumber = 1
	constTagString = 2
	constTagBigInt = 3
	constTagBool   = 4
	constTagNull   = 5
)

// EncodeConst 将单个常量值编码到 w。支持 number/string/bigint/bool/null
// （NativeCallback 的 cbConstValue 会把 bool/null 字面量加入常量池）。
func EncodeConst(w io.Writer, v Value) error {
	switch v.Type() {
	case TypeNumber:
		if _, err := w.Write([]byte{constTagNumber}); err != nil {
			return err
		}
		f, _ := v.Float()
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(f))
		_, err := w.Write(buf[:])
		return err
	case TypeString:
		if _, err := w.Write([]byte{constTagString}); err != nil {
			return err
		}
		return writeLenString(w, v.String())
	case TypeBigInt:
		if _, err := w.Write([]byte{constTagBigInt}); err != nil {
			return err
		}
		bi, _ := BigIntValue(v)
		// *big.Int 用 TextMarshaler/Unmarshaler（十进制字符串）。
		return writeLenString(w, bi.String())
	case TypeBoolean:
		if _, err := w.Write([]byte{constTagBool}); err != nil {
			return err
		}
		b, _ := v.Bool()
		if b {
			_, err := w.Write([]byte{1})
			return err
		}
		_, err := w.Write([]byte{0})
		return err
	case TypeNull:
		_, err := w.Write([]byte{constTagNull})
		return err
	default:
		return fmt.Errorf("const codec: unsupported constant type %s value=%q goType=%T", v.Type(), v.String(), v)
	}
}

// DecodeConst 从 r 读取并解码单个常量值。
func DecodeConst(r io.Reader) (Value, error) {
	var tag [1]byte
	if _, err := io.ReadFull(r, tag[:]); err != nil {
		return nil, err
	}
	switch tag[0] {
	case constTagNumber:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		return Number(math.Float64frombits(binary.LittleEndian.Uint64(buf[:]))), nil
	case constTagString:
		s, err := readLenString(r)
		if err != nil {
			return nil, err
		}
		return Str(s), nil
	case constTagBigInt:
		s, err := readLenString(r)
		if err != nil {
			return nil, err
		}
		bi, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("const codec: invalid big int %q", s)
		}
		return BigInt(bi), nil
	case constTagBool:
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		return Boolean(b[0] != 0), nil
	case constTagNull:
		return Null(), nil
	default:
		return nil, fmt.Errorf("const codec: unknown tag %d", tag[0])
	}
}

// writeLenString 写入 uvarint 长度前缀 + UTF-8 字节。
func writeLenString(w io.Writer, s string) error {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(s)))
	if _, err := w.Write(lenBuf[:n]); err != nil {
		return err
	}
	if len(s) > 0 {
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
	}
	return nil
}

// readLenString 读取 uvarint 长度前缀 + UTF-8 字节。
func readLenString(r io.Reader) (string, error) {
	n, err := binary.ReadUvarint(byteReaderOf(r))
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// byteReaderOf 将 io.Reader 包装为 io.ByteReader（binary.ReadUvarint 需要）。
type byteReader struct{ r io.Reader }

func (b byteReader) ReadByte() (byte, error) {
	var buf [1]byte
	if _, err := io.ReadFull(b.r, buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func byteReaderOf(r io.Reader) io.ByteReader { return byteReader{r: r} }
