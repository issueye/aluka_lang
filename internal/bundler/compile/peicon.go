// Package compile —— PE 文件级图标注入（aluka build --gui --icon）：
// 在产物基座上重写 .rsrc 资源段，使 Explorer/任务栏展示应用自身图标。
//
// 实现方式（不做全文件重排，保证 payload 偏移语义稳定）：
//  1. 解析旧 .rsrc 目录树，保留全部非图标资源（VERSIONINFO 等）的数据块；
//  2. 以新 .ico 重建 RT_GROUP_ICON + RT_ICON 子树，与保留资源合并为新的 .rsrc 数据；
//  3. 新数据追加到文件末尾（旧 .rsrc 原始数据成为死字节，体积代价通常 <100KB）；
//     复用旧节的 VirtualAddress，仅改写节表项（VirtualSize/SizeOfRawData/
//     PointerToRawData）、数据目录 IMAGE_DIRECTORY_ENTRY_RESOURCE 与 SizeOfImage。
//
// 基座无 .rsrc 时追加新节表项（要求节表后有空闲空间，正常 Go 构建产物均满足）。
package compile

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// PE / 资源相关常量。
const (
	rtIcon      = 3
	rtGroupIcon = 14

	imageDirEntryResource = 2
)

// resBlob 是一个资源数据叶子：(type, id, lang) → 原始字节。
type resBlob struct {
	typ, id, lang uint32
	data          []byte
}

// icoImage 是 .ico 中的单张图像。
type icoImage struct {
	dir  []byte // ICONDIRENTRY 前 12 字节（宽/高/色深/平面/位数/字节数）
	data []byte // 图像原始字节
}

// parseICO 解析 .ico 文件，校验目录与图像边界。
func parseICO(ico []byte) ([]icoImage, error) {
	if len(ico) < 6 || ico[0] != 0 || ico[1] != 0 {
		return nil, fmt.Errorf("peicon: not a .ico file")
	}
	if ico[2] != 1 || ico[3] != 0 { // idType 必须为 1（icon）
		return nil, fmt.Errorf("peicon: .ico type is cursor, not icon")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("peicon: .ico contains no images")
	}
	images := make([]icoImage, 0, count)
	for i := 0; i < count; i++ {
		entryOff := 6 + i*16
		if entryOff+16 > len(ico) {
			return nil, fmt.Errorf("peicon: .ico directory truncated")
		}
		size := binary.LittleEndian.Uint32(ico[entryOff+8 : entryOff+12])
		offset := binary.LittleEndian.Uint32(ico[entryOff+12 : entryOff+16])
		if size == 0 || int64(offset)+int64(size) > int64(len(ico)) {
			return nil, fmt.Errorf("peicon: .ico image %d out of range", i)
		}
		images = append(images, icoImage{
			dir:  append([]byte(nil), ico[entryOff:entryOff+12]...),
			data: append([]byte(nil), ico[offset:offset+size]...),
		})
	}
	return images, nil
}

// buildGroupIcon 由 .ico 目录构造 RT_GROUP_ICON 数据（GRPICONDIR）。
func buildGroupIcon(images []icoImage) []byte {
	buf := make([]byte, 0, 6+14*len(images))
	buf = append(buf, 0, 0) // idReserved
	buf = append(buf, 1, 0) // idType = icon
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(images)))
	for i, img := range images {
		buf = append(buf, img.dir...)                            // ICONDIRENTRY 前 12 字节
		buf = binary.LittleEndian.AppendUint16(buf, uint16(i+1)) // nID（RT_ICON 资源 ID）
	}
	return buf
}

// resNode 是资源目录树节点（按 type → id → lang 三层组织）。
type resNode struct {
	types []uint32
	ids   map[uint32][]uint32          // type → 有序 id 列表
	langs map[uint64][]uint32          // (type<<32|id) → 有序 lang 列表
	data  map[uint64]map[uint32][]byte // (type<<32|id) → lang → 字节
	l2Off map[uint32]uint32            // type → L2 目录偏移
	l3Off map[uint64]uint32            // (type<<32|id) → L3 目录偏移
}

