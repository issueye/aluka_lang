package bytecode

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件实现 bytecode.Module 的二进制序列化/反序列化，供磁盘缓存（1C.14）使用。
// 格式版本号变更时，旧缓存自动失效。

// FormatVersion 是字节码缓存格式版本。当 Module 布局、常量编码或编译器
// 语义变化（如函数声明提升修复）时递增，使旧缓存自动失效。
// v3：新增 OpMakeRegexp 指令（RegExp 引擎）；switch break 目标、前缀自增、
//
//	标签 continue/break、正则字面量等编译器语义修复。
//
// v4 → v5：新增 FuncTemplate.ArgumentsSlot（实现函数级 `arguments` 对象）。
// v5 → v6：新增解构参数（ParamPatterns）编译语义。
// v9 → v10：新增 OpGetPropLocal superinstruction（O2-D1）。
// v10 → v11：OpNewObject operand 携带批量对象字面量属性数量（ME-2）。
// v11 → v12：FuncTemplate.NoArgumentsObject（O-5 调用快速路径：函数体未引用
// arguments 时跳过每帧 arguments 对象创建）。
const FormatVersion = 12

// Magic header 用于快速识别缓存文件。
var cacheMagic = []byte("ALUKABC1")

// Serialize 将 Module 编码到 w。格式：
//
//	magic(8) | version(u32) | funcCount(u32) | classCount(u32) | funcs... | classes...
func Serialize(w io.Writer, mod *Module) error {
	if _, err := w.Write(cacheMagic); err != nil {
		return err
	}
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], FormatVersion)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(mod.Functions)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(mod.Classes)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for _, fn := range mod.Functions {
		if err := serializeFuncTemplate(w, fn); err != nil {
			return err
		}
	}
	for _, cls := range mod.Classes {
		if err := serializeClassTemplate(w, cls); err != nil {
			return err
		}
	}
	return nil
}

// Deserialize 从 r 读取并重建 Module。校验 magic 与版本号，不匹配返回错误。
func Deserialize(r io.Reader) (*Module, error) {
	var magic [8]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, err
	}
	if string(magic[:]) != string(cacheMagic) {
		return nil, fmt.Errorf("bytecode cache: bad magic header")
	}
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	version := binary.LittleEndian.Uint32(hdr[0:4])
	if version != FormatVersion {
		return nil, fmt.Errorf("bytecode cache: version mismatch (file=%d, want=%d)", version, FormatVersion)
	}
	funcCount := binary.LittleEndian.Uint32(hdr[4:8])
	classCount := binary.LittleEndian.Uint32(hdr[8:12])

	mod := &Module{
		Functions: make([]*FuncTemplate, funcCount),
		Classes:   make([]*ClassTemplate, classCount),
	}
	for i := uint32(0); i < funcCount; i++ {
		fn, err := deserializeFuncTemplate(r)
		if err != nil {
			return nil, err
		}
		mod.Functions[i] = fn
	}
	for i := uint32(0); i < classCount; i++ {
		cls, err := deserializeClassTemplate(r)
		if err != nil {
			return nil, err
		}
		mod.Classes[i] = cls
	}
	return mod, nil
}

// --- FuncTemplate --------------------------------------------------------

