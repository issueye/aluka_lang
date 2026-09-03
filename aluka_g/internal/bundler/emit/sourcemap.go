// Package emit —— Source Map v3 生成器（M2-4）。
//
// 提供符合 Source Map v3 规范（RFC / TC39）的 Base64-VLQ 编解码与外链 map 生成。
package emit

import (
	"encoding/json"
	"fmt"
	"strings"
)

const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// EncodeVLQ 将一个整数编码为 Base64-VLQ 序列。
func EncodeVLQ(value int) string {
	var sb strings.Builder
	var vlq int
	if value < 0 {
		vlq = ((-value) << 1) | 1
	} else {
		vlq = value << 1
	}
	for {
		digit := vlq & 31 // 5 位
		vlq >>= 5
		if vlq > 0 {
			digit |= 32 // 延续位
		}
		sb.WriteByte(base64Chars[digit])
		if vlq == 0 {
			break
		}
	}
	return sb.String()
}

// DecodeVLQ 解码一段 Base64-VLQ 序列，返回解码出的整数列表。
func DecodeVLQ(s string) ([]int, error) {
	var result []int
	var shift uint
	var value int

	charMap := make(map[byte]int, len(base64Chars))
	for i := 0; i < len(base64Chars); i++ {
		charMap[base64Chars[i]] = i
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		digit, ok := charMap[c]
		if !ok {
			return nil, fmt.Errorf("invalid base64 char: %c", c)
		}
		hasContinuation := (digit & 32) != 0
		digit &= 31
		value += digit << shift
		shift += 5

		if !hasContinuation {
			isNegative := (value & 1) == 1
			decoded := value >> 1
			if isNegative {
				decoded = -decoded
			}
			result = append(result, decoded)
			value = 0
			shift = 0
		}
	}
	return result, nil
}

// MappingSegment 代表 mappings 中的一个点位映射。
type MappingSegment struct {
	GeneratedCol int // 生成文件列（0-based）
	SourceIndex  int // 源文件索引（0-based，负数表示不包含）
	OriginalLine int // 源文件行（0-based）
	OriginalCol  int // 源文件列（0-based）
	NameIndex    int // 符号表名称索引（0-based，负数表示不包含）
}

// SourceMapV3 遵循 Source Map Revision 3 提案格式。
type SourceMapV3 struct {
	Version        int      `json:"version"`
	File           string   `json:"file"`
	SourceRoot     string   `json:"sourceRoot,omitempty"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent,omitempty"`
	Names          []string `json:"names"`
	Mappings       string   `json:"mappings"`
}

// SourceMapBuilder 用于逐行构建 SourceMap 的 mappings。
type SourceMapBuilder struct {
	file           string
	sources        []string
	sourcesContent []string
	names          []string

	prevSourceIdx int
	prevOrigLine  int
	prevOrigCol   int
	prevNameIdx   int

	lines []string
}

// NewSourceMapBuilder 创建一个新的 SourceMap 构建器。
func NewSourceMapBuilder(file string) *SourceMapBuilder {
	return &SourceMapBuilder{
		file:           file,
		sources:        make([]string, 0),
		sourcesContent: make([]string, 0),
		names:          make([]string, 0),
		lines:          make([]string, 0),
	}
}

// AddSource 添加源文件及其源码内容，返回 sourceIndex。
func (b *SourceMapBuilder) AddSource(sourcePath, content string) int {
	for i, s := range b.sources {
		if s == sourcePath {
			return i
		}
	}
	idx := len(b.sources)
	b.sources = append(b.sources, sourcePath)
	b.sourcesContent = append(b.sourcesContent, content)
	return idx
}

// AddLineMappings 为生成代码的当前行添加一组 mapping segment。
func (b *SourceMapBuilder) AddLineMappings(segments []MappingSegment) {
	if len(segments) == 0 {
		b.lines = append(b.lines, "")
		return
	}

	var sb strings.Builder
	prevGenCol := 0

	for i, seg := range segments {
		if i > 0 {
			sb.WriteString(",")
		}
		// 1. 生成列相对偏移
		genColDelta := seg.GeneratedCol - prevGenCol
		sb.WriteString(EncodeVLQ(genColDelta))
		prevGenCol = seg.GeneratedCol

		if seg.SourceIndex >= 0 {
			// 2. 源文件索引相对偏移
			sourceIdxDelta := seg.SourceIndex - b.prevSourceIdx
			sb.WriteString(EncodeVLQ(sourceIdxDelta))
			b.prevSourceIdx = seg.SourceIndex

			// 3. 源文件行相对偏移
			origLineDelta := seg.OriginalLine - b.prevOrigLine
			sb.WriteString(EncodeVLQ(origLineDelta))
			b.prevOrigLine = seg.OriginalLine

			// 4. 源文件列相对偏移
			origColDelta := seg.OriginalCol - b.prevOrigCol
			sb.WriteString(EncodeVLQ(origColDelta))
			b.prevOrigCol = seg.OriginalCol

			if seg.NameIndex >= 0 {
				// 5. 符号索引相对偏移
				nameIdxDelta := seg.NameIndex - b.prevNameIdx
				sb.WriteString(EncodeVLQ(nameIdxDelta))
				b.prevNameIdx = seg.NameIndex
			}
		}
	}
	b.lines = append(b.lines, sb.String())
}

// Build 生成 SourceMapV3 结构。
func (b *SourceMapBuilder) Build() *SourceMapV3 {
	mappings := strings.Join(b.lines, ";")
	return &SourceMapV3{
		Version:        3,
		File:           b.file,
		Sources:        b.sources,
		SourcesContent: b.sourcesContent,
		Names:          b.names,
		Mappings:       mappings,
	}
}

// ToJSON 将 SourceMap 序列化为 JSON 字符串。
func (b *SourceMapBuilder) ToJSON() (string, error) {
	data, err := json.Marshal(b.Build())
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GenerateSimpleSourceMap 为模块 bundle 生成基础文件/行级映射。
// bundleText: 产物代码；modules: 模块源码清单与行数信息。
func GenerateSimpleSourceMap(outFile string, moduleSources map[string]string) (string, error) {
	b := NewSourceMapBuilder(outFile)
	for path, content := range moduleSources {
		srcIdx := b.AddSource(path, content)
		lines := strings.Split(content, "\n")
		for lineIdx := range lines {
			b.AddLineMappings([]MappingSegment{
				{
					GeneratedCol: 0,
					SourceIndex:  srcIdx,
					OriginalLine: lineIdx,
					OriginalCol:  0,
					NameIndex:    -1,
				},
			})
		}
	}
	return b.ToJSON()
}
