// Package compile 实现 `aluka build --compile` 的产物打包（子方案 B2：
// payload 自附加）。产物 = aluka 基座 + payload + footer。
//
// 布局（docs/build-compile-plan.md §5.1）：
//
//	[payload header]  magic(8) "ALUKABDL" | PayloadVersion(u32) | manifestLen(u32) | dataLen(u32)
//	[manifest]        JSON：入口/模块表（路径 → offset,len）/formatVersion/平台/构建时间
//	[模块字节码流]    每模块 = bytecode.Serialize 输出（复用磁盘缓存格式）
//	[footer]          magic(8) "ALUKAFTR" | payloadOffset(u64) | payloadLen(u64) | sha256(32)
//
// 版本策略：PayloadVersion 标识 payload 自身布局；manifest 内记录字节码
// FormatVersion，运行时校验，不匹配报"产物由不兼容版本构建"。
package compile

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sort"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// PayloadVersion 是 payload 布局版本。布局变更（header/manifest 字段）时递增。
// v3：使用 zlib 压缩 manifest 与 data 数据区，产物体积缩减 40%~70%。
const PayloadVersion = 3

// payloadMagic 是 payload 数据段的起始魔数。
var payloadMagic = []byte("ALUKABDL")

// footerMagic 是产物文件尾部的魔数（启动检测入口）。
var footerMagic = []byte("ALUKAFTR")

// FooterSize 是 footer 固定长度：magic(8) + payloadOffset(8) + payloadLen(8) + sha256(32)。
const FooterSize = 8 + 8 + 8 + 32

// headerSize 是 payload header 固定长度：magic(8) + 3×u32。
const headerSize = 8 + 4*3

