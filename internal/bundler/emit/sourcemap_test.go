package emit

import (
	"encoding/json"
	"testing"
)

// TestVLQEncodeDecode 测试 Base64-VLQ 的双向编解码一致性
func TestVLQEncodeDecode(t *testing.T) {
	testCases := []int{
		0, 1, -1, 15, -15, 16, -16, 31, -31, 32, -32,
		100, -100, 12345, -12345, 999999, -999999,
	}

	for _, val := range testCases {
		encoded := EncodeVLQ(val)
		decoded, err := DecodeVLQ(encoded)
		if err != nil {
			t.Fatalf("DecodeVLQ(%d -> %q) error: %v", val, encoded, err)
		}
		if len(decoded) != 1 || decoded[0] != val {
			t.Fatalf("VLQ roundtrip mismatch for %d: got %v, want [%d]", val, decoded, val)
		}
	}
}

// TestSourceMapBuilder 测试 Source Map v3 JSON 产出与结构合法性
func TestSourceMapBuilder(t *testing.T) {
	builder := NewSourceMapBuilder("dist/bundle.js")
	s1 := builder.AddSource("src/index.ts", "console.log(1);\nconsole.log(2);")
	s2 := builder.AddSource("src/util.ts", "export const add = (a, b) => a + b;")

	builder.AddLineMappings([]MappingSegment{
		{GeneratedCol: 0, SourceIndex: s1, OriginalLine: 0, OriginalCol: 0, NameIndex: -1},
		{GeneratedCol: 8, SourceIndex: s1, OriginalLine: 0, OriginalCol: 8, NameIndex: -1},
	})
	builder.AddLineMappings([]MappingSegment{
		{GeneratedCol: 0, SourceIndex: s1, OriginalLine: 1, OriginalCol: 0, NameIndex: -1},
	})
	builder.AddLineMappings([]MappingSegment{
		{GeneratedCol: 0, SourceIndex: s2, OriginalLine: 0, OriginalCol: 0, NameIndex: -1},
	})

	jsonStr, err := builder.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON error: %v", err)
	}

	var sm SourceMapV3
	if err := json.Unmarshal([]byte(jsonStr), &sm); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if sm.Version != 3 {
		t.Errorf("version = %d, want 3", sm.Version)
	}
	if sm.File != "dist/bundle.js" {
		t.Errorf("file = %q, want 'dist/bundle.js'", sm.File)
	}
	if len(sm.Sources) != 2 || sm.Sources[0] != "src/index.ts" || sm.Sources[1] != "src/util.ts" {
		t.Errorf("sources mismatch: %v", sm.Sources)
	}
	if len(sm.SourcesContent) != 2 {
		t.Errorf("sourcesContent length = %d, want 2", len(sm.SourcesContent))
	}
	if sm.Mappings == "" {
		t.Errorf("mappings is empty")
	}
}

// TestGenerateSimpleSourceMap 测试便捷生成简易 SourceMap
func TestGenerateSimpleSourceMap(t *testing.T) {
	sources := map[string]string{
		"main.ts": "const x = 1;\nconsole.log(x);",
	}
	jsonStr, err := GenerateSimpleSourceMap("main.js", sources)
	if err != nil {
		t.Fatalf("GenerateSimpleSourceMap error: %v", err)
	}

	var sm SourceMapV3
	if err := json.Unmarshal([]byte(jsonStr), &sm); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(sm.Sources) != 1 || sm.Sources[0] != "main.ts" {
		t.Errorf("unexpected sources: %v", sm.Sources)
	}
}