func serializeFuncTemplate(w io.Writer, fn *FuncTemplate) error {
	// 标量字段
	if err := writeString(w, fn.Name); err != nil {
		return err
	}
	var scalars [9 * 4]byte // NumParams, NumLocals, IsVarArgs, IsGenerator, IsAsync, IsArrow, len(Code), ArgumentsSlot, NoArgumentsObject
	binary.LittleEndian.PutUint32(scalars[0:4], uint32(fn.NumParams))
	binary.LittleEndian.PutUint32(scalars[4:8], uint32(fn.NumLocals))
	binary.LittleEndian.PutUint32(scalars[8:12], boolToU32(fn.IsVarArgs))
	binary.LittleEndian.PutUint32(scalars[12:16], boolToU32(fn.IsGenerator))
	binary.LittleEndian.PutUint32(scalars[16:20], boolToU32(fn.IsAsync))
	binary.LittleEndian.PutUint32(scalars[20:24], boolToU32(fn.IsArrow))
	binary.LittleEndian.PutUint32(scalars[24:28], uint32(len(fn.Code)))
	binary.LittleEndian.PutUint32(scalars[28:32], uint32(fn.ArgumentsSlot))
	binary.LittleEndian.PutUint32(scalars[32:36], boolToU32(fn.NoArgumentsObject))
	if _, err := w.Write(scalars[:]); err != nil {
		return err
	}
	// Code
	if len(fn.Code) > 0 {
		if _, err := w.Write(fn.Code); err != nil {
			return err
		}
	}
	// SourceFile
	if err := writeString(w, fn.SourceFile); err != nil {
		return err
	}
	// Constants
	if err := writeU32(w, uint32(len(fn.Constants))); err != nil {
		return err
	}
	for _, c := range fn.Constants {
		if err := engine.EncodeConst(w, c); err != nil {
			return err
		}
	}
	// Upvalues
	if err := writeU32(w, uint32(len(fn.Upvalues))); err != nil {
		return err
	}
	for _, uv := range fn.Upvalues {
		if err := writeU32(w, boolToU32(uv.IsLocal)); err != nil {
			return err
		}
		if err := writeU32(w, uint32(uv.Index)); err != nil {
			return err
		}
	}
	// TryTable
	if err := writeU32(w, uint32(len(fn.TryTable))); err != nil {
		return err
	}
	for _, te := range fn.TryTable {
		var buf [5 * 4]byte
		binary.LittleEndian.PutUint32(buf[0:4], uint32(te.StartPC))
		binary.LittleEndian.PutUint32(buf[4:8], uint32(te.CatchPC))
		binary.LittleEndian.PutUint32(buf[8:12], uint32(te.FinallyPC))
		binary.LittleEndian.PutUint32(buf[12:16], boolToU32(te.HasCatch))
		binary.LittleEndian.PutUint32(buf[16:20], boolToU32(te.HasFinally))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	// LineStarts
	if err := writeU32(w, uint32(len(fn.LineStarts))); err != nil {
		return err
	}
	for _, le := range fn.LineStarts {
		var buf [2 * 4]byte
		binary.LittleEndian.PutUint32(buf[0:4], uint32(le.PC))
		binary.LittleEndian.PutUint32(buf[4:8], uint32(le.Line))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	return nil
}

func deserializeFuncTemplate(r io.Reader) (*FuncTemplate, error) {
	name, err := readString(r)
	if err != nil {
		return nil, err
	}
	var scalars [9 * 4]byte
	if _, err := io.ReadFull(r, scalars[:]); err != nil {
		return nil, err
	}
	fn := &FuncTemplate{
		Name:        name,
		NumParams:   int(binary.LittleEndian.Uint32(scalars[0:4])),
		NumLocals:   int(binary.LittleEndian.Uint32(scalars[4:8])),
		IsVarArgs:   u32ToBool(binary.LittleEndian.Uint32(scalars[8:12])),
		IsGenerator: u32ToBool(binary.LittleEndian.Uint32(scalars[12:16])),
		IsAsync:     u32ToBool(binary.LittleEndian.Uint32(scalars[16:20])),
		IsArrow:     u32ToBool(binary.LittleEndian.Uint32(scalars[20:24])),
		// ArgumentsSlot 是带符号的哨兵槽：-1 表示箭头函数（无 own arguments）。
		// 序列化时经 uint32 存储，反序列化必须按 int32 解释，否则 -1 变成
		// 4294967295（正数），绕过 callClosure 的 `>= 0` 检查导致栈越界 panic。
		ArgumentsSlot:     int(int32(binary.LittleEndian.Uint32(scalars[28:32]))),
		NoArgumentsObject: u32ToBool(binary.LittleEndian.Uint32(scalars[32:36])),
	}
	codeLen := binary.LittleEndian.Uint32(scalars[24:28])
	if codeLen > 0 {
		fn.Code = make([]byte, codeLen)
		if _, err := io.ReadFull(r, fn.Code); err != nil {
			return nil, err
		}
	}
	fn.SourceFile, err = readString(r)
	if err != nil {
		return nil, err
	}
	// Constants
	constCount, err := readU32(r)
	if err != nil {
		return nil, err
	}
	if constCount > 0 {
		fn.Constants = make([]engine.Value, constCount)
		for i := uint32(0); i < constCount; i++ {
			v, err := engine.DecodeConst(r)
			if err != nil {
				return nil, err
			}
			fn.Constants[i] = v
		}
	}
	// Upvalues
	uvCount, err := readU32(r)
	if err != nil {
		return nil, err
	}
	if uvCount > 0 {
		fn.Upvalues = make([]UpvalueCapture, uvCount)
		for i := uint32(0); i < uvCount; i++ {
			isLocal, err := readU32(r)
			if err != nil {
				return nil, err
			}
			idx, err := readU32(r)
			if err != nil {
				return nil, err
			}
			fn.Upvalues[i] = UpvalueCapture{IsLocal: u32ToBool(isLocal), Index: int(idx)}
		}
	}
	// TryTable
	tryCount, err := readU32(r)
	if err != nil {
		return nil, err
	}
	if tryCount > 0 {
		fn.TryTable = make([]TryEntry, tryCount)
		for i := uint32(0); i < tryCount; i++ {
			var buf [5 * 4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			fn.TryTable[i] = TryEntry{
				StartPC:    int(binary.LittleEndian.Uint32(buf[0:4])),
				CatchPC:    int(binary.LittleEndian.Uint32(buf[4:8])),
				FinallyPC:  int(binary.LittleEndian.Uint32(buf[8:12])),
				HasCatch:   u32ToBool(binary.LittleEndian.Uint32(buf[12:16])),
				HasFinally: u32ToBool(binary.LittleEndian.Uint32(buf[16:20])),
			}
		}
	}
	// LineStarts
	lineCount, err := readU32(r)
	if err != nil {
		return nil, err
	}
	if lineCount > 0 {
		fn.LineStarts = make([]LineEntry, lineCount)
		for i := uint32(0); i < lineCount; i++ {
			var buf [2 * 4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			fn.LineStarts[i] = LineEntry{
				PC:   int(binary.LittleEndian.Uint32(buf[0:4])),
				Line: int(binary.LittleEndian.Uint32(buf[4:8])),
			}
		}
	}
	return fn, nil
}

// --- ClassTemplate -------------------------------------------------------

func serializeClassTemplate(w io.Writer, cls *ClassTemplate) error {
	if err := writeString(w, cls.Name); err != nil {
		return err
	}
	var hdr [4 * 4]byte
	binary.LittleEndian.PutUint32(hdr[0:4], boolToU32(cls.HasSuper))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(cls.CtorIdx))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(cls.Methods)))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(cls.ComputedIdx)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for _, m := range cls.Methods {
		var buf [3 * 4]byte
		binary.LittleEndian.PutUint32(buf[0:4], uint32(m.TmplIdx))
		binary.LittleEndian.PutUint32(buf[4:8], boolToU32(m.Static))
		binary.LittleEndian.PutUint32(buf[8:12], uint32(m.Kind))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
		if err := writeString(w, m.Name); err != nil {
			return err
		}
	}
	for _, ci := range cls.ComputedIdx {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], uint32(ci))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	return nil
}