func resKey(typ, id uint32) uint64 { return uint64(typ)<<32 | uint64(id) }

func buildResTree(blobs []resBlob) *resNode {
	n := &resNode{
		ids:   map[uint32][]uint32{},
		langs: map[uint64][]uint32{},
		data:  map[uint64]map[uint32][]byte{},
		l2Off: map[uint32]uint32{},
		l3Off: map[uint64]uint32{},
	}
	for _, b := range blobs {
		if _, ok := n.data[resKey(b.typ, b.id)]; !ok {
			n.types = append(n.types, b.typ)
			n.ids[b.typ] = append(n.ids[b.typ], b.id)
		}
		if _, ok := n.data[resKey(b.typ, b.id)][b.lang]; !ok {
			n.langs[resKey(b.typ, b.id)] = append(n.langs[resKey(b.typ, b.id)], b.lang)
		}
		if n.data[resKey(b.typ, b.id)] == nil {
			n.data[resKey(b.typ, b.id)] = map[uint32][]byte{}
		}
		n.data[resKey(b.typ, b.id)][b.lang] = b.data
	}
	sort.Slice(n.types, func(i, j int) bool { return n.types[i] < n.types[j] })
	for t := range n.ids {
		sort.Slice(n.ids[t], func(i, j int) bool { return n.ids[t][i] < n.ids[t][j] })
	}
	for k := range n.langs {
		langs := n.langs[k]
		sort.Slice(langs, func(i, j int) bool { return langs[i] < langs[j] })
	}
	return n
}

// buildResourceSection 序列化资源目录树（三级：type → id → lang → 数据）。
// rsrcVA 是节的虚拟地址（数据叶子的 OffsetToData 需写成 RVA）。
func buildResourceSection(blobs []resBlob, rsrcVA uint32) []byte {
	n := buildResTree(blobs)

	// 布局（目录与数据项 8 字节对齐）：
	//   L1 目录 | L2 目录（每 type）| L3 目录（每 id）| 数据项 | 数据块
	l1Size := alignTo(uint32(16+8*len(n.types)), 8)
	var l2Size, l3Size, entrySize, blobSize uint32
	for _, t := range n.types {
		l2Size += alignTo(uint32(16+8*len(n.ids[t])), 8)
		for _, id := range n.ids[t] {
			l3Size += alignTo(uint32(16+8*len(n.langs[resKey(t, id)])), 8)
			entrySize += alignTo(uint32(16*len(n.langs[resKey(t, id)])), 8)
			for _, lang := range n.langs[resKey(t, id)] {
				blobSize += alignTo(uint32(len(n.data[resKey(t, id)][lang])), 8)
			}
		}
	}
	l2Base, l3Base := l1Size, l1Size+l2Size
	entryBase, blobBase := l3Base+l3Size, l3Base+l3Size+entrySize

	buf := make([]byte, blobBase+blobSize)
	putDir := func(off, entries int) {
		binary.LittleEndian.PutUint16(buf[off+12:], 0) // NumberOfNamedEntries
		binary.LittleEndian.PutUint16(buf[off+14:], uint16(entries))
	}

	// 计算各目录偏移
	off := l2Base
	for _, t := range n.types {
		n.l2Off[t] = off
		off += alignTo(uint32(16+8*len(n.ids[t])), 8)
	}
	off = l3Base
	for _, t := range n.types {
		for _, id := range n.ids[t] {
			n.l3Off[resKey(t, id)] = off
			off += alignTo(uint32(16+8*len(n.langs[resKey(t, id)])), 8)
		}
	}

	// 数据项与数据块
	nextEntry, nextBlob := entryBase, blobBase
	entryOffFor := map[uint64]map[uint32]uint32{}
	for _, t := range n.types {
		for _, id := range n.ids[t] {
			for _, lang := range n.langs[resKey(t, id)] {
				data := n.data[resKey(t, id)][lang]
				if entryOffFor[resKey(t, id)] == nil {
					entryOffFor[resKey(t, id)] = map[uint32]uint32{}
				}
				entryOffFor[resKey(t, id)][lang] = nextEntry
				binary.LittleEndian.PutUint32(buf[nextEntry:], rsrcVA+nextBlob) // OffsetToData（RVA）
				binary.LittleEndian.PutUint32(buf[nextEntry+4:], uint32(len(data)))
				copy(buf[nextBlob:], data)
				nextEntry += 16
				nextBlob += alignTo(uint32(len(data)), 8)
			}
		}
	}

	// L3 目录：lang → 数据项偏移
	for _, t := range n.types {
		for _, id := range n.ids[t] {
			dir := int(n.l3Off[resKey(t, id)])
			putDir(dir, len(n.langs[resKey(t, id)]))
			for i, lang := range n.langs[resKey(t, id)] {
				binary.LittleEndian.PutUint32(buf[dir+16+i*8:], lang)
				binary.LittleEndian.PutUint32(buf[dir+16+i*8+4:], entryOffFor[resKey(t, id)][lang])
			}
		}
	}

	// L2 目录：id → L3 目录偏移（子目录高位标记）
	for _, t := range n.types {
		dir := int(n.l2Off[t])
		putDir(dir, len(n.ids[t]))
		for i, id := range n.ids[t] {
			binary.LittleEndian.PutUint32(buf[dir+16+i*8:], id)
			binary.LittleEndian.PutUint32(buf[dir+16+i*8+4:], n.l3Off[resKey(t, id)]|0x80000000)
		}
	}

	// L1 目录：type → L2 目录偏移
	putDir(0, len(n.types))
	for i, t := range n.types {
		binary.LittleEndian.PutUint32(buf[16+i*8:], t)
		binary.LittleEndian.PutUint32(buf[16+i*8+4:], n.l2Off[t]|0x80000000)
	}
	return buf
}