func compressBytes(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressBytes(src []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ModuleType 标记模块类型（决定产物模式的词法参数与 TLA 语义）。
const (
	ModuleTypeESM = "esm"
	ModuleTypeCJS = "cjs"
)

// ModuleTypeOf 返回 ModuleKind 对应的 payload 模块类型字符串。
func ModuleTypeOf(kind module.ModuleKind) string {
	if kind == module.ModuleESM {
		return ModuleTypeESM
	}
	return ModuleTypeCJS
}

// EntryInfo 描述一个模块条目在数据区中的位置。
type EntryInfo struct {
	Path       string `json:"path"`       // 模块标识（构建时路径，产物模式作为虚拟路径）
	ModuleType string `json:"moduleType"` // "esm" | "cjs"
	SourceKind string `json:"sourceKind"` // "javascript" | "typescript" | "json"
	ModuleKind string `json:"moduleKind"` // "esm" | "cjs" | "script"
	Offset     uint32 `json:"offset"`     // 相对数据区起始的偏移
	Length     uint32 `json:"length"`     // 序列化字节数
}

// Manifest 是 payload 的元数据（JSON 编码）。
type Manifest struct {
	PayloadVersion uint32      `json:"payloadVersion"`
	FormatVersion  uint32      `json:"formatVersion"` // 字节码格式版本（运行时校验）
	Entry          string      `json:"entry"`         // 入口模块路径
	// RootDir 是入口文件所在目录（构建机绝对路径）。运行时对未嵌入的
	// 非静态动态导入（import(变量)）按 RootDir 回退文件系统现场加载
	// 未预编译模块（T2-B4 扩展，docs/jiti-dynamic-import-plan.md M1）。
	// 旧产物无此字段时保持原有「仅绝对路径可回退」语义。
	RootDir string `json:"rootDir,omitempty"`
	Modules []EntryInfo `json:"modules"`
	// Resolutions 是构建期解析映射：父模块路径 → specifier → 解析后的模块
	// 路径（构建机绝对路径）。产物运行时不做文件系统解析，直接查映射加载
	// 嵌入的预编译模块（M2，docs/build-compile-plan.md §5.3）。
	Resolutions map[string]map[string]string `json:"resolutions,omitempty"`
	// Assets 是嵌入的 JSON 资源（M3，B2.3.4）：虚拟路径 → 原始 JSON 字节。
	// import x from './data.json' 的依赖在构建期收集为资源而非模块。
	Assets map[string]string `json:"assets,omitempty"`
	// WebAssets 是 GUI 模式（aluka build --gui）内嵌的前端静态资源：
	// 相对路径 → base64 内容（HTML/CSS/JS/图片/字体等），运行时挂载到
	// aluka://app/ 内存虚拟协议。与 Assets（JSON 模块资源）语义分离。
	WebAssets map[string]string `json:"webAssets,omitempty"`
	// Icon 是 GUI 模式（--icon）内嵌的应用图标（.ico 原始字节 base64），
	// 运行时应用于窗口标题栏/任务栏与默认托盘图标。
	Icon      string `json:"icon,omitempty"`
	Platform  string `json:"platform"` // 构建平台（GOOS/GOARCH）
	CreatedAt string `json:"createdAt"`
}

// EntryData 是一个待打包模块（编译产物）。
type EntryData struct {
	Path       string
	ModuleType string // ModuleTypeESM | ModuleTypeCJS
	SourceKind module.SourceKind
	ModuleKind module.ModuleKind
	Stage      module.TransformStage
	Module     *bytecode.Module
}

// Pack 打包 payload 数据段（不含 footer；footer 由 Build 阶段写入文件）。
// modules 按路径排序保证输出确定性；resolutions 为构建期解析映射
// （M2）；assets 为嵌入的 JSON 资源（M3，虚拟路径 → 原始字节）。
func Pack(entryPath string, modules []*EntryData, resolutions map[string]map[string]string, assets map[string][]byte) ([]byte, error) {
	return PackWithOptions(entryPath, modules, resolutions, assets, PackOptions{})
}

// PackWithWebAssets 在 Pack 基础上追加 GUI 前端静态资源（--gui 模式）。
// webAssets 为相对路径 → 原始字节；base64 编码后写入 manifest
// （manifest 整体 zlib 压缩，体积膨胀可忽略）。
func PackWithWebAssets(entryPath string, modules []*EntryData, resolutions map[string]map[string]string, assets map[string][]byte, webAssets map[string][]byte) ([]byte, error) {
	return PackWithOptions(entryPath, modules, resolutions, assets, PackOptions{WebAssets: webAssets})
}

// PackOptions 是打包可选项（GUI 资源与运行时磁盘回退基准）。
type PackOptions struct {
	// WebAssets 前端静态资源：相对路径 → 原始字节（aluka://app/ 虚拟协议）。
	WebAssets map[string][]byte
	// Icon 应用图标（.ico 文件原始字节），应用于窗口/任务栏/托盘。
	Icon []byte
	// RootDir 入口文件所在目录（构建机绝对路径）：写入 manifest，供产物
	// 运行时的非静态动态导入回退文件系统（未嵌入模块现场加载）。
	RootDir string
}

// PackWithOptions 打包 payload 并附带 GUI 选项（内嵌资源/图标）。
func PackWithOptions(entryPath string, modules []*EntryData, resolutions map[string]map[string]string, assets map[string][]byte, opts PackOptions) ([]byte, error) {
	sorted := make([]*EntryData, len(modules))
	copy(sorted, modules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	// 序列化各模块并记录偏移。
	var data bytes.Buffer
	entries := make([]EntryInfo, 0, len(sorted))
	for _, m := range sorted {
		offset := uint32(data.Len())
		if err := bytecode.Serialize(&data, m.Module); err != nil {
			return nil, fmt.Errorf("compile: serialize module %q: %w", m.Path, err)
		}
		entries = append(entries, EntryInfo{
			Path:       m.Path,
			ModuleType: m.ModuleType,
			SourceKind: m.SourceKind.String(),
			ModuleKind: m.ModuleKind.String(),
			Offset:     offset,
			Length:     uint32(data.Len()) - offset,
		})
	}

	assetStrings := make(map[string]string, len(assets))
	for k, v := range assets {
		assetStrings[k] = string(v)
	}

	var webAssetStrings map[string]string
	if len(opts.WebAssets) > 0 {
		webAssetStrings = make(map[string]string, len(opts.WebAssets))
		for k, v := range opts.WebAssets {
			webAssetStrings[k] = base64.StdEncoding.EncodeToString(v)
		}
	}

	var iconStr string
	if len(opts.Icon) > 0 {
		iconStr = base64.StdEncoding.EncodeToString(opts.Icon)
	}

	manifest := Manifest{
		PayloadVersion: PayloadVersion,
		FormatVersion:  bytecode.FormatVersion,
		Entry:          entryPath,
		RootDir:        opts.RootDir,
		Modules:        entries,
		Resolutions:    resolutions,
		Assets:         assetStrings,
		WebAssets:      webAssetStrings,
		Icon:           iconStr,
		Platform:       platformString(),
		CreatedAt:      time.Now().Format(time.RFC3339),
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("compile: marshal manifest: %w", err)
	}

	compressedManifest, err := compressBytes(manifestJSON)
	if err != nil {
		return nil, fmt.Errorf("compile: compress manifest: %w", err)
	}

	compressedData, err := compressBytes(data.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile: compress data: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(payloadMagic)
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], PayloadVersion)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(compressedManifest)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(compressedData)))
	buf.Write(hdr[:])
	buf.Write(compressedManifest)
	buf.Write(compressedData)
	return buf.Bytes(), nil
}

