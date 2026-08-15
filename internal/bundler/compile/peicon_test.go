package compile

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeSyntheticPE 构造带 .rsrc（旧图标 + VERSIONINFO）的最小 PE32+ 镜像。
func makeSyntheticPE(t *testing.T) []byte {
	t.Helper()

	// 旧资源：RT_ICON(3,1) + RT_VERSION(16,1)
	oldIcon := makeTestICOImage(t, 0xFF0000FF) // 蓝
	oldVersion := []byte("version-blob-old")
	oldRsrc := buildResourceSection([]resBlob{
		{typ: rtIcon, id: 1, lang: 0x409, data: oldIcon},
		{typ: rtGroupIcon, id: 1, lang: 0x409, data: buildGroupIcon([]icoImage{{
			dir: []byte{32, 32, 0, 0, 1, 0, 32, 0, byte(len(oldIcon)), 0, 0, 0},
		}})},
		{typ: 16, id: 1, lang: 0x409, data: oldVersion},
	}, 0x2000)

	const fileAlign = 0x200
	const secAlign = 0x1000

	text := []byte{0x90, 0x90, 0x90, 0x90}
	textSize := uint32(alignTo(uint32(len(text)), fileAlign))
	rsrcSize := uint32(alignTo(uint32(len(oldRsrc)), fileAlign))

	var buf bytes.Buffer
	// DOS header + stub
	dos := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(dos[0x3C:], 0x40)
	buf.Write(dos)
	// PE signature
	buf.WriteString("PE\x00\x00")
	// FileHeader: machine=amd64, sections=2, optSize=240
	fh := make([]byte, 20)
	binary.LittleEndian.PutUint16(fh[0:], 0x8664)
	binary.LittleEndian.PutUint16(fh[2:], 2)
	binary.LittleEndian.PutUint16(fh[16:], 240)
	buf.Write(fh)
	// OptionalHeader PE32+（含 16 个数据目录）
	opt := make([]byte, 240)
	binary.LittleEndian.PutUint16(opt[0:], 0x20b)
	binary.LittleEndian.PutUint32(opt[32:], secAlign)
	binary.LittleEndian.PutUint32(opt[36:], fileAlign)
	binary.LittleEndian.PutUint32(opt[56:], 0x3000) // SizeOfImage
	dd := 112
	binary.LittleEndian.PutUint32(opt[dd+2*8:], 0x2000) // RESOURCE RVA
	binary.LittleEndian.PutUint32(opt[dd+2*8+4:], uint32(len(oldRsrc)))
	buf.Write(opt)
	// Section table: .text（raw 0x200）+ .rsrc（raw 0x400）
	buf.WriteString(".text\x00\x00\x00")
	sec := make([]byte, 32)
	binary.LittleEndian.PutUint32(sec[0:], uint32(len(text)))
	binary.LittleEndian.PutUint32(sec[4:], 0x1000)
	binary.LittleEndian.PutUint32(sec[8:], textSize)
	binary.LittleEndian.PutUint32(sec[12:], 0x200)
	buf.Write(sec)
	buf.WriteString(".rsrc\x00\x00\x00")
	sec = make([]byte, 32)
	binary.LittleEndian.PutUint32(sec[0:], uint32(len(oldRsrc)))
	binary.LittleEndian.PutUint32(sec[4:], 0x2000)
	binary.LittleEndian.PutUint32(sec[8:], rsrcSize)
	binary.LittleEndian.PutUint32(sec[12:], 0x400)
	buf.Write(sec)
	// 填充到 0x200 后放 .text，0x400 后放 .rsrc
	buf.Write(make([]byte, int(0x200-buf.Len())))
	buf.Write(text)
	buf.Write(make([]byte, int(0x400-int(buf.Len()))))
	buf.Write(oldRsrc)
	buf.Write(make([]byte, int(rsrcSize-uint32(len(oldRsrc)))))
	return buf.Bytes()
}

// makeTestICOImage 生成 1x1 32bpp 单色 ICO 图像数据（BITMAPINFOHEADER + 像素 + AND 掩码）。
func makeTestICOImage(t *testing.T, rgba uint32) []byte {
	t.Helper()
	img := make([]byte, 48)
	binary.LittleEndian.PutUint32(img[0:], 40)  // biSize
	binary.LittleEndian.PutUint32(img[4:], 1)   // width
	binary.LittleEndian.PutUint32(img[8:], 2)   // height（含掩码行，翻倍）
	binary.LittleEndian.PutUint16(img[12:], 1)  // planes
	binary.LittleEndian.PutUint16(img[14:], 32) // bitcount
	binary.LittleEndian.PutUint32(img[20:], 48) // biSizeImage
	binary.LittleEndian.PutUint32(img[40:], rgba)
	return img
}