func alignTo(v, a uint32) uint32 {
	if a == 0 {
		return v
	}
	return (v + a - 1) & ^(a - 1)
}

// parseResourceDir 解析旧 .rsrc 目录树，提取全部数据叶子。
// 仅支持整数 ID 资源（windres/Go syso 产物均满足；命名资源返回错误）。
func parseResourceDir(section []byte, rsrcVA uint32) ([]resBlob, error) {
	var out []resBlob
	u16 := func(off int) uint32 { return uint32(binary.LittleEndian.Uint16(section[off:])) }
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(section[off:]) }

	var walk func(dirOff, level int, typ, id uint32) error
	walk = func(dirOff, level int, typ, id uint32) error {
		if dirOff+16 > len(section) {
			return fmt.Errorf("peicon: resource directory out of range")
		}
		named := int(u16(dirOff + 12))
		ids := int(u16(dirOff + 14))
		if named > 0 {
			return fmt.Errorf("peicon: named resource entries not supported")
		}
		for i := 0; i < ids; i++ {
			ent := dirOff + 16 + i*8
			if ent+8 > len(section) {
				return fmt.Errorf("peicon: resource entry out of range")
			}
			name := u32(ent)
			offset := u32(ent + 4)
			switch level {
			case 0: // type 级
				if offset&0x80000000 == 0 {
					return fmt.Errorf("peicon: unexpected data entry at type level")
				}
				if err := walk(int(offset&^0x80000000), 1, name, 0); err != nil {
					return err
				}
			case 1: // id 级
				if offset&0x80000000 == 0 {
					return fmt.Errorf("peicon: unexpected data entry at id level")
				}
				if err := walk(int(offset&^0x80000000), 2, typ, name); err != nil {
					return err
				}
			case 2: // lang 级 → 数据项
				if offset&0x80000000 != 0 {
					return fmt.Errorf("peicon: unexpected subdirectory at lang level")
				}
				de := int(offset)
				if de+16 > len(section) {
					return fmt.Errorf("peicon: data entry out of range")
				}
				rva := u32(de)
				size := u32(de + 4)
				raw := int(rva - rsrcVA)
				if size == 0 || raw < 0 || raw+int(size) > len(section) {
					return fmt.Errorf("peicon: resource data out of range")
				}
				out = append(out, resBlob{typ: typ, id: id, lang: name, data: append([]byte(nil), section[raw:raw+int(size)]...)})
			}
		}
		return nil
	}
	if err := walk(0, 0, 0, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// InjectIcon 将 .ico 写入 PE 的 .rsrc 资源段（替换既有图标组，
// 保留 VERSIONINFO 等其他资源），返回新的 PE 字节。
func InjectIcon(exe, ico []byte) ([]byte, error) {
	images, err := parseICO(ico)
	if err != nil {
		return nil, err
	}

	// ---- PE 头解析 ----
	if len(exe) < 0x40 {
		return nil, fmt.Errorf("peicon: too short for PE")
	}
	peOff := int(binary.LittleEndian.Uint32(exe[0x3C:0x40]))
	if peOff+4+20 > len(exe) || string(exe[peOff:peOff+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("peicon: bad PE signature")
	}
	fileHeader := peOff + 4
	numSections := int(binary.LittleEndian.Uint16(exe[fileHeader+2 : fileHeader+4]))
	optSize := int(binary.LittleEndian.Uint16(exe[fileHeader+16 : fileHeader+18]))
	opt := fileHeader + 20
	if opt+optSize > len(exe) {
		return nil, fmt.Errorf("peicon: optional header truncated")
	}
	magic := binary.LittleEndian.Uint16(exe[opt : opt+2])
	var ddOff int
	switch magic {
	case 0x20b: // PE32+
		ddOff = opt + 112
	case 0x10b: // PE32
		ddOff = opt + 96
	default:
		return nil, fmt.Errorf("peicon: unknown optional header magic %#x", magic)
	}
	secAlign := binary.LittleEndian.Uint32(exe[opt+32 : opt+36])
	fileAlign := binary.LittleEndian.Uint32(exe[opt+36 : opt+40])
	if secAlign == 0 || fileAlign == 0 {
		return nil, fmt.Errorf("peicon: bad alignment values")
	}
	secTable := opt + optSize

	// ---- 定位 .rsrc ----
	rsrcIdx := -1
	for i := 0; i < numSections; i++ {
		e := secTable + i*40
		if e+40 > len(exe) {
			return nil, fmt.Errorf("peicon: section table truncated")
		}
		if string(bytesTrimZ(exe[e:e+8])) == ".rsrc" {
			rsrcIdx = i
			break
		}
	}

	// ---- 收集资源：保留非图标 + 新图标 ----
	var blobs []resBlob
	var rsrcVA uint32
	if rsrcIdx >= 0 {
		e := secTable + rsrcIdx*40
		rsrcVA = binary.LittleEndian.Uint32(exe[e+12 : e+16])
		rawPtr := binary.LittleEndian.Uint32(exe[e+20 : e+24])
		rawSize := binary.LittleEndian.Uint32(exe[e+16 : e+20])
		end := uint64(rawPtr) + uint64(rawSize)
		if end > uint64(len(exe)) {
			end = uint64(len(exe))
		}
		old, err := parseResourceDir(exe[rawPtr:end], rsrcVA)
		if err != nil {
			return nil, fmt.Errorf("peicon: parse existing .rsrc: %w", err)
		}
		for _, b := range old {
			if b.typ != rtIcon && b.typ != rtGroupIcon {
				blobs = append(blobs, b)
			}
		}
	} else {
		// 基座无 .rsrc：新 VA 取所有节尾的最大对齐地址
		maxVA := uint32(0)
		for i := 0; i < numSections; i++ {
			e := secTable + i*40
			va := binary.LittleEndian.Uint32(exe[e+12 : e+16])
			vs := binary.LittleEndian.Uint32(exe[e+8 : e+12])
			if v := va + alignTo(vs, secAlign); v > maxVA {
				maxVA = v
			}
		}
		rsrcVA = maxVA
	}

	for i, img := range images {
		blobs = append(blobs, resBlob{typ: rtIcon, id: uint32(i + 1), lang: 0x409, data: img.data})
	}
	blobs = append(blobs, resBlob{typ: rtGroupIcon, id: 1, lang: 0x409, data: buildGroupIcon(images)})

	// ---- 构建新资源段并追加到文件末尾 ----
	data := buildResourceSection(blobs, rsrcVA)
	rawAligned := alignTo(uint32(len(data)), fileAlign)
	newRawOff := alignTo(uint32(len(exe)), fileAlign)

	out := make([]byte, int(newRawOff)+int(rawAligned))
	copy(out, exe)
	copy(out[newRawOff:], data)

	// ---- 修补节表 / 数据目录 / SizeOfImage ----
	u32put := func(off int, v uint32) { binary.LittleEndian.PutUint32(out[off:off+4], v) }
	u16put := func(off int, v uint16) { binary.LittleEndian.PutUint16(out[off:off+2], v) }

	if rsrcIdx >= 0 {
		e := secTable + rsrcIdx*40
		copy(out[e:e+8], ".rsrc\x00\x00\x00")
		u32put(e+8, uint32(len(data))) // VirtualSize
		u32put(e+16, rawAligned)       // SizeOfRawData
		u32put(e+20, newRawOff)        // PointerToRawData
	} else {
		hdrOff := secTable + numSections*40
		// 节表空间校验：新表项不得覆盖第一个节的数据
		firstRaw := uint32(0xFFFFFFFF)
		for i := 0; i < numSections; i++ {
			e := secTable + i*40
			if p := binary.LittleEndian.Uint32(exe[e+20 : e+24]); p > 0 && p < firstRaw {
				firstRaw = p
			}
		}
		if uint32(hdrOff)+40 > firstRaw {
			return nil, fmt.Errorf("peicon: no room in section table for .rsrc")
		}
		copy(out[hdrOff:hdrOff+8], ".rsrc\x00\x00\x00")
		u32put(hdrOff+8, uint32(len(data)))
		u32put(hdrOff+12, rsrcVA)
		u32put(hdrOff+16, rawAligned)
		u32put(hdrOff+20, newRawOff)
		u32put(hdrOff+36, 0x40000040) // INITIALIZED_DATA | READ
		u16put(fileHeader+2, uint16(numSections+1))
	}

	u32put(ddOff+imageDirEntryResource*8, rsrcVA)
	u32put(ddOff+imageDirEntryResource*8+4, uint32(len(data)))

	needImage := rsrcVA + alignTo(uint32(len(data)), secAlign)
	if cur := binary.LittleEndian.Uint32(out[opt+56 : opt+60]); cur < needImage {
		u32put(opt+56, needImage)
	}
	return out, nil
}

// bytesTrimZ 去除节名尾部的 NUL。
func bytesTrimZ(b []byte) []byte {
	for i, c := range b {
		if c == 0 {
			return b[:i]
		}
	}
	return b
}

// SetPESubsystemGUI 将 PE 子系统切换为 IMAGE_SUBSYSTEM_WINDOWS_GUI（=2）。
// GUI 产物双击运行时 Windows 不再创建控制台（等效 go build -ldflags
// -H=windowsgui），比运行期 FreeConsole 更彻底——不存在先闪后隐的黑框。
// stdout/stderr 重定向到管道/文件仍然有效（仅跳过控制台自动附着）。
func SetPESubsystemGUI(exe []byte) ([]byte, error) {
	if len(exe) < 0x40 {
		return nil, fmt.Errorf("peicon: too short for PE")
	}
	peOff := int(binary.LittleEndian.Uint32(exe[0x3C:0x40]))
	if peOff+4+20+70 > len(exe) || string(exe[peOff:peOff+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("peicon: bad PE signature")
	}
	opt := peOff + 4 + 20
	switch binary.LittleEndian.Uint16(exe[opt : opt+2]) {
	case 0x20b, 0x10b: // PE32+ / PE32（Subsystem 偏移两者相同：+68）
	default:
		return nil, fmt.Errorf("peicon: unknown optional header magic")
	}
	binary.LittleEndian.PutUint16(exe[opt+68:opt+70], 2)
	return exe, nil
}
