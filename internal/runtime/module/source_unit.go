package module

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// SourceKind 描述源码语言层；它与 ESM/CJS 模块语义相互独立。
type SourceKind uint8

const (
	SourceJavaScript SourceKind = iota
	SourceTypeScript
	SourceJSON
)

func (k SourceKind) String() string {
	switch k {
	case SourceTypeScript:
		return "typescript"
	case SourceJSON:
		return "json"
	default:
		return "javascript"
	}
}

// ModuleKind 描述模块封装与执行协议。
type ModuleKind uint8

const (
	ModuleScript ModuleKind = iota
	ModuleESM
	ModuleCommonJS
)

func (k ModuleKind) String() string {
	switch k {
	case ModuleESM:
		return "esm"
	case ModuleCommonJS:
		return "cjs"
	default:
		return "script"
	}
}

// TransformStage 记录 SourceUnit 已完成的单向变换阶段。
type TransformStage uint16

const (
	StageParsed TransformStage = 1 << iota
	StageTypeStripped
	StageShaken
	StageMinified
	StageESMLowered
	StageWrapped
	StageBytecodeOptimized
)

// SourceUnit 是 JS/TS 前端与模块/优化后端之间的稳定中间表示。
// Program 在一条构建管线内由单一所有者按阶段原地变换，禁止从 Source 重新解析。
type SourceUnit struct {
	Path       string
	Source     []byte
	SourceKind SourceKind
	ModuleKind ModuleKind
	Program    *ast.Program
	HasTLA     bool
	Stage      TransformStage
}

// DetectSourceKind 仅按规范化扩展名识别语言层。
func DetectSourceKind(path string) SourceKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return SourceTypeScript
	case ".json":
		return SourceJSON
	default:
		return SourceJavaScript
	}
}

// ParseSourceUnit 执行一次源码前端处理：TS policy 校验与 JS AST 解析。
func ParseSourceUnit(src []byte, path string, kind ModuleKind) (*SourceUnit, error) {
	src = stripSourceBOM(src)
	sourceKind := DetectSourceKind(path)
	if sourceKind == SourceJSON {
		return &SourceUnit{Path: path, Source: src, SourceKind: sourceKind, ModuleKind: kind}, nil
	}
	if err := checkUnsupportedTS(string(src), path); err != nil {
		return nil, err
	}
	prog, err := parser.ParseModule(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse error in %q: %w", path, err)
	}
	stage := StageParsed
	if sourceKind == SourceTypeScript {
		// 当前 parser 在建 AST 时执行 strip-only；显式记录该事实，后续可替换为
		// 独立 tsstrip pass，而不让模块层重新推断。
		stage |= StageTypeStripped
	}
	return &SourceUnit{
		Path:       path,
		Source:     src,
		SourceKind: sourceKind,
		ModuleKind: kind,
		Program:    prog,
		HasTLA:     ast.HasTopLevelAwait(prog),
		Stage:      stage,
	}, nil
}

func stripSourceBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}