// ParsePayload 解析 payload 数据段，返回 manifest 与数据区字节。
func ParsePayload(data []byte) (*Manifest, []byte, error) {
	if len(data) < headerSize {
		return nil, nil, fmt.Errorf("compile: payload too short")
	}
	if !bytes.Equal(data[:8], payloadMagic) {
		return nil, nil, fmt.Errorf("compile: bad payload magic")
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != 2 && version != PayloadVersion {
		return nil, nil, fmt.Errorf("compile: payload version mismatch (file=%d, want=%d)", version, PayloadVersion)
	}
	manifestLen := binary.LittleEndian.Uint32(data[12:16])
	dataLen := binary.LittleEndian.Uint32(data[16:20])
	if uint64(headerSize)+uint64(manifestLen)+uint64(dataLen) != uint64(len(data)) {
		return nil, nil, fmt.Errorf("compile: payload length mismatch")
	}

	rawManifest := data[headerSize : headerSize+manifestLen]
	rawData := data[headerSize+manifestLen:]

	var manifestJSON []byte
	var moduleData []byte
	if version == 3 {
		var err error
		manifestJSON, err = decompressBytes(rawManifest)
		if err != nil {
			return nil, nil, fmt.Errorf("compile: decompress manifest: %w", err)
		}
		moduleData, err = decompressBytes(rawData)
		if err != nil {
			return nil, nil, fmt.Errorf("compile: decompress data: %w", err)
		}
	} else {
		manifestJSON = rawManifest
		moduleData = rawData
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, nil, fmt.Errorf("compile: parse manifest: %w", err)
	}
	if manifest.FormatVersion != bytecode.FormatVersion {
		return nil, nil, fmt.Errorf("compile: payload built with incompatible bytecode format (file=%d, want=%d); rebuild with current aluka", manifest.FormatVersion, bytecode.FormatVersion)
	}
	return &manifest, moduleData, nil
}

// LoadModule 从数据区反序列化指定路径的模块。
func (m *Manifest) LoadModule(data []byte, path string) (*bytecode.Module, error) {
	for _, e := range m.Modules {
		if e.Path != path {
			continue
		}
		if int(e.Offset)+int(e.Length) > len(data) {
			return nil, fmt.Errorf("compile: module %q out of range", path)
		}
		mod, err := bytecode.Deserialize(bytes.NewReader(data[e.Offset : e.Offset+e.Length]))
		if err != nil {
			return nil, fmt.Errorf("compile: deserialize module %q: %w", path, err)
		}
		return mod, nil
	}
	return nil, fmt.Errorf("compile: module %q not found in payload", path)
}

// ModuleTypeOf 返回模块类型（esm/cjs）；未找到返回 ""。
func (m *Manifest) ModuleTypeOf(path string) string {
	for _, e := range m.Modules {
		if e.Path == path {
			return e.ModuleType
		}
	}
	return ""
}

// SourceKindOf 返回模块的源码语言分类；旧产物（v1）无该字段时按扩展名回推。
func (m *Manifest) SourceKindOf(path string) string {
	for _, e := range m.Modules {
		if e.Path == path {
			if e.SourceKind != "" {
				return e.SourceKind
			}
			return module.DetectSourceKind(path).String()
		}
	}
	return ""
}

// ModuleKindOf 返回模块协议分类；旧产物（v1）无该字段时回退到 ModuleType。
func (m *Manifest) ModuleKindOf(path string) string {
	for _, e := range m.Modules {
		if e.Path == path {
			if e.ModuleKind != "" {
				return e.ModuleKind
			}
			return e.ModuleType
		}
	}
	return ""
}

// === Footer ==============================================================

// MakeFooter 构造产物文件 footer：magic + payloadOffset + payloadLen + sha256(payload)。
func MakeFooter(payloadOffset, payloadLen uint64, payload []byte) []byte {
	footer := make([]byte, FooterSize)
	copy(footer[0:8], footerMagic)
	binary.LittleEndian.PutUint64(footer[8:16], payloadOffset)
	binary.LittleEndian.PutUint64(footer[16:24], payloadLen)
	sum := sha256.Sum256(payload)
	copy(footer[24:56], sum[:])
	return footer
}

// ParseFooter 解析文件尾部字节，返回 payload 偏移/长度与校验和。
// ok=false 表示尾部不是合法 footer（非产物文件）。
func ParseFooter(b []byte) (offset, length uint64, sum [32]byte, ok bool) {
	if len(b) < FooterSize || !bytes.Equal(b[:8], footerMagic) {
		return 0, 0, sum, false
	}
	offset = binary.LittleEndian.Uint64(b[8:16])
	length = binary.LittleEndian.Uint64(b[16:24])
	copy(sum[:], b[24:56])
	return offset, length, sum, true
}

// VerifyPayload 校验 payload 的 sha256 是否与 footer 记录一致。
func VerifyPayload(payload []byte, sum [32]byte) bool {
	got := sha256.Sum256(payload)
	return bytes.Equal(got[:], sum[:])
}

// platformString 返回构建平台标识（GOOS/GOARCH）。
func platformString() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