func deserializeClassTemplate(r io.Reader) (*ClassTemplate, error) {
	name, err := readString(r)
	if err != nil {
		return nil, err
	}
	var hdr [4 * 4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	cls := &ClassTemplate{
		Name:     name,
		HasSuper: u32ToBool(binary.LittleEndian.Uint32(hdr[0:4])),
		CtorIdx:  int(binary.LittleEndian.Uint32(hdr[4:8])),
	}
	methodCount := binary.LittleEndian.Uint32(hdr[8:12])
	if methodCount > 0 {
		cls.Methods = make([]ClassMethodTemplate, methodCount)
		for i := uint32(0); i < methodCount; i++ {
			var buf [3 * 4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			m := ClassMethodTemplate{
				TmplIdx: int(binary.LittleEndian.Uint32(buf[0:4])),
				Static:  u32ToBool(binary.LittleEndian.Uint32(buf[4:8])),
				Kind:    MethodKindValue(binary.LittleEndian.Uint32(buf[8:12])),
			}
			m.Name, err = readString(r)
			if err != nil {
				return nil, err
			}
			cls.Methods[i] = m
		}
	}
	computedCount := binary.LittleEndian.Uint32(hdr[12:16])
	if computedCount > 0 {
		cls.ComputedIdx = make([]int, computedCount)
		for i := uint32(0); i < computedCount; i++ {
			var buf [4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			cls.ComputedIdx[i] = int(binary.LittleEndian.Uint32(buf[:]))
		}
	}
	return cls, nil
}

// --- 基础读写辅助 --------------------------------------------------------

func writeU32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func readU32(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

func writeString(w io.Writer, s string) error {
	if err := writeU32(w, uint32(len(s))); err != nil {
		return err
	}
	if len(s) > 0 {
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
	}
	return nil
}

func readString(r io.Reader) (string, error) {
	n, err := readU32(r)
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

func boolToU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func u32ToBool(v uint32) bool { return v != 0 }
