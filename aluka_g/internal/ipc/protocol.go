package ipc

// Package ipc 实现了 Aluka 原生 IPC 通讯协议 (Aluka IPC Protocol - AIP)。
// 纯 Go 实现，无 CGO，支持 Windows 命名管道与 Unix 域套接字。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// AIP 协议魔数与版本常量。
const (
	MagicNumber  uint32 = 0x414C4B01 // ASCII "ALK\x01"
	ProtoVersion uint8  = 0x01
	HeaderLength int    = 16 // 16 字节固定帧头
)

// AIP 帧标志位 (Flags)。
const (
	FlagCompressed uint8 = 0x01 // 载荷已压缩 (Gzip)
	FlagEncrypted  uint8 = 0x02 // 载荷已加密 (AES-GCM)
	FlagBinaryRaw  uint8 = 0x04 // 载荷为原始二进制流
	FlagStreamEnd  uint8 = 0x08 // 流式分片结束帧
)

// AIP 消息操作码 (Message Type Opcode)。
const (
	OpPing         uint16 = 0x0001 // 心跳 Ping
	OpPong         uint16 = 0x0002 // 心跳 Pong
	OpRPCRequest   uint16 = 0x0010 // RPC 方法调用请求
	OpRPCResponse  uint16 = 0x0011 // RPC 方法调用成功响应
	OpRPCError     uint16 = 0x0012 // RPC 方法调用异常响应
	OpEventEmit    uint16 = 0x0020 // 单向事件广播 (Pub/Sub)
	OpStreamChunk  uint16 = 0x0030 // 流式大数据分片
	OpDisconnect   uint16 = 0x00FF // 优雅断开通知
)

// Header AIP 16 字节定长帧头。
type Header struct {
	Magic      uint32 // 魔数 (0x414C4B01)
	Version    uint8  // 协议版本 (0x01)
	Flags      uint8  // 标志位
	MsgType    uint16 // 操作码
	SequenceID uint32 // 请求序号
	PayloadLen uint32 // 载荷长度
}

// Frame 完整的 AIP 数据帧。
type Frame struct {
	Header  Header
	Payload []byte
}

// EncodeHeader 将 Header 编码为 16 字节大端序切片。
func EncodeHeader(h Header) []byte {
	buf := make([]byte, HeaderLength)
	binary.BigEndian.PutUint32(buf[0:4], h.Magic)
	buf[4] = h.Version
	buf[5] = h.Flags
	binary.BigEndian.PutUint16(buf[6:8], h.MsgType)
	binary.BigEndian.PutUint32(buf[8:12], h.SequenceID)
	binary.BigEndian.PutUint32(buf[12:16], h.PayloadLen)
	return buf
}

// DecodeHeader 从 16 字节切片解码 Header。
func DecodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderLength {
		return Header{}, errors.New("aip: header buffer too short")
	}
	magic := binary.BigEndian.Uint32(buf[0:4])
	if magic != MagicNumber {
		return Header{}, fmt.Errorf("aip: invalid magic number 0x%08X (expected 0x%08X)", magic, MagicNumber)
	}
	return Header{
		Magic:      magic,
		Version:    buf[4],
		Flags:      buf[5],
		MsgType:    binary.BigEndian.Uint16(buf[6:8]),
		SequenceID: binary.BigEndian.Uint32(buf[8:12]),
		PayloadLen: binary.BigEndian.Uint32(buf[12:16]),
	}, nil
}

// ReadFrame 从 io.Reader 中读取一个完整的 AIP 数据帧。
func ReadFrame(r io.Reader) (*Frame, error) {
	hdrBuf := make([]byte, HeaderLength)
	if _, err := io.ReadFull(r, hdrBuf); err != nil {
		return nil, err
	}
	hdr, err := DecodeHeader(hdrBuf)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, hdr.PayloadLen)
	if hdr.PayloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("aip: failed to read payload (%d bytes): %w", hdr.PayloadLen, err)
		}
	}

	return &Frame{
		Header:  hdr,
		Payload: payload,
	}, nil
}

// WriteFrame 向 io.Writer 写入一个完整的 AIP 数据帧（单次原子写入）。
func WriteFrame(w io.Writer, f *Frame) error {
	f.Header.Magic = MagicNumber
	f.Header.Version = ProtoVersion
	f.Header.PayloadLen = uint32(len(f.Payload))

	hdrBuf := EncodeHeader(f.Header)
	totalBuf := make([]byte, HeaderLength+len(f.Payload))
	copy(totalBuf[0:HeaderLength], hdrBuf)
	if len(f.Payload) > 0 {
		copy(totalBuf[HeaderLength:], f.Payload)
	}
	_, err := w.Write(totalBuf)
	return err
}