// makeTestICO 生成含一张 1x1 图像的 .ico 文件字节。
func makeTestICO(t *testing.T, rgba uint32) []byte {
	t.Helper()
	img := makeTestICOImage(t, rgba)
	ico := make([]byte, 6+16+len(img))
	ico[2] = 1 // type icon
	ico[4] = 1 // count
	e := 6
	ico[e] = 1   // width
	ico[e+1] = 1 // height
	ico[e+4] = 1 // planes
	ico[e+6] = 32
	binary.LittleEndian.PutUint32(ico[e+8:], uint32(len(img)))
	binary.LittleEndian.PutUint32(ico[e+12:], uint32(6+16))
	copy(ico[6+16:], img)
	return ico
}

// TestInjectIconRoundTrip：合成 PE 注入后校验图标替换与 VERSIONINFO 保留。
func TestInjectIconRoundTrip(t *testing.T) {
	pe := makeSyntheticPE(t)
	ico := makeTestICO(t, 0xFF00FF00) // 绿（区别于旧蓝）

	out, err := InjectIcon(pe, ico)
	if err != nil {
		t.Fatalf("InjectIcon: %v", err)
	}

	// 重新解析 PE：定位 .rsrc
	peOff := int(binary.LittleEndian.Uint32(out[0x3C:]))
	fh := peOff + 4
	numSec := int(binary.LittleEndian.Uint16(out[fh+2:]))
	optSize := int(binary.LittleEndian.Uint16(out[fh+16:]))
	st := fh + 20 + optSize
	var rsrcRaw, rsrcLen uint32
	for i := 0; i < numSec; i++ {
		e := st + i*40
		if string(bytesTrimZ(out[e:e+8])) == ".rsrc" {
			rsrcLen = binary.LittleEndian.Uint32(out[e+16:])
			rsrcRaw = binary.LittleEndian.Uint32(out[e+20:])
		}
	}
	if rsrcRaw == 0 {
		t.Fatal(".rsrc section not found after injection")
	}
	if rsrcRaw < uint32(len(pe)) {
		t.Fatalf("new .rsrc raw data should be appended at end: raw=%d, oldLen=%d", rsrcRaw, len(pe))
	}

	blobs, err := parseResourceDir(out[rsrcRaw:rsrcRaw+rsrcLen], 0x2000)
	if err != nil {
		t.Fatalf("parse injected .rsrc: %v", err)
	}

	var group, icon, version []byte
	for _, b := range blobs {
		switch {
		case b.typ == rtGroupIcon && b.id == 1:
			group = b.data
		case b.typ == rtIcon:
			icon = b.data
		case b.typ == 16:
			version = b.data
		}
	}
	if group == nil || icon == nil {
		t.Fatalf("icon resources missing after injection: %+v", blobs)
	}
	if version == nil || string(version) != "version-blob-old" {
		t.Fatalf("VERSIONINFO not preserved: %q", version)
	}
	if !bytes.Equal(icon, makeTestICOImage(t, 0xFF00FF00)) {
		t.Fatal("injected icon image mismatch")
	}
	// 组目录：count=1，nID=1
	if len(group) != 6+14 || binary.LittleEndian.Uint16(group[4:]) != 1 || binary.LittleEndian.Uint16(group[18:]) != 1 {
		t.Fatalf("unexpected group icon data: % x", group)
	}
}

// TestInjectIconNoRsrc：基座无 .rsrc 时追加新节。
func TestInjectIconNoRsrc(t *testing.T) {
	// 复用合成 PE 构造，但把节名改成 .data（去掉 .rsrc）
	pe := makeSyntheticPE(t)
	peOff := int(binary.LittleEndian.Uint32(pe[0x3C:]))
	fh := peOff + 4
	optSize := int(binary.LittleEndian.Uint16(pe[fh+16:]))
	st := fh + 20 + optSize
	copy(pe[st+40:st+48], ".data\x00\x00\x00")

	out, err := InjectIcon(pe, makeTestICO(t, 0xFFFF0000))
	if err != nil {
		t.Fatalf("InjectIcon (no .rsrc): %v", err)
	}
	if n := binary.LittleEndian.Uint16(out[fh+2:]); n != 3 {
		t.Fatalf("NumberOfSections = %d, want 3", n)
	}
	name := string(bytesTrimZ(out[st+80 : st+88]))
	if name != ".rsrc" {
		t.Fatalf("new section name = %q", name)
	}
}

// TestParseICOErrors：非法 .ico 输入报错。
func TestParseICOErrors(t *testing.T) {
	if _, err := parseICO([]byte("garbage")); err == nil {
		t.Error("expected error for non-ico input")
	}
	if _, err := parseICO(makeTestICO(t, 0xFF)); err != nil {
		t.Errorf("valid ico rejected: %v", err)
	}
}
